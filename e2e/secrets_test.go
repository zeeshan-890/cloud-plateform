package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSecrets_Lifecycle(t *testing.T) {
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	base := p.Path("/environments/production/secrets")

	// create -> metadata only
	r := admin.Must(t, "POST", base, map[string]string{"name": "E2E_DB_PASSWORD", "value": "hunter2-hunter2-hunter2"}, 201)
	if strings.Contains(string(r.Body), "hunter2-hunter2-hunter2") {
		t.Errorf("create response leaks the plaintext: %s", r.Body)
	}
	if r := admin.Do("POST", base, map[string]string{"name": "E2E_DB_PASSWORD", "value": "again"}); r.Status != 409 {
		t.Errorf("duplicate secret: want 409, got %d", r.Status)
	}
	for _, bad := range []string{"1BAD", "A-B", "has space", "", strings.Repeat("A", 200)} {
		if r := admin.Do("POST", base, map[string]string{"name": bad, "value": "x"}); r.Status != 400 {
			t.Errorf("invalid secret name %q: want 400, got %d", truncate(bad, 20), r.Status)
		}
	}
	if r := admin.Do("POST", base, map[string]string{"name": "E2E_EMPTY", "value": ""}); r.Status != 400 {
		t.Errorf("empty value: want 400, got %d", r.Status)
	}

	// list -> hint but never plaintext
	l := admin.Must(t, "GET", base, nil, 200)
	if strings.Contains(string(l.Body), "hunter2-hunter2-hunter2") {
		t.Fatalf("secret list leaks plaintext: %s", l.Body)
	}
	var meta any
	for _, s := range Arr(l.Map(t), "secrets") {
		if Str(s, "name") == "E2E_DB_PASSWORD" {
			meta = s
		}
	}
	if meta == nil || Str(meta, "value_hint") == "" || Str(meta, "value") != "" {
		t.Fatalf("list entry malformed: %v", meta)
	}
	if l := admin.Must(t, "GET", p.Path("/secrets"), nil, 200); strings.Contains(string(l.Body), "hunter2-hunter2-hunter2") {
		t.Errorf("/secrets leaks plaintext")
	}

	// reveal
	g := admin.Must(t, "GET", base+"/E2E_DB_PASSWORD", nil, 200).Map(t)
	if Str(g, "secret", "value") != "hunter2-hunter2-hunter2" || !Bool(g, "revealed") {
		t.Errorf("reveal failed: %v", g)
	}

	// rotate (new value) -> version 2
	rot := admin.Must(t, "PUT", base+"/E2E_DB_PASSWORD", map[string]string{"value": "rotated-value-123"}, 200).Map(t)
	if Num(rot, "secret", "current_version") != 2 {
		t.Errorf("rotation should bump the version to 2: %v", rot)
	}
	// rename keeps the value
	admin.Must(t, "PUT", base+"/E2E_DB_PASSWORD", map[string]string{"new_name": "E2E_DB_PASS_RENAMED"}, 200)
	admin.Must(t, "GET", base+"/E2E_DB_PASSWORD", nil, 404)
	g = admin.Must(t, "GET", base+"/E2E_DB_PASS_RENAMED", nil, 200).Map(t)
	if Str(g, "secret", "value") != "rotated-value-123" {
		t.Errorf("rename lost the value: %v", g)
	}
	// rename onto an existing name is a conflict
	admin.Must(t, "POST", base, map[string]string{"name": "E2E_OTHER", "value": "v"}, 201)
	if r := admin.Do("PUT", base+"/E2E_OTHER", map[string]string{"new_name": "E2E_DB_PASS_RENAMED"}); r.Status != 409 {
		t.Errorf("rename onto existing: want 409, got %d", r.Status)
	}

	// the shared "all" scope and unknown environments
	admin.Must(t, "POST", p.Path("/environments/all/secrets"), map[string]string{"name": "E2E_ALL_ONE", "value": "a"}, 201)
	if r := admin.Do("GET", p.Path("/environments/nonsense/secrets"), nil); r.Status != 400 {
		t.Errorf("unknown environment: want 400, got %d", r.Status)
	}

	// delete
	admin.Must(t, "DELETE", base+"/E2E_DB_PASS_RENAMED", nil, 204)
	admin.Must(t, "GET", base+"/E2E_DB_PASS_RENAMED", nil, 404)
	admin.Must(t, "DELETE", base+"/E2E_DB_PASS_RENAMED", nil, 404)

	// audit trail of reveals (the gateway may not expose it; report rather than fail)
	if a := admin.Do("GET", p.Path("/secrets/audit"), nil); a.Status == 200 {
		if !strings.Contains(string(a.Body), "secret.reveal") {
			t.Errorf("audit log does not contain secret.reveal: %s", truncate(string(a.Body), 300))
		}
	} else {
		t.Logf("GAP: GET %s/secrets/audit is not reachable through the gateway (%d); reveal auditing cannot be verified black-box", p.Path(""), a.Status)
	}
}

