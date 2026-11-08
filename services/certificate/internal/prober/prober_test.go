package prober

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T, cn string) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn, Organization: []string{cn}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour * 365),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func (ca *testCA) issue(t *testing.T, cn string, sans []string, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		DNSNames: sans, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}
}

// startTLS runs a real TLS server that selects a certificate by SNI (like Traefik) and falls back
// to def (the "default cert") for unknown names.
func startTLS(t *testing.T, bySNI map[string]tls.Certificate, def tls.Certificate) string {
	t.Helper()
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if c, ok := bySNI[h.ServerName]; ok {
				return &c, nil
			}
			return &def, nil
		},
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if tc, ok := c.(*tls.Conn); ok {
					_ = tc.Handshake()
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func defaultCert(t *testing.T) tls.Certificate {
	ca := newCA(t, TraefikDefaultCN)
	return ca.issue(t, TraefikDefaultCN, []string{"traefik.default"}, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
}

func TestProbeIssuedCertificate(t *testing.T) {
	ca := newCA(t, "Test CA")
	now := time.Now()
	addr := startTLS(t, map[string]tls.Certificate{
		"app.example.test": ca.issue(t, "app.example.test", []string{"app.example.test"}, now.Add(-time.Hour), now.Add(60*24*time.Hour)),
	}, defaultCert(t))
	p := &Prober{Addr: addr, Roots: ca.pool, Timeout: 3 * time.Second}
	res := p.Probe(context.Background(), "app.example.test")
	if !res.Reachable || !res.ChainOK || res.DefaultCrt {
		t.Fatalf("res=%+v", res)
	}
	if !strings.Contains(res.Issuer, "Test CA") || len(res.DNSNames) != 1 || res.DNSNames[0] != "app.example.test" || res.Serial == "" {
		t.Fatalf("details missing: %+v", res)
	}
	ev := Evaluate(res, now, now.Add(-time.Minute), 30*time.Minute)
	if ev.Status != StatusIssued || ev.DaysLeft < 58 || ev.DaysLeft > 60 {
		t.Fatalf("ev=%+v", ev)
	}
}

func TestProbeExpiringCertificate(t *testing.T) {
	ca := newCA(t, "Test CA")
	now := time.Now()
	addr := startTLS(t, map[string]tls.Certificate{
		"soon.example.test": ca.issue(t, "soon", []string{"soon.example.test"}, now.Add(-time.Hour), now.Add(10*24*time.Hour)),
	}, defaultCert(t))
	res := (&Prober{Addr: addr, Roots: ca.pool}).Probe(context.Background(), "soon.example.test")
	ev := Evaluate(res, now, time.Time{}, time.Hour)
	if ev.Status != StatusExpiring || ev.DaysLeft != 9 && ev.DaysLeft != 10 {
		t.Fatalf("ev=%+v res=%+v", ev, res)
	}
}

func TestProbeExpiredCertificate(t *testing.T) {
	ca := newCA(t, "Test CA")
	now := time.Now()
	addr := startTLS(t, map[string]tls.Certificate{
		"old.example.test": ca.issue(t, "old", []string{"old.example.test"}, now.Add(-48*time.Hour), now.Add(-time.Hour)),
	}, defaultCert(t))
	res := (&Prober{Addr: addr, Roots: ca.pool}).Probe(context.Background(), "old.example.test")
	ev := Evaluate(res, now, time.Time{}, time.Hour)
	if ev.Status != StatusFailed || !strings.Contains(ev.Error, "expired") {
		t.Fatalf("ev=%+v res=%+v", ev, res)
	}
}

func TestProbeDefaultCertMeansPendingThenFailed(t *testing.T) {
	ca := newCA(t, "Test CA")
	addr := startTLS(t, nil, defaultCert(t))
	res := (&Prober{Addr: addr, Roots: ca.pool}).Probe(context.Background(), "nocert.example.test")
	if !res.DefaultCrt || res.ChainOK {
		t.Fatalf("res=%+v", res)
	}
	now := time.Now()
	if ev := Evaluate(res, now, now.Add(-2*time.Minute), 30*time.Minute); ev.Status != StatusPending {
		t.Fatalf("within grace must be pending: %+v", ev)
	}
	ev := Evaluate(res, now, now.Add(-2*time.Hour), 30*time.Minute)
	if ev.Status != StatusFailed || !strings.Contains(ev.Error, "default certificate") {
		t.Fatalf("after grace must be failed with guidance: %+v", ev)
	}
}

func TestProbeWrongHostnameFails(t *testing.T) {
	ca := newCA(t, "Test CA")
	now := time.Now()
	// server returns a cert for another name for this SNI
	addr := startTLS(t, map[string]tls.Certificate{
		"app.example.test": ca.issue(t, "other", []string{"other.example.test"}, now.Add(-time.Hour), now.Add(90*24*time.Hour)),
	}, defaultCert(t))
	res := (&Prober{Addr: addr, Roots: ca.pool}).Probe(context.Background(), "app.example.test")
	ev := Evaluate(res, now, time.Time{}, time.Hour)
	if ev.Status != StatusFailed || res.ChainOK {
		t.Fatalf("hostname mismatch must fail: %+v %+v", ev, res)
	}
}

func TestProbeUntrustedIssuerFails(t *testing.T) {
	trusted := newCA(t, "Trusted")
	rogue := newCA(t, "Rogue")
	now := time.Now()
	addr := startTLS(t, map[string]tls.Certificate{
		"app.example.test": rogue.issue(t, "app", []string{"app.example.test"}, now.Add(-time.Hour), now.Add(90*24*time.Hour)),
	}, defaultCert(t))
	res := (&Prober{Addr: addr, Roots: trusted.pool}).Probe(context.Background(), "app.example.test")
	ev := Evaluate(res, now, time.Time{}, time.Hour)
	if ev.Status != StatusFailed || !strings.Contains(ev.Error, "not valid") {
		t.Fatalf("ev=%+v", ev)
	}
}

func TestProbeStagingAcceptedOnlyWhenEnabled(t *testing.T) {
	staging := newCA(t, "(STAGING) Pretend Pear X1")
	now := time.Now()
	addr := startTLS(t, map[string]tls.Certificate{
		"app.example.test": staging.issue(t, "app", []string{"app.example.test"}, now.Add(-time.Hour), now.Add(80*24*time.Hour)),
	}, defaultCert(t))
	empty := x509.NewCertPool()

	res := (&Prober{Addr: addr, Roots: empty}).Probe(context.Background(), "app.example.test")
	if ev := Evaluate(res, now, time.Time{}, time.Hour); ev.Status != StatusFailed {
		t.Fatalf("staging chain must be rejected when ACME_STAGING=false: %+v", ev)
	}
	res = (&Prober{Addr: addr, Roots: empty, Staging: true}).Probe(context.Background(), "app.example.test")
	ev := Evaluate(res, now, time.Time{}, time.Hour)
	if ev.Status != StatusIssued || !res.Staging {
		t.Fatalf("ev=%+v res=%+v", ev, res)
	}
	// a non-staging untrusted issuer is still rejected with Staging on
	rogue := newCA(t, "Rogue CA")
	addr2 := startTLS(t, map[string]tls.Certificate{
		"app.example.test": rogue.issue(t, "app", []string{"app.example.test"}, now.Add(-time.Hour), now.Add(80*24*time.Hour)),
	}, defaultCert(t))
	res = (&Prober{Addr: addr2, Roots: empty, Staging: true}).Probe(context.Background(), "app.example.test")
	if ev := Evaluate(res, now, time.Time{}, time.Hour); ev.Status != StatusFailed {
		t.Fatalf("rogue issuer must fail even in staging mode: %+v", ev)
	}
	// staging chain with wrong name is rejected too
	res = (&Prober{Addr: addr, Roots: empty, Staging: true}).Probe(context.Background(), "unrelated.example.test")
	if res.ChainOK {
		t.Fatalf("name mismatch must not pass in staging mode: %+v", res)
	}
}

func TestProbeUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens any more
	res := (&Prober{Addr: addr, Timeout: time.Second}).Probe(context.Background(), "app.example.test")
	if res.Reachable || res.Err == "" {
		t.Fatalf("res=%+v", res)
	}
	ev := Evaluate(res, time.Now(), time.Time{}, time.Hour)
	if ev.Status != StatusFailed || !strings.Contains(ev.Error, "probe failed") {
		t.Fatalf("ev=%+v", ev)
	}
}

func TestProbeNonTLSServerTimesOut(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(3 * time.Second); c.Close() }() // never speaks TLS
		}
	}()
	start := time.Now()
	res := (&Prober{Addr: ln.Addr().String(), Timeout: 300 * time.Millisecond}).Probe(context.Background(), "x.example.test")
	if res.Reachable || time.Since(start) > 2*time.Second {
		t.Fatalf("must time out quickly: res=%+v after %v", res, time.Since(start))
	}
}
