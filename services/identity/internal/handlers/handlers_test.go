package handlers

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jp-cloud/identity/internal/store"
)

func TestHashToken(t *testing.T) {
	token := "jp_pat_test1234567890abcdef"
	hash1 := hashToken(token)
	hash2 := hashToken(token)

	if hash1 == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash1 != hash2 {
		t.Fatal("hash must be deterministic")
	}
	if hash1 == token {
		t.Fatal("hash must not equal raw token")
	}
}

func TestRandomToken(t *testing.T) {
	tok1, err := randomToken(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tok2, err := randomToken(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok1) != 32 { // 16 bytes = 32 hex chars
		t.Fatalf("expected 32 hex characters, got %d", len(tok1))
	}
	if tok1 == tok2 {
		t.Fatal("two random tokens must not match")
	}
}

func TestPublicUserZeroPasswordLeak(t *testing.T) {
	user := &store.User{
		ID:           "u-123",
		Email:        "alice@example.com",
		Name:         "Alice",
		PasswordHash: "$2a$10$supersecretbcrypthashthatmustneverbeexposed",
		CreatedAt:    time.Now(),
	}

	pub := publicUser(user)
	if _, ok := pub["password_hash"]; ok {
		t.Fatal("CRITICAL: password_hash leaked in publicUser map!")
	}
	if pub["email"] != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %v", pub["email"])
	}
	if pub["id"] != "u-123" {
		t.Errorf("expected id u-123, got %v", pub["id"])
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.50:54321"

	if ip := clientIP(req); ip != "192.168.1.50" {
		t.Errorf("expected 192.168.1.50, got %s", ip)
	}

	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	if ip := clientIP(req); ip != "203.0.113.195" {
		t.Errorf("expected 203.0.113.195 from XFF, got %s", ip)
	}
}
