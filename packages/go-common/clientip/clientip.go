// Package clientip resolves the real client address of a request. X-Forwarded-For is only
// honoured when the TCP peer is one of the configured trusted proxies (e.g. Traefik), so a
// client can never spoof its address by sending the header itself.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// Header is the trusted header the gateway uses to hand the resolved address to services
// (honoured only on requests that carry a valid internal token).
const Header = "X-JP-Client-IP"

// Resolver resolves client IPs.
type Resolver struct {
	nets []*net.IPNet
}

// New parses a list of IPs/CIDRs. Invalid entries are ignored.
func New(trusted []string) *Resolver {
	r := &Resolver{}
	for _, t := range trusted {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !strings.Contains(t, "/") {
			if ip := net.ParseIP(t); ip != nil {
				if ip.To4() != nil {
					t += "/32"
				} else {
					t += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(t); err == nil {
			r.nets = append(r.nets, n)
		}
	}
	return r
}

func (r *Resolver) trusted(ip net.IP) bool {
	if r == nil || ip == nil {
		return false
	}
	for _, n := range r.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Peer returns the IP of the TCP peer.
func Peer(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(req.RemoteAddr)
	}
	return host
}

// FromRequest returns the client IP: the right-most X-Forwarded-For entry that is not a
// trusted proxy, provided the peer itself is trusted; otherwise the peer address.
func (r *Resolver) FromRequest(req *http.Request) string {
	peer := Peer(req)
	if !r.trusted(net.ParseIP(peer)) {
		return peer
	}
	xff := req.Header.Values("X-Forwarded-For")
	if len(xff) == 0 {
		if real := net.ParseIP(strings.TrimSpace(req.Header.Get("X-Real-IP"))); real != nil {
			return real.String()
		}
		return peer
	}
	parts := strings.Split(strings.Join(xff, ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			continue
		}
		if r.trusted(ip) {
			continue
		}
		return ip.String()
	}
	return peer
}
