package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func dnsZone() string { return getenv("E2E_DNS_ZONE", "e2e.test") }

// rootDomain returns the platform ROOT_DOMAIN as reported by the domain service config.
func rootDomain(t *testing.T, c *Client, p *Project) string {
	t.Helper()
	r := c.Must(t, "GET", p.Path("/domains"), nil, 200).Map(t)
	if s := Str(r, "config", "root_domain"); s != "" {
		return s
	}
	return Load(t).RootDom
}

func TestDomains_HostnameValidation(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	root := rootDomain(t, admin, p)
	zone := dnsZone()

	var before []string
	if e.SSH != "" {
		before = e.TraefikDynamicFiles(t)
	}

	bad := []string{
		"a." + zone + "\nevil.com",
		"a." + zone + "\r\nHost(`evil.com`)",
		"a`b." + zone,
		"a." + zone + "`) || Host(`evil.com",
		"a b." + zone,
		"a." + zone + "/path",
		"a." + zone + ":8080",
		"http://a." + zone,
		"user@a." + zone,
		"*." + zone,
		"a_b." + zone,
		"-a." + zone,
		"a-." + zone,
		"a..b." + zone,
		"1.2.3.4",
		"192.168.0.10",
		"::1",
		"[::1]",
		"localhost",
		"x.localhost",
		"singlelabel",
		"",
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + "." + zone, // 300 chars
		strings.Repeat("a", 64) + "." + zone,
		"xn--" + strings.Repeat("a", 70) + "." + zone,
	}
	if root != "" {
		bad = append(bad, root, "foo."+root, "api."+root, "www."+root, "admin."+root, "traefik."+root, "deep.sub."+root)
	}
	for _, h := range bad {
		r := admin.Do("POST", p.Path("/domains"), map[string]string{"hostname": h})
		if r.Status < 400 || r.Status >= 500 {
			t.Errorf("hostname %q: want 4xx, got %d %s", truncate(h, 40), r.Status, truncate(string(r.Body), 150))
		}
	}
	// uppercase: either rejected or normalised to lowercase - never stored verbatim
	up := "UPPER-" + randHex(2) + "." + zone
	r := admin.Do("POST", p.Path("/domains"), map[string]string{"hostname": up})
	if r.Status == 201 {
		if got := Str(r.Map(t), "domain", "hostname"); got != strings.ToLower(up) {
			t.Errorf("uppercase hostname stored as %q", got)
		}
	} else if r.Status < 400 || r.Status >= 500 {
		t.Errorf("uppercase hostname: unexpected status %d", r.Status)
	}
	// bogus verification types / bodies
	if r := admin.Do("POST", p.Path("/domains"), map[string]string{"hostname": "ok-" + randHex(2) + "." + zone, "verification_type": "magic"}); r.Status != 400 {
		t.Errorf("unknown verification_type: want 400, got %d", r.Status)
	}

	// nothing written to the Traefik dynamic directory by rejected hostnames
	if e.SSH != "" {
		after := e.TraefikDynamicFiles(t)
		if len(after) != len(before) {
			t.Errorf("rejected hostnames changed the traefik dynamic dir: before=%d after=%d (%v)", len(before), len(after), after)
		}
		if g := e.TraefikDynamicGrep(t, "evil.com"); g != "" {
			t.Errorf("injection payload reached the traefik config: %s", truncate(g, 300))
		}
		if g := e.TraefikDynamicGrep(t, "`) ||"); g != "" {
			t.Errorf("rule injection reached the traefik config: %s", truncate(g, 300))
		}
	}
	// system domain cannot be removed through the domain API
	for _, d := range Arr(admin.Must(t, "GET", p.Path("/domains"), nil, 200).Map(t), "domains") {
		if Str(d, "kind") == "system" {
			if r := admin.Do("DELETE", p.Path("/domains/"+Str(d, "id")), nil); r.Status != 400 {
				t.Errorf("deleting the system domain: want 400, got %d", r.Status)
			}
			if r := admin.Do("POST", p.Path("/domains/"+Str(d, "id")+"/verify"), nil); r.Status != 400 {
				t.Errorf("verifying the system domain: want 400, got %d", r.Status)
			}
		}
	}
}

