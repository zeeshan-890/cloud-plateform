package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const zeroUUID = "00000000-0000-4000-8000-000000000001"

func TestSecurity_InfrastructurePortsNotPublic(t *testing.T) {
	e := Load(t)
	// positive control: the public entry points answer
	pub := []string{e.EdgePort}
	for _, p := range pub {
		if !e.PortOpen(p) {
			t.Fatalf("public port %s is not reachable; wrong E2E_BASE_URL / E2E_EDGE_HOST?", p)
		}
	}
	closed := []string{"5432", "6379", "8080", "5000", "9000", "9001", "8000", "3306", "27017", "5672", "15672", "9092", "2375", "2376", "9200", "8081", "9090", "3100"}
	for p := 8001; p <= 8030; p++ {
		closed = append(closed, strconv.Itoa(p))
	}
	for _, p := range closed {
		if e.PortOpen(p) {
			t.Errorf("port %s is reachable from outside the host", p)
		}
	}
	// the host must listen on nothing but ssh/80/443/3000 on non-loopback addresses
	if e.SSH != "" {
		out := e.Run(t, `ss -H -ltn | awk '{print $4}' | grep -v -E '^(127\.|\[::1\]|::1)' | sed 's/.*://' | sort -un | tr '\n' ' '`)
		allowed := map[string]bool{"22": true, "80": true, "443": true, "3000": true, "111": true /* OS rpcbind (RHEL default), not platform */}
		for _, f := range strings.Fields(out) {
			if f == "*" || allowed[f] {
				continue
			}
			if n, _ := strconv.Atoi(f); n >= 32768 { // ephemeral
				continue
			}
			t.Errorf("host listens on non-loopback port %s (allowed: 22,80,443,3000)", f)
		}
	}
}

func TestSecurity_TenantContainerCannotReachPlatform(t *testing.T) {
	d := deployFixture(t)
	e := d.E

	targets := []string{"postgres:5432", "redis:6379", "secret:8014", "identity:8001", "gateway:8000", "runtime:8010",
		"jp-postgres-1:5432", "jp-redis-1:6379", "jp-gateway-1:8000", "jp-runtime-1:8010", "host.docker.internal:2375",
		"172.17.0.1:2375", "172.17.0.1:22", "172.17.0.1:5432", "172.17.0.1:6379", "172.17.0.1:8000"}
	// plus the real addresses of the platform containers on the platform network
	out, _ := e.TryRun(`docker ps --filter name=jp- --format '{{.Names}}' | while read n; do ip=$(docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}} {{end}}' $n); echo "$n $ip"; done`)
	ports := map[string]string{"postgres": "5432", "redis": "6379", "gateway": "8000", "identity": "8001", "runtime": "8010", "secret": "8014", "minio": "9000", "registry": "5000", "traefik": "8080", "domain": "8012"}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for svc, port := range ports {
			if strings.Contains(f[0], svc) {
				for _, ip := range f[1:] {
					targets = append(targets, ip+":"+port)
				}
			}
		}
	}
	code, body, err := e.EdgeGet(d.Host, "/net?t="+strings.Join(targets, ","))
	if err != nil || code != 200 {
		t.Fatalf("/net probe failed: %d %v %s", code, err, truncate(body, 200))
	}
	var res map[string]string
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for tgt, st := range res {
		if st == "open" {
			t.Errorf("tenant container can connect to %s", tgt)
		}
	}
	if len(res) != len(targets) {
		t.Logf("probed %d targets, %d answered", len(targets), len(res))
	}
	// the docker socket is not inside the container
	c := e.RunningApp(t, d.P.ID)
	if o, _ := e.TryRun("docker exec " + c.ID + " ls /var/run/docker.sock"); !strings.Contains(o, "No such file") {
		t.Errorf("docker.sock visible in tenant container: %s", o)
	}
	// and the tenant cannot see other tenants: second project's app is unreachable by name/IP
	p2 := NewProject(t, d.Admin, d.Org)
	DeployApp(t, d.Admin, p2, d.Image, nil)
	WaitEdge(t, e, SystemHost(t, d.Admin, p2), "/health", 200, 90*time.Second)
	c2 := e.RunningApp(t, p2.ID)
	ip2 := strings.TrimSpace(e.Run(t, `docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}} {{end}}' `+c2.ID))
	cross := []string{c2.Name + ":8080"}
	for _, ip := range strings.Fields(ip2) {
		cross = append(cross, ip+":8080")
	}
	code, body, _ = e.EdgeGet(d.Host, "/net?t="+strings.Join(cross, ","))
	var res2 map[string]string
	_ = json.Unmarshal([]byte(body), &res2)
	for tgt, st := range res2 {
		if st == "open" {
			t.Errorf("project A container can reach project B container at %s", tgt)
		}
	}
}