func TestSecrets_InjectionAndEnvironmentIsolation(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)

	admin.Must(t, "POST", p.Path("/environments/production/secrets"), map[string]string{"name": "E2E_PROD_ONLY", "value": "prod-v1"}, 201)
	admin.Must(t, "POST", p.Path("/environments/preview/secrets"), map[string]string{"name": "E2E_PREVIEW_ONLY", "value": "preview-only-value"}, 201)
	admin.Must(t, "POST", p.Path("/environments/all/secrets"), map[string]string{"name": "E2E_ALL_SHARED", "value": "shared-v1"}, 201)

	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)

	code, body, err := e.EdgeGet(host, "/env")
	if err != nil || code != 200 {
		t.Fatalf("/env: %d %v %s", code, err, body)
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode /env: %v", err)
	}
	if Str(m, "markers", "E2E_PROD_ONLY") != "prod-v1" {
		t.Errorf("production secret not injected into the running container: %v", Dig(m, "markers"))
	}
	if Str(m, "markers", "E2E_ALL_SHARED") != "shared-v1" {
		t.Errorf("'all' scoped secret not injected: %v", Dig(m, "markers"))
	}
	if v := Str(m, "markers", "E2E_PREVIEW_ONLY"); v != "" {
		t.Errorf("preview secret leaked into the production container: %q", v)
	}
	for _, n := range Arr(m, "names") {
		s, _ := n.(string)
		for _, bad := range []string{"INTERNAL_TOKEN", "JWT_SECRET", "MASTER_KEY", "REDIS_PASSWORD", "POSTGRES_PASSWORD", "MINIO_ROOT_PASSWORD"} {
			if strings.Contains(strings.ToUpper(s), bad) {
				t.Errorf("platform secret %s visible inside tenant container", s)
			}
		}
	}

	// specific environment wins over "all"; a changed secret reaches the NEW container after a redeploy
	admin.Must(t, "POST", p.Path("/environments/production/secrets"), map[string]string{"name": "E2E_ALL_SHARED", "value": "prod-wins"}, 201)
	admin.Must(t, "PUT", p.Path("/environments/production/secrets/E2E_PROD_ONLY"), map[string]string{"value": "prod-v2"}, 200)
	DeployApp(t, admin, p, img, nil)
	Eventually(t, 60*time.Second, time.Second, "rotated secret visible in new container", func() (bool, string) {
		code, body, err := e.EdgeGet(host, "/env")
		if err != nil || code != 200 {
			return false, "edge not ready"
		}
		return strings.Contains(body, `"E2E_PROD_ONLY":"prod-v2"`) && strings.Contains(body, `"E2E_ALL_SHARED":"prod-wins"`), truncate(body, 300)
	})
	c := e.RunningApp(t, p.ID)
	ins := e.Inspect(t, c.ID)
	if v, _ := ins.EnvValue("E2E_PROD_ONLY"); v != "prod-v2" {
		t.Errorf("docker env E2E_PROD_ONLY=%q", v)
	}
	if ins.EnvHas("E2E_PREVIEW_ONLY") {
		t.Errorf("preview secret present in production container env")
	}
	for k, v := range ins.Config.Labels {
		if strings.Contains(v, "prod-v2") || strings.Contains(k, "E2E_PROD_ONLY") {
			t.Errorf("secret value leaked into label %s", k)
		}
	}
}
