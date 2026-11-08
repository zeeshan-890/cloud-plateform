package internalauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }

func TestRequire(t *testing.T) {
	t.Setenv("JP_ENV", "production")
	t.Setenv("INTERNAL_TOKEN", "tok-0123456789")
	h := Require(http.HandlerFunc(ok))
	req := httptest.NewRequest("GET", "/internal/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("missing token: %d", rec.Code)
	}
	req.Header.Set(Header, "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	req.Header.Set(Header, "tok-0123456789")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !Valid(req) {
		t.Fatalf("right token: %d", rec.Code)
	}
}

func TestFailsClosedInProductionWithoutToken(t *testing.T) {
	t.Setenv("JP_ENV", "production")
	t.Setenv("INTERNAL_TOKEN", "")
	rec := httptest.NewRecorder()
	Require(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	if Valid(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("Valid must be false without a configured token")
	}
}

func TestOpenInDevelopmentAndClientSigns(t *testing.T) {
	t.Setenv("JP_ENV", "development")
	t.Setenv("INTERNAL_TOKEN", "")
	rec := httptest.NewRecorder()
	Require(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("dev mode: %d", rec.Code)
	}
	t.Setenv("INTERNAL_TOKEN", "signme")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get(Header) }))
	defer srv.Close()
	resp, err := NewClient(0).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "signme" {
		t.Fatalf("client must sign requests, got %q", got)
	}
}
