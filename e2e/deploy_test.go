package e2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// deployed is a project running the fixture image behind its system hostname.
type deployed struct {
	E     *Env
	Admin *Client
	Org   *Org
	P     *Project
	Host  string
	Image string
}

func deployFixture(t *testing.T) *deployed {
	t.Helper()
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)
	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)
	return &deployed{E: e, Admin: admin, Org: org, P: p, Host: host, Image: img}
}

func (d *deployed) version(t *testing.T) string {
	t.Helper()
	code, body, err := d.E.EdgeGet(d.Host, "/health")
	if err != nil || code != 200 {
		return ""
	}
	if i := strings.Index(body, `"version":"`); i >= 0 {
		v := body[i+11:]
		return v[:strings.IndexByte(v, '"')]
	}
	return ""
}

func TestDeploy_ContainerHardeningAndRouting(t *testing.T) {
	d := deployFixture(t)
	e, p := d.E, d.P

	if want := p.Slug + "-" + d.Org.Slug; !strings.HasPrefix(d.Host, want+".") {
		t.Errorf("system hostname %q should start with <project-slug>-<org-slug>.", d.Host)
	}
	if e.RootDom != "" && !strings.HasSuffix(d.Host, "."+e.RootDom) {
		t.Errorf("system hostname %q not under ROOT_DOMAIN %s", d.Host, e.RootDom)
	}
	code, body, _ := e.EdgeGet(d.Host, "/health")
	if code != 200 || !strings.Contains(body, "jp-e2e-addon-probe") {
		t.Fatalf("app not reachable through the edge: %d %s", code, body)
	}
	// unknown hosts do not route to the app
	if code, _, _ := e.EdgeGet("nope-"+randHex(3)+".invalid-host.test", "/health"); code != 404 {
		t.Errorf("unknown Host header: want 404, got %d", code)
	}

	c := e.RunningApp(t, p.ID)
	ins := e.Inspect(t, c.ID)
	hc := ins.HostConfig
	capDropAll := false
	for _, cd := range hc.CapDrop {
		if strings.EqualFold(cd, "ALL") {
			capDropAll = true
		}
	}
	if !capDropAll {
		t.Errorf("CapDrop must contain ALL, got %v", hc.CapDrop)
	}
	for _, ca := range hc.CapAdd {
		if !strings.EqualFold(strings.TrimPrefix(ca, "CAP_"), "NET_BIND_SERVICE") {
			t.Errorf("unexpected added capability %s", ca)
		}
	}
	nnp := false
	for _, so := range hc.SecurityOpt {
		if strings.HasPrefix(so, "no-new-privileges") {
			nnp = true
		}
	}
	if !nnp {
		t.Errorf("no-new-privileges missing: %v", hc.SecurityOpt)
	}
	if hc.Memory <= 0 {
		t.Errorf("no memory limit set (Memory=%d)", hc.Memory)
	}
	if hc.PidsLimit == nil || *hc.PidsLimit <= 0 {
		t.Errorf("no pids limit set")
	}
	if hc.Privileged {
		t.Errorf("container runs privileged")
	}
	if hc.NetworkMode == "host" {
		t.Errorf("container uses host networking")
	}
	for _, b := range hc.Binds {
		t.Errorf("host bind mount present: %s", b)
	}
	for _, m := range ins.Mounts {
		if strings.Contains(m.Source, "docker.sock") || strings.Contains(m.Destination, "docker.sock") {
			t.Errorf("docker socket mounted: %+v", m)
		}
		if m.Type == "bind" {
			t.Errorf("bind mount present: %+v", m)
		}
	}
	for p, b := range ins.Net.Ports {
		if len(b) > 0 {
			t.Errorf("container publishes port %s on the host: %v", p, b)
		}
	}
	for p, b := range hc.PortBindings {
		if len(b) > 0 {
			t.Errorf("port binding present for %s: %v", p, b)
		}
	}
	// networks: only the private project network, NOT the platform net
	if len(ins.Net.Networks) != 1 {
		t.Errorf("app must be attached to exactly one network, got %v", ins.Net.Networks)
	}
	if _, ok := ins.Net.Networks[p.Network()]; !ok {
		t.Errorf("app not on project network %s: %v", p.Network(), ins.Net.Networks)
	}
	// labels: no routers in labels (domain service owns routers), managed labels present
	for k := range ins.Config.Labels {
		if strings.HasPrefix(k, "traefik.http.routers") {
			t.Errorf("container label defines a router (%s); routers must only come from the domain service", k)
		}
	}
	if ins.Config.Labels["jp.managed"] != "true" || ins.Config.Labels["jp.project"] != p.ID {
		t.Errorf("jp.* labels missing: %v", ins.Config.Labels)
	}
	// the project network exists and traefik is attached to it
	nets := e.Run(t, `docker network ls --format '{{.Name}}' | grep -x `+p.Network()+` || true`)
	if nets != p.Network() {
		t.Fatalf("project network %s does not exist", p.Network())
	}
	members := e.Run(t, `docker network inspect -f '{{range .Containers}}{{.Name}} {{end}}' `+p.Network())
	if !strings.Contains(strings.ToLower(members), "traefik") {
		t.Errorf("traefik is not attached to %s (members: %s)", p.Network(), members)
	}
	// platform network membership: no platform container name besides traefik on the tenant network
	for _, n := range strings.Fields(members) {
		ln := strings.ToLower(n)
		if strings.HasPrefix(ln, "jp-") && !strings.Contains(ln, "traefik") && ln != "jp-prober" && !tenantContainerName.MatchString(ln) {
			t.Errorf("unexpected platform container %s on the tenant network", n)
		}
	}
	// runtime API view
	inst := d.Admin.Must(t, "GET", p.Path("/runtime/instances"), nil, 200).Map(t)
	found := false
	for _, i := range Arr(inst, "instances") {
		if Str(i, "status") == "running" {
			found = true
			if Str(i, "network") != p.Network() {
				t.Errorf("instance network = %q, want %s", Str(i, "network"), p.Network())
			}
		}
	}
	if !found {
		t.Errorf("no running instance in the runtime API: %v", inst)
	}
	cs := d.Admin.Must(t, "GET", p.Path("/runtime/containers"), nil, 200)
	if !strings.Contains(string(cs.Body), "container") && !strings.Contains(string(cs.Body), c.ID[:8]) {
		t.Logf("runtime/containers body: %s", truncate(string(cs.Body), 300))
	}
}

