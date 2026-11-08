package handlers

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/notification/internal/mailer"
)

// fakeSMTP is a minimal in-process SMTP server.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	msgs     []string
	auth     []string
	failNext int // number of connections to reject with 421
	tlsCfg   *tls.Config
	sawTLS   bool
}

func selfSigned(t *testing.T) *tls.Config {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func newFakeSMTP(t *testing.T, tlsCfg *tls.Config) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln, tlsCfg: tlsCfg}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSMTP) addr() (string, string) {
	h, p, _ := net.SplitHostPort(s.ln.Addr().String())
	return h, p
}

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	s.mu.Lock()
	if s.failNext > 0 {
		s.failNext--
		s.mu.Unlock()
		c.Write([]byte("421 try later\r\n"))
		return
	}
	s.mu.Unlock()
	r := bufio.NewReader(c)
	w := func(l string) { c.Write([]byte(l + "\r\n")) }
	w("220 fake ESMTP")
	inData := false
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.msgs = append(s.msgs, data.String())
				s.mu.Unlock()
				data.Reset()
				w("250 queued")
				continue
			}
			data.WriteString(line + "\n")
			continue
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			c.Write([]byte("250-fake\r\n"))
			if s.tlsCfg != nil && !s.sawTLS {
				c.Write([]byte("250-STARTTLS\r\n"))
			}
			w("250 AUTH PLAIN")
		case up == "STARTTLS":
			w("220 go")
			tc := tls.Server(c, s.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			s.mu.Lock()
			s.sawTLS = true
			s.mu.Unlock()
			c = tc
			r = bufio.NewReader(tc)
			w = func(l string) { tc.Write([]byte(l + "\r\n")) }
		case strings.HasPrefix(up, "AUTH PLAIN"):
			parts := strings.Fields(line)
			raw, _ := base64.StdEncoding.DecodeString(parts[2])
			s.mu.Lock()
			s.auth = append(s.auth, strings.ReplaceAll(string(raw), "\x00", "|"))
			s.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(up, "MAIL"), strings.HasPrefix(up, "RCPT"):
			w("250 ok")
		case up == "DATA":
			inData = true
			w("354 go")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (s *fakeSMTP) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func setSMTPEnv(t *testing.T, s *fakeSMTP, tlsMode string) {
	host, port := s.addr()
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_FROM", "JP Cloud <noreply@example.com>")
	t.Setenv("SMTP_TLS", tlsMode)
}

func newHandler(t *testing.T, sender mailer.Sender) (*Handler, *httptest.Server) {
	h := &Handler{Sender: sender, DashboardURL: "https://app.example.com", Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx, 2)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close(); cancel(); h.Wait() })
	return h, srv
}

func post(t *testing.T, url, body string, hdr ...string) int {
	req, _ := http.NewRequest("POST", url+"/notify", strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestInviteEmailDeliveredOverSMTPWithAuth(t *testing.T) {
	s := newFakeSMTP(t, nil)
	setSMTPEnv(t, s, "none")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "pw")
	h, srv := newHandler(t, mailer.SMTP{})

	code := post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"tok123","org_name":"Acme","role":"member","inviter_name":"Alice"}`)
	if code != 202 {
		t.Fatalf("notify: %d", code)
	}
	waitFor(t, "message", func() bool { return len(s.messages()) == 1 })
	m := s.messages()[0]
	for _, want := range []string{"To: bob@example.com", "text/plain", "text/html", "https://app.example.com/invite/accept?token=tok123", "Acme", "Alice invited you"} {
		if !strings.Contains(m, want) {
			t.Errorf("message missing %q:\n%s", want, m)
		}
	}
	if len(s.auth) != 1 || s.auth[0] != "|mailer|pw" {
		t.Fatalf("AUTH PLAIN credentials not used: %v", s.auth)
	}
	waitFor(t, "sent record", func() bool { r := h.Records(); return len(r) == 1 && r[0].Status == "sent" })
}

func TestStartTLSIsUsed(t *testing.T) {
	s := newFakeSMTP(t, selfSigned(t))
	setSMTPEnv(t, s, "starttls")
	// self-signed server: allow in the test sender only
	sender := insecureSender{}
	_, srv := newHandler(t, sender)
	if code := post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`); code != 202 {
		t.Fatalf("notify: %d", code)
	}
	waitFor(t, "message", func() bool { return len(s.messages()) == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sawTLS {
		t.Fatal("message must be sent after STARTTLS")
	}
}

type insecureSender struct{}

func (insecureSender) Send(ctx context.Context, cfg mailer.Config, m mailer.Message) error {
	cfg.InsecureSkipVerify = true
	return mailer.SMTP{}.Send(ctx, cfg, m)
}

func TestStartTLSRefusedWhenServerLacksIt(t *testing.T) {
	s := newFakeSMTP(t, nil) // no STARTTLS offered
	setSMTPEnv(t, s, "starttls")
	h, srv := newHandler(t, mailer.SMTP{})
	post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`)
	waitFor(t, "failed record", func() bool { r := h.Records(); return len(r) == 1 && r[0].Status == "failed" })
	if len(s.messages()) != 0 {
		t.Fatal("must not downgrade to plaintext silently")
	}
	if !strings.Contains(h.Records()[0].Error, "STARTTLS") {
		t.Fatalf("error should say why: %q", h.Records()[0].Error)
	}
}

func TestRetriesWithBackoffThenSucceeds(t *testing.T) {
	s := newFakeSMTP(t, nil)
	s.failNext = 2
	setSMTPEnv(t, s, "none")
	h, srv := newHandler(t, mailer.SMTP{})
	post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`)
	waitFor(t, "delivery after retries", func() bool { return len(s.messages()) == 1 })
	waitFor(t, "sent record", func() bool { r := h.Records(); return len(r) == 1 && r[0].Status == "sent" && r[0].Attempts == 3 })
}

