package dnscheck

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// testDNS is a real DNS server (UDP+TCP) on localhost serving a mutable zone.
type testDNS struct {
	mu       sync.Mutex
	records  map[string][]dns.RR // key: lower fqdn + "/" + type string
	rcode    map[string]int
	silent   bool // never answer
	truncate bool // UDP answers are truncated, forcing TCP
	addr     string
	servers  []*dns.Server
}

func key(name string, t uint16) string {
	return strings.ToLower(dns.Fqdn(name)) + "/" + dns.TypeToString[t]
}

func (s *testDNS) add(rr string) {
	r, err := dns.NewRR(rr)
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(r.Header().Name, r.Header().Rrtype)
	s.records[k] = append(s.records[k], r)
}

func (s *testDNS) handle(w dns.ResponseWriter, req *dns.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.silent {
		return
	}
	m := new(dns.Msg)
	m.SetReply(req)
	q := req.Question[0]
	if rc, ok := s.rcode[strings.ToLower(q.Name)]; ok {
		m.Rcode = rc
		_ = w.WriteMsg(m)
		return
	}
	_, isUDP := w.RemoteAddr().(*net.UDPAddr)
	if s.truncate && isUDP {
		m.Truncated = true
		_ = w.WriteMsg(m)
		return
	}
	found := false
	name := q.Name
	if q.Qtype == dns.TypeCNAME {
		// answer with the whole chain
		for hop := 0; hop < 5; hop++ {
			cn := s.records[key(name, dns.TypeCNAME)]
			if len(cn) == 0 {
				break
			}
			m.Answer = append(m.Answer, cn...)
			name = cn[0].(*dns.CNAME).Target
			found = true
		}
	} else {
		// follow CNAMEs like a recursive resolver, then answer the requested type
		for hop := 0; hop < 5; hop++ {
			cn := s.records[key(name, dns.TypeCNAME)]
			if len(cn) == 0 {
				break
			}
			m.Answer = append(m.Answer, cn...)
			name = cn[0].(*dns.CNAME).Target
			found = true
		}
		if rr := s.records[key(name, q.Qtype)]; len(rr) > 0 {
			m.Answer = append(m.Answer, rr...)
			found = true
		}
	}
	if !found {
		exists := false
		for k := range s.records {
			if strings.HasPrefix(k, strings.ToLower(dns.Fqdn(q.Name))+"/") {
				exists = true
			}
		}
		if !exists {
			m.Rcode = dns.RcodeNameError
		}
	}
	_ = w.WriteMsg(m)
}

func startDNS(t *testing.T) *testDNS {
	t.Helper()
	s := &testDNS{records: map[string][]dns.RR{}, rcode: map[string]int{}}
	var pc net.PacketConn
	var ln net.Listener
	var addr string
	for i := 0; i < 50; i++ { // same port for UDP+TCP; retry when the TCP side is unavailable
		p, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		l, err := net.Listen("tcp", p.LocalAddr().String())
		if err != nil {
			_ = p.Close()
			continue
		}
		pc, ln, addr = p, l, p.LocalAddr().String()
		break
	}
	if pc == nil {
		t.Fatal("could not allocate a UDP+TCP port pair")
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handle)
	udp := &dns.Server{PacketConn: pc, Handler: mux}
	tcp := &dns.Server{Listener: ln, Handler: mux}
	started := make(chan struct{}, 2)
	udp.NotifyStartedFunc = func() { started <- struct{}{} }
	tcp.NotifyStartedFunc = func() { started <- struct{}{} }
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	<-started
	<-started
	s.addr = addr
	s.servers = []*dns.Server{udp, tcp}
	t.Cleanup(func() {
		for _, sv := range s.servers {
			_ = sv.Shutdown()
		}
	})
	return s
}

func newVerifier(s *testDNS) *Verifier {
	return &Verifier{Resolver: s.addr, CNAMETarget: "edge.jp.example.net", ATargets: []string{"203.0.113.10", "2001:db8::10"}, Timeout: 700 * time.Millisecond, Attempts: 1}
}

const tok = "0123456789abcdef0123456789abcdef"

func TestNewTokenEntropy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tk, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tk) != 32 || seen[tk] {
			t.Fatalf("token %q len=%d dup=%v", tk, len(tk), seen[tk])
		}
		seen[tk] = true
	}
}

