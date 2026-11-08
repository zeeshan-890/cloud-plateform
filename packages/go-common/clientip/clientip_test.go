package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	r := New([]string{"10.0.0.0/8", "172.18.0.5"})
	cases := []struct {
		name, remote, xff, want string
	}{
		{"untrusted peer ignores xff", "203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},
		{"trusted peer uses xff", "10.1.2.3:80", "198.51.100.7", "198.51.100.7"},
		{"spoofed leftmost ignored", "10.1.2.3:80", "6.6.6.6, 198.51.100.7", "198.51.100.7"},
		{"chain through trusted proxies", "10.1.2.3:80", "198.51.100.7, 10.9.9.9", "198.51.100.7"},
		{"single ip proxy", "172.18.0.5:80", "198.51.100.8", "198.51.100.8"},
		{"no xff falls back to peer", "10.1.2.3:80", "", "10.1.2.3"},
		{"garbage xff falls back to peer", "10.1.2.3:80", "not-an-ip", "10.1.2.3"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := r.FromRequest(req); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestNoTrustedProxies(t *testing.T) {
	r := New(nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := r.FromRequest(req); got != "10.0.0.1" {
		t.Fatalf("got %q", got)
	}
}
