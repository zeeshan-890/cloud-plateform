// Package dnscheck performs REAL DNS ownership verification. There is no stub mode:
// a hostname is verified only when a DNS answer from the configured resolver proves it.
package dnscheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// ChallengePrefix is the label under which the TXT proof is expected.
const ChallengePrefix = "_jp-challenge."

// Method names.
const (
	MethodTXT   = "txt"
	MethodCNAME = "cname"
	MethodA     = "a"
)

// NewToken returns a 128-bit random verification token (hex).
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TXTValue is the TXT payload the customer must publish.
func TXTValue(token string) string { return "jp-verify=" + token }

// Result of a DNS verification attempt.
type Result struct {
	OK      bool     `json:"ok"`
	Method  string   `json:"method"`
	Detail  string   `json:"detail"`
	Records []string `json:"records,omitempty"`
}

// Verifier talks to a recursive resolver.
type Verifier struct {
	// Resolver is host:port of the resolver (DNS_RESOLVER). Empty = system resolvers.
	Resolver string
	// CNAMETarget is DOMAIN_CNAME_TARGET (the platform's edge name).
	CNAMETarget string
	// ATargets are the platform's public IPs (DOMAIN_A_TARGETS).
	ATargets []string
	// Timeout per query (default 4s).
	Timeout time.Duration
	// Attempts per server (default 2).
	Attempts int
}

func (v *Verifier) timeout() time.Duration {
	if v.Timeout > 0 {
		return v.Timeout
	}
	return 4 * time.Second
}

func (v *Verifier) servers() []string {
	if v.Resolver != "" {
		return []string{ensurePort(v.Resolver)}
	}
	if cc, err := dns.ClientConfigFromFile("/etc/resolv.conf"); err == nil && len(cc.Servers) > 0 {
		out := make([]string, 0, len(cc.Servers))
		for _, s := range cc.Servers {
			out = append(out, net.JoinHostPort(s, cc.Port))
		}
		return out
	}
	return []string{"1.1.1.1:53", "8.8.8.8:53"}
}

func ensurePort(a string) string {
	if _, _, err := net.SplitHostPort(a); err == nil {
		return a
	}
	return net.JoinHostPort(a, "53")
}

// ErrNXDomain is returned for NXDOMAIN answers.
var ErrNXDomain = errors.New("name does not exist")

