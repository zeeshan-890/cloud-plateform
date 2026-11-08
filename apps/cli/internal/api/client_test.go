package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jp-cloud/jp/internal/config"
)

func TestTwoStepMFALogin(t *testing.T) {
	var bodies []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["mfa_token"] == "" {
			_, _ = w.Write([]byte(`{"mfa_required":true,"mfa_token":"tok-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref"}`))
	}))
	defer srv.Close()
	c := New(&config.Config{APIURL: srv.URL})

	res, err := c.Login("a@b.c", "pw")
	if err != nil || !res.MFARequired || res.MFAToken != "tok-1" || res.AccessToken != "" {
		t.Fatalf("step 1: %+v %v", res, err)
	}
	res, err = c.LoginMFA("tok-1", "123456")
	if err != nil || res.AccessToken != "acc" {
		t.Fatalf("step 2: %+v %v", res, err)
	}
	if _, resent := bodies[1]["password"]; resent {
		t.Fatal("the password must not be resent in the second step")
	}
	if bodies[1]["mfa_token"] != "tok-1" || bodies[1]["totp_code"] != "123456" {
		t.Fatalf("second step body: %v", bodies[1])
	}
}

func TestLogoutSendsRefreshTokenAndSurfacesFailure(t *testing.T) {
	var got map[string]string
	status := 204
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(&config.Config{APIURL: srv.URL, AccessToken: "acc", RefreshToken: "ref"})
	if err := c.Logout(); err != nil || got["refresh_token"] != "ref" {
		t.Fatalf("logout: %v %v", err, got)
	}
	status = 503
	if err := c.Logout(); err == nil {
		t.Fatal("a failed server-side logout must be reported")
	}
}

func TestErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		e    APIError
		want string
	}{
		{APIError{Status: 502, Message: "upstream service unavailable"}, "service unavailable"},
		{APIError{Status: 429}, "too many requests"},
		{APIError{Status: 403, Message: "admin role or higher required"}, "permission denied: admin"},
		{APIError{Status: 404, Code: "not_found", Message: "x"}, "404/not_found"},
	} {
		if got := tc.e.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%d: %q does not contain %q", tc.e.Status, got, tc.want)
		}
	}
}

func TestResponseSizeIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 1<<20)))
	}))
	defer srv.Close()
	c := New(&config.Config{APIURL: srv.URL, AccessToken: "x"})
	if raw, err := c.doRaw(http.MethodGet, "/x", nil, true); err != nil || len(raw) > maxResponseBytes {
		t.Fatalf("raw=%d err=%v", len(raw), err)
	}
}

func TestConfigDriftUsesPostWithConfig(t *testing.T) {
	var method string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"drift":true,"method":"config-compare","details":["runtime: differs"]}`))
	}))
	defer srv.Close()
	c := New(&config.Config{APIURL: srv.URL, AccessToken: "x"})
	out, err := c.ConfigDrift("o", "p", map[string]any{"runtime": "go"})
	if err != nil || out["drift"] != true || method != "POST" || body["config"] == nil {
		t.Fatalf("%v %v %s %v", out, err, method, body)
	}
}
