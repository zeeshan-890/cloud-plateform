package dnscheck

import (
	"context"
	"testing"
)

func TestVerifyStub(t *testing.T) {
	ctx := context.Background()
	res := Verify(ctx, "app.example.com", "cname", "token-1", "cname.jp.cloud", true)

	if !res.OK {
		t.Fatalf("expected stubbed check to be OK")
	}
	if !res.Stubbed {
		t.Errorf("expected Stubbed flag to be true")
	}
	if res.Method != "cname" {
		t.Errorf("expected cname method, got %s", res.Method)
	}
}

func TestVerifyUnknownMethod(t *testing.T) {
	ctx := context.Background()
	res := Verify(ctx, "app.example.com", "unknown_method", "token-1", "target", false)

	if res.OK {
		t.Fatal("expected unknown method to fail")
	}
	if res.Detail != "unknown verification method" {
		t.Errorf("expected unknown verification method detail, got %s", res.Detail)
	}
}
