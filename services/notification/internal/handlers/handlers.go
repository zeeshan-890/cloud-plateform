package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/notification/internal/mailer"
)

// Record is the outcome of one delivery (kept in a bounded ring for operators).
type Record struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	To        string    `json:"to"`
	Status    string    `json:"status"` // queued | sent | failed
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type job struct {
	id  int64
	typ string
	msg mailer.Message
}

// Handler accepts notification requests and delivers them asynchronously with retries.
type Handler struct {
	Log          *slog.Logger
	Sender       mailer.Sender
	DashboardURL string
	// Settings returns platform settings (from the identity service); may be nil.
	Settings func(ctx context.Context) map[string]string
	// Backoff is the wait before each retry; len(Backoff)+1 attempts are made.
	Backoff []time.Duration

	queue chan job
	mu    sync.Mutex
	seq   int64
	ring  []Record
	wg    sync.WaitGroup
}

const (
	queueSize = 256
	ringSize  = 200
)

// Start launches the delivery workers; they stop when ctx is cancelled.
func (h *Handler) Start(ctx context.Context, workers int) {
	if h.Backoff == nil {
		h.Backoff = []time.Duration{2 * time.Second, 10 * time.Second, 45 * time.Second, 3 * time.Minute}
	}
	if h.Sender == nil {
		h.Sender = mailer.SMTP{}
	}
	h.queue = make(chan job, queueSize)
	for i := 0; i < workers; i++ {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-h.queue:
					h.deliver(ctx, j)
				}
			}
		}()
	}
}

// Wait blocks until workers exit (after the Start context is cancelled).
func (h *Handler) Wait() { h.wg.Wait() }

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("POST /notify", internalauth.Require(http.HandlerFunc(h.Notify)))
	mux.Handle("GET /internal/deliveries", internalauth.Require(http.HandlerFunc(h.Deliveries)))
}

type notifyReq struct {
	Type        string `json:"type"`
	Email       string `json:"email"`
	Token       string `json:"token"`
	OrgID       string `json:"org_id"`
	OrgName     string `json:"org_name"`
	Role        string `json:"role"`
	InviterName string `json:"inviter_name"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
}

// config merges environment SMTP_* with platform settings (settings win).
func (h *Handler) config(ctx context.Context) mailer.Config {
	c := mailer.Config{
		Host: os.Getenv("SMTP_HOST"), Port: os.Getenv("SMTP_PORT"), Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("SMTP_FROM"), TLS: strings.ToLower(os.Getenv("SMTP_TLS")),
	}
	if h.Settings != nil {
		s := h.Settings(ctx)
		set := func(dst *string, key string) {
			if v := s[key]; v != "" {
				*dst = v
			}
		}
		set(&c.Host, "smtp_host")
		set(&c.Port, "smtp_port")
		set(&c.Username, "smtp_username")
		set(&c.Password, "smtp_password")
		set(&c.From, "smtp_from")
		set(&c.TLS, "smtp_tls")
	}
	if c.TLS == "" {
		c.TLS = "starttls"
	}
	return c
}

func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	var req notifyReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	to, err := mailer.ValidAddress(req.Email)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "valid email required")
		return
	}
	var msg mailer.Message
	switch req.Type {
	case "org.invite":
		if req.Token == "" || len(req.Token) > 200 {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "token required")
			return
		}
		if h.DashboardURL == "" {
			httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "PUBLIC_DASHBOARD_URL is not configured; cannot build the accept link")
			return
		}
		msg = mailer.InviteMessage(to, h.DashboardURL, req.Token, req.OrgName, req.Role, req.InviterName)
	default:
		if strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.Body) == "" || len(req.Body) > 100000 {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "subject and body required")
			return
		}
		msg = mailer.Message{To: to, Subject: req.Subject, Text: req.Body}
	}
	if !h.config(r.Context()).Configured() {
		// Fail loudly: the caller (and the log) learn that nothing will be delivered.
		h.log().Error("notification rejected: SMTP not configured", "type", req.Type)
		httpx.Error(w, http.StatusServiceUnavailable, "smtp_not_configured", mailer.ErrNotConfigured.Error())
		return
	}
	h.mu.Lock()
	h.seq++
	j := job{id: h.seq, typ: req.Type, msg: msg}
	h.mu.Unlock()
	h.record(Record{ID: j.id, Type: j.typ, To: to, Status: "queued"})
	select {
	case h.queue <- j:
		httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "channel": "smtp", "id": j.id})
	default:
		h.record(Record{ID: j.id, Type: j.typ, To: to, Status: "failed", Error: "queue full"})
		httpx.Error(w, http.StatusServiceUnavailable, "queue_full", "notification queue is full; retry later")
	}
}

func (h *Handler) deliver(ctx context.Context, j job) {
	var lastErr error
	attempts := len(h.Backoff) + 1
	for a := 1; a <= attempts; a++ {
		cfg := h.config(ctx)
		lastErr = h.Sender.Send(ctx, cfg, j.msg)
		if lastErr == nil {
			h.record(Record{ID: j.id, Type: j.typ, To: j.msg.To, Status: "sent", Attempts: a})
			h.log().Info("notification sent", "id", j.id, "type", j.typ, "attempts", a)
			return
		}
		h.record(Record{ID: j.id, Type: j.typ, To: j.msg.To, Status: "queued", Attempts: a, Error: lastErr.Error()})
		h.log().Warn("notification attempt failed", "id", j.id, "attempt", a, "err", lastErr)
		if a == attempts {
			break
		}
		t := time.NewTimer(h.Backoff[a-1])
		select {
		case <-ctx.Done():
			t.Stop()
			h.record(Record{ID: j.id, Type: j.typ, To: j.msg.To, Status: "failed", Attempts: a, Error: "shutdown before delivery: " + lastErr.Error()})
			return
		case <-t.C:
		}
	}
	h.record(Record{ID: j.id, Type: j.typ, To: j.msg.To, Status: "failed", Attempts: attempts, Error: lastErr.Error()})
	h.log().Error("notification delivery FAILED permanently", "id", j.id, "type", j.typ, "to", j.msg.To, "err", lastErr)
}

func (h *Handler) record(r Record) {
	r.UpdatedAt = time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.ring {
		if h.ring[i].ID == r.ID {
			h.ring[i] = r
			return
		}
	}
	if len(h.ring) >= ringSize {
		h.ring = h.ring[1:]
	}
	h.ring = append(h.ring, r)
}

// Records returns a snapshot of recent deliveries.
func (h *Handler) Records() []Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Record(nil), h.ring...)
}

func (h *Handler) Deliveries(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"deliveries": h.Records()})
}

// IdentitySettings returns a Settings func reading <identityURL>/internal/settings (cached 30s).
func IdentitySettings(identityURL string, hc *http.Client) func(context.Context) map[string]string {
	var (
		mu     sync.Mutex
		cached map[string]string
		at     time.Time
	)
	return func(ctx context.Context) map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && time.Since(at) < 30*time.Second {
			return cached
		}
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(identityURL, "/")+"/internal/settings", nil)
		if err != nil {
			return cached
		}
		resp, err := hc.Do(req)
		if err != nil {
			return cached
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			return cached
		}
		var out struct {
			Settings map[string]string `json:"settings"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
			return cached
		}
		cached, at = out.Settings, time.Now()
		return cached
	}
}