type failSender struct{ n int }

func (f *failSender) Send(context.Context, mailer.Config, mailer.Message) error {
	f.n++
	return errors.New("smtp down")
}

func TestPermanentFailureIsRecorded(t *testing.T) {
	t.Setenv("SMTP_HOST", "x")
	t.Setenv("SMTP_FROM", "a@example.com")
	fs := &failSender{}
	h, srv := newHandler(t, fs)
	post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`)
	waitFor(t, "failed", func() bool { r := h.Records(); return len(r) == 1 && r[0].Status == "failed" })
	r := h.Records()[0]
	if r.Attempts != 3 || fs.n != 3 || r.Error != "smtp down" {
		t.Fatalf("record: %+v attempts=%d", r, fs.n)
	}
}

func TestUnconfiguredSMTPFailsLoudly(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")
	_, srv := newHandler(t, &failSender{})
	if code := post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`); code != 503 {
		t.Fatalf("unconfigured SMTP must be reported, got %d", code)
	}
}

func TestNotifyRequiresInternalToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	t.Setenv("SMTP_HOST", "x")
	t.Setenv("SMTP_FROM", "a@example.com")
	_, srv := newHandler(t, &failSender{})
	body := `{"type":"org.invite","email":"bob@example.com","token":"t","role":"viewer"}`
	if code := post(t, srv.URL, body); code != 403 {
		t.Fatalf("unauthenticated notify: %d", code)
	}
	if code := post(t, srv.URL, body, internalauth.Header, "internal-token-for-tests"); code != 202 {
		t.Fatalf("authenticated notify: %d", code)
	}
}

func TestHeaderInjectionRejected(t *testing.T) {
	t.Setenv("SMTP_HOST", "x")
	t.Setenv("SMTP_FROM", "a@example.com")
	_, srv := newHandler(t, &failSender{})
	if code := post(t, srv.URL, `{"type":"org.invite","email":"bob@example.com\r\nBcc: evil@example.com","token":"t","role":"viewer"}`); code != 400 {
		t.Fatalf("CRLF in address must be rejected: %d", code)
	}
	msg, _, _, err := mailer.Build("a@example.com", mailer.Message{To: "b@example.com", Subject: "hi\r\nBcc: evil@x.com", Text: "x"})
	if err != nil || strings.Contains(string(msg), "\r\nBcc:") {
		t.Fatalf("subject must be sanitised: %v\n%s", err, msg)
	}
}

func TestInviteHTMLEscapes(t *testing.T) {
	tag := "<" + "scr" + "ipt>"
	m := mailer.InviteMessage("b@example.com", "https://app.example.com/", "a&b<c>", tag+"x", "member", "")
	if strings.Contains(m.HTML, tag) || !strings.Contains(m.Text, "token=a%26b%3Cc%3E") {
		t.Fatalf("escaping: %s | %s", m.HTML, m.Text)
	}
}
