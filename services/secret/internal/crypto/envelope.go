// Package crypto implements envelope encryption of secret values with AES-256-GCM.
//
// Key handling (one derivation only, see deriveKey):
//   - production (JP_ENV=production) requires SECRETS_MASTER_KEY to be set and to be
//     base64(32 bytes) or hex(32 bytes); the dev key and arbitrary passphrases are refused.
//   - development / tests may omit it (a fixed dev key is used) or use any passphrase.
//   - SECRETS_KEY_VERSION (default 1) labels the current key; SECRETS_MASTER_KEY_PREVIOUS
//     (+ SECRETS_KEY_VERSION_PREVIOUS, default current-1) keeps old rows readable while they
//     are re-encrypted with the rotate endpoint.
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
	"strconv"
	"strings"
)

// DefaultDevKey is only accepted outside production.
const DefaultDevKey = "jp-dev-secrets-master-key-32b!!"

var (
	ErrInvalidKey = errors.New("invalid SECRETS_MASTER_KEY")
	ErrNoKey      = errors.New("no key for key_version")
)

type keyring struct {
	version int
	gcm     cipher.AEAD
}

// Envelope encrypts with the current key and decrypts with current or previous.
type Envelope struct {
	cur        keyring
	prev       *keyring
	KeyVersion int
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Options configure NewEnvelope explicitly (tests).
type Options struct {
	Production      bool
	Key             string
	Version         int
	PreviousKey     string
	PreviousVersion int
}

// NewFromEnv reads the configuration from the environment.
func NewFromEnv() (*Envelope, error) {
	o := Options{
		Production:  strings.EqualFold(strings.TrimSpace(os.Getenv("JP_ENV")), "production"),
		Key:         strings.TrimSpace(os.Getenv("SECRETS_MASTER_KEY")),
		PreviousKey: strings.TrimSpace(os.Getenv("SECRETS_MASTER_KEY_PREVIOUS")),
		Version:     1,
	}
	if v := strings.TrimSpace(os.Getenv("SECRETS_KEY_VERSION")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("SECRETS_KEY_VERSION must be a positive integer")
		}
		o.Version = n
	}
	o.PreviousVersion = o.Version - 1
	if v := strings.TrimSpace(os.Getenv("SECRETS_KEY_VERSION_PREVIOUS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("SECRETS_KEY_VERSION_PREVIOUS must be a positive integer")
		}
		o.PreviousVersion = n
	}
	return NewEnvelope(o)
}

// NewEnvelope builds an Envelope from explicit options.
func NewEnvelope(o Options) (*Envelope, error) {
	key := o.Key
	if key == "" {
		if o.Production {
			return nil, fmt.Errorf("%w: SECRETS_MASTER_KEY is required in production (base64 or hex of 32 random bytes)", ErrInvalidKey)
		}
		key = DefaultDevKey
	}
	if o.Production && key == DefaultDevKey {
		return nil, fmt.Errorf("%w: the development key must not be used in production", ErrInvalidKey)
	}
	if o.Version < 1 {
		o.Version = 1
	}
	raw, err := deriveKey(key, o.Production)
	if err != nil {
		return nil, err
	}
	g, err := newGCM(raw)
	if err != nil {
		return nil, err
	}
	e := &Envelope{cur: keyring{version: o.Version, gcm: g}, KeyVersion: o.Version}
	if o.PreviousKey != "" {
		praw, err := deriveKey(o.PreviousKey, o.Production)
		if err != nil {
			return nil, fmt.Errorf("SECRETS_MASTER_KEY_PREVIOUS: %w", err)
		}
		pg, err := newGCM(praw)
		if err != nil {
			return nil, err
		}
		pv := o.PreviousVersion
		if pv < 1 {
			pv = o.Version - 1
		}
		if pv == o.Version {
			return nil, fmt.Errorf("previous key version must differ from the current one")
		}
		e.prev = &keyring{version: pv, gcm: pg}
	}
	return e, nil
}

// deriveKey is THE key derivation. base64(32) and hex(32) are accepted everywhere;
// any other string (hashed with SHA-256) only outside production.
func deriveKey(raw string, production bool) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if production {
		return nil, fmt.Errorf("%w: must be base64 or hex encoding of exactly 32 bytes", ErrInvalidKey)
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

// Encrypt returns ciphertext and nonce, sealed with the current key.
func (e *Envelope) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, e.cur.gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return e.cur.gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

// Decrypt opens data written under keyVersion (0 = unknown: try current then previous).
func (e *Envelope) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	return e.DecryptVersion(ciphertext, nonce, 0)
}

// DecryptVersion opens data written under keyVersion.
func (e *Envelope) DecryptVersion(ciphertext, nonce []byte, keyVersion int) ([]byte, error) {
	if len(nonce) != e.cur.gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce size")
	}
	try := func(k *keyring) ([]byte, error) { return k.gcm.Open(nil, nonce, ciphertext, nil) }
	switch {
	case keyVersion == e.cur.version:
		return try(&e.cur)
	case e.prev != nil && keyVersion == e.prev.version:
		return try(e.prev)
	case keyVersion != 0:
		// version unknown to us: last resort, try the other key (covers mislabeled rows)
		if p, err := try(&e.cur); err == nil {
			return p, nil
		}
		if e.prev != nil {
			if p, err := try(e.prev); err == nil {
				return p, nil
			}
		}
		return nil, ErrNoKey
	}
	if p, err := try(&e.cur); err == nil {
		return p, nil
	}
	if e.prev != nil {
		return try(e.prev)
	}
	return nil, errors.New("decrypt failed")
}

// Hint returns a safe display fragment (never the full value).
func Hint(value string) string {
	v := strings.TrimSpace(value)
	if len(v) <= 4 {
		return "****"
	}
	return "****" + v[len(v)-4:]
}
