package e2e

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type engineCase struct {
	Engine string
	Env    string // canonical env var read by the fixture
	Name   string // add-on name
	Port   int
	ID     string // add-on id once created
	CID    string // container id
	Pass   string // plaintext password parsed from the secret
}

func TestAddons_RealEnginesReachableFromApp(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)
	admin.Timeout = 10 * time.Minute
	defer func() { admin.Timeout = 60 * time.Second }()

	// --- catalog: only what the catalog marks provisionable is attempted
	cat := admin.Must(t, "GET", p.Path("/addons/catalog"), nil, 200).Map(t)
	prov := map[string]bool{}
	ports := map[string]int{}
	for _, it := range Arr(cat, "catalog") {
		prov[Str(it, "id")] = Bool(it, "provisionable")
		ports[Str(it, "id")] = int(Num(it, "default_port"))
	}
	if Num(cat, "total") < 5 {
		t.Errorf("catalog suspiciously small: total=%v", Dig(cat, "total"))
	}
	if p, ok := prov["redis"]; ok && !p {
		t.Logf("redis is not provisionable on this host")
	}
	all := []engineCase{
		{Engine: "postgres", Env: "DATABASE_URL", Name: "e2e-pg"},
		{Engine: "redis", Env: "REDIS_URL", Name: "e2e-redis"},
		{Engine: "mongodb", Env: "MONGODB_URI", Name: "e2e-mongo"},
		{Engine: "mysql", Env: "MYSQL_URL", Name: "e2e-mysql"},
		{Engine: "rabbitmq", Env: "RABBITMQ_URL", Name: "e2e-rabbit"},
	}
	var cases []*engineCase
	for i := range all {
		c := all[i]
		if !prov[c.Engine] {
			t.Logf("engine %s not provisionable per catalog (skipped)", c.Engine)
			continue
		}
		c.Port = ports[c.Engine]
		cases = append(cases, &c)
	}
	if len(cases) == 0 {
		t.Skip("no provisionable engines in the catalog")
	}

	// --- deploy the app first, add-ons second (the app is redeployed afterwards to receive the env)
	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)

	for _, c := range cases {
		c := c
		t.Run("provision "+c.Engine, func(t *testing.T) {
			start := time.Now()
			r := admin.Must(t, "POST", p.Path("/addons"), map[string]any{"engine": c.Engine, "name": c.Name, "secret_name": c.Env}, 201)
			a := Dig(r.Map(t), "addon")
			c.ID, c.CID = Str(a, "id"), Str(a, "container_id")
			if Str(a, "status") != "ready" {
				t.Fatalf("add-on status = %q (want ready, error=%q)", Str(a, "status"), Str(a, "error"))
			}
			if c.ID == "" || c.CID == "" {
				t.Fatalf("add-on without id/container: %s", truncate(string(r.Body), 400))
			}
			t.Logf("%s ready in %s", c.Engine, time.Since(start).Round(time.Second))
			if Dig(a, "public_url") != nil {
				t.Errorf("add-on exposes a public_url: %v", Dig(a, "public_url"))
			}
			found := false
			for _, k := range Arr(a, "secret_keys") {
				if k == c.Env {
					found = true
				}
			}
			if !found {
				t.Errorf("secret_keys %v do not include %s", Arr(a, "secret_keys"), c.Env)
			}
			if Dig(a, "connection") != nil && !Bool(a, "connection", "masked") {
				t.Errorf("connection must be masked in the create response: %v", Dig(a, "connection"))
			}
		})
	}

	// --- name collision (cheap engine) -> 409, nothing leaked
	if len(cases) > 0 {
		c := cases[0]
		r := admin.Do("POST", p.Path("/addons"), map[string]any{"engine": c.Engine, "name": c.Name, "secret_name": c.Env + "_DUP"})
		if r.Status != 409 {
			t.Errorf("duplicate add-on name: want 409, got %d %s", r.Status, truncate(string(r.Body), 200))
		}
		if r := admin.Do("POST", p.Path("/addons"), map[string]any{"engine": c.Engine, "name": "e2e-collide-secret", "secret_name": c.Env}); r.Status != 409 {
			t.Errorf("secret name collision: want 409, got %d %s", r.Status, truncate(string(r.Body), 200))
		}
		// engines outside the catalog / custom images through the catalog endpoint are refused
		if r := admin.Do("POST", p.Path("/addons"), map[string]any{"engine": "does-not-exist", "name": "e2e-nope"}); r.Status != 400 {
			t.Errorf("unknown engine: want 400, got %d", r.Status)
		}
		if r := admin.Do("POST", p.Path("/addons"), map[string]any{"engine": "redis", "name": "e2e-img", "docker_image": "evil/image"}); r.Status != 400 {
			t.Errorf("docker_image through catalog endpoint: want 400, got %d", r.Status)
		}
	}

	// --- infrastructure: not reachable from outside, no published ports, private network only
	for _, c := range cases {
		if c.CID == "" {
			continue
		}
		ins := e.Inspect(t, c.CID)
		for pt, b := range ins.Net.Ports {
			if len(b) > 0 {
				t.Errorf("%s publishes %s on the host: %v", c.Engine, pt, b)
			}
		}
		for pt, b := range ins.HostConfig.PortBindings {
			if len(b) > 0 {
				t.Errorf("%s has port binding %s: %v", c.Engine, pt, b)
			}
		}
		if _, ok := ins.Net.Networks[p.Network()]; !ok || len(ins.Net.Networks) != 1 {
			t.Errorf("%s must be only on %s: %v", c.Engine, p.Network(), ins.Net.Networks)
		}
		for _, m := range ins.Mounts {
			if strings.Contains(m.Source, "docker.sock") || m.Type == "bind" {
				t.Errorf("%s has a host mount: %+v", c.Engine, m)
			}
		}
		if ins.HostConfig.Privileged {
			t.Errorf("%s runs privileged", c.Engine)
		}
		if c.Port > 0 && e.PortOpen(strconv.Itoa(c.Port)) {
			t.Errorf("%s port %d is reachable from outside the host", c.Engine, c.Port)
		}
	}

	// --- env var names injected into the secret store
	names := secretNames(t, admin, p)
	for _, c := range cases {
		if !names[c.Env] {
			t.Errorf("secret %s missing in production env (have %v)", c.Env, keys(names))
		}
	}
	// passwords: parse from the secret (admin reveal)
	for _, c := range cases {
		g := admin.Must(t, "GET", p.Path("/environments/production/secrets/"+c.Env), nil, 200).Map(t)
		if u, err := url.Parse(Str(g, "secret", "value")); err == nil && u.User != nil {
			c.Pass, _ = u.User.Password()
		}
	}

	// --- restart the app: add-on env must reach the container and every engine must really work
	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)
	code, body, err := e.EdgeGet(host, "/env")
	if err != nil || code != 200 {
		t.Fatalf("/env: %d %v", code, err)
	}
	var envm map[string]any
	_ = json.Unmarshal([]byte(body), &envm)
	have := map[string]bool{}
	for _, n := range Arr(envm, "names") {
		if s, ok := n.(string); ok {
			have[s] = true
		}
	}
	for _, c := range cases {
		if !have[c.Env] {
			t.Errorf("%s not injected into the app container", c.Env)
		}
	}
	for _, c := range cases {
		c := c
		t.Run("probe "+c.Engine, func(t *testing.T) {
			var last string
			Eventually(t, 90*time.Second, 3*time.Second, c.Engine+" probe ok", func() (bool, string) {
				code, body, err := e.EdgeGet(host, "/probe?only="+c.Engine)
				if err != nil {
					return false, err.Error()
				}
				last = body
				var m map[string]any
				_ = json.Unmarshal([]byte(body), &m)
				return code == 200 && Bool(m, c.Engine, "configured") && Bool(m, c.Engine, "ok"), truncate(last, 300)
			})
		})
	}

	// --- access control: a developer (addons view, no reveal) never sees passwords
	dev := CreateUser(t, admin, org, "member", false, Grant{"project_id": p.ID, "preset": "developer"})
	for _, path := range []string{p.Path("/addons"), p.Path("/environments/production/secrets"), p.Path("/environments/production/secrets/" + cases[0].Env)} {
		r := dev.C.Do("GET", path, nil)
		if r.Status != 200 {
			t.Errorf("developer GET %s: %d", path, r.Status)
			continue
		}
		for _, c := range cases {
			if c.Pass != "" && strings.Contains(string(r.Body), c.Pass) {
				t.Errorf("developer response of %s contains the %s password", path, c.Engine)
			}
		}
	}
	for _, c := range cases {
		r := dev.C.Must(t, "GET", p.Path("/addons/"+c.ID), nil, 200)
		if c.Pass != "" && strings.Contains(string(r.Body), c.Pass) {
			t.Errorf("developer GET add-on leaks the %s password", c.Engine)
		}
		if conn := Dig(r.Map(t), "addon", "connection"); conn != nil && !Bool(conn, "masked") {
			t.Errorf("developer sees an unmasked connection: %v", conn)
		}
	}
	if r := dev.C.Do("POST", p.Path("/addons"), map[string]any{"engine": "redis", "name": "dev-redis"}); r.Status != 403 {
		t.Errorf("developer creating an add-on: want 403, got %d", r.Status)
	}
	if r := dev.C.Do("DELETE", p.Path("/addons/"+cases[0].ID), nil); r.Status != 403 {
		t.Errorf("developer deleting an add-on: want 403, got %d", r.Status)
	}
	viewer := CreateUser(t, admin, org, "member", false, Grant{"project_id": p.ID, "preset": "viewer"})
	if r := viewer.C.Do("GET", p.Path("/addons"), nil); r.Status != 403 {
		t.Errorf("viewer (no addons area) listing add-ons: want 403, got %d", r.Status)
	}

	// --- deleting removes container and secrets
	for _, c := range cases {
		admin.Must(t, "DELETE", p.Path("/addons/"+c.ID+"?purge=true"), nil, 204)
		out, _ := e.TryRun("docker ps -a -q --filter id=" + c.CID)
		if strings.TrimSpace(out) != "" {
			t.Errorf("%s container %s still exists after delete", c.Engine, c.CID)
		}
		admin.Must(t, "GET", p.Path("/addons/"+c.ID), nil, 404)
	}
	names = secretNames(t, admin, p)
	for _, c := range cases {
		if names[c.Env] {
			t.Errorf("secret %s still present after the add-on was deleted", c.Env)
		}
		for n := range names {
			if strings.Contains(strings.ToLower(n), strings.ReplaceAll(c.Name, "-", "_")) {
				t.Errorf("leftover secret %s for deleted add-on %s", n, c.Name)
			}
		}
	}
	for _, ctr := range e.ProjectContainers(t, p.ID) {
		if ctr.Kind == "addon" {
			t.Errorf("addon container %s still exists", ctr.Name)
		}
	}
}

func secretNames(t *testing.T, c *Client, p *Project) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, s := range Arr(c.Must(t, "GET", p.Path("/environments/production/secrets"), nil, 200).Map(t), "secrets") {
		out[Str(s, "name")] = true
	}
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
