package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSingleJoin(t *testing.T) {
	tests := []struct {
		base string
		path string
		want string
	}{
		{"http://api:8000", "/v1/users", "http://api:8000/v1/users"},
		{"http://api:8000/", "v1/users", "http://api:8000/v1/users"},
		{"http://api:8000/", "/v1/users", "http://api:8000/v1/users"},
		{"http://api:8000/prefix", "/v1/users", "http://api:8000/prefix/v1/users"},
	}

	for _, tt := range tests {
		got := singleJoin(tt.base, tt.path)
		if got != tt.want {
			t.Errorf("singleJoin(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
		}
	}
}

func TestCopyHeadersHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "keep-alive")
	src.Set("Upgrade", "websocket")
	src.Set("X-Custom-Header", "allowed-value")
	src.Set("Authorization", "Bearer token123")

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("Connection") != "" {
		t.Error("hop-by-hop Connection header was not stripped")
	}
	if dst.Get("Upgrade") != "" {
		t.Error("hop-by-hop Upgrade header was not stripped")
	}
	if dst.Get("X-Custom-Header") != "allowed-value" {
		t.Errorf("expected allowed-value, got %s", dst.Get("X-Custom-Header"))
	}
	if dst.Get("Authorization") != "Bearer token123" {
		t.Errorf("expected Bearer token123, got %s", dst.Get("Authorization"))
	}
}

func TestReverseProxyPassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "u-42" {
			t.Errorf("missing X-User-ID header, got %s", r.Header.Get("X-User-ID"))
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream-Echo", "pong")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(append([]byte("echo:"), body...))
	}))
	defer upstream.Close()

	proxy, err := New(upstream.URL)
	if err != nil {
		t.Fatalf("failed to create reverse proxy: %v", err)
	}

	req := httptest.NewRequest("POST", "/test", strings.NewReader("hello-proxy"))
	req.Header.Set("X-User-ID", "u-42")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}
	if rec.Header().Get("X-Upstream-Echo") != "pong" {
		t.Errorf("expected header X-Upstream-Echo: pong")
	}
	if rec.Body.String() != "echo:hello-proxy" {
		t.Errorf("expected body 'echo:hello-proxy', got %s", rec.Body.String())
	}
}
