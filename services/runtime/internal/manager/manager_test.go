package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/testutil"
)

const (
	orgID  = "11111111-1111-4111-8111-111111111111"
	projID = "22222222-2222-4222-8222-222222222222"
	dep1   = "aaaaaaaa-1111-4111-8111-111111111111"
	dep2   = "bbbbbbbb-2222-4222-8222-222222222222"
)

type fakeSecrets struct {
	mu   sync.Mutex
	envs []string
	env  map[string]string
}

func (f *fakeSecrets) Env(_ context.Context, org, proj, environment string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.envs = append(f.envs, environment)
	out := map[string]string{}
	for k, v := range f.env {
		out[k] = v
	}
	if environment == "preview" {
		out["ONLY_PREVIEW"] = "1"
	}
	return out, nil
}

func (f *fakeSecrets) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.envs[len(f.envs)-1]
}

type fakeRoutes struct {
	fd   *testutil.FakeDocker
	fail error
	got  []string
}

func (r *fakeRoutes) SetService(_ context.Context, project, svc string) error {
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, svc)
	r.fd.Record("route " + svc)
	return nil
}

func (r *fakeRoutes) SetUpstreams(_ context.Context, project, svc string, upstreams []string) error {
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, "upstreams:"+strings.Join(upstreams, ","))
	r.fd.Record("route-upstreams " + strings.Join(upstreams, ","))
	return nil
}

type fakeProber struct {
	fd     *testutil.FakeDocker
	status map[string]int // by image
}

func (p *fakeProber) Probe(_ context.Context, ip string, port int, path string) (int, error) {
	c := p.fd.ByIP(ip)
	if c == nil {
		return 0, errors.New("connection refused")
	}
	if s, ok := p.status[c.Image]; ok {
		return s, nil
	}
	return 200, nil
}

type rig struct {
	fd      *testutil.FakeDocker
	repo    *testutil.MemRepo
	m       *Manager
	secrets *fakeSecrets
	routes  *fakeRoutes
	prober  *fakeProber
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fd := testutil.NewFakeDocker()
	t.Cleanup(fd.Close)
	r := &rig{fd: fd, repo: testutil.NewMemRepo(), secrets: &fakeSecrets{env: map[string]string{"DATABASE_URL": "postgres://x"}},
		routes: &fakeRoutes{fd: fd}, prober: &fakeProber{fd: fd, status: map[string]int{}}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.m = New(ctx, Config{TraefikContainer: "jp-traefik-1", ProberContainer: "jp-prober", HealthTimeout: 400 * time.Millisecond, HealthInterval: 5 * time.Millisecond,
		DrainTime: 0, StopTimeout: time.Second, MaxJobs: 2}, fd.Engine(), r.repo, r.secrets, r.routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.m.Prober = r.prober
	return r
}

func (r *rig) deploy(t *testing.T, dep, image, strategy string, preview bool) (*DeployResult, error) {
	t.Helper()
	return r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, DeploymentID: dep, ImageRef: image, Strategy: strategy, Preview: preview})
}

func idx(events []string, ev string) int {
	for i, e := range events {
		if e == ev {
			return i
		}
	}
	return -1
}

