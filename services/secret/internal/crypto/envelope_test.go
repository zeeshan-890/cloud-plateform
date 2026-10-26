package crypto

import (
	"bytes"
	"testing"
)

func TestEnvelopeEncryptDecrypt(t *testing.T) {
	env, err := NewFromEnv()
	if err != nil {
		t.Fatalf("failed to init envelope: %v", err)
	}

	secret := []byte("postgres://user:super_secret_pw@db.internal:5432/production")
	ciphertext, nonce, err := env.Encrypt(secret)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}
	if bytes.Equal(ciphertext, secret) {
		t.Fatal("ciphertext must not match plaintext")
	}

	decrypted, err := env.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}
	if !bytes.Equal(decrypted, secret) {
		t.Fatalf("expected %s, got %s", string(secret), string(decrypted))
	}

	// Tampered ciphertext test (AEAD integrity)
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] ^= 0xFF
	if _, err := env.Decrypt(tampered, nonce); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}

	// Invalid nonce size
	if _, err := env.Decrypt(ciphertext, []byte("short")); err == nil {
		t.Fatal("expected error decrypting with invalid nonce size")
	}
}

func TestHint(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "****"},
		{"abc", "****"},
		{"secret_password_1234", "****1234"},
		{"abcdefg", "****defg"},
	}

	for _, tt := range tests {
		got := Hint(tt.input)
		if got != tt.want {
			t.Errorf("Hint(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
