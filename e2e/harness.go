// Package e2e is a black-box end-to-end suite. It only talks to a deployed platform over
// HTTP (E2E_BASE_URL, e.g. http://161.118.168.6:3000) and, for infrastructure assertions,
// over SSH (E2E_SSH, e.g. oracle-2nd). It never imports service code.
//
// Run:  cd e2e && GOWORK=off E2E_BASE_URL=http://HOST:3000 E2E_SSH=oracle-2nd go test -v -count=1 -timeout 60m ./...
package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Env holds suite configuration.
type Env struct {
	BaseURL   string // dashboard/gateway entry, no trailing slash
	API       string // BaseURL + /api/v1
	SSH       string // ssh host alias (optional)
	EdgeHost  string // host or IP of Traefik (default: host of BaseURL)
	EdgePort  string // default 80
	RootDom   string // ROOT_DOMAIN of the install
	AdminMail string
	AdminPass string
}

// Load reads configuration from the environment.
func Load(t testing.TB) *Env {
	t.Helper()
	envMu.Lock()
	defer envMu.Unlock()
	if cachedEnv != nil {
		return cachedEnv
	}
	base := strings.TrimRight(os.Getenv("E2E_BASE_URL"), "/")
	if base == "" {
		t.Skip("E2E_BASE_URL not set (suite requires a deployed platform)")
	}
	e := &Env{BaseURL: base, API: base + "/api/v1", SSH: os.Getenv("E2E_SSH"),
		EdgePort: getenv("E2E_EDGE_PORT", "80"), RootDom: os.Getenv("E2E_ROOT_DOMAIN"),
		AdminMail: getenv("E2E_ADMIN_EMAIL", "admin@e2e.test"), AdminPass: getenv("E2E_ADMIN_PASSWORD", "E2e-Passw0rd-"+randHex(4))}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	if i := strings.IndexAny(host, ":/"); i >= 0 {
		host = host[:i]
	}
	e.EdgeHost = getenv("E2E_EDGE_HOST", host)
	cachedEnv = e
	return e
}

var (
	envMu     sync.Mutex
	cachedEnv *Env
)

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func randHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }

// RandName returns a unique lowercase label.
func RandName(prefix string) string { return prefix + "-" + randHex(3) }

// Client is a JSON API client bound to one user session.
type Client struct {
	Env     *Env
	Token   string
	Refresh string
	HTTP    *http.Client

	// AutoRefresh makes Do transparently renew an expired access token (admin client only).
	AutoRefresh bool
	Timeout     time.Duration // per-request timeout (default 60s)
	LoginName   string
	LoginPass   string
	mu          sync.Mutex
}

// NewClient returns an unauthenticated client.
func (e *Env) NewClient() *Client {
	return &Client{Env: e, Timeout: 60 * time.Second, HTTP: &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Resp is a decoded response.
type Resp struct {
	Status int
	Body   []byte
	Header http.Header
}

// JSON decodes the body into v.
func (r *Resp) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %q: %v", truncate(string(r.Body), 400), err)
	}
}

// Map decodes the body into a generic map.
func (r *Resp) Map(t testing.TB) map[string]any { var m map[string]any; r.JSON(t, &m); return m }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Do performs a JSON request against /api/v1 (path starts with "/").
func (c *Client) Do(method, path string, body any) *Resp { return c.DoH(method, path, body, nil) }

// DoH is Do with extra request headers.
func (c *Client) DoH(method, path string, body any, hdr map[string]string) *Resp {
	var raw []byte
	ct := ""
	if body != nil {
		raw, _ = json.Marshal(body)
		ct = "application/json"
	}
	return c.DoRaw(method, path, raw, ct, hdr)
}

// DoRaw sends raw bytes (any content type). When AutoRefresh is set, a 401 triggers one token renewal and a retry.
func (c *Client) DoRaw(method, path string, body []byte, contentType string, hdr map[string]string) *Resp {
	r := c.once(method, path, body, contentType, hdr)
	if r.Status == 401 && c.AutoRefresh && c.renew() {
		r = c.once(method, path, body, contentType, hdr)
	}
	return r
}

func (c *Client) tok() string { c.mu.Lock(); defer c.mu.Unlock(); return c.Token }

func (c *Client) once(method, path string, body []byte, contentType string, hdr map[string]string) *Resp {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	tmo := c.Timeout
	if tmo <= 0 {
		tmo = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmo)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.Env.API+path, rdr)
	if err != nil {
		return &Resp{Status: 0, Body: []byte(err.Error())}
	}
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if t := c.tok(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &Resp{Status: 0, Body: []byte(err.Error())}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	return &Resp{Status: resp.StatusCode, Body: b, Header: resp.Header}
}

// renew refreshes (or re-logs-in) the session of an AutoRefresh client.
func (c *Client) renew() bool {
	c.mu.Lock()
	rt := c.Refresh
	c.mu.Unlock()
	try := func(path string, body any) bool {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", c.Env.API+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != 200 {
			return false
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		c.mu.Lock()
		c.SetTokens(m)
		c.mu.Unlock()
		return true
	}
	if rt != "" && try("/auth/refresh", map[string]string{"refresh_token": rt}) {
		return true
	}
	if c.LoginName != "" {
		return try("/auth/login", map[string]string{"login": c.LoginName, "password": c.LoginPass})
	}
	return false
}

// Must asserts status is one of want and returns the response.
func (c *Client) Must(t testing.TB, method, path string, body any, want ...int) *Resp {
	t.Helper()
	r := c.Do(method, path, body)
	for _, w := range want {
		if r.Status == w {
			return r
		}
	}
	t.Fatalf("%s %s: status %d (want %v): %s", method, path, r.Status, want, truncate(string(r.Body), 600))
	return r
}

// SetTokens stores tokens from an auth response.
func (c *Client) SetTokens(m map[string]any) {
	if s, _ := m["access_token"].(string); s != "" {
		c.Token = s
	}
	if s, _ := m["refresh_token"].(string); s != "" {
		c.Refresh = s
	}
}

// TOTP computes an RFC 6238 code (SHA1, 6 digits, 30s) for a base32 secret.
func TOTP(secret string, at time.Time) string {
	s := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return "000000"
	}
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(ctr[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	code := (binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code)
}

// HMACSig returns a GitHub style sha256 signature header value.
func HMACSig(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// SSH runs a command on the target host (requires E2E_SSH).
func (e *Env) Run(t testing.TB, cmd string) string {
	t.Helper()
	if e.SSH == "" {
		t.Skip("E2E_SSH not set")
	}
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "-o", "LogLevel=ERROR", e.SSH, cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh %q: %v\n%s", cmd, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TryRun is Run without failing the test.
func (e *Env) TryRun(cmd string) (string, error) {
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "-o", "LogLevel=ERROR", e.SSH, cmd).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Eventually polls fn until it returns true or timeout elapses.
func Eventually(t testing.TB, timeout, every time.Duration, what string, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		ok, msg := fn()
		if ok {
			return
		}
		last = msg
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s: %s", what, last)
		}
		time.Sleep(every)
	}
}

// EdgeGet requests http://EdgeHost:EdgePort/path with a Host header (routing test).
func (e *Env) EdgeGet(host, path string) (int, string, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:%s%s", e.EdgeHost, e.EdgePort, path), nil)
	req.Host = host
	cl := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(b), nil
}

// PortOpen reports whether a TCP port on the edge host accepts connections from here.
func (e *Env) PortOpen(port string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(e.EdgeHost, port), 3*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