func TestDeploy_StopStart(t *testing.T) {
	d := deployFixture(t)
	insts := Arr(d.Admin.Must(t, "GET", d.P.Path("/runtime/instances"), nil, 200).Map(t), "instances")
	if len(insts) == 0 {
		t.Fatal("no instances")
	}
	id := Str(insts[0], "id")
	d.Admin.Must(t, "POST", d.P.Path("/runtime/instances/"+id+"/stop"), nil, 200)
	Eventually(t, 40*time.Second, time.Second, "edge stops serving a stopped app", func() (bool, string) {
		code, _, _ := d.E.EdgeGet(d.Host, "/health")
		return code != 200, "still 200"
	})
	d.Admin.Must(t, "POST", d.P.Path("/runtime/instances/"+id+"/start"), nil, 200)
	WaitEdge(t, d.E, d.Host, "/health", 200, 60*time.Second)
}

func TestDeploy_RollingRedeployZeroDowntime(t *testing.T) {
	d := deployFixture(t)
	v2 := DerivedImage(t, "2", "ENV APP_VERSION=2")
	if v := d.version(t); v != "1" {
		t.Fatalf("initial version = %q, want 1", v)
	}
	oldID := d.E.RunningApp(t, d.P.ID).ID

	poll := StartPoller(d.E, d.Host, "/health")
	time.Sleep(1500 * time.Millisecond)
	DeployApp(t, d.Admin, d.P, v2, nil)
	// keep polling through the drain period of the old container
	time.Sleep(15 * time.Second)
	total, bad := poll.Stop()
	if total < 50 {
		t.Fatalf("poller ran only %d requests", total)
	}
	if len(bad) > 0 {
		t.Errorf("downtime during rolling redeploy: %d/%d requests failed: %v", len(bad), total, bad[:minInt(len(bad), 8)])
	}
	if poll.Seen("1") == 0 || poll.Seen("2") == 0 {
		t.Errorf("expected both versions to be seen (v1=%d v2=%d)", poll.Seen("1"), poll.Seen("2"))
	}
	if v := d.version(t); v != "2" {
		t.Errorf("after redeploy version = %q, want 2", v)
	}
	// old container is gone (stopped) and exactly one app container runs
	Eventually(t, 40*time.Second, time.Second, "old container stopped", func() (bool, string) {
		apps := Running(d.E.ProjectApps(t, d.P.ID))
		if len(apps) != 1 {
			return false, "running app containers != 1"
		}
		return apps[0].ID != oldID, "old container still running"
	})
}

