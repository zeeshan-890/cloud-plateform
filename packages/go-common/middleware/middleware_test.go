package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtutil "github.com/jp-cloud/go-common/jwt"
)

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }

func TestCORSAllowlistOnly(t *testing.T) {
	h := CORS([]string{"https://app.example.com"})(http.HandlerFunc(ok))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("unlisted origin must not be reflected")
	}
	req.Header.Set("Origin", "https://app.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("listed origin should be allowed with credentials: %v", rec.Header())
	}
}

func TestCORSWildcardNeverWithCredentials(t *testing.T) {
	h := CORS([]string{"*"})(http.HandlerFunc(ok))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard should answer *: %v", rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("wildcard must never combine with credentials")
	}
}

func TestRequestIDSanitised(t *testing.T) {
	h := RequestID(http.HandlerFunc(ok))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got == "" || got == "bad id with spaces" {
		t.Fatalf("unsafe id must be replaced: %q", got)
	}
}

func TestBearerAuthDenyHooks(t *testing.T) {
	jm := jwtutil.NewManager("0123456789abcdef0123456789abcdef", time.Minute, time.Hour)
	tok, _, _ := jm.IssueAccess("u1", "a@b.c", "A")
	call := func() int {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		BearerAuth(jm)(http.HandlerFunc(ok)).ServeHTTP(rec, req)
		return rec.Code
	}
	if call() != 200 {
		t.Fatal("valid token should pass")
	}
	DenyCheck = func(context.Context, string) bool { return true }
	if call() != 401 {
		t.Fatal("DenyCheck must reject")
	}
	DenyCheck = nil
	DenyCheckClaims = func(context.Context, *jwtutil.Claims) bool { return true }
	if call() != 401 {
		t.Fatal("DenyCheckClaims must reject")
	}
	DenyCheckClaims = nil
}
