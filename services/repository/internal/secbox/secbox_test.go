package secbox

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSealOpenAndProductionKeyRules(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	b, err := New(key, true)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := b.Seal("ghp_secret_token")
	if strings.Contains(string(enc), "ghp_") {
		t.Fatal("plaintext in ciphertext")
	}
	if got, err := b.Open(enc); err != nil || got != "ghp_secret_token" {
		t.Fatalf("%q %v", got, err)
	}
	enc[len(enc)-1] ^= 1
	if _, err := b.Open(enc); err == nil {
		t.Fatal("tampering undetected")
	}
	if e, _ := b.Seal(""); e != nil {
		t.Fatal("empty must stay empty")
	}
	for _, bad := range []string{"", "short", devKey} {
		if _, err := New(bad, true); err == nil {
			t.Errorf("production accepted %q", bad)
		}
	}
	if _, err := New("", false); err != nil {
		t.Fatalf("dev default rejected: %v", err)
	}
}