func TestSecurity_InternalEndpointsNotExposed(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	paths := []string{
		"/internal/users/" + zeroUUID, "/internal/users/lookup", "/internal/settings", "/internal/security/stats",
		"/internal/runtime/stats", "/internal/runtime/addon", "/internal/runtime/desired",
		"/internal/orgs/" + zeroUUID + "/projects/" + zeroUUID + "/env?environment=production",
		"/internal/orgs/" + zeroUUID, "/internal/routes/" + zeroUUID, "/internal/domains/system",
		"/internal/projects/" + zeroUUID, "/internal/addons/provision", "/internal/deployments/from-git",
		"/internal/pats/verify", "/internal/repos/" + zeroUUID + "/clone-info",
	}
	roots := []string{e.BaseURL, e.API, e.API + "/internal"}
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, url string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, url, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, string(b)
	}
	forged := map[string]string{"X-Internal-Token": "internal-token", "X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1"}
	authd := map[string]string{"Authorization": "Bearer " + admin.tok()}
	for _, root := range roots {
		for _, p := range paths {
			for _, m := range []string{"GET", "POST", "DELETE", "PUT"} {
				for name, hdr := range map[string]map[string]string{"anon": nil, "forged-token": forged, "admin-jwt": authd} {
					code, body := do(m, root+p, hdr)
					if code >= 200 && code < 300 {
						t.Errorf("%s %s%s (%s) answered %d: %s", m, strings.TrimPrefix(root, e.BaseURL), p, name, code, truncate(body, 120))
					}
					if code >= 500 && code != 502 && code != 0 {
						t.Errorf("%s %s%s (%s) -> %d", m, strings.TrimPrefix(root, e.BaseURL), p, name, code)
					}
				}
			}
		}
	}
}

func TestSecurity_AuthenticationRequiredEverywhere(t *testing.T) {
	e := Load(t)
	_ = AdminClient(t)
	anon := e.NewClient()
	O, P := zeroUUID, zeroUUID
	pp := "/orgs/" + O + "/projects/" + P
	routes := []struct{ m, p string }{
		{"POST", "/auth/logout"}, {"POST", "/auth/logout-all"}, {"GET", "/auth/me"}, {"POST", "/auth/password"}, {"GET", "/auth/sessions"},
		{"DELETE", "/auth/sessions/" + zeroUUID}, {"POST", "/auth/pats"}, {"GET", "/auth/pats"}, {"DELETE", "/auth/pats/" + zeroUUID},
		{"POST", "/auth/mfa/setup"}, {"POST", "/auth/mfa/enable"}, {"POST", "/auth/mfa/disable"},
		{"GET", "/system/settings"}, {"PATCH", "/system/settings"}, {"GET", "/system/security"}, {"GET", "/system/cluster"},
		{"GET", "/cluster/nodes"}, {"POST", "/system/maintenance/cleanup"},
		{"POST", "/orgs"}, {"GET", "/orgs"}, {"POST", "/orgs/invites/accept"}, {"GET", "/orgs/" + O}, {"DELETE", "/orgs/" + O},
		{"POST", "/orgs/" + O + "/invites"}, {"GET", "/orgs/" + O + "/invites"}, {"POST", "/orgs/" + O + "/users"}, {"GET", "/orgs/" + O + "/users"},
		{"PATCH", "/orgs/" + O + "/users/" + zeroUUID}, {"DELETE", "/orgs/" + O + "/users/" + zeroUUID}, {"PUT", "/orgs/" + O + "/users/" + zeroUUID + "/grants"},
		{"POST", "/orgs/" + O + "/users/" + zeroUUID + "/reset-password"}, {"GET", "/orgs/" + O + "/access-presets"}, {"GET", "/orgs/" + O + "/members"},
		{"POST", "/orgs/" + O + "/projects"}, {"GET", "/orgs/" + O + "/projects"}, {"GET", pp}, {"PATCH", pp}, {"DELETE", pp},
		{"GET", pp + "/config"}, {"PUT", pp + "/config"}, {"GET", pp + "/config/drift"},
		{"POST", pp + "/repos"}, {"GET", pp + "/repos"}, {"GET", pp + "/deploy-key"}, {"POST", pp + "/deploy-key/generate"},
		{"GET", "/orgs/" + O + "/github/installations"}, {"GET", "/orgs/" + O + "/github/repos"},
		{"POST", pp + "/deployments"}, {"GET", pp + "/deployments"}, {"GET", pp + "/deployments/" + zeroUUID}, {"POST", pp + "/deployments/rollback"},
		{"GET", pp + "/builds"}, {"GET", pp + "/builds/" + zeroUUID + "/logs"}, {"GET", pp + "/images"},
		{"GET", pp + "/runtime/instances"}, {"POST", pp + "/runtime/instances"}, {"POST", pp + "/runtime/instances/" + zeroUUID + "/stop"},
		{"GET", pp + "/runtime/containers"}, {"GET", pp + "/runtime/logs"},
		{"GET", pp + "/domains"}, {"POST", pp + "/domains"}, {"POST", pp + "/domains/" + zeroUUID + "/verify"}, {"DELETE", pp + "/domains/" + zeroUUID},
		{"GET", pp + "/certificates"},
		{"GET", pp + "/environments"}, {"GET", pp + "/secrets"}, {"GET", pp + "/environments/production/secrets"}, {"POST", pp + "/environments/production/secrets"},
		{"GET", pp + "/environments/production/secrets/X"}, {"PUT", pp + "/environments/production/secrets/X"}, {"DELETE", pp + "/environments/production/secrets/X"},
		{"GET", pp + "/logs"}, {"POST", pp + "/logs/ingest"}, {"GET", pp + "/metrics"},
		{"GET", pp + "/storage/bucket"}, {"POST", pp + "/storage/bucket"}, {"GET", pp + "/storage/objects"}, {"POST", pp + "/storage/objects"}, {"DELETE", pp + "/storage/objects"},
		{"GET", pp + "/databases"}, {"POST", pp + "/databases"}, {"GET", pp + "/databases/" + zeroUUID},
		{"GET", "/orgs/" + O + "/addons"}, {"GET", "/orgs/" + O + "/addons/catalog"}, {"GET", pp + "/addons"}, {"POST", pp + "/addons"}, {"DELETE", pp + "/addons/" + zeroUUID},
		{"GET", pp + "/cron"}, {"POST", pp + "/cron"}, {"GET", pp + "/queues"},
		{"POST", pp + "/ai/explain"}, {"POST", "/orgs/" + O + "/ai/ask"}, {"GET", "/orgs/" + O + "/ai/config"},
	}
	for _, rt := range routes {
		var body any
		if rt.m == "POST" || rt.m == "PUT" || rt.m == "PATCH" {
			body = map[string]string{}
		}
		if r := anon.Do(rt.m, rt.p, body); r.Status != 401 {
			t.Errorf("unauthenticated %s %s: want 401, got %d", rt.m, rt.p, r.Status)
		}
		// malformed / forged bearer
		bad := e.NewClient()
		bad.Token = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ4IiwiYWRtIjp0cnVlfQ."
		if r := bad.Do(rt.m, rt.p, body); r.Status != 401 {
			t.Errorf("alg=none token %s %s: want 401, got %d", rt.m, rt.p, r.Status)
		}
	}
	// unknown API paths are real 404s (never HTML)
	admin := AdminClient(t)
	r := admin.Do("GET", "/definitely/not/a/route", nil)
	if r.Status != 404 || bytes.Contains(bytes.ToLower(r.Body), []byte("<html")) {
		t.Errorf("unknown API path: want JSON 404, got %d %s", r.Status, truncate(string(r.Body), 100))
	}
}

