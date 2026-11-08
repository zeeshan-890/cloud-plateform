// Package hostname implements strict hostname validation. Every hostname that ends up in
// a Traefik rule, a file name or an API response MUST pass through this package first.
package hostname

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Policy carries the platform-owned names that tenants must never claim.
type Policy struct {
	RootDomain       string // ROOT_DOMAIN, e.g. jp.example.com
	ManagementDomain string // MANAGEMENT_DOMAIN
	PreviewBase      string // PREVIEW_BASE_DOMAIN
	// AllowInternalTLDs permits *.localhost / *.local etc. (development only).
	AllowInternalTLDs bool
}

// reservedLabels are first labels directly under ROOT_DOMAIN that belong to the platform.
var reservedLabels = map[string]bool{
	"www": true, "api": true, "panel": true, "dashboard": true, "admin": true,
	"traefik": true, "registry": true, "mail": true, "smtp": true, "ns1": true, "ns2": true,
	"status": true, "auth": true, "console": true, "minio": true, "grafana": true,
}

var internalTLDs = map[string]bool{
	"localhost": true, "local": true, "internal": true, "lan": true, "arpa": true,
	"onion": true, "home": true, "corp": true, "invalid": true,
}

// Errors returned by Validate.
var (
	ErrEmpty    = errors.New("hostname is required")
	ErrTooLong  = errors.New("hostname exceeds 253 characters")
	ErrFormat   = errors.New("hostname must be a lowercase RFC 1123 name (a-z, 0-9, '-', dot separated, labels up to 63 chars)")
	ErrIP       = errors.New("IP addresses are not allowed; use a DNS name")
	ErrReserved = errors.New("hostname is reserved by the platform")
	ErrInternal = errors.New("internal or special-use domains are not allowed")
	ErrWildcard = errors.New("wildcard hostnames are not allowed")
)

// Normalize trims whitespace, a single trailing dot and lowercases ASCII. It does NOT
// try to convert unicode; callers must send punycode (xn--) names.
func Normalize(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}

// ValidSyntax checks RFC 1123 syntax only (no policy), lowercase required.
func ValidSyntax(h string) error {
	if h == "" {
		return ErrEmpty
	}
	if len(h) > 253 {
		return ErrTooLong
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return ErrIP
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%w: at least two labels required", ErrFormat)
	}
	for _, l := range labels {
		if err := validLabel(l); err != nil {
			return err
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return ErrIP // 1.2.3.4 style or numeric TLD
	}
	return nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validLabel(l string) error {
	if l == "" || len(l) > 63 {
		return ErrFormat
	}
	if l[0] == '-' || l[len(l)-1] == '-' {
		return ErrFormat
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return ErrFormat
		}
	}
	if len(l) >= 4 && l[2] == '-' && l[3] == '-' && !strings.HasPrefix(l, "xn--") {
		return ErrFormat
	}
	return nil
}

func (p Policy) lower(s string) string { return Normalize(s) }

// under reports whether h equals base or is a subdomain of it (label boundary aware).
func under(h, base string) bool {
	if base == "" {
		return false
	}
	return h == base || strings.HasSuffix(h, "."+base)
}

// ValidateCustom validates a tenant-supplied custom domain.
func (p Policy) ValidateCustom(h string) error {
	if strings.Contains(h, "*") {
		return ErrWildcard
	}
	if err := ValidSyntax(h); err != nil {
		return err
	}
	if !p.AllowInternalTLDs {
		tld := h[strings.LastIndexByte(h, '.')+1:]
		if internalTLDs[tld] {
			return ErrInternal
		}
	}
	root := p.lower(p.RootDomain)
	if root != "" {
		if h == root {
			return fmt.Errorf("%w: %s is the platform apex", ErrReserved, h)
		}
		if under(h, root) {
			// everything under the platform root is allocated by the platform itself.
			return fmt.Errorf("%w: names under %s are managed by the platform", ErrReserved, root)
		}
	}
	if m := p.lower(p.ManagementDomain); m != "" && under(h, m) {
		return fmt.Errorf("%w: management domain", ErrReserved)
	}
	if pb := p.lower(p.PreviewBase); pb != "" && under(h, pb) {
		return fmt.Errorf("%w: preview domains are allocated automatically", ErrReserved)
	}
	first := h[:strings.IndexByte(h, '.')]
	if reservedLabels[first] && root != "" && under(h, root) {
		return ErrReserved
	}
	return nil
}

// ValidateSystem validates a platform generated hostname (<label>.<ROOT_DOMAIN>).
func (p Policy) ValidateSystem(h string) error {
	if err := ValidSyntax(h); err != nil {
		return err
	}
	root := p.lower(p.RootDomain)
	if root == "" || !strings.HasSuffix(h, "."+root) {
		return fmt.Errorf("%w: system hostnames must be directly under ROOT_DOMAIN", ErrFormat)
	}
	label := strings.TrimSuffix(h, "."+root)
	if strings.Contains(label, ".") {
		return fmt.Errorf("%w: system hostnames must be a single label under ROOT_DOMAIN", ErrFormat)
	}
	if reservedLabels[label] {
		return ErrReserved
	}
	if m := p.lower(p.ManagementDomain); m != "" && h == m {
		return ErrReserved
	}
	return nil
}

// ValidatePreview validates a hostname created through the preview endpoint. It must be
// below PREVIEW_BASE_DOMAIN. A leading "*." wildcard is allowed (returned wildcard=true).
func (p Policy) ValidatePreview(h string) (wildcard bool, err error) {
	if strings.HasPrefix(h, "*.") {
		wildcard = true
		h = h[2:]
	}
	if strings.Contains(h, "*") {
		return false, ErrWildcard
	}
	if err := ValidSyntax(h); err != nil {
		return false, err
	}
	pb := p.lower(p.PreviewBase)
	if pb == "" {
		return false, fmt.Errorf("%w: PREVIEW_BASE_DOMAIN is not configured", ErrReserved)
	}
	if h == pb && !wildcard {
		return false, fmt.Errorf("%w: preview base apex", ErrReserved)
	}
	if !under(h, pb) {
		return false, fmt.Errorf("%w: preview hostnames must be under %s", ErrFormat, pb)
	}
	return wildcard, nil
}

// Slug lowercases s and reduces it to [a-z0-9-] (no leading/trailing hyphen). Empty on failure.
func Slug(s string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
