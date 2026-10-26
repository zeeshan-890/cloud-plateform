package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	StepDuration = 30 * time.Second
	Digits       = 6
)

var b32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret creates a new random 160-bit (20 bytes) Base32 encoded secret.
func GenerateSecret() (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return b32Encoding.EncodeToString(bytes), nil
}

// GenerateBackupCodes generates n cryptographically random alphanumeric backup codes.
func GenerateBackupCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		bytes := make([]byte, 5)
		if _, err := rand.Read(bytes); err != nil {
			return nil, err
		}
		// Format: xxxx-xxxx
		s := fmt.Sprintf("%x", bytes)
		codes[i] = fmt.Sprintf("%s-%s", s[:4], s[4:8])
	}
	return codes, nil
}

// KeyURI generates an otpauth:// URI for authenticator apps (Google Authenticator, 1Password, etc.).
func KeyURI(account, issuer, secret string) string {
	label := url.PathEscape(fmt.Sprintf("%s:%s", issuer, account))
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s?%s", label, q.Encode())
}

// GenerateCode computes the 6-digit TOTP code for a secret at a given time.
func GenerateCode(secret string, t time.Time) (string, error) {
	key, err := b32Encoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("invalid base32 secret: %w", err)
	}

	counter := uint64(t.Unix() / int64(StepDuration.Seconds()))
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)

	// Dynamic truncation (RFC 4226)
	offset := h[len(h)-1] & 0x0f
	code := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code %= 1000000

	return fmt.Sprintf("%06d", code), nil
}

// Validate checks whether a code is valid for the secret at time t (with +/- 1 step drift tolerance).
func Validate(code, secret string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return false
	}

	// Check current, previous, and next intervals
	for delta := -1; delta <= 1; delta++ {
		checkTime := t.Add(time.Duration(delta) * StepDuration)
		expected, err := GenerateCode(secret, checkTime)
		if err == nil && hmac.Equal([]byte(code), []byte(expected)) {
			return true
		}
	}
	return false
}
