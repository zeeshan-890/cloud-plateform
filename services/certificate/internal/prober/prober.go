// Package prober reads the certificate that Traefik really serves for a hostname (TLS
// handshake with SNI) and derives an honest certificate status from it.
package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Status values reported by the service.
const (
	StatusPending     = "pending"      // no usable certificate yet
	StatusIssued      = "issued"       // valid certificate served
	StatusExpiring    = "expiring"     // valid but < ExpiringDays left
	StatusFailed      = "failed"       // probe or certificate problem (Error has the text)
	StatusTLSDisabled = "tls_disabled" // platform runs without TLS
)

// ExpiringDays is the warning threshold.
const ExpiringDays = 21

// TraefikDefaultCN is the CN of the self-signed fallback Traefik serves when it has no certificate.
const TraefikDefaultCN = "TRAEFIK DEFAULT CERT"

// Result is the raw outcome of one probe.
type Result struct {
	Reachable  bool      `json:"reachable"`
	Err        string    `json:"error,omitempty"` // transport / handshake error
	Issuer     string    `json:"issuer,omitempty"`
	Subject    string    `json:"subject,omitempty"`
	Serial     string    `json:"serial,omitempty"`
	NotBefore  time.Time `json:"not_before,omitempty"`
	NotAfter   time.Time `json:"not_after,omitempty"`
	DNSNames   []string  `json:"sans,omitempty"`
	ChainOK    bool      `json:"chain_ok"`
	ChainErr   string    `json:"chain_error,omitempty"`
	DefaultCrt bool      `json:"default_cert"`
	Staging    bool      `json:"staging,omitempty"`
}

// Prober performs TLS probes against the edge.
type Prober struct {
	Addr    string        // host:port of Traefik's websecure entrypoint
	Timeout time.Duration // default 5s
	Roots   *x509.CertPool
	Staging bool // accept Let's Encrypt staging chains ("(STAGING)" issuers)
	Now     func() time.Time
	// Dial overrides the TCP dialer (tests).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (p *Prober) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Prober) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 5 * time.Second
}

// Probe handshakes with SNI=host and inspects the served certificate.
func (p *Prober) Probe(ctx context.Context, host string) Result {
	var res Result
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: p.timeout()}
		dial = d.DialContext
	}
	raw, err := dial(ctx, "tcp", p.Addr)
	if err != nil {
		res.Err = "connect " + p.Addr + ": " + err.Error()
		return res
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(p.timeout()))

	conn := tls.Client(raw, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // we verify manually below to report precise reasons
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"http/1.1"},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		res.Err = "tls handshake: " + err.Error()
		return res
	}
	res.Reachable = true
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		res.Err = "server presented no certificate"
		return res
	}
	leaf := certs[0]
	res.Issuer = leaf.Issuer.String()
	res.Subject = leaf.Subject.String()
	res.Serial = leaf.SerialNumber.Text(16)
	res.NotBefore, res.NotAfter = leaf.NotBefore.UTC(), leaf.NotAfter.UTC()
	res.DNSNames = append([]string(nil), leaf.DNSNames...)
	res.DefaultCrt = leaf.Subject.CommonName == TraefikDefaultCN

	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	opts := x509.VerifyOptions{DNSName: host, Roots: p.Roots, Intermediates: inter, CurrentTime: p.now()}
	if _, err := leaf.Verify(opts); err != nil {
		if p.Staging && strings.Contains(leaf.Issuer.String(), "(STAGING)") {
			// Let's Encrypt staging chains are not in system roots. Still insist on a matching name and validity window.
			if herr := leaf.VerifyHostname(host); herr != nil {
				res.ChainErr = herr.Error()
				return res
			}
			if now := p.now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				res.ChainErr = "certificate outside its validity window"
				return res
			}
			res.ChainOK, res.Staging = true, true
			return res
		}
		res.ChainErr = err.Error()
		return res
	}
	res.ChainOK = true
	return res
}

// Evaluation is the derived, user-facing state.
type Evaluation struct {
	Status   string
	Error    string
	DaysLeft int
}

// Evaluate maps a probe result to a status. pendingSince is when issuance was requested;
// after pendingGrace without a certificate the state becomes failed with guidance.
func Evaluate(res Result, now time.Time, pendingSince time.Time, pendingGrace time.Duration) Evaluation {
	if !res.Reachable {
		return Evaluation{Status: StatusFailed, Error: "TLS probe failed: " + res.Err}
	}
	if res.DefaultCrt {
		if !pendingSince.IsZero() && now.Sub(pendingSince) > pendingGrace {
			return Evaluation{Status: StatusFailed, Error: fmt.Sprintf(
				"no certificate issued after %s (Traefik still serves its default certificate); check that the domain's DNS points at this server and that ports 80/443 are reachable",
				pendingGrace.Round(time.Minute))}
		}
		return Evaluation{Status: StatusPending}
	}
	if !res.ChainOK {
		return Evaluation{Status: StatusFailed, Error: "served certificate is not valid: " + res.ChainErr}
	}
	days := int(res.NotAfter.Sub(now).Hours() / 24)
	if days < 0 {
		return Evaluation{Status: StatusFailed, Error: "certificate expired " + res.NotAfter.Format(time.RFC3339), DaysLeft: days}
	}
	if days < ExpiringDays {
		return Evaluation{Status: StatusExpiring, DaysLeft: days}
	}
	return Evaluation{Status: StatusIssued, DaysLeft: days}
}

// ErrNotConfigured is returned when no probe address is set.
var ErrNotConfigured = errors.New("TLS probe address not configured")
