package totp

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPGenerationAndValidation(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}
	if len(secret) < 16 {
		t.Fatalf("secret too short: %s", secret)
	}

	now := time.Now().UTC()
	code, err := GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateCode failed: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got: %s", code)
	}

	// Immediate validation
	if !Validate(code, secret, now) {
		t.Fatalf("expected code %s to be valid at %v", code, now)
	}

	// Validation within 20s drift (same window)
	if !Validate(code, secret, now.Add(20*time.Second)) {
		t.Fatal("expected code to be valid with 20s drift")
	}

	// Invalid code test
	if Validate("000000", secret, now) && code != "000000" {
		t.Fatal("invalid code should not validate")
	}

	// Short code test
	if Validate("123", secret, now) {
		t.Fatal("short code should not validate")
	}
}

func TestKeyURI(t *testing.T) {
	uri := KeyURI("admin@example.com", "JP Cloud", "JBSWY3DPEHPK3PXP")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("expected otpauth:// prefix, got: %s", uri)
	}
	if !strings.Contains(uri, "secret=JBSWY3DPEHPK3PXP") {
		t.Errorf("expected secret in query, got: %s", uri)
	}
	if !strings.Contains(uri, "issuer=JP+Cloud") {
		t.Errorf("expected issuer in query, got: %s", uri)
	}
}

func TestGenerateBackupCodes(t *testing.T) {
	codes, err := GenerateBackupCodes(8)
	if err != nil {
		t.Fatalf("GenerateBackupCodes failed: %v", err)
	}
	if len(codes) != 8 {
		t.Fatalf("expected 8 codes, got %d", len(codes))
	}
	for _, c := range codes {
		if len(c) != 9 || c[4] != '-' { // format xxxx-xxxx
			t.Errorf("invalid backup code format: %s", c)
		}
	}
}