func TestDeployHardeningLabelsAndNetwork(t *testing.T) {
	r := newRig(t)
	res, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err != nil {
		t.Fatal(err)
	}
	net := "jp_p_22222222"
	if _, ok := r.fd.Networks[net]; !ok {
		t.Fatalf("project network not created: %v", r.fd.Networks)
	}
	if r.fd.NetLabels[net]["jp.project"] != projID || r.fd.NetLabels[net]["jp.managed"] != "true" {
		t.Fatalf("network labels: %v", r.fd.NetLabels[net])
	}
	// Traefik (routing) and the prober sidecar (health probes by container IP) join the project network; the runtime does not
	got := r.fd.Networks[net]
	hasTraefik, hasSelf := false, false
	for _, c := range got {
		if c == "jp-traefik-1" {
			hasTraefik = true
		}
		if c == "jp-prober" {
			hasSelf = true
		}
	}
	if !hasTraefik || !hasSelf || len(got) != 2 {
		t.Fatalf("project network attachments want traefik+prober (never the runtime), got: %v", got)
	}
	c := r.fd.ByName(res.Instances[0].ContainerName)
	if c == nil {
		t.Fatal("container not created")
	}
	host := c.Body["HostConfig"].(map[string]any)
	if host["NetworkMode"] != net {
		t.Errorf("NetworkMode = %v, want %s (must not be the platform network)", host["NetworkMode"], net)
	}
	if host["Privileged"] != false {
		t.Error("privileged")
	}
	if caps := host["CapDrop"].([]any); len(caps) != 1 || caps[0] != "ALL" {
		t.Errorf("CapDrop = %v", caps)
	}
	if caps := host["CapAdd"].([]any); len(caps) != 1 || caps[0] != "NET_BIND_SERVICE" {
		t.Errorf("CapAdd = %v", caps)
	}
	if so := host["SecurityOpt"].([]any); len(so) != 1 || so[0] != "no-new-privileges:true" {
		t.Errorf("SecurityOpt = %v", so)
	}
	if host["PidsLimit"].(float64) != 512 {
		t.Errorf("PidsLimit = %v", host["PidsLimit"])
	}
	if host["Memory"].(float64) != 512<<20 || host["MemorySwap"].(float64) != 512<<20 {
		t.Errorf("memory = %v swap=%v", host["Memory"], host["MemorySwap"])
	}
	if host["NanoCpus"].(float64) != 1e9 {
		t.Errorf("NanoCpus = %v", host["NanoCpus"])
	}
	if host["ReadonlyRootfs"] != false {
		t.Error("ReadonlyRootfs must be false per contract")
	}
	if _, ok := host["Tmpfs"].(map[string]any)["/tmp"]; !ok {
		t.Error("tmpfs /tmp missing")
	}
	lc := host["LogConfig"].(map[string]any)
	if lc["Type"] != "json-file" || lc["Config"].(map[string]any)["max-size"] != "10m" || lc["Config"].(map[string]any)["max-file"] != "3" {
		t.Errorf("LogConfig = %v", lc)
	}
	for _, forbidden := range []string{"Binds", "Devices", "PidMode", "IpcMode", "UsernsMode", "CgroupnsMode"} {
		if _, ok := host[forbidden]; ok {
			t.Errorf("HostConfig must not set %s", forbidden)
		}
	}
	if host["RestartPolicy"].(map[string]any)["Name"] != "on-failure" {
		t.Errorf("RestartPolicy = %v", host["RestartPolicy"])
	}
	// labels: exactly the contract set, and no routers
	want := map[string]string{
		"traefik.enable": "true", "traefik.docker.network": net,
		"traefik.http.services.jp-app-22222222-aaaaaaaa.loadbalancer.server.port": "8080",
		"jp.managed": "true", "jp.org": orgID, "jp.project": projID, "jp.deployment": dep1, "jp.kind": "app",
	}
	if len(c.Labels) != len(want) {
		t.Errorf("labels = %v", c.Labels)
	}
	for k, v := range want {
		if c.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, c.Labels[k], v)
		}
	}
	for k := range c.Labels {
		if strings.Contains(k, ".routers.") {
			t.Errorf("router label leaked: %s", k)
		}
	}
	env := fmt.Sprint(c.Body["Env"])
	if !strings.Contains(env, "PORT=8080") || !strings.Contains(env, "DATABASE_URL=postgres://x") {
		t.Errorf("env = %s", env)
	}
	if res.Service != "jp-app-22222222-aaaaaaaa" || res.Port != 8080 {
		t.Errorf("result = %+v", res)
	}
}

