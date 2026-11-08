package secretbox

import "testing"

func TestSealOpen(t *testing.T) {
	key := DeriveKey("master", "totp")
	s, err := Seal(key, "JBSWY3DPEHPK3PXP", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(s) || s == "JBSWY3DPEHPK3PXP" {
		t.Fatal("not sealed")
	}
	pt, err := Open(key, s, "user-1")
	if err != nil || pt != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %v %q", err, pt)
	}
	if _, err := Open(key, s, "user-2"); err == nil {
		t.Fatal("aad mismatch must fail")
	}
	if _, err := Open(DeriveKey("other", "totp"), s, "user-1"); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := Open(key, "plain", ""); err != ErrNotSealed {
		t.Fatalf("expected ErrNotSealed, got %v", err)
	}
	if string(DeriveKey("m", "a")) == string(DeriveKey("m", "b")) {
		t.Fatal("purposes must derive different keys")
	}
}