func TestVerifyTXTChallenge(t *testing.T) {
	s := startDNS(t)
	s.add(`_jp-challenge.app.example.com. 60 IN TXT "jp-verify=` + tok + `"`)
	res := newVerifier(s).Verify(context.Background(), "app.example.com", tok, "txt")
	if !res.OK || res.Method != MethodTXT {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyTXTLegacyOnHost(t *testing.T) {
	s := startDNS(t)
	s.add(`app.example.com. 60 IN TXT "v=spf1 -all"`)
	s.add(`app.example.com. 60 IN TXT "jp-verify=` + tok + `"`)
	if res := newVerifier(s).Verify(context.Background(), "app.example.com", tok, "txt"); !res.OK {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyTXTWrongTokenAndPrefixTricks(t *testing.T) {
	s := startDNS(t)
	s.add(`_jp-challenge.app.example.com. 60 IN TXT "jp-verify=other"`)
	s.add(`app.example.com. 60 IN TXT "jp-verify=` + tok + `-extra"`)
	s.add(`app.example.com. 60 IN TXT "` + tok + `"`) // bare token must NOT be accepted
	res := newVerifier(s).Verify(context.Background(), "app.example.com", tok, "txt")
	if res.OK {
		t.Fatalf("must not verify: %+v", res)
	}
}

func TestVerifyNoRecords(t *testing.T) {
	s := startDNS(t)
	res := newVerifier(s).Verify(context.Background(), "nothing.example.com", tok, "txt")
	if res.OK || res.Detail == "" {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyCNAMEExact(t *testing.T) {
	s := startDNS(t)
	s.add(`www.example.com. 60 IN CNAME edge.jp.example.net.`)
	res := newVerifier(s).Verify(context.Background(), "www.example.com", tok, "cname")
	if !res.OK || res.Method != MethodCNAME {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyCNAMEChain(t *testing.T) {
	s := startDNS(t)
	s.add(`shop.example.com. 60 IN CNAME lb.cdn.example.org.`)
	s.add(`lb.cdn.example.org. 60 IN CNAME EDGE.jp.example.net.`)
	if res := newVerifier(s).Verify(context.Background(), "shop.example.com", tok, "cname"); !res.OK {
		t.Fatalf("chain through intermediate must verify: %+v", res)
	}
}

func TestVerifyCNAMESuffixBypassRejected(t *testing.T) {
	cases := []string{
		"evil-edge.jp.example.net.",     // HasSuffix bug: "evil-" + target
		"xedge.jp.example.net.",         // label glued
		"edge.jp.example.net.evil.com.", // target as prefix
		"edge.jp.example.net.attacker.org.",
		"sub.edge.jp.example.net.", // subdomain of target is not the target
	}
	for _, target := range cases {
		s := startDNS(t)
		s.add(`victim.example.com. 60 IN CNAME ` + target)
		res := newVerifier(s).Verify(context.Background(), "victim.example.com", tok, "cname")
		if res.OK {
			t.Errorf("CNAME %s must NOT verify: %+v", target, res)
		}
	}
}

func TestVerifyARecords(t *testing.T) {
	s := startDNS(t)
	s.add(`example.org. 60 IN A 203.0.113.10`)
	res := newVerifier(s).Verify(context.Background(), "example.org", tok, "a")
	if !res.OK || res.Method != MethodA {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyARecordMismatchAndPartial(t *testing.T) {
	s := startDNS(t)
	s.add(`wrong.example.org. 60 IN A 198.51.100.7`)
	s.add(`partial.example.org. 60 IN A 203.0.113.10`)
	s.add(`partial.example.org. 60 IN A 198.51.100.7`) // round-robin to a third party
	for _, h := range []string{"wrong.example.org", "partial.example.org"} {
		if res := newVerifier(s).Verify(context.Background(), h, tok, "a"); res.OK {
			t.Errorf("%s must not verify: %+v", h, res)
		}
	}
}

func TestVerifyAAAA(t *testing.T) {
	s := startDNS(t)
	s.add(`v6.example.org. 60 IN AAAA 2001:db8::10`)
	if res := newVerifier(s).Verify(context.Background(), "v6.example.org", tok, "a"); !res.OK {
		t.Fatalf("%+v", res)
	}
}

func TestVerifyDisabledMethodsWhenNotConfigured(t *testing.T) {
	s := startDNS(t)
	s.add(`app.example.com. 60 IN CNAME edge.jp.example.net.`)
	v := newVerifier(s)
	v.CNAMETarget = ""
	v.ATargets = nil
	if res := v.Verify(context.Background(), "app.example.com", tok, "cname"); res.OK {
		t.Fatalf("with no CNAME target configured only TXT can verify: %+v", res)
	}
}

func TestVerifyTimeoutSilentServer(t *testing.T) {
	s := startDNS(t)
	s.silent = true
	v := newVerifier(s)
	v.Timeout = 150 * time.Millisecond
	start := time.Now()
	res := v.Verify(context.Background(), "app.example.com", tok, "txt")
	if res.OK {
		t.Fatalf("must fail: %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("verification hung for %v", d)
	}
}

func TestVerifyContextCancel(t *testing.T) {
	s := startDNS(t)
	s.silent = true
	v := newVerifier(s)
	v.Timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if res := v.Verify(ctx, "app.example.com", tok, "txt"); res.OK {
		t.Fatal("must fail")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("context deadline not honoured")
	}
}

func TestVerifyServFail(t *testing.T) {
	s := startDNS(t)
	s.rcode["_jp-challenge.app.example.com."] = dns.RcodeServerFailure
	s.rcode["app.example.com."] = dns.RcodeServerFailure
	res := newVerifier(s).Verify(context.Background(), "app.example.com", tok, "txt")
	if res.OK || !strings.Contains(res.Detail, "SERVFAIL") {
		t.Fatalf("res=%+v", res)
	}
}

func TestVerifyTCPFallbackOnTruncation(t *testing.T) {
	s := startDNS(t)
	s.truncate = true
	s.add(`_jp-challenge.app.example.com. 60 IN TXT "jp-verify=` + tok + `"`)
	if res := newVerifier(s).Verify(context.Background(), "app.example.com", tok, "txt"); !res.OK {
		t.Fatalf("truncated UDP must fall back to TCP: %+v", res)
	}
}

func TestSameName(t *testing.T) {
	if !sameName("EDGE.example.net.", "edge.example.net") {
		t.Fatal("case/trailing dot")
	}
	if sameName("", "") || sameName("evil-edge.example.net", "edge.example.net") {
		t.Fatal("bad match")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("DNS_RESOLVER", "127.0.0.1:5353")
	t.Setenv("DOMAIN_CNAME_TARGET", "edge.x.net")
	t.Setenv("DOMAIN_A_TARGETS", "1.2.3.4, 5.6.7.8 ,")
	v := FromEnv()
	if v.Resolver != "127.0.0.1:5353" || v.CNAMETarget != "edge.x.net" || len(v.ATargets) != 2 {
		t.Fatalf("%+v", v)
	}
	if got := ensurePort("1.1.1.1"); got != "1.1.1.1:53" {
		t.Fatal(got)
	}
}
