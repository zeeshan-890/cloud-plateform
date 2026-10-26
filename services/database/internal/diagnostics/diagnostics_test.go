package diagnostics

import (
	"strings"
	"testing"
)

func TestRedactQuery(t *testing.T) {
	raw := "SELECT id, email FROM users WHERE id = 42 AND email = 'alice@example.com' AND secret = 'my-super-secret-key'"
	redacted := RedactQuery(raw)

	if strings.Contains(redacted, "my-super-secret-key") {
		t.Errorf("secret leaked in redacted query: %s", redacted)
	}
	if strings.Contains(redacted, "alice@example.com") {
		t.Errorf("email literal leaked in redacted query: %s", redacted)
	}
	if strings.Contains(redacted, "42") {
		t.Errorf("numeric literal leaked in redacted query: %s", redacted)
	}
}

func TestGenerateFingerprint(t *testing.T) {
	q1 := "SELECT * FROM orders WHERE user_id = 100 AND status = 'pending'"
	q2 := "SELECT   *   FROM orders WHERE user_id = 9999   AND status = 'shipped'"

	fp1 := GenerateFingerprint(q1)
	fp2 := GenerateFingerprint(q2)

	if fp1 != fp2 {
		t.Errorf("expected fingerprints to match:\nfp1: %s\nfp2: %s", fp1, fp2)
	}
}
