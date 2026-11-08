package redact

import (
	"bytes"
	"strings"
	"testing"
)

func TestStringScrubsCredentials(t *testing.T) {
	r := New("super-literal-secret")
	cases := []string{
		"cloning https://x-access-token:ghs_abcdefghijklmnopqrstuvwxyz0123456789@github.com/o/r.git",
		"fatal: https://user:p4ssw0rd@example.com/x failed",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked",
		"github_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz",
		"Authorization: Bearer abcdef123456.ghijkl",
		"my super-literal-secret is here",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabcdef\n-----END OPENSSH PRIVATE KEY-----",
		`password="hunter2hunter2"`,
		"API_KEY=abcd1234efgh5678",
	}
	for _, c := range cases {
		out := r.String(c)
		for _, bad := range []string{"ghs_abc", "p4ssw0rd", "ghp_abc", "github_pat_11", "abcdef123456", "super-literal", "BEGIN OPENSSH", "hunter2", "abcd1234efgh"} {
			if strings.Contains(out, bad) {
				t.Errorf("%q still contains %q -> %q", c, bad, out)
			}
		}
	}
	if got := r.String("plain build output: step 1/5"); got != "plain build output: step 1/5" {
		t.Errorf("clean text altered: %q", got)
	}
}

func TestWriterCatchesSecretsSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	r := New("tok-1234567890")
	w := r.Writer(&out)
	_, _ = w.Write([]byte("push to tok-12345"))
	_, _ = w.Write([]byte("67890 now\nnext line\npartial"))
	_ = w.Close()
	if strings.Contains(out.String(), "tok-1234567890") {
		t.Fatalf("leaked: %q", out.String())
	}
	if !strings.Contains(out.String(), "next line") || !strings.HasSuffix(out.String(), "partial") {
		t.Fatalf("lines lost: %q", out.String())
	}
}
