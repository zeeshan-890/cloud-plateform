package proxy

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSingleJoin(t *testing.T) {
	for _, tt := range []struct{ base, path, want string }{
		{"", "/v1/users", "/v1/users"},
		{"/", "v1/users", "/v1/users"},
		{"/prefix", "/v1/users", "/prefix/v1/users"},
		{"/prefix/", "/x", "/prefix/x"},
	} {
		if got := singleJoin(tt.base, tt.path); got != tt.want {
			t.Errorf("singleJoin(%q,%q)=%q want %q", tt.base, tt.path, got, tt.want)
		}
	}
}

func serve(t *testing.T, p *Reverse, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func TestPassThroughAndHopByHop(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/test" || r.URL.RawQuery != "a=1" {
			t.Errorf("path/query: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("X-Forwarded-For") != "203.0.113.5" {
			t.Errorf("XFF must be the resolved client ip, got %q", r.Header.Get("X-Forwarded-For"))
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream-Echo", "pong")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(append([]byte("echo:"), body...))
	}))
	defer up.Close()
	p, err := New(up.URL + "/prefix")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/test?a=1", strings.NewReader("hello-proxy"))
	req.Header.Set("X-Forwarded-For", "6.6.6.6") // client-supplied: must not survive
	req = req.WithContext(WithClientIP(req.Context(), "203.0.113.5"))
	rec := serve(t, p, req)
	if rec.Code != 201 || rec.Body.String() != "echo:hello-proxy" || rec.Header().Get("X-Upstream-Echo") != "pong" {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("upstream CORS headers must be dropped")
	}
}

func TestUpstreamDownIsReal502NotFabricated(t *testing.T) {
	p, _ := New("http://127.0.0.1:1")
	for _, path := range []string{"/orgs/o/projects/p/domains", "/orgs/o/projects/p/logs", "/orgs/o/projects/p/builds", "/orgs/o/addons"} {
		rec := serve(t, p, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("%s: want 502, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_gateway"`) || strings.Contains(rec.Body.String(), "[]") {
			t.Fatalf("%s: body %s", path, rec.Body)
		}
	}
}

func TestSignedProxyAddsInternalTokenOnlyWhenAsked(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	var got http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer up.Close()
	plain, _ := New(up.URL)
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Internal-Token", "client-forged")
	req.Header.Set("X-JP-Client-IP", "1.1.1.1")
	serve(t, plain, req)
	if got.Get("X-Internal-Token") != "" || got.Get("X-JP-Client-IP") != "" {
		t.Fatalf("unsigned proxy must not forward internal headers: %v", got)
	}
	signed, _ := New(up.URL, Signed())
	req = httptest.NewRequest("GET", "/x", nil).WithContext(WithClientIP(context.Background(), "198.51.100.1"))
	serve(t, signed, req)
	if got.Get("X-Internal-Token") != "internal-token-for-tests" || got.Get("X-JP-Client-IP") != "198.51.100.1" {
		t.Fatalf("signed proxy headers: %v", got)
	}
}

func TestStreamingIsFlushedNotBuffered(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("line-1\n"))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("line-2\n"))
	}))
	defer up.Close()
	p, _ := New(up.URL)
	gw := httptest.NewServer(p)
	defer gw.Close()
	resp, err := http.Get(gw.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- l
	}()
	select {
	case l := <-first:
		if l != "line-1\n" {
			t.Fatalf("got %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk was buffered until upstream finished")
	}
	close(release)
}
