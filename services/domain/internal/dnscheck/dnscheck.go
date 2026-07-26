package dnscheck

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Result of a DNS verification attempt.
type Result struct {
	OK      bool   `json:"ok"`
	Method  string `json:"method"`
	Detail  string `json:"detail"`
	Stubbed bool   `json:"stubbed,omitempty"`
}

// Verify checks TXT (jp-verify=<token>) or CNAME pointing at expected target.
// When stub=true, returns success with stubbed=true (for local/dev without real DNS).
func Verify(ctx context.Context, hostname, method, token, cnameTarget string, stub bool) Result {
	method = strings.ToLower(method)
	if stub {
		return Result{OK: true, Method: method, Detail: "DNS check stubbed (force or DOMAIN_DNS_STUB=true)", Stubbed: true}
	}
	resolver := &net.Resolver{}
	switch method {
	case "txt":
		txts, err := resolver.LookupTXT(ctx, hostname)
		if err != nil {
			return Result{OK: false, Method: method, Detail: err.Error()}
		}
		want := "jp-verify=" + token
		for _, t := range txts {
			if strings.Contains(t, want) || t == token {
				return Result{OK: true, Method: method, Detail: "TXT record matched"}
			}
		}
		return Result{OK: false, Method: method, Detail: fmt.Sprintf("TXT %q not found; got %v", want, txts)}
	case "cname":
		if cnameTarget == "" {
			cnameTarget = "cname.jp.localhost"
		}
		cname, err := resolver.LookupCNAME(ctx, hostname)
		if err != nil {
			return Result{OK: false, Method: method, Detail: err.Error()}
		}
		cname = strings.TrimSuffix(strings.ToLower(cname), ".")
		target := strings.TrimSuffix(strings.ToLower(cnameTarget), ".")
		if strings.HasSuffix(cname, target) || cname == target {
			return Result{OK: true, Method: method, Detail: "CNAME matched " + cname}
		}
		return Result{OK: false, Method: method, Detail: fmt.Sprintf("CNAME %s does not point to %s", cname, target)}
	default:
		return Result{OK: false, Method: method, Detail: "unknown verification method"}
	}
}

// WithTimeout wraps Verify with a short timeout.
func WithTimeout(hostname, method, token, cnameTarget string, stub bool) Result {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Verify(ctx, hostname, method, token, cnameTarget, stub)
}