// query sends one question to the resolver(s) with UDP then TCP fallback.
func (v *Verifier) query(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.RecursionDesired = true
	var lastErr error
	attempts := v.Attempts
	if attempts <= 0 {
		attempts = 2
	}
	for _, srv := range v.servers() {
		for i := 0; i < attempts; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, netw := range []string{"udp", "tcp"} {
				c := &dns.Client{Net: netw, Timeout: v.timeout()}
				resp, _, err := c.ExchangeContext(ctx, m, srv)
				if err != nil {
					lastErr = err
					continue
				}
				if resp.Truncated && netw == "udp" {
					continue
				}
				switch resp.Rcode {
				case dns.RcodeSuccess:
					return resp, nil
				case dns.RcodeNameError:
					return resp, ErrNXDomain
				default:
					lastErr = fmt.Errorf("resolver rcode %s", dns.RcodeToString[resp.Rcode])
				}
				break
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no resolver answered")
	}
	return nil, lastErr
}

// LookupTXT returns TXT strings at name (each record's strings concatenated).
func (v *Verifier) LookupTXT(ctx context.Context, name string) ([]string, error) {
	resp, err := v.query(ctx, name, dns.TypeTXT)
	if err != nil {
		if errors.Is(err, ErrNXDomain) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, rr := range resp.Answer {
		if t, ok := rr.(*dns.TXT); ok {
			out = append(out, strings.Join(t.Txt, ""))
		}
	}
	return out, nil
}

// CNAMEChain returns the lowercase CNAME targets (without trailing dot) found when
// resolving name, following the chain returned by the resolver.
func (v *Verifier) CNAMEChain(ctx context.Context, name string) ([]string, error) {
	resp, err := v.query(ctx, name, dns.TypeCNAME)
	if err != nil {
		if errors.Is(err, ErrNXDomain) {
			return nil, nil
		}
		return nil, err
	}
	var chain []string
	cur := strings.ToLower(dns.Fqdn(name))
	for hop := 0; hop < 8; hop++ {
		next := ""
		for _, rr := range resp.Answer {
			if c, ok := rr.(*dns.CNAME); ok && strings.ToLower(c.Hdr.Name) == cur {
				next = strings.ToLower(c.Target)
				break
			}
		}
		if next == "" {
			break
		}
		chain = append(chain, strings.TrimSuffix(next, "."))
		cur = next
	}
	return chain, nil
}

// LookupAddrs returns A and AAAA addresses for name (the resolver follows CNAMEs).
func (v *Verifier) LookupAddrs(ctx context.Context, name string) ([]string, error) {
	var out []string
	var firstErr error
	for _, qt := range []uint16{dns.TypeA, dns.TypeAAAA} {
		resp, err := v.query(ctx, name, qt)
		if err != nil {
			if errors.Is(err, ErrNXDomain) {
				return nil, nil
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, rr := range resp.Answer {
			switch r := rr.(type) {
			case *dns.A:
				out = append(out, r.A.String())
			case *dns.AAAA:
				out = append(out, r.AAAA.String())
			}
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// sameName compares DNS names on label boundaries (exact match, case-insensitive,
// ignoring a trailing dot). "evil-target.com" never equals "target.com".
func sameName(a, b string) bool {
	a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".")
	b = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(b)), ".")
	return a != "" && a == b
}

// Verify checks every accepted proof for hostname and returns the first that matches.
// preferred only shapes the Method reported on failure.
func (v *Verifier) Verify(ctx context.Context, hostname, token, preferred string) Result {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*v.timeout()+2*time.Second)
		defer cancel()
	}
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	var details []string

	// 1. TXT at _jp-challenge.<host> then legacy on host.
	want := TXTValue(token)
	for _, name := range []string{ChallengePrefix + hostname, hostname} {
		txts, err := v.LookupTXT(ctx, name)
		if err != nil {
			details = append(details, fmt.Sprintf("TXT %s: %v", name, err))
			continue
		}
		for _, t := range txts {
			if strings.TrimSpace(t) == want {
				return Result{OK: true, Method: MethodTXT, Detail: "TXT record " + name + " matched", Records: []string{t}}
			}
		}
		if len(txts) == 0 {
			details = append(details, fmt.Sprintf("no TXT at %s", name))
		} else {
			details = append(details, fmt.Sprintf("TXT at %s does not contain %s", name, want))
		}
	}

	// 2. CNAME to the platform target.
	if v.CNAMETarget != "" {
		chain, err := v.CNAMEChain(ctx, hostname)
		switch {
		case err != nil:
			details = append(details, "CNAME lookup: "+err.Error())
		case len(chain) == 0:
			details = append(details, "no CNAME at "+hostname)
		default:
			for _, c := range chain {
				if sameName(c, v.CNAMETarget) {
					return Result{OK: true, Method: MethodCNAME, Detail: "CNAME points to " + c, Records: chain}
				}
			}
			details = append(details, fmt.Sprintf("CNAME %s does not point to %s", strings.Join(chain, " -> "), v.CNAMETarget))
		}
	}

	// 3. A/AAAA equal to the platform's public IPs (apex domains cannot use CNAME).
	if len(v.ATargets) > 0 {
		addrs, err := v.LookupAddrs(ctx, hostname)
		switch {
		case err != nil:
			details = append(details, "A/AAAA lookup: "+err.Error())
		case len(addrs) == 0:
			details = append(details, "no A/AAAA at "+hostname)
		default:
			if allIn(addrs, v.ATargets) {
				return Result{OK: true, Method: MethodA, Detail: "A/AAAA records match platform addresses", Records: addrs}
			}
			details = append(details, fmt.Sprintf("A/AAAA %s not all in %s", strings.Join(addrs, ","), strings.Join(v.ATargets, ",")))
		}
	}
	return Result{OK: false, Method: preferred, Detail: strings.Join(details, "; ")}
}

func allIn(addrs, targets []string) bool {
	set := map[string]bool{}
	for _, t := range targets {
		if ip := net.ParseIP(strings.TrimSpace(t)); ip != nil {
			set[ip.String()] = true
		}
	}
	if len(set) == 0 {
		return false
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || !set[ip.String()] {
			return false
		}
	}
	return true
}

// FromEnv builds a Verifier from DNS_RESOLVER / DOMAIN_CNAME_TARGET / DOMAIN_A_TARGETS.
func FromEnv() *Verifier {
	v := &Verifier{
		Resolver:    strings.TrimSpace(os.Getenv("DNS_RESOLVER")),
		CNAMETarget: strings.TrimSpace(os.Getenv("DOMAIN_CNAME_TARGET")),
	}
	for _, s := range strings.Split(os.Getenv("DOMAIN_A_TARGETS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			v.ATargets = append(v.ATargets, s)
		}
	}
	return v
}