func TestSecurity_BodyLimits(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	big := func(n int) []byte {
		return []byte(`{"name":"` + strings.Repeat("a", n) + `","slug":"x"}`)
	}
	// ~1.5 MiB: past the service-level 1 MiB cap
	r := admin.DoRaw("POST", "/orgs", big(1536<<10), "application/json", nil)
	if r.Status != 400 && r.Status != 413 {
		t.Errorf("1.5MiB JSON body: want 400/413, got %d", r.Status)
	}
	// 6 MiB: past the gateway cap
	r = admin.DoRaw("POST", "/orgs", big(6<<20), "application/json", nil)
	if r.Status != 413 && r.Status != 400 && r.Status != 0 {
		t.Errorf("6MiB JSON body: want 413, got %d", r.Status)
	}
	// unauthenticated endpoints are capped too
	r = e.NewClient().DoRaw("POST", "/auth/login", big(2<<20), "application/json", nil)
	if r.Status != 400 && r.Status != 413 && r.Status != 0 {
		t.Errorf("2MiB login body: want 400/413, got %d", r.Status)
	}
	// nothing was created
	for _, o := range Arr(admin.Must(t, "GET", "/orgs", nil, 200).Map(t), "orgs") {
		if strings.Count(Str(o, "name"), "a") > 100000 {
			t.Errorf("oversized organization was created")
		}
	}
}

