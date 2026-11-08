// Package crypt encrypts per-org API keys at rest with AES-256-GCM.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
)

// ErrNoKey means no encryption key is configured: per-org keys cannot be stored.
var ErrNoKey = errors.New("AI_ENC_KEY (or SECRETS_MASTER_KEY) is not configured")

// Box seals and opens values; the org id is bound as additional authenticated data.
type Box struct{ aead cipher.AEAD }

// New derives an AES-256-GCM key from secret (SHA-256 with a domain separator).
func New(secret string) (*Box, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrNoKey
	}
	k := sha256.Sum256([]byte("jp-ai-config-v1|" + secret))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: g}, nil
}

// FromEnv uses AI_ENC_KEY, else SECRETS_MASTER_KEY.
func FromEnv() (*Box, error) {
	s := os.Getenv("AI_ENC_KEY")
	if strings.TrimSpace(s) == "" {
		s = os.Getenv("SECRETS_MASTER_KEY")
	}
	return New(s)
}

// Seal returns nonce||ciphertext.
func (b *Box) Seal(orgID, plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(orgID)), nil
}

// Open decrypts a value sealed for the same org.
func (b *Box) Open(orgID string, sealed []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	pt, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(orgID))
	if err != nil {
		return "", errors.New("cannot decrypt stored key (wrong encryption key or tampered data)")
	}
	return string(pt), nil
}
