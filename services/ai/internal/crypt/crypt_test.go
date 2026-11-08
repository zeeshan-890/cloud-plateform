package crypt

import (
	"strings"
	"testing"
)

func TestSealOpenRoundTripAndOrgBinding(t *testing.T) {
	b, err := New("master-secret")
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b.Seal("org-1", "AIza-super-secret-key")
	if strings.Contains(string(sealed), "super-secret") {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := b.Open("org-1", sealed)
	if err != nil || got != "AIza-super-secret-key" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := b.Open("org-2", sealed); err == nil {
		t.Fatal("a ciphertext must not open for another org")
	}
	other, _ := New("different-secret")
	if _, err := other.Open("org-1", sealed); err == nil {
		t.Fatal("wrong key must fail")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open("org-1", sealed); err == nil {
		t.Fatal("tampering must be detected")
	}
	s2, _ := b.Seal("org-1", "AIza-super-secret-key")
	s3, _ := b.Seal("org-1", "AIza-super-secret-key")
	if string(s2) == string(s3) {
		t.Fatal("nonce must be random")
	}
}

func TestNoKeyConfigured(t *testing.T) {
	t.Setenv("AI_ENC_KEY", "")
	t.Setenv("SECRETS_MASTER_KEY", "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("must fail without a key")
	}
	t.Setenv("SECRETS_MASTER_KEY", "abc")
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
}
