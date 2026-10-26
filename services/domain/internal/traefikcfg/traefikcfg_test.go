package traefikcfg

import (
	"os"
	"strings"
	"testing"
)

func TestWriteDomainAndRemove(t *testing.T) {
	tmpDir := t.TempDir()
	writer := New(tmpDir, "http://10.0.0.5:8000", "letsencrypt")

	file, err := writer.WriteDomain("api.example.com", "proj-99", true)
	if err != nil {
		t.Fatalf("WriteDomain failed: %v", err)
	}

	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("failed to read generated yaml: %v", err)
	}

	str := string(content)
	if !strings.Contains(str, "Host(`api.example.com`)") {
		t.Errorf("expected Host rule in YAML, got:\n%s", str)
	}
	if !strings.Contains(str, "certResolver: letsencrypt") {
		t.Errorf("expected certResolver in YAML, got:\n%s", str)
	}
	if !strings.Contains(str, "http://10.0.0.5:8000") {
		t.Errorf("expected backend URL in YAML, got:\n%s", str)
	}

	// Test removal
	if err := writer.RemoveDomain("api.example.com"); err != nil {
		t.Fatalf("RemoveDomain failed: %v", err)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("expected file %s to be removed", file)
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"example.com", "example-com"},
		{"*.example.com", "star-example-com"},
		{"API.Example.COM", "api-example-com"},
	}

	for _, tt := range tests {
		got := sanitize(tt.in)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
