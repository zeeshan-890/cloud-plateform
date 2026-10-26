package jwtutil

import (
	"testing"
	"time"
)

func TestIssueAndParseJWT(t *testing.T) {
	mgr := NewManager("test-super-secret-key-32-chars-ok", 15*time.Minute, 7*24*time.Hour)

	token, exp, err := mgr.IssueAccess("user-123", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if exp.Before(time.Now()) {
		t.Fatal("expected exp to be in the future")
	}

	claims, err := mgr.Parse(token)
	if err != nil {
		t.Fatalf("failed to parse valid token: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("expected UserID user-123, got %s", claims.UserID)
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("expected Email alice@example.com, got %s", claims.Email)
	}
	if claims.Type != "access" {
		t.Errorf("expected Type access, got %s", claims.Type)
	}
}

func TestExpiredToken(t *testing.T) {
	mgr := NewManager("test-super-secret-key-32-chars-ok", -1*time.Minute, -1*time.Minute)

	token, _, err := mgr.IssueAccess("user-123", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = mgr.Parse(token)
	if err != ErrExpiredToken {
		t.Fatalf("expected ErrExpiredToken, got %v", err)
	}
}

func TestInvalidSignature(t *testing.T) {
	mgr1 := NewManager("key-1-abcdefghijklmnopqrstuvwxyz", 15*time.Minute, time.Hour)
	mgr2 := NewManager("key-2-abcdefghijklmnopqrstuvwxyz", 15*time.Minute, time.Hour)

	token, _, _ := mgr1.IssueAccess("user-1", "user@example.com", "User")
	_, err := mgr2.Parse(token)
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}
