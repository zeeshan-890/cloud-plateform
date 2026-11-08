package redact

import (
	"strings"
	"testing"
)

func TestRedactsSecrets(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	cases := []struct{ name, in, secret string }{
		{"bearer", "GET /api Authorization: Bearer abcdef1234567890abcdef", "abcdef1234567890abcdef"},
		{"bearer inline", "retry with bearer sk_live_9f8e7d6c5b4a3210", "sk_live_9f8e7d6c5b4a3210"},
		{"url creds", "connecting to postgres://app:Sup3rS3cret@addon-db:5432/app", "Sup3rS3cret"},
		{"redis url", "REDIS_URL=redis://:pa55w0rd123@addon-cache:6379/0", "pa55w0rd123"},
		{"password kv", "login failed password=hunter2hunter2 user=bob", "hunter2hunter2"},
		{"json kv", `{"client_secret": "abc123XYZ789", "ok": true}`, "abc123XYZ789"},
		{"api key kv", "api_key: 1234567890abcdef", "1234567890abcdef"},
		{"jwt", "token was " + jwt, jwt},
		{"aws", "key AKIAIOSFODNN7EXAMPLE used", "AKIAIOSFODNN7EXAMPLE"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"google", "AIzaSyA-1234567890abcdefghijklmnopqrstuv", "AIzaSyA-1234567890abcdefghijklmnopqrstuv"},
		{"openai", "sk-proj-abcdefghijklmnop1234", "sk-proj-abcdefghijklmnop1234"},
		{"cookie", "Cookie: session=abcdef0123456789; theme=dark", "abcdef0123456789"},
		{"env dump", "NODE_ENV=production\nDATABASE_URL=postgres://u:p@h/db\nPORT=3000", "postgres://u:p@h/db"},
		{"docker inspect", `"Env": ["PATH=/usr/bin", "STRIPE_KEY=sk_test_51Habcdefghij"]`, "sk_test_51Habcdefghij"},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA7\n-----END RSA PRIVATE KEY-----", "MIIEowIBAAKCAQEA7"},
		{"hex token", "deploy key 0123456789abcdef0123456789abcdef01234567 set", "0123456789abcdef0123456789abcdef01234567"},
		{"internal token", "X-Internal-Token: s3cr3t-internal-value", "s3cr3t-internal-value"},
	}
	for _, c := range cases {
		got := Text(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: secret survived: %q -> %q", c.name, c.in, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s: nothing redacted: %q", c.name, got)
		}
	}
}

func TestKeepsUsefulContext(t *testing.T) {
	in := "Error: connect ECONNREFUSED 10.0.0.5:5432\n    at TCPConnectWrap.afterConnect (net.js:1141:16)\nnpm ERR! code ELIFECYCLE\nlistening on port 3000"
	if got := Text(in); got != in {
		t.Fatalf("useful diagnostics must survive redaction:\n%s", got)
	}
	if got := Text("commit 3f2a1b9 built in 12s"); !strings.Contains(got, "3f2a1b9") {
		t.Fatalf("short hashes must survive: %s", got)
	}
}

func TestUrlHostKept(t *testing.T) {
	got := Text("postgres://app:secretpw@addon-db:5432/app")
	if !strings.Contains(got, "addon-db:5432/app") || strings.Contains(got, "secretpw") {
		t.Fatal(got)
	}
}

func TestTruncateKeepsTailAndUTF8(t *testing.T) {
	s := strings.Repeat("a", 100) + "éétail"
	got := Truncate(s, 8)
	if !strings.HasSuffix(got, "tail") || !strings.HasPrefix(got, "...[truncated]") {
		t.Fatal(got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatal("broke a UTF-8 sequence")
		}
	}
	if Truncate("short", 100) != "short" {
		t.Fatal("short text unchanged")
	}
}

func TestPrepareRedactsBeforeTruncating(t *testing.T) {
	secret := "password=" + strings.Repeat("Z", 60)
	in := strings.Repeat("x", 50) + secret
	if got := Prepare(in, 80); strings.Contains(got, "ZZZZZ") {
		t.Fatalf("secret leaked through truncation: %s", got)
	}
}