func TestRollingStartsNewBeforeStoppingOldAndGates(t *testing.T) {
	r := newRig(t)
	first, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err != nil {
		t.Fatal(err)
	}
	oldName := first.Instances[0].ContainerName
	res, err := r.deploy(t, dep2, "registry.local/app:2", "rolling", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Replaced) != 1 {
		t.Fatalf("replaced = %v", res.Replaced)
	}
	ev := r.fd.EventsSnapshot()
	newName := res.Instances[0].ContainerName
	startNew, stopOld := idx(ev, "start "+newName), idx(ev, "stop "+oldName)
	if startNew < 0 || stopOld < 0 || startNew > stopOld {
		t.Fatalf("new container must start before old stops: %v", ev)
	}
	if r.fd.ByName(oldName) != nil {
		t.Error("old container should be removed")
	}
	if c := r.fd.ByName(newName); c == nil || !c.Running {
		t.Error("new container should be running")
	}
	for _, in := range r.repo.Instances {
		if in.ContainerName == oldName && (in.Status != "stopped" || in.DesiredState != "stopped") {
			t.Errorf("old instance state = %s/%s", in.Status, in.DesiredState)
		}
	}
}

func TestRollingHealthGateFailureKeepsOldRunning(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	r.prober.status["registry.local/app:bad"] = 503
	_, err := r.deploy(t, dep2, "registry.local/app:bad", "rolling", false)
	var de *DeployError
	if !errors.As(err, &de) || de.Stage != "health_gate" {
		t.Fatalf("want health_gate DeployError, got %v", err)
	}
	live := r.fd.Live()
	if len(live) != 1 || live[0].Image != "registry.local/app:1" || !live[0].Running {
		t.Fatalf("old container must still be the only live, running one: %+v", live)
	}
	failed := 0
	for _, in := range r.repo.Instances {
		if in.ImageRef == "registry.local/app:bad" {
			failed++
			if in.Status != "failed" || in.Error == "" {
				t.Errorf("failed instance state = %s err=%q", in.Status, in.Error)
			}
		}
		if in.ImageRef == "registry.local/app:1" && in.Status != "running" {
			t.Errorf("old instance must stay running, is %s", in.Status)
		}
	}
	if failed != 1 {
		t.Errorf("failed instances = %d", failed)
	}
}

func TestCrashingContainerFailsGateFast(t *testing.T) {
	r := newRig(t)
	r.fd.CrashImages["registry.local/app:crash"] = true
	start := time.Now()
	_, err := r.deploy(t, dep1, "registry.local/app:crash", "rolling", false)
	var de *DeployError
	if !errors.As(err, &de) || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Error("exited container should fail the gate immediately, not wait for the timeout")
	}
	if len(r.fd.Live()) != 0 {
		t.Error("crashed container must be removed")
	}
}

func TestBlueGreenFlipsRouteThenDrainsOld(t *testing.T) {
	r := newRig(t)
	res1, err := r.deploy(t, dep1, "registry.local/app:1", "blue_green", false)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Color != "blue" || res1.Service != "jp-app-22222222-aaaaaaaa" {
		t.Fatalf("first colour: %+v", res1)
	}
	oldName := res1.Instances[0].ContainerName
	res2, err := r.deploy(t, dep2, "registry.local/app:2", "blue_green", false)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Color != "green" || res2.Service != "jp-app-22222222-bbbbbbbb" {
		t.Fatalf("second colour: %+v", res2)
	}
	if got := r.routes.got; len(got) != 2 || got[1] != "jp-app-22222222-bbbbbbbb" {
		t.Fatalf("routes = %v", got)
	}
	ev := r.fd.EventsSnapshot()
	flip := idx(ev, "route jp-app-22222222-bbbbbbbb")
	if flip < 0 || flip > idx(ev, "stop "+oldName) {
		t.Fatalf("route must flip before the old colour is stopped: %v", ev)
	}
	res3, err := r.deploy(t, "cccccccc-3333-4333-8333-333333333333", "registry.local/app:3", "blue_green", false)
	if err != nil || res3.Color != "blue" {
		t.Fatalf("third colour = %+v %v", res3, err)
	}
}

