// Package secretbox provides AES-256-GCM sealing for small secrets stored in the database
// (TOTP seeds, platform settings). Keys are derived from a master secret per purpose.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "enc:v1:"

// ErrNotSealed is returned by Open for values that are not in sealed form.
var ErrNotSealed = errors.New("secretbox: value is not sealed")

// DeriveKey derives a 32-byte key from master for purpose (HMAC-SHA256 as a PRF).
func DeriveKey(master, purpose string) []byte {
	m := hmac.New(sha256.New, []byte(master))
	m.Write([]byte("jp-cloud/secretbox/v1/" + purpose))
	return m.Sum(nil)
}

// IsSealed reports whether v looks like a sealed value.
func IsSealed(v string) bool { return strings.HasPrefix(v, prefix) }

// Seal encrypts plaintext. aad (e.g. the owning user id) is authenticated but not stored.
func Seal(key []byte, plaintext, aad string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return prefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decrypts a value produced by Seal.
func Open(key []byte, sealed, aad string) (string, error) {
	if !IsSealed(sealed) {
		return "", ErrNotSealed
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("secretbox: ciphertext too short")
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(aad))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("secretbox: key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
