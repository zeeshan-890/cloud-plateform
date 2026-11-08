package hostname

import (
	"errors"
	"strings"
	"testing"
)

func pol() Policy {
	return Policy{RootDomain: "jp.example.com", ManagementDomain: "panel.example.org", PreviewBase: "preview.jp.example.com"}
}

func TestValidateCustom(t *testing.T) {
	long63 := strings.Repeat("a", 63)
	long64 := strings.Repeat("a", 64)
	tooLong := strings.Repeat(long63+".", 4) + "com" // > 253
	cases := []struct {
		name    string
		host    string
		wantErr error // nil = valid; errAny = any error
	}{
		{"simple", "example.com", nil},
		{"subdomain", "app.shop.example.co.uk", nil},
		{"digits-hyphen", "my-app-2.example.io", nil},
		{"punycode", "xn--bcher-kva.example", nil},
		{"label 63", long63 + ".com", nil},
		{"label 64", long64 + ".com", ErrFormat},
		{"too long", tooLong, ErrTooLong},
		{"empty", "", ErrEmpty},
		{"single label", "localhost", ErrFormat},
		{"uppercase", "Example.com", ErrFormat},
		{"unicode idn", "bücher.example", ErrFormat},
		{"emoji", "😀.example", ErrFormat},
		{"leading hyphen", "-a.example.com", ErrFormat},
		{"trailing hyphen", "a-.example.com", ErrFormat},
		{"double dot", "a..example.com", ErrFormat},
		{"leading dot", ".example.com", ErrFormat},
		{"trailing dot", "example.com.", ErrFormat},
		{"underscore", "a_b.example.com", ErrFormat},
		{"space", "a b.example.com", ErrFormat},
		{"ipv4", "1.2.3.4", ErrIP},
		{"ipv6", "::1", ErrIP},
		{"ipv4 mapped", "10.0.0.1", ErrIP},
		{"numeric tld", "example.123", ErrIP},
		{"wildcard", "*.example.com", ErrWildcard},
		{"backtick injection", "a`) || Host(`b.example.com", ErrFormat},
		{"rule injection", "x.com`) || HostRegexp(`.*", ErrWildcard},
		{"yaml newline injection", "a.example.com\nhttp:\n  routers:", ErrFormat},
		{"yaml comment injection", "a.example.com # pwn", ErrFormat},
		{"crlf", "a.example.com\r\nX: y", ErrFormat},
		{"null byte", "a.example.com\x00", ErrFormat},
		{"path", "a.example.com/../x", ErrFormat},
		{"colon port", "a.example.com:8080", ErrFormat},
		{"at sign", "user@a.example.com", ErrFormat},
		{"localhost tld", "app.localhost", ErrInternal},
		{"local tld", "printer.local", ErrInternal},
		{"internal tld", "db.internal", ErrInternal},
		{"root apex", "jp.example.com", ErrReserved},
		{"under root", "foo.jp.example.com", ErrReserved},
		{"www under root", "www.jp.example.com", ErrReserved},
		{"api under root", "api.jp.example.com", ErrReserved},
		{"suffix lookalike ok", "evil-jp.example.com", nil},
		{"root as part of other", "jp.example.com.evil.net", nil},
		{"management", "panel.example.org", ErrReserved},
		{"under management", "x.panel.example.org", ErrReserved},
		{"preview base", "preview.jp.example.com", ErrReserved},
		{"under preview", "pr-1.preview.jp.example.com", ErrReserved},
		{"consecutive hyphen non-idn", "ab--cd.example.com", ErrFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pol().ValidateCustom(tc.host)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateCustom(%q) unexpected error: %v", tc.host, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateCustom(%q) accepted, want %v", tc.host, tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateCustom(%q) = %v, want %v", tc.host, err, tc.wantErr)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize("  Example.COM. "); got != "example.com" {
		t.Fatalf("got %q", got)
	}
	if err := pol().ValidateCustom(Normalize("  Example.COM. ")); err != nil {
		t.Fatal(err)
	}
}

func TestInternalTLDAllowedInDev(t *testing.T) {
	p := pol()
	p.AllowInternalTLDs = true
	if err := p.ValidateCustom("app.localhost"); err != nil {
		t.Fatalf("dev policy should allow .localhost: %v", err)
	}
	if err := p.ValidateCustom("jp.example.com"); err == nil {
		t.Fatal("apex must stay reserved in dev")
	}
}

func TestValidateSystem(t *testing.T) {
	p := pol()
	ok := []string{"shop-acme.jp.example.com", "a-b-c.jp.example.com"}
	for _, h := range ok {
		if err := p.ValidateSystem(h); err != nil {
			t.Errorf("%q: %v", h, err)
		}
	}
	bad := []string{"shop.acme.jp.example.com", "shop-acme.other.com", "www.jp.example.com", "api.jp.example.com", "jp.example.com", "Shop.jp.example.com"}
	for _, h := range bad {
		if err := p.ValidateSystem(h); err == nil {
			t.Errorf("%q should be rejected", h)
		}
	}
}

func TestValidatePreview(t *testing.T) {
	p := pol()
	if w, err := p.ValidatePreview("pr-1-abc12345.preview.jp.example.com"); err != nil || w {
		t.Fatalf("plain preview: w=%v err=%v", w, err)
	}
	if w, err := p.ValidatePreview("*.preview.jp.example.com"); err != nil || !w {
		t.Fatalf("wildcard preview: w=%v err=%v", w, err)
	}
	for _, h := range []string{"example.com", "preview.jp.example.com", "evilpreview.jp.example.com", "*.example.com", "a.*.preview.jp.example.com", "x`.preview.jp.example.com", "*.*.preview.jp.example.com"} {
		if _, err := p.ValidatePreview(h); err == nil {
			t.Errorf("preview %q must be rejected", h)
		}
	}
	if _, err := (Policy{}).ValidatePreview("a.b.com"); err == nil {
		t.Error("preview without PREVIEW_BASE_DOMAIN must be rejected")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"My App!": "my-app", "  --x--": "x", "A_B.c": "a-b-c", "ü": ""} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
	}
}
