package traefikcfg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
)

// MaxUpstreams is the maximum number of upstream servers per route.
const MaxUpstreams = 16

func shortURL(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// ValidateUpstream checks a single upstream and returns its canonical form (http://ip:port).
func ValidateUpstream(raw string) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("invalid upstream %q: %s", shortURL(raw), why)
	}
	if raw == "" || len(raw) > 200 {
		return bad("empty or too long")
	}
	for _, c := range raw {
		if c <= ' ' || c == 0x7f {
			return bad("contains whitespace or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return bad("not a valid URL")
	}
	if u.Scheme != "http" {
		return bad("scheme must be http")
	}
	if u.User != nil {
		return bad("userinfo not allowed")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return bad("query and fragment not allowed")
	}
	if u.Path != "" || u.RawPath != "" {
		return bad("path not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return bad("host required")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return bad("host must be a literal IPv4 or IPv6 address")
	}
	ip = ip.Unmap()
	ps := u.Port()
	if ps == "" {
		return bad("explicit port required")
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return bad("port must be 1-65535")
	}
	switch {
	case ip.IsLoopback():
		return bad("loopback address not allowed")
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast():
		return bad("link-local address not allowed")
	case ip.IsMulticast():
		return bad("multicast address not allowed")
	case ip.IsUnspecified():
		return bad("unspecified address not allowed")
	case ip.Is4() && ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return bad("broadcast address not allowed")
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

// ValidateUpstreams validates a list (max 16, no duplicates) and returns canonical URLs.
func ValidateUpstreams(list []string) ([]string, error) {
	if len(list) > MaxUpstreams {
		return nil, fmt.Errorf("too many upstreams (max %d)", MaxUpstreams)
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, raw := range list {
		c, err := ValidateUpstream(raw)
		if err != nil {
			return nil, err
		}
		if seen[c] {
			return nil, fmt.Errorf("duplicate upstream %q", shortURL(raw))
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// UpstreamServiceName is the stable file-provider service name of a hostname's upstream pool.
func UpstreamServiceName(host string, wildcard bool) string {
	key := host
	if wildcard {
		key += "*"
	}
	sum := sha256.Sum256([]byte(key))
	return "jp-u-" + hex.EncodeToString(sum[:])[:16]
}
