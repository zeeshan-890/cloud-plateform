package handlers

import (
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"example.com", "example-com"},
		{"Sub.Domain.Org", "sub-domain-org"},
		{"my-host", "my-host"},
	}
	for _, tc := range cases {
		got := sanitize(tc.in)
		if got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "apple", "banana"); got != "apple" {
		t.Errorf("firstNonEmpty returned %q, want apple", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("firstNonEmpty returned %q, want empty", got)
	}
}
