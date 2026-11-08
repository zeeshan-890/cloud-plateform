package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func key32(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestEnvelopeEncryptDecrypt(t *testing.T) {
	env, err := NewEnvelope(Options{})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("postgres://user:super_secret_pw@db.internal:5432/production")
	ct, nonce, err := env.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, secret) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := env.Decrypt(ct, nonce)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("roundtrip: %v %q", err, got)
	}
	tampered := append([]byte(nil), ct...)
	tampered[0] ^= 0xFF
	if _, err := env.Decrypt(tampered, nonce); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err := env.Decrypt(ct, []byte("short")); err == nil {
		t.Fatal("bad nonce accepted")
	}
}

func TestProductionRefusesWeakKeys(t *testing.T) {
	bad := []string{"", DefaultDevKey, "a-passphrase-not-base64-or-hex-but-long!!", "short", key32(1)[:20], hex.EncodeToString(make([]byte, 16))}
	for _, k := range bad {
		if _, err := NewEnvelope(Options{Production: true, Key: k}); err == nil {
			t.Errorf("production accepted key %q", k)
		}
	}
	if _, err := NewEnvelope(Options{Production: true, Key: key32(7)}); err != nil {
		t.Errorf("base64(32) rejected: %v", err)
	}
	if _, err := NewEnvelope(Options{Production: true, Key: hex.EncodeToString(bytes.Repeat([]byte{9}, 32))}); err != nil {
		t.Errorf("hex(32) rejected: %v", err)
	}
	// dev may use passphrases
	if _, err := NewEnvelope(Options{Key: "any passphrase"}); err != nil {
		t.Errorf("dev passphrase rejected: %v", err)
	}
}

func TestNewFromEnvProduction(t *testing.T) {
	t.Setenv("JP_ENV", "production")
	t.Setenv("SECRETS_MASTER_KEY", "")
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("empty key must be refused in production")
	}
	t.Setenv("SECRETS_MASTER_KEY", DefaultDevKey)
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("dev key must be refused in production")
	}
	t.Setenv("SECRETS_MASTER_KEY", key32(3))
	t.Setenv("SECRETS_KEY_VERSION", "2")
	e, err := NewFromEnv()
	if err != nil || e.KeyVersion != 2 {
		t.Fatalf("e=%v err=%v", e, err)
	}
}

func TestRotationWithPreviousKey(t *testing.T) {
	oldEnv, _ := NewEnvelope(Options{Production: true, Key: key32(1), Version: 1})
	ct, nonce, _ := oldEnv.Encrypt([]byte("v1-secret"))

	newEnv, err := NewEnvelope(Options{Production: true, Key: key32(2), Version: 2, PreviousKey: key32(1), PreviousVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := newEnv.DecryptVersion(ct, nonce, 1)
	if err != nil || string(got) != "v1-secret" {
		t.Fatalf("previous-key fallback failed: %v", err)
	}
	if got, err := newEnv.Decrypt(ct, nonce); err != nil || string(got) != "v1-secret" {
		t.Fatalf("unversioned fallback failed: %v", err)
	}
	// re-encrypt under the new key
	ct2, n2, _ := newEnv.Encrypt(got)
	if p, err := newEnv.DecryptVersion(ct2, n2, 2); err != nil || string(p) != "v1-secret" {
		t.Fatal("re-encrypted value unreadable")
	}
	// without the previous key the old row is unreadable
	only, _ := NewEnvelope(Options{Production: true, Key: key32(2), Version: 2})
	if _, err := only.DecryptVersion(ct, nonce, 1); err == nil {
		t.Fatal("old row readable without the previous key")
	}
	if _, err := NewEnvelope(Options{Key: key32(1), Version: 1, PreviousKey: key32(2), PreviousVersion: 1}); err == nil {
		t.Fatal("same version for both keys must be refused")
	}
}

func TestHint(t *testing.T) {
	for in, want := range map[string]string{"": "****", "abc": "****", "secret_password_1234": "****1234", "abcdefg": "****defg"} {
		if got := Hint(in); got != want {
			t.Errorf("Hint(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(Hint("topsecretvalue9999"), "topsecret") {
		t.Fatal("hint leaks value")
	}
}