func TestDomains_SystemSubdomainRoutes(t *testing.T) {
	d := deployFixture(t)
	// registered system domain is routed and the app answers on it
	code, body, _ := d.E.EdgeGet(d.Host, "/health")
	if code != 200 || !strings.Contains(body, "jp-e2e-addon-probe") {
		t.Fatalf("system hostname does not route to the app: %d %s", code, body)
	}
	if g := d.E.TraefikDynamicGrep(t, d.Host); g == "" {
		t.Errorf("no router for %s in the traefik dynamic dir", d.Host)
	}
	// two projects never share a system hostname
	p2 := NewProject(t, d.Admin, d.Org)
	if h2 := SystemHost(t, d.Admin, p2); h2 == d.Host {
		t.Errorf("two projects share the system hostname %s", h2)
	}
	// duplicate slug in the same org -> 409
	if r := d.Admin.Do("POST", "/orgs/"+d.Org.ID+"/projects", map[string]string{"name": "dup", "slug": d.P.Slug}); r.Status != 409 {
		t.Errorf("duplicate project slug: want 409, got %d", r.Status)
	}
	// deleting the project removes its router file
	d.Admin.Timeout = 10 * time.Minute
	d.Admin.Must(t, "DELETE", d.P.Path(""), nil, 204)
	d.Admin.Timeout = 60 * time.Second
	Eventually(t, 30*time.Second, time.Second, "system router removed", func() (bool, string) {
		return d.E.TraefikDynamicGrep(t, d.Host) == "", "router file still present"
	})
	if code, _, _ := d.E.EdgeGet(d.Host, "/health"); code == 200 {
		t.Errorf("deleted project still routed")
	}
}