func TestDeploy_FailedVersionKeepsOldServing(t *testing.T) {
	d := deployFixture(t)
	crash := DerivedImage(t, "crash", "CMD node -e process.exit(1)")
	oldID := d.E.RunningApp(t, d.P.ID).ID

	poll := StartPoller(d.E, d.Host, "/health")
	time.Sleep(1000 * time.Millisecond)

	// 1) crashing app: health gate must fail, deployment reported as failed
	r := d.Admin.Do("POST", d.P.Path("/runtime/instances"), map[string]any{"image_ref": crash, "port": 8080})
	if r.Status != 422 {
		t.Errorf("crashing version: want 422 deploy_failed, got %d %s", r.Status, truncate(string(r.Body), 300))
	} else if Str(r.Map(t), "error", "code") != "deploy_failed" {
		t.Errorf("crashing version: error code = %q", Str(r.Map(t), "error", "code"))
	}
	// 2) image that does not exist
	r = d.Admin.Do("POST", d.P.Path("/runtime/instances"), map[string]any{"image_ref": ImageBase() + ":does-not-exist-" + randHex(2), "port": 8080})
	if r.Status < 400 {
		t.Errorf("non-existent image accepted: %d %s", r.Status, truncate(string(r.Body), 200))
	}
	// 3) invalid image references are rejected up-front
	for _, bad := range []string{"", "-v /:/host alpine", "alpine; rm -rf /", "a b", "ALPINE:1 --privileged"} {
		if r := d.Admin.Do("POST", d.P.Path("/runtime/instances"), map[string]any{"image_ref": bad}); r.Status != 400 {
			t.Errorf("image_ref %q: want 400, got %d", bad, r.Status)
		}
	}
	time.Sleep(2 * time.Second)
	total, badObs := poll.Stop()
	if len(badObs) > 0 {
		t.Errorf("old version stopped serving while a failed deploy was attempted: %d/%d bad: %v", len(badObs), total, badObs[:minInt(len(badObs), 8)])
	}
	if v := d.version(t); v != "1" {
		t.Errorf("old version must keep serving, got %q", v)
	}
	apps := Running(d.E.ProjectApps(t, d.P.ID))
	if len(apps) != 1 || apps[0].ID != oldID {
		t.Errorf("exactly the original container should be running after failed deploys, got %+v", apps)
	}
	// the failed attempt is visible in the runtime API
	inst := d.Admin.Must(t, "GET", d.P.Path("/runtime/instances"), nil, 200).Map(t)
	sawFailed := false
	for _, i := range Arr(inst, "instances") {
		if st := Str(i, "status"); st == "failed" || st == "exited" || st == "crashed" || Str(i, "error") != "" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Logf("note: no failed instance record visible in /runtime/instances: %s", truncate(string(inst2s(inst)), 400))
	}
}

func inst2s(m map[string]any) string {
	var sb strings.Builder
	for _, i := range Arr(m, "instances") {
		sb.WriteString(Str(i, "id") + ":" + Str(i, "status") + " ")
	}
	return sb.String()
}

func TestDeploy_RollbackToPreviousImage(t *testing.T) {
	d := deployFixture(t)
	v2 := DerivedImage(t, "2", "ENV APP_VERSION=2")
	DeployApp(t, d.Admin, d.P, v2, nil)
	Eventually(t, 60*time.Second, time.Second, "v2 serving", func() (bool, string) { return d.version(t) == "2", d.version(t) })

	poll := StartPoller(d.E, d.Host, "/health")
	time.Sleep(time.Second)
	DeployApp(t, d.Admin, d.P, d.Image, nil) // roll back = redeploy the previous image
	time.Sleep(12 * time.Second)
	_, bad := poll.Stop()
	if len(bad) > 0 {
		t.Errorf("downtime during rollback: %v", bad[:minInt(len(bad), 8)])
	}
	if v := d.version(t); v != "1" {
		t.Errorf("rollback did not restore v1: %q", v)
	}
	// deployment-level rollback with no recorded git deployment is a clean 404 (not a 5xx)
	if r := d.Admin.Do("POST", d.P.Path("/deployments/rollback"), nil); r.Status != 404 {
		t.Errorf("rollback without earlier deployments: want 404, got %d %s", r.Status, truncate(string(r.Body), 200))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// tenant containers are named jp-<project8>-<id8> (add-ons: jp-addon-...)
var tenantContainerName = regexp.MustCompile(`^jp-([0-9a-f]{8}|addon|preview)-`)
