package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// DefaultDevKey is used when SECRETS_MASTER_KEY is unset (simulate / local only).
const DefaultDevKey = "jp-dev-secrets-master-key-32b!!"

var ErrInvalidKey = errors.New("invalid SECRETS_MASTER_KEY")

// Envelope encrypts secrets at rest with AES-256-GCM using a master key from env.
type Envelope struct {
	gcm        cipher.AEAD
	KeyVersion int
}

// NewFromEnv loads SECRETS_MASTER_KEY (base64, hex, or raw). Falls back to DefaultDevKey.
func NewFromEnv() (*Envelope, error) {
	raw := strings.TrimSpace(os.Getenv("SECRETS_MASTER_KEY"))
	if raw == "" {
		raw = DefaultDevKey
	}
	key, err := deriveKey(raw)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Envelope{gcm: gcm, KeyVersion: 1}, nil
}

func deriveKey(raw string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	// Hash arbitrary-length secrets to 32 bytes (dev-friendly).
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

// Encrypt returns ciphertext and nonce.
func (e *Envelope) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, e.gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = e.gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt recovers plaintext.
func (e *Envelope) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) != e.gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce size")
	}
	return e.gcm.Open(nil, nonce, ciphertext, nil)
}

// Hint returns a safe display fragment (never the full value).
func Hint(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return "****"
	}
	if len(v) <= 4 {
		return "****"
	}
	return "****" + v[len(v)-4:]
}
