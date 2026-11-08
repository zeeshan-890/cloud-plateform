// Package internalauth protects service-to-service (/internal/*) endpoints with a
// shared secret (INTERNAL_TOKEN) so that tenant containers or anything else that can
// reach a service port cannot call them.
package internalauth

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"time"
)

// Header carries the shared internal token.
const Header = "X-Internal-Token"

// Token returns the configured token.
func Token() string { return strings.TrimSpace(os.Getenv("INTERNAL_TOKEN")) }

func devMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("JP_ENV"))) {
	case "", "development", "dev", "test":
		return true
	}
	return false
}

// Require wraps a handler so it only runs for callers presenting the internal token.
// With no token configured it fails closed unless JP_ENV is development/test.
func Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := Token()
		if want == "" {
			if devMode() {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, `{"error":{"code":"internal_auth_unconfigured","message":"INTERNAL_TOKEN not configured"}}`, http.StatusServiceUnavailable)
			return
		}
		got := r.Header.Get(Header)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, `{"error":{"code":"forbidden","message":"internal endpoint"}}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Valid reports whether the request carries the correct internal token (always false when
// no token is configured, even in dev, so callers use it only to widen trust).
func Valid(r *http.Request) bool {
	want := Token()
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get(Header)), []byte(want)) == 1
}

// RequireFunc is Require for a HandlerFunc.
func RequireFunc(h http.HandlerFunc) http.Handler { return Require(h) }

// Sign adds the token to an outgoing request.
func Sign(req *http.Request) {
	if t := Token(); t != "" {
		req.Header.Set(Header, t)
	}
}

type transport struct{ base http.RoundTripper }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	Sign(r2)
	return t.base.RoundTrip(r2)
}

// NewClient returns an http.Client that signs every request with the internal token.
// Use it for ALL calls to other platform services.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transport{base: http.DefaultTransport}}
}
