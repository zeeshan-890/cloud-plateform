package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestMapCommitState(t *testing.T) {
	cases := map[string]string{
		"pending":  "pending",
		"success":  "success",
		"ready":    "success",
		"failure":  "failure",
		"failed":   "failure",
		"error":    "failure",
		"":         "pending",
	}
	for in, want := range cases {
		if got := mapCommitState(in); got != want {
			t.Fatalf("mapCommitState(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNewAndJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemData := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
	c, err := New(12345, "jp-test", pemData, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Configured() {
		t.Fatal("expected configured")
	}
	tok, err := c.appJWT()
	if err != nil || tok == "" {
		t.Fatalf("jwt: %v %q", err, tok)
	}
	url := c.InstallURL("abc")
	if url != "https://github.com/apps/jp-test/installations/new?state=abc" {
		t.Fatalf("install url: %s", url)
	}
}

func TestNewFromEnvNotConfigured(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "")
	_, err := NewFromEnv(nil)
	if err != ErrNotConfigured {
		t.Fatalf("want ErrNotConfigured got %v", err)
	}
}