func TestSystem_AdminEndpoints(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	plain := CreateUser(t, admin, org, "admin", false)

	// non-admin (even an org admin) is refused
	for _, path := range []string{"/system/security", "/system/cluster", "/system/settings", "/cluster/nodes"} {
		if r := plain.C.Do("GET", path, nil); r.Status != 403 {
			t.Errorf("non-admin GET %s: want 403, got %d", path, r.Status)
		}
		if r := e.NewClient().Do("GET", path, nil); r.Status != 401 {
			t.Errorf("anonymous GET %s: want 401, got %d", path, r.Status)
		}
	}
	if r := plain.C.Do("PATCH", "/system/settings", map[string]any{"settings": map[string]string{}}); r.Status != 403 {
		t.Errorf("non-admin PATCH settings: want 403, got %d", r.Status)
	}
	if r := plain.C.Do("POST", "/system/maintenance/cleanup", nil); r.Status != 403 {
		t.Errorf("non-admin maintenance cleanup: want 403, got %d", r.Status)
	}

	// security checks are real
	sec := admin.Must(t, "GET", "/system/security", nil, 200).Map(t)
	checks := Arr(sec, "checks")
	if len(checks) < 8 {
		t.Fatalf("expected a real set of checks, got %d: %s", len(checks), truncate(mustJSON(sec), 300))
	}
	allowed := map[string]bool{}
	for _, id := range strings.Split(getenv("E2E_ALLOW_FAIL", "tls,admin_mfa"), ",") {
		allowed[strings.TrimSpace(id)] = true
	}
	counts := map[string]int{}
	for _, c := range checks {
		st, id := Str(c, "status"), Str(c, "id")
		counts[st]++
		if id == "" || Str(c, "title") == "" || Str(c, "detail") == "" || (st != "pass" && st != "warn" && st != "fail") {
			t.Errorf("malformed check: %v", c)
		}
		if st == "fail" && !allowed[id] {
			t.Errorf("security check %q FAILED: %s", id, Str(c, "detail"))
		}
		if st == "fail" && allowed[id] {
			t.Logf("tolerated failing check %s (E2E_ALLOW_FAIL): %s", id, Str(c, "detail"))
		}
	}
	for _, k := range []string{"pass", "warn", "fail"} {
		if int(Num(sec, "summary", k)) != counts[k] {
			t.Errorf("summary.%s = %v, counted %d", k, Dig(sec, "summary", k), counts[k])
		}
	}

	// cluster numbers are real
	cl := admin.Must(t, "GET", "/system/cluster", nil, 200).Map(t)
	node := Dig(cl, "node")
	if Num(node, "cpu", "cores") <= 0 {
		t.Errorf("cpu.cores = %v", Dig(node, "cpu", "cores"))
	}
	if Num(node, "memory", "total_bytes") <= 0 || Num(node, "memory", "used_bytes") <= 0 {
		t.Errorf("memory not populated: %v", Dig(node, "memory"))
	}
	if Num(node, "disk", "total_bytes") <= 0 || Num(node, "disk", "used_bytes") <= 0 {
		t.Errorf("disk not populated: %v", Dig(node, "disk"))
	}
	// counts cover tenant (jp.managed) containers only, so an empty platform legitimately reports 0
	if Dig(node, "containers") == nil {
		t.Errorf("container stats missing: %v", node)
	}
	if Str(cl, "mode") != "single-node" {
		t.Errorf("mode = %q", Str(cl, "mode"))
	}
	if e.SSH != "" {
		out := e.Run(t, "nproc")
		if n, _ := strconv.Atoi(out); n > 0 && int(Num(node, "cpu", "cores")) != n {
			t.Errorf("reported cores %v != nproc %d", Dig(node, "cpu", "cores"), n)
		}
		mem := e.Run(t, `awk '/MemTotal/{print $2*1024}' /proc/meminfo`)
		if want, _ := strconv.ParseFloat(mem, 64); want > 0 {
			got := Num(node, "memory", "total_bytes")
			if got < want*0.9 || got > want*1.1 {
				t.Errorf("memory total %v differs from /proc/meminfo %v", got, want)
			}
		}
	}
	if st := admin.Must(t, "GET", "/system/settings", nil, 200).Map(t); Dig(st, "editable") == nil || Dig(st, "platform") == nil {
		t.Errorf("settings payload missing: %v", st)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestSecurity_NoStackTraceOrSecretsInErrors(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/orgs/not-a-uuid"}, {"GET", "/orgs/not-a-uuid/projects/also-bad"}, {"GET", "/orgs/" + zeroUUID + "/projects/" + zeroUUID + "/domains/'"},
	} {
		r := admin.Do(tc.m, tc.p, nil)
		s := strings.ToLower(string(r.Body))
		for _, leak := range []string{"goroutine ", "panic:", ".go:", "pq:", "pgx", "sql:", "jwt_secret", "internal_token", os.Getenv("INTERNAL_TOKEN")} {
			if leak != "" && strings.Contains(s, strings.ToLower(leak)) {
				t.Errorf("%s %s leaks internals (%q): %s", tc.m, tc.p, leak, truncate(string(r.Body), 200))
			}
		}
		if r.Status >= 500 && r.Status != 502 {
			t.Errorf("%s %s -> %d", tc.m, tc.p, r.Status)
		}
	}
	_ = e
}