func TestBlueGreenRouteFailureKeepsOld(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "blue_green", false); err != nil {
		t.Fatal(err)
	}
	r.routes.fail = errors.New("domain service down")
	_, err := r.deploy(t, dep2, "registry.local/app:2", "blue_green", false)
	var de *DeployError
	if !errors.As(err, &de) || de.Stage != "route_switch" {
		t.Fatalf("err = %v", err)
	}
	live := r.fd.Live()
	if len(live) != 1 || live[0].Image != "registry.local/app:1" {
		t.Fatalf("old colour must keep serving: %+v", live)
	}
}

func TestStagedRoutingNewContainerCarriesNoSharedService(t *testing.T) {
	r := newRig(t)
	first, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err != nil {
		t.Fatal(err)
	}
	oldName := first.Instances[0].ContainerName
	// the new version fails its gate: nothing may have been routed to it, old routing untouched
	r.prober.status["registry.local/app:bad"] = 503
	if _, err := r.deploy(t, dep2, "registry.local/app:bad", "rolling", false); err == nil {
		t.Fatal("expected gate failure")
	}
	if got := r.routes.got; len(got) != 1 || got[0] != "jp-app-22222222-aaaaaaaa" {
		t.Fatalf("a failed deploy must not flip routes: %v", got)
	}
	if c := r.fd.ByName(oldName); c == nil || !c.Running {
		t.Fatal("old container must keep serving")
	}
	// every container carries its OWN service label, never the one the routers point at
	for _, c := range r.fd.Live() {
		n := 0
		for k := range c.Labels {
			if strings.HasPrefix(k, "traefik.http.services.") {
				n++
				if !strings.Contains(k, "jp-app-22222222-aaaaaaaa") {
					t.Errorf("unexpected service label %s", k)
				}
			}
		}
		if n != 1 {
			t.Errorf("container %s has %d service labels", c.Name, n)
		}
	}
	// successful redeploy: start -> flip -> stop old, new service is unique
	res, err := r.deploy(t, "dddddddd-4444-4444-8444-444444444444", "registry.local/app:2", "rolling", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Service != "jp-app-22222222-dddddddd" {
		t.Fatalf("service = %s", res.Service)
	}
	ev := r.fd.EventsSnapshot()
	st, fl, so := idx(ev, "start "+res.Instances[0].ContainerName), idx(ev, "route jp-app-22222222-dddddddd"), idx(ev, "stop "+oldName)
	if !(st >= 0 && st < fl && fl < so) {
		t.Fatalf("order must be start -> flip -> stop old: %v", ev)
	}
}

func TestPreviewIsIsolatedFromProduction(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	prev, err := r.deploy(t, dep2, "registry.local/app:pr", "blue_green", true)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Service != "jp-prev-bbbbbbbb" {
		t.Fatalf("preview service = %s", prev.Service)
	}
	if len(prev.Replaced) != 0 {
		t.Fatal("preview must never replace production")
	}
	if len(r.fd.Live()) != 2 {
		t.Fatalf("production must keep running next to the preview: %d live", len(r.fd.Live()))
	}
	if len(r.routes.got) != 1 { // only the production deploy flipped
		t.Errorf("preview must not touch production routes: %v", r.routes.got)
	}
	c := r.fd.ByName(prev.Instances[0].ContainerName)
	if c.Labels["jp.kind"] != "preview" || c.Labels["traefik.http.services.jp-prev-bbbbbbbb.loadbalancer.server.port"] == "" {
		t.Errorf("preview labels = %v", c.Labels)
	}
	if got := r.secrets.last(); got != "preview" {
		t.Errorf("secret environment for preview = %q", got)
	}
	// a later production rolling deploy must not stop the preview
	if _, err := r.deploy(t, "dddddddd-4444-4444-8444-444444444444", "registry.local/app:2", "rolling", false); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range r.fd.Live() {
		if c.Labels["jp.kind"] == "preview" && c.Running {
			found = true
		}
	}
	if !found {
		t.Error("preview was stopped by a production deploy")
	}
}

func TestDeployIsIdempotentPerDeployment(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	res, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err != nil || !res.Idempotent {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n := len(r.fd.Live()); n != 1 {
		t.Fatalf("duplicate containers created: %d", n)
	}
}

func TestDeployValidation(t *testing.T) {
	r := newRig(t)
	bads := []DeployRequest{
		{OrgID: "x", ProjectID: projID, ImageRef: "a:b"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "--privileged"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "evil image"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "repo/UPPER"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "a/b:1", Strategy: "chaos"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "a/b:1", DeploymentID: "../etc"},
		{OrgID: orgID, ProjectID: projID, ImageRef: "a/b:1", Spec: Spec{Env: map[string]string{"A B": "x"}}},
	}
	for i, b := range bads {
		_, err := r.m.Deploy(context.Background(), b)
		var br *ErrBadRequest
		if !errors.As(err, &br) {
			t.Errorf("case %d: want ErrBadRequest, got %v", i, err)
		}
	}
	if len(r.fd.RequestsSnapshot()) != 0 {
		t.Errorf("invalid requests must not reach docker: %v", r.fd.RequestsSnapshot())
	}
}

func TestPortDetectionOrder(t *testing.T) {
	r := newRig(t)
	r.fd.ExposedPorts = []int{9090}
	res, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err != nil || res.Port != 9090 {
		t.Fatalf("image exposed port: %+v %v", res, err)
	}
	r.secrets.env["PORT"] = "7000"
	res, err = r.deploy(t, dep2, "registry.local/app:2", "rolling", false)
	if err != nil || res.Port != 7000 {
		t.Fatalf("PORT secret should win over image: %+v %v", res, err)
	}
	res, err = r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, ImageRef: "registry.local/app:3", Spec: Spec{Port: 4321}})
	if err != nil || res.Port != 4321 {
		t.Fatalf("jp.yaml port should win: %+v %v", res, err)
	}
	if env := fmt.Sprint(r.fd.ByName(res.Instances[0].ContainerName).Body["Env"]); !strings.Contains(env, "PORT=4321") {
		t.Errorf("PORT env = %s", env)
	}
}