func TestDomains_CustomDomainDNSVerification(t *testing.T) {
	d := deployFixture(t)
	if os.Getenv("E2E_REMOTE_REPO") == "" {
		t.Skip("E2E_REMOTE_REPO not set: needed to edit the CoreDNS test zone (scripts/e2e-dns.sh) on the server")
	}
	e, admin, p := d.E, d.Admin, d.P
	zone := dnsZone()
	host := RandName("app") + "." + zone
	challenge := "_jp-challenge." + host
	t.Cleanup(func() {
		if repo := os.Getenv("E2E_REMOTE_REPO"); repo != "" && e.SSH != "" {
			_, _ = e.TryRun("cd " + repo + " && ./scripts/e2e-dns.sh del " + challenge + " TXT " + zone + "; ./scripts/e2e-dns.sh del " + host + " '' " + zone)
		}
	})

	// create: pending, with real instructions
	// attempt to smuggle verification flags: the API must either reject the unknown fields (400) or ignore them (201 + pending)
	probe := admin.Do("POST", p.Path("/domains"), map[string]any{"hostname": host, "force": true, "status": "verified", "verified": true})
	var r map[string]any
	switch probe.Status {
	case 400, 422:
		r = admin.Must(t, "POST", p.Path("/domains"), map[string]any{"hostname": host}, 201).Map(t)
	case 201:
		r = probe.Map(t)
	default:
		t.Fatalf("create domain: status %d: %s", probe.Status, truncate(string(probe.Body), 300))
	}
	dom := Dig(r, "domain")
	id := Str(dom, "id")
	if Str(dom, "status") != "pending" || Bool(dom, "routed") {
		t.Fatalf("new custom domain must be pending and unrouted (client-supplied status/force ignored): %v", dom)
	}
	token := Str(r, "verification", "token")
	var txtName, txtValue string
	for _, rec := range Arr(r, "verification", "records") {
		if Str(rec, "type") == "TXT" {
			txtName, txtValue = Str(rec, "name"), Str(rec, "value")
		}
	}
	if token == "" || txtName != challenge || txtValue != "jp-verify="+token {
		t.Fatalf("verification instructions wrong: name=%q value=%q token=%q", txtName, txtValue, token)
	}
	if r := admin.Do("POST", p.Path("/domains"), map[string]string{"hostname": host}); r.Status != 409 {
		t.Errorf("same hostname twice in one project: want 409, got %d", r.Status)
	}

	// unverified -> edge answers 404 and no router file exists
	if code, _, _ := e.EdgeGet(host, "/health"); code != 404 {
		t.Errorf("unverified custom domain: want 404 from the edge, got %d", code)
	}
	if g := e.TraefikDynamicGrep(t, host); g != "" {
		t.Errorf("unverified domain has a router: %s", g)
	}

	// no DNS record -> verification fails; `force` does not help
	time.Sleep(3500 * time.Millisecond)
	vr := admin.Do("POST", p.Path("/domains/"+id+"/verify"), nil)
	if vr.Status != 400 || Bool(vr.Map(t), "verified") {
		t.Errorf("verify without DNS record: want 400 verified=false, got %d %s", vr.Status, truncate(string(vr.Body), 200))
	}
	time.Sleep(3500 * time.Millisecond)
	vr = admin.Do("POST", p.Path("/domains/"+id+"/verify"), map[string]any{"force": true, "skip_dns": true})
	if vr.Status == 200 {
		t.Errorf("verify with force=true succeeded without DNS")
	}
	if code, _, _ := e.EdgeGet(host, "/health"); code != 404 {
		t.Errorf("still unverified, edge must answer 404, got %d", code)
	}

	// wrong token in DNS -> still fails
	e.DNS(t, "set "+challenge+" TXT jp-verify=wrongtoken "+zone)
	vr = admin.Do("POST", p.Path("/domains/"+id+"/verify"), nil)
	if vr.Status != 400 {
		t.Errorf("verify with wrong TXT token: want 400, got %d", vr.Status)
	}

	// correct record -> verified and routed to the app
	e.DNS(t, "set "+challenge+" TXT jp-verify="+token+" "+zone)
	time.Sleep(3500 * time.Millisecond)
	vr = admin.Must(t, "POST", p.Path("/domains/"+id+"/verify"), nil, 200)
	if !Bool(vr.Map(t), "verified") || !Bool(vr.Map(t), "domain", "routed") {
		t.Fatalf("verified response malformed: %s", truncate(string(vr.Body), 300))
	}
	WaitEdge(t, e, host, "/health", 200, 40*time.Second)
	if g := e.TraefikDynamicGrep(t, host); g == "" {
		t.Errorf("verified domain has no router file")
	}

	// the same hostname cannot be claimed by a second project (unique among routed hosts)
	p2 := NewProject(t, admin, d.Org)
	r2 := admin.Must(t, "POST", p2.Path("/domains"), map[string]string{"hostname": host}, 201).Map(t)
	id2, tok2 := Str(r2, "domain", "id"), Str(r2, "verification", "token")
	e.DNS(t, "set "+host+" TXT jp-verify="+tok2+" "+zone)
	time.Sleep(3500 * time.Millisecond)
	if v2 := admin.Do("POST", p2.Path("/domains/"+id2+"/verify"), nil); v2.Status != 409 {
		t.Errorf("second project verifying an already routed hostname: want 409, got %d %s", v2.Status, truncate(string(v2.Body), 200))
	}
	if code, body, _ := e.EdgeGet(host, "/health"); code != 200 || !strings.Contains(body, "jp-e2e-addon-probe") {
		t.Errorf("hostname hijacked by the second project: %d", code)
	}
	e.DNS(t, "del "+host+" TXT "+zone)

	// records removed -> re-verification fails (and the periodic job downgrades; manual verify reports it)
	e.DNS(t, "del "+challenge+" TXT "+zone)
	time.Sleep(3500 * time.Millisecond)
	vr = admin.Do("POST", p.Path("/domains/"+id+"/verify"), nil)
	if vr.Status == 200 {
		t.Errorf("re-verify after removing the DNS record still reports verified")
	}
	cur := admin.Must(t, "GET", p.Path("/domains/"+id), nil, 200).Map(t)
	t.Logf("status after record removal + failed re-verify: %s (periodic re-verification downgrades after two failed 6h runs)", Str(cur, "domain", "status"))

	// restore + delete: router file removed, host no longer routed
	e.DNS(t, "set "+challenge+" TXT jp-verify="+token+" "+zone)
	time.Sleep(3500 * time.Millisecond)
	admin.Must(t, "POST", p.Path("/domains/"+id+"/verify"), nil, 200)
	WaitEdge(t, e, host, "/health", 200, 30*time.Second)
	admin.Must(t, "DELETE", p.Path("/domains/"+id), nil, 204)
	Eventually(t, 30*time.Second, time.Second, "router removed after domain delete", func() (bool, string) {
		return e.TraefikDynamicGrep(t, host) == "", "router file remains"
	})
	// Traefik reloads its file provider asynchronously (throttled), so allow a couple of seconds
	Eventually(t, 20*time.Second, 500*time.Millisecond, "deleted domain no longer routed", func() (bool, string) {
		code, _, _ := e.EdgeGet(host, "/health")
		return code == 404, fmt.Sprintf("still routed: %d", code)
	})
	// deleting again / foreign id -> 404
	admin.Must(t, "DELETE", p.Path("/domains/"+id), nil, 404)
}
