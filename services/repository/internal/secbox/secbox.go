// Package secbox encrypts credentials stored by the repository service (AES-256-GCM).
// REPO_ENC_KEY must be base64 or hex of 32 bytes in production; development accepts any
// passphrase (SHA-256) or falls back to a fixed dev key.
package secbox

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

const devKey = "jp-dev-repository-enc-key-32by!!"

// Box seals and opens small secrets.
type Box struct{ gcm cipher.AEAD }

// New builds a Box. In production an explicit, well-formed key is mandatory.
func New(key string, production bool) (*Box, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		if production {
			return nil, errors.New("REPO_ENC_KEY is required in production (base64 or hex of 32 random bytes)")
		}
		key = devKey
	}
	var raw []byte
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		raw = b
	} else if b, err := hex.DecodeString(key); err == nil && len(b) == 32 {
		raw = b
	} else if production {
		return nil, errors.New("REPO_ENC_KEY must be base64 or hex of exactly 32 bytes")
	} else {
		s := sha256.Sum256([]byte(key))
		raw = s[:]
	}
	if production && key == devKey {
		return nil, errors.New("the development key must not be used in production")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: g}, nil
}

// NewFromEnv reads REPO_ENC_KEY and JP_ENV.
func NewFromEnv() (*Box, error) {
	return New(os.Getenv("REPO_ENC_KEY"), strings.EqualFold(os.Getenv("JP_ENV"), "production"))
}

// Seal returns nonce||ciphertext. Empty input yields nil.
func (b *Box) Seal(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Open decrypts the output of Seal.
func (b *Box) Open(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	n := b.gcm.NonceSize()
	if len(data) < n+b.gcm.Overhead() {
		return "", fmt.Errorf("ciphertext too short")
	}
	p, err := b.gcm.Open(nil, data[:n], data[n:], nil)
	return string(p), err
}