func TestJobRunsInSeparateContainerAndNeverTouchesProduction(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	r.fd.ExitCodeForJob = 3
	run, err := r.m.RunJob(context.Background(), JobRequest{OrgID: orgID, ProjectID: projID, CronID: "c1", Name: "nightly", Command: []string{"node", "job.js"}})
	if err != nil {
		t.Fatal(err)
	}
	r.m.Wait()
	got := r.repo.JobSnapshot(run.ID)
	if got.Status != "failed" || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("job run = %+v", got)
	}
	var job *testutil.FakeContainer
	r.fd.Mu().Lock()
	for _, c := range r.fd.Containers {
		if c.Labels["jp.kind"] == "job" {
			job = c
		}
	}
	r.fd.Mu().Unlock()
	if job == nil {
		t.Fatal("no job container created")
	}
	host := job.Body["HostConfig"].(map[string]any)
	if host["AutoRemove"] != true {
		t.Error("AutoRemove must be set")
	}
	if fmt.Sprint(job.Body["Cmd"]) != "[node job.js]" {
		t.Errorf("Cmd = %v", job.Body["Cmd"])
	}
	if _, ok := job.Labels["traefik.enable"]; ok {
		t.Error("jobs must not be routable")
	}
	if !strings.HasPrefix(job.Name, "jp-job-22222222-") {
		t.Errorf("job name = %s", job.Name)
	}
	for _, ev := range r.fd.EventsSnapshot() {
		if strings.HasPrefix(ev, "stop ") {
			t.Errorf("job run touched production: %s", ev)
		}
	}
	prod := 0
	for _, c := range r.fd.Live() {
		if c.Labels["jp.kind"] == "app" && c.Running {
			prod++
		}
	}
	if prod != 1 {
		t.Errorf("production containers running = %d", prod)
	}
	run2, err := r.m.RunJob(context.Background(), JobRequest{OrgID: orgID, ProjectID: projID, Name: "again", Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	r.m.Wait()
	if r.repo.JobSnapshot(run2.ID).ContainerName == got.ContainerName {
		t.Error("job container names must be unique")
	}
}

func TestJobSuccessAndNoImage(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.RunJob(context.Background(), JobRequest{OrgID: orgID, ProjectID: projID, Name: "x"}); err == nil {
		t.Fatal("job without image and without production deployment must be rejected")
	}
	run, err := r.m.RunJob(context.Background(), JobRequest{OrgID: orgID, ProjectID: projID, ImageRef: "registry.local/job:1", Command: []string{"echo", "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	r.m.Wait()
	if got := r.repo.JobSnapshot(run.ID); got.Status != "succeeded" || *got.ExitCode != 0 {
		t.Fatalf("run = %+v", got)
	}
}

func TestAddonLifecycle(t *testing.T) {
	r := newRig(t)
	res, err := r.m.StartAddon(context.Background(), AddonRequest{OrgID: orgID, ProjectID: projID, Name: "pg-main", Image: "postgres:16", Port: 5432,
		Env: map[string]string{"POSTGRES_PASSWORD": "s3cret"}, Volume: "/var/lib/postgresql/data"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Host != "addon-pg-main" || res.Port != 5432 || res.ContainerID == "" {
		t.Fatalf("res = %+v", res)
	}
	c := r.fd.ByName("jp-addon-22222222-pg-main")
	host := c.Body["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "jp_p_22222222" || host["Privileged"] != false {
		t.Errorf("addon host config: %v", host)
	}
	if aliases := fmt.Sprint(c.Body["NetworkingConfig"]); !strings.Contains(aliases, "addon-pg-main") {
		t.Errorf("alias missing: %s", aliases)
	}
	if _, ok := c.Labels["traefik.enable"]; ok {
		t.Error("add-ons must not be routable")
	}
	mounts := host["Mounts"].([]any)
	if m := mounts[0].(map[string]any); m["Type"] != "volume" || m["Target"] != "/var/lib/postgresql/data" {
		t.Errorf("mounts = %v", mounts)
	}
	r.fd.Volumes["jp-addon-22222222-pg-main"] = map[string]string{"jp.managed": "true", "jp.project": projID}
	if _, err := r.m.StartAddon(context.Background(), AddonRequest{OrgID: orgID, ProjectID: projID, Name: "pg-main", Image: "postgres:16", Port: 5432}); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	app, _ := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if err := r.m.RemoveAddon(context.Background(), app.Instances[0].ContainerID, true); err == nil {
		t.Fatal("RemoveAddon must refuse app containers")
	}
	if err := r.m.RemoveAddon(context.Background(), res.ContainerID, true); err != nil {
		t.Fatal(err)
	}
	if r.fd.ByName("jp-addon-22222222-pg-main") != nil {
		t.Error("addon container should be gone")
	}
	if _, ok := r.fd.Volumes["jp-addon-22222222-pg-main"]; ok {
		t.Error("purge must delete the volume")
	}
	if err := r.m.RemoveAddon(context.Background(), res.ContainerID, true); err != nil {
		t.Fatalf("second removal: %v", err)
	}
}

func TestDeleteProjectCascade(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.StartAddon(context.Background(), AddonRequest{OrgID: orgID, ProjectID: projID, Name: "redis", Image: "redis:7", Port: 6379}); err != nil {
		t.Fatal(err)
	}
	other := "99999999-9999-4999-8999-999999999999"
	if _, err := r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: other, ImageRef: "registry.local/other:1"}); err != nil {
		t.Fatal(err)
	}
	r.fd.Volumes["v1"] = map[string]string{"jp.managed": "true", "jp.project": projID}
	r.fd.Volumes["v2"] = map[string]string{"jp.managed": "true", "jp.project": other}
	rep, err := r.m.DeleteProject(context.Background(), projID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ContainersRemoved != 2 || rep.VolumesRemoved != 1 || !rep.NetworkRemoved || len(rep.Errors) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if _, ok := r.fd.Networks["jp_p_22222222"]; ok {
		t.Error("network should be removed")
	}
	if _, ok := r.fd.Volumes["v2"]; !ok {
		t.Error("other project's volume removed")
	}
	live := r.fd.Live()
	if len(live) != 1 || live[0].Labels["jp.project"] != other {
		t.Errorf("other tenant's container affected: %+v", live)
	}
	if left, _ := r.repo.ListByProject(context.Background(), projID); len(left) != 0 {
		t.Error("instance rows should be deleted")
	}
}

func TestProjectContainersAreScoped(t *testing.T) {
	r := newRig(t)
	other := "99999999-9999-4999-8999-999999999999"
	otherOrg := "88888888-8888-4888-8888-888888888888"
	_, _ = r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	if _, err := r.m.Deploy(context.Background(), DeployRequest{OrgID: otherOrg, ProjectID: other, ImageRef: "registry.local/other:1"}); err != nil {
		t.Fatal(err)
	}
	list, err := r.m.ProjectContainers(context.Background(), orgID, projID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	sawFilter := false
	for _, q := range r.fd.RequestsSnapshot() {
		if strings.Contains(q, "/containers/json") && strings.Contains(q, "jp.org%3D"+orgID) {
			sawFilter = true
		}
	}
	if !sawFilter {
		t.Error("docker list must be filtered server-side by jp.org/jp.project labels")
	}
}

func TestProbeReportsTruth(t *testing.T) {
	r := newRig(t)
	res, _ := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	in, _ := r.repo.GetByID(context.Background(), res.Instances[0].ID)
	pr, err := r.m.Probe(context.Background(), in)
	if err != nil || !pr.Healthy || in.Status != "running" {
		t.Fatalf("probe = %+v err=%v", pr, err)
	}
	r.prober.status["registry.local/app:1"] = 500
	pr, _ = r.m.Probe(context.Background(), in)
	if pr.Healthy || in.Status != "unhealthy" {
		t.Fatalf("5xx must be unhealthy: %+v / %s", pr, in.Status)
	}
	r.fd.Mu().Lock()
	r.fd.ByNameLocked(in.ContainerName).Removed = true
	r.fd.Mu().Unlock()
	pr, _ = r.m.Probe(context.Background(), in)
	if pr.Exists || in.Status != "failed" || in.Error != "container missing" {
		t.Fatalf("missing container: %+v / %s %s", pr, in.Status, in.Error)
	}
}

func TestRestartDoesNotCreateDuplicates(t *testing.T) {
	r := newRig(t)
	res, _ := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	in, _ := r.repo.GetByID(context.Background(), res.Instances[0].ID)
	r.fd.Mu().Lock()
	r.fd.ByNameLocked(in.ContainerName).Running = false
	r.fd.Mu().Unlock()
	if err := r.m.Restart(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(r.fd.Live()) != 1 || len(r.repo.Instances) != 1 {
		t.Fatalf("restart created duplicates: %d containers / %d rows", len(r.fd.Live()), len(r.repo.Instances))
	}
	if in.RestartCount != 1 || in.Status != "running" {
		t.Errorf("instance = %+v", in)
	}
}

func TestReconcileMarksMissingAndRemovesOrphans(t *testing.T) {
	r := newRig(t)
	res, _ := r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	_, err := r.fd.Engine().Create(context.Background(), dockerx.ContainerSpec{Name: "jp-orphan", Image: "x/y:1", Network: "jp_p_22222222",
		Labels: map[string]string{"jp.managed": "true", "jp.kind": "app", "jp.org": orgID, "jp.project": projID}})
	if err != nil {
		t.Fatal(err)
	}
	r.fd.Mu().Lock()
	r.fd.ByNameLocked(res.Instances[0].ContainerName).Removed = true
	r.fd.Mu().Unlock()
	rep, err := r.m.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.MarkedMissing != 1 || rep.OrphansRemoved != 1 {
		t.Fatalf("report = %+v", rep)
	}
	in, _ := r.repo.GetByID(context.Background(), res.Instances[0].ID)
	if in.Status != "failed" {
		t.Errorf("status = %s", in.Status)
	}
}

func TestDockerDownReturnsErrorsNotSimulation(t *testing.T) {
	fd := testutil.NewFakeDocker()
	eng := fd.Engine()
	fd.Close()
	repo := testutil.NewMemRepo()
	m := New(context.Background(), Config{HealthTimeout: 50 * time.Millisecond}, eng, repo, &fakeSecrets{}, &fakeRoutes{fd: fd}, nil)
	_, err := m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, ImageRef: "a/b:1"})
	if err == nil || !ErrDockerDown(err) {
		t.Fatalf("want docker unavailable error, got %v", err)
	}
	if eng.Mode() != dockerx.ModeDocker || eng.Simulated() {
		t.Fatal("engine must stay in docker mode")
	}
	if _, err := m.Stats(context.Background()); !ErrDockerDown(err) {
		t.Fatalf("stats err = %v", err)
	}
	for _, in := range repo.Instances {
		if in.Status == "running" {
			t.Error("instance marked running although docker is down")
		}
	}
}

func TestStopByDeployment(t *testing.T) {
	r := newRig(t)
	_, _ = r.deploy(t, dep1, "registry.local/app:1", "rolling", false)
	prev, _ := r.deploy(t, dep2, "registry.local/app:pr", "rolling", true)
	n, err := r.m.StopByDeployment(context.Background(), dep2)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if r.fd.ByName(prev.Instances[0].ContainerName) != nil {
		t.Error("preview container should be removed")
	}
	if len(r.fd.Live()) != 1 {
		t.Error("production must remain")
	}
	if _, err := r.m.StopByDeployment(context.Background(), "not-a-uuid"); err == nil {
		t.Error("invalid deployment id should be rejected")
	}
}

type sinkRec struct{ got []MetricSample }

func (s *sinkRec) Push(_ context.Context, b []MetricSample) error {
	s.got = append(s.got, b...)
	return nil
}

func TestStatsAndMetricsSampling(t *testing.T) {
	r := newRig(t)
	if _, err := r.deploy(t, dep1, "registry.local/app:1", "rolling", false); err != nil {
		t.Fatal(err)
	}
	s, err := r.m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// cpu = (1e6/5e7) * 2 cpus * 1000 = 40 millicores ; mem = 100MiB - 20MiB inactive file
	if s.ManagedRunning != 1 || s.ManagedTotal != 1 || s.CPUMillicores < 39.9 || s.CPUMillicores > 40.1 || s.MemUsedBytes != 80<<20 {
		t.Fatalf("stats = %+v", s)
	}
	sink := &sinkRec{}
	if err := r.m.SampleOnce(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 2 || sink.got[0].OrgID != orgID || sink.got[0].ProjectID != projID || sink.got[0].Name != "container_cpu_millicores" {
		t.Fatalf("samples = %+v", sink.got)
	}
}

func TestForkPreviewGetsNoSecrets(t *testing.T) {
	r := newRig(t)
	res, err := r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, DeploymentID: dep2, ImageRef: "registry.local/app:fork", Preview: true, NoSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	env := fmt.Sprint(r.fd.ByName(res.Instances[0].ContainerName).Body["Env"])
	if strings.Contains(env, "DATABASE_URL") || strings.Contains(env, "ONLY_PREVIEW") {
		t.Fatalf("fork code received secrets: %s", env)
	}
	r.secrets.mu.Lock()
	n := len(r.secrets.envs)
	r.secrets.mu.Unlock()
	if n != 0 {
		t.Fatal("secret service must not even be asked for fork previews")
	}
}
