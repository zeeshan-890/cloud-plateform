package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeminiKeyInHeaderNotURLAndModelPath(t *testing.T) {
	var gotPath, gotQuery, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotKey = r.URL.Path, r.URL.RawQuery, r.Header.Get("x-goog-api-key")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"  hello  "}]}}]}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), GeminiBase: srv.URL}
	out, err := c.Chat(context.Background(), Config{Provider: ProviderGemini, Model: "gemini-2.0-flash", APIKey: "KEY-123456"}, "sys", "hi")
	if err != nil || out != "hello" {
		t.Fatalf("%q %v", out, err)
	}
	if gotKey != "KEY-123456" || strings.Contains(gotQuery, "KEY") || gotPath != "/v1beta/models/gemini-2.0-flash:generateContent" {
		t.Fatalf("path=%s query=%s key=%s", gotPath, gotQuery, gotKey)
	}
}

func TestErrorsNeverContainTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"error":"API key SUPERSECRETKEY is invalid"}`)
	}))
	defer srv.Close()
	// the provider echoing the key in its body is the provider's problem; transport errors must be scrubbed
	c := &Client{HTTP: &http.Client{Timeout: time.Second}, GeminiBase: "http://127.0.0.1:1/SUPERSECRETKEY"}
	_, err := c.Chat(context.Background(), Config{Provider: ProviderGemini, Model: "m1", APIKey: "SUPERSECRETKEY"}, "", "hi")
	if err == nil || strings.Contains(err.Error(), "SUPERSECRETKEY") {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenAICompatible(t *testing.T) {
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	out, err := c.Chat(context.Background(), Config{Provider: ProviderOpenAI, Model: "gpt-4o-mini", APIKey: "sk-test-1234", BaseURL: srv.URL + "/v1"}, "s", "u")
	if err != nil || out != "ok" || auth != "Bearer sk-test-1234" || path != "/v1/chat/completions" {
		t.Fatalf("%q %v %s %s", out, err, auth, path)
	}
}

func TestChatRejectsBadInput(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient}
	for _, cfg := range []Config{
		{Provider: ProviderGemini, Model: "m", APIKey: ""},
		{Provider: ProviderGemini, Model: "../x", APIKey: "k-123456"},
		{Provider: ProviderGemini, Model: "a/b", APIKey: "k-123456"},
		{Provider: "other", Model: "m", APIKey: "k-123456"},
	} {
		if _, err := c.Chat(context.Background(), cfg, "", "x"); err == nil {
			t.Errorf("%+v accepted", cfg)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	if u, err := ValidateBaseURL("", false); err != nil || u != DefaultOpenAIBase {
		t.Fatal(u, err)
	}
	for _, bad := range []string{"http://api.example.com", "ftp://x", "https://u:p@x.com", "https://x.com/v1?k=1", "//x.com", "https://"} {
		if _, err := ValidateBaseURL(bad, false); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := ValidateBaseURL("http://ollama.local:11434/v1", true); err != nil {
		t.Errorf("operator opt-in must allow http: %v", err)
	}
}

func TestSSRFProtectionBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "internal-secret") }))
	defer srv.Close()
	blocked := &Client{HTTP: NewHTTPClient(2*time.Second, false)}
	_, err := blocked.Chat(context.Background(), Config{Provider: ProviderOpenAI, Model: "m", APIKey: "k-123456", BaseURL: srv.URL}, "", "x")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback must be blocked, got %v", err)
	}
	allowed := &Client{HTTP: NewHTTPClient(2*time.Second, true)}
	_, err = allowed.Chat(context.Background(), Config{Provider: ProviderOpenAI, Model: "m", APIKey: "k-123456", BaseURL: srv.URL}, "", "x")
	if err != nil && strings.Contains(err.Error(), "blocked") {
		t.Fatalf("operator opt-in must allow private addresses: %v", err)
	}
}

func TestHeuristicLabelsItself(t *testing.T) {
	out := HeuristicExplain("npm ERR! code ELIFECYCLE\nECONNREFUSED")
	if !strings.Contains(out, "no AI model configured") || !strings.Contains(out, "npm install/build failed") {
		t.Fatal(out)
	}
}
