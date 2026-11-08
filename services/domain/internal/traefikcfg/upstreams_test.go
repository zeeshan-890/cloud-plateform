package traefikcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateUpstreamTable(t *testing.T) {
	ok := map[string]string{
		"http://10.0.0.179:32768":   "http://10.0.0.179:32768",
		"http://203.0.113.5:80":     "http://203.0.113.5:80",
		"http://[2001:db8::1]:8080": "http://[2001:db8::1]:8080",
		"http://192.168.1.2:65535":  "http://192.168.1.2:65535",
		"http://10.0.0.1:1":         "http://10.0.0.1:1",
	}
	for in, want := range ok {
		got, err := ValidateUpstream(in)
		if err != nil || got != want {
			t.Errorf("%q => %q, %v", in, got, err)
		}
	}
	bad := []string{
		"", "https://10.0.0.1:80", "ftp://10.0.0.1:80", "10.0.0.1:80", "//10.0.0.1:80",
		"http://example.com:80", "http://localhost:80", "http://10.0.0.1", "http://10.0.0.1:0",
		"http://10.0.0.1:65536", "http://10.0.0.1:abc", "http://10.0.0.1:80/x", "http://10.0.0.1:80/",
		"http://u:p@10.0.0.1:80", "http://10.0.0.1:80?a=1", "http://10.0.0.1:80#f", "http://10.0.0.1:80?",
		"http://127.0.0.1:80", "http://127.1.2.3:80", "http://[::1]:80", "http://169.254.169.254:80",
		"http://169.254.0.1:80", "http://[fe80::1]:80", "http://[fe80::1%25eth0]:80", "http://224.0.0.1:80",
		"http://[ff02::1]:80", "http://0.0.0.0:80", "http://[::]:80", "http://255.255.255.255:80",
		"http://[::ffff:127.0.0.1]:80", "http://[::ffff:169.254.169.254]:80", "http://10.0.0.1:80 ",
		"http://10.0.0.1:80\nfoo: bar", "http://0x7f000001:80", "http://2130706433:80", "HTTP://10.0.0.1",
		"http://" + strings.Repeat("a", 300) + ":80",
	}
	for _, in := range bad {
		if got, err := ValidateUpstream(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		} else if len(err.Error()) > 200 {
			t.Errorf("error too long: %d", len(err.Error()))
		}
	}
}

func TestValidateUpstreamsListRules(t *testing.T) {
	if _, err := ValidateUpstreams([]string{"http://10.0.0.1:80", "http://10.0.0.1:80"}); err == nil {
		t.Error("duplicates accepted")
	}
	var many []string
	// build valid distinct addresses
	for i := 1; i <= 17; i++ {
		many = append(many, "http://10.0.0."+itoa(i)+":80")
	}
	if _, err := ValidateUpstreams(many[:16]); err != nil {
		t.Errorf("16 rejected: %v", err)
	}
	if _, err := ValidateUpstreams(many); err == nil {
		t.Error("17 accepted")
	}
	if got, err := ValidateUpstreams(nil); err != nil || len(got) != 0 {
		t.Error("nil must be ok/empty")
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestRenderUpstreamSingleGolden(t *testing.T) {
	got, err := Render(Route{Hostname: "app.example.com", Service: "jp-app-1a2b3c4d", Upstreams: []string{"http://10.0.0.179:32768"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	name := UpstreamServiceName("app.example.com", false)
	want := "http:\n    routers:\n        " + RouterName("app.example.com", false) + ":\n" +
		"            rule: Host(`app.example.com`)\n" +
		"            entryPoints:\n                - web\n" +
		"            service: " + name + "@file\n" +
		"            middlewares:\n                - jp-retry@file\n" +
		"    services:\n        " + name + ":\n" +
		"            loadBalancer:\n                servers:\n                    - url: http://10.0.0.179:32768\n" +
		"                passHostHeader: true\n"
	if string(got) != want {
		t.Fatalf("--- got\n%s\n--- want\n%s", got, want)
	}
	if !strings.HasPrefix(name, "jp-u-") || len(name) != len("jp-u-")+16 {
		t.Fatalf("name %q", name)
	}
}

func TestRenderUpstreamManyWildcardTLS(t *testing.T) {
	r := Route{Hostname: "x.example.com", Wildcard: true, Service: "jp-app-1", TLS: true,
		Upstreams: []string{"http://10.0.0.1:80", "http://[2001:db8::1]:81"}}
	b, err := Render(r, "le")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, w := range []string{"HostRegexp(", "- web\n                - websecure", "certResolver: le",
		"service: " + UpstreamServiceName("x.example.com", true) + "@file", "- url: http://10.0.0.1:80", "- url: http://[2001:db8::1]:81",
		"jp-retry@file", "passHostHeader: true"} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
	if strings.Contains(s, "@docker") {
		t.Error("must not reference docker service")
	}
	if UpstreamServiceName("x.example.com", true) == UpstreamServiceName("x.example.com", false) {
		t.Error("wildcard flag must change name")
	}
	if UpstreamServiceName("x.example.com", true) != UpstreamServiceName("x.example.com", true) {
		t.Error("not stable")
	}
}

func TestRenderRejectsBadUpstream(t *testing.T) {
	if _, err := Render(Route{Hostname: "a.example.com", Service: "jp-app-1", Upstreams: []string{"http://169.254.169.254:80"}}, ""); err == nil {
		t.Fatal("metadata upstream rendered")
	}
}

func TestSwitchUpstreamDockerLeavesNoDanglingFiles(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "")
	host := "a.example.com"
	up := Route{Hostname: host, Service: "jp-app-1", Upstreams: []string{"http://10.0.0.5:80"}}
	dk := Route{Hostname: host, Service: "jp-app-1"}
	count := func() (int, string) {
		fs, _ := filepath.Glob(filepath.Join(dir, "domain-*.yml"))
		if len(fs) != 1 {
			return len(fs), ""
		}
		b, _ := os.ReadFile(fs[0])
		return 1, string(b)
	}
	for i, r := range []Route{up, dk, up, dk} {
		if _, err := m.Reconcile([]Route{r}); err != nil {
			t.Fatal(err)
		}
		n, body := count()
		if n != 1 {
			t.Fatalf("step %d: %d domain files", i, n)
		}
		if hasUp := strings.Contains(body, "services:"); hasUp != (len(r.Upstreams) > 0) {
			t.Fatalf("step %d: stale services block state:\n%s", i, body)
		}
	}
	// Write path as well, plus a non-managed file survives.
	_ = os.WriteFile(filepath.Join(dir, "gateway.yml"), []byte("x"), 0o644)
	if _, _, err := m.Write(up); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	if fs, _ := filepath.Glob(filepath.Join(dir, "domain-*.yml")); len(fs) != 0 {
		t.Fatalf("left %v", fs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gateway.yml")); err != nil {
		t.Fatal("unmanaged file removed")
	}
}
