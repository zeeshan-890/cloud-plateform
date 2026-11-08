package manager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
	"github.com/jp-cloud/runtime/internal/testutil"
)

const (
	nodeIDa   = "99999999-9999-4999-8999-999999999999"
	nodeToken = "tok-0123456789abcdef0123456789abcdef"
	nodePriv  = "10.9.9.9"
)

type fakeNodes struct {
	mu        sync.Mutex
	node      *store.Node
	placement map[string]string
}

func (f *fakeNodes) GetNode(_ context.Context, id string) (*store.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.node != nil && f.node.ID == id {
		c := *f.node
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeNodes) Placement(_ context.Context, p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.placement[p], nil
}
func (f *fakeNodes) DeletePlacement(_ context.Context, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.placement, p)
	return nil
}

// remoteProber answers only for the node's published port, like a real app behind the node's private IP.
type remoteProber struct {
	mu    sync.Mutex
	hits  []string
	alive map[string]bool
	// any: answer for every port on the node's private IP (the runtime chooses the port)
	any bool
}

func (p *remoteProber) Probe(_ context.Context, ip string, port int, _ string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	p.hits = append(p.hits, addr)
	if p.alive[addr] || (p.any && ip == nodePriv) {
		return 200, nil
	}
	return 0, errors.New("connection refused")
}

type nodeRig struct {
	local, remote *testutil.FakeDocker
	repo          *testutil.MemRepo
	nodes         *fakeNodes
	m             *Manager
	routes        *fakeRoutes
	prober        *remoteProber
	key           []byte
}

func newNodeRig(t *testing.T) *nodeRig {
	t.Helper()
	local, remote := testutil.NewFakeDocker(), testutil.NewFakeDocker()
	t.Cleanup(local.Close)
	t.Cleanup(remote.Close)
	key := secretbox.DeriveKey("test-master", "runtime-nodes")
	sealed, err := secretbox.Seal(key, nodeToken, "runtime-node:node-a")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(remote.Srv.URL)
	r := &nodeRig{local: local, remote: remote, repo: testutil.NewMemRepo(), key: key,
		routes: &fakeRoutes{fd: local}, prober: &remoteProber{alive: map[string]bool{}, any: true},
		nodes: &fakeNodes{placement: map[string]string{}, node: &store.Node{ID: nodeIDa, Name: "node-a", Addr: u.Host, PrivateIP: nodePriv, TokenSealed: sealed, Enabled: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.m = New(ctx, Config{TraefikContainer: "jp-traefik-1", ProberContainer: "jp-prober", HealthTimeout: 300 * time.Millisecond, HealthInterval: 5 * time.Millisecond,
		StopTimeout: time.Second, MaxJobs: 2}, local.Engine(), r.repo, &fakeSecrets{env: map[string]string{}}, r.routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.m.Prober = r.prober
	r.m.Nodes, r.m.NodeKey = r.nodes, key
	return r
}

func (r *nodeRig) place(node string) {
	r.nodes.mu.Lock()
	r.nodes.placement[projID] = node
	r.nodes.mu.Unlock()
}

func (r *nodeRig) deploy(t *testing.T, dep, image string) (*DeployResult, error) {
	t.Helper()
	return r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, DeploymentID: dep, ImageRef: image, Strategy: "rolling", Spec: Spec{Port: 3000}})
}

func TestPlacement_AppRunsOnRemoteNodeAndIsRoutedByUpstream(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	// the app answers only on the node's published private-IP port (the first port the fake daemon hands out)
	res, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	hp := res.Instances[0].HostPort
	if len(res.Instances) != 1 || res.Instances[0].NodeID != nodeIDa || hp < hostPortMin || hp > hostPortMax {
		t.Fatalf("instance must record its node and a published port in %d-%d: %+v", hostPortMin, hostPortMax, res.Instances)
	}
	// every container landed on the remote daemon, none on this server
	if n := len(r.remote.Live()); n != 1 {
		t.Fatalf("remote containers = %d, want 1", n)
	}
	if n := len(r.local.Live()); n != 0 {
		t.Fatalf("local containers = %d, want 0 (the project is placed on node-a)", n)
	}
	// published on the node's PRIVATE ip only, with a daemon-chosen host port
	hc, _ := r.remote.Live()[0].Body["HostConfig"].(map[string]any)
	pb := hc["PortBindings"].(map[string]any)["3000/tcp"].([]any)[0].(map[string]any)
	if pb["HostIp"] != nodePriv || pb["HostPort"] != strconv.Itoa(hp) {
		t.Fatalf("port must be published on the private IP at the runtime-chosen FIXED port %d: %+v", hp, pb)
	}
	// hardening still applies on the remote daemon
	if hc["Privileged"] != false || hc["NetworkMode"] != "jp_p_22222222" {
		t.Fatalf("hardening lost on remote: %+v", hc)
	}
	// the control plane authenticated to the node agent with the stored token
	if len(r.remote.Tokens) != 1 || r.remote.Tokens[0] != nodeToken {
		t.Fatalf("node token not sent: %v", r.remote.Tokens)
	}
	// health gate probed the published private-IP port, not a container IP
	if len(r.prober.hits) == 0 || r.prober.hits[0] != net.JoinHostPort(nodePriv, strconv.Itoa(hp)) {
		t.Fatalf("probe hits = %v", r.prober.hits)
	}
	// routing uses an upstream URL, not a local Traefik service; Traefik and the prober were not attached remotely
	if len(r.routes.got) != 1 || r.routes.got[0] != "upstreams:http://"+net.JoinHostPort(nodePriv, strconv.Itoa(hp)) {
		t.Fatalf("routes = %v", r.routes.got)
	}
	for _, ev := range r.remote.EventsSnapshot() {
		if strings.Contains(ev, "jp-traefik-1") || strings.Contains(ev, "jp-prober") {
			t.Fatalf("remote node must not get traefik/prober attached: %v", ev)
		}
	}
}

func TestPlacement_UnplacedProjectStaysLocal(t *testing.T) {
	r := newNodeRig(t)
	// local deploys use the ordinary container-IP probe; make the local fake healthy
	r.m.Prober = &fakeProber{fd: r.local, status: map[string]int{}}
	res, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Instances[0].NodeID != "" || res.Instances[0].HostPort != 0 {
		t.Fatalf("unplaced project must stay local: %+v", res.Instances[0])
	}
	if len(r.remote.Live()) != 0 || len(r.local.Live()) != 1 {
		t.Fatalf("local=%d remote=%d", len(r.local.Live()), len(r.remote.Live()))
	}
	if len(r.routes.got) != 1 || strings.HasPrefix(r.routes.got[0], "upstreams:") {
		t.Fatalf("local deploys keep the Traefik service route: %v", r.routes.got)
	}
}

func TestPlacement_DisabledOrMissingNodeFailsClosed(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	r.nodes.mu.Lock()
	r.nodes.node.Enabled = false
	r.nodes.mu.Unlock()
	_, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	var de *DeployError
	if !errors.As(err, &de) || de.Stage != "placement" || !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("a disabled node must stop the deploy at placement, got %v", err)
	}
	if len(r.local.Live()) != 0 {
		t.Fatal("must never fall back to running on this server")
	}
	// a node row that vanished behaves the same
	r.nodes.mu.Lock()
	r.nodes.node = nil
	r.nodes.mu.Unlock()
	r.m.InvalidateNode(nodeIDa)
	if _, err := r.deploy(t, "bbbbbbbb-1111-4111-8111-111111111111", "127.0.0.1:5000/org/app:1"); !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("missing node: %v", err)
	}
}

func TestPlacement_UnreachableNodeIsReportedNotGuessed(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	r.remote.Close() // the node agent is down
	_, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	if err == nil || !errors.Is(err, dockerx.ErrUnavailable) || !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("unreachable node must surface ErrUnavailable and ErrNodeUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "node-a") || !strings.Contains(err.Error(), "node agent") {
		t.Fatalf("the error must name the server and say what to check: %v", err)
	}
	if len(r.local.Live()) != 0 {
		t.Fatal("must never fall back to this server")
	}
}

func TestReconcile_UnreachableNodeNeverMarksInstancesMissing(t *testing.T) {
	r := newNodeRig(t)
	ctx := context.Background()
	in := &store.Instance{OrgID: orgID, ProjectID: projID, Role: "app", Status: "running", DesiredState: "running", ContainerID: "c-remote", ContainerName: "x",
		Port: 3000, NodeID: nodeIDa, Network: "jp_p_22222222"}
	if err := r.repo.Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	r.remote.Close()
	rep, err := r.m.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.repo.GetByID(ctx, in.ID)
	if rep.MarkedMissing != 0 || got.Status != "running" {
		t.Fatalf("an unreachable node says nothing about its containers: report=%+v status=%s", rep, got.Status)
	}
}

func TestDeleteProject_CleansEveryServerAndPlacement(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	if _, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1"); err != nil {
		t.Fatal(err)
	}
	// also something left over on this server for the same project
	if _, err := r.local.Engine().EnsureNetwork(context.Background(), "jp_p_22222222", map[string]string{"jp.managed": "true", "jp.project": projID}); err != nil {
		t.Fatal(err)
	}
	rep, err := r.m.DeleteProject(context.Background(), projID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.remote.Live()) != 0 || rep.ContainersRemoved != 1 {
		t.Fatalf("remote containers must be removed: live=%d report=%+v", len(r.remote.Live()), rep)
	}
	if _, still := r.remote.Networks["jp_p_22222222"]; still {
		t.Fatal("remote project network must be removed")
	}
	if id, _ := r.nodes.Placement(context.Background(), projID); id != "" {
		t.Fatal("placement must be dropped with the project")
	}
}

func TestPlacement_PreviewsStayLocal(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	r.m.Prober = &fakeProber{fd: r.local, status: map[string]int{}}
	res, err := r.m.Deploy(context.Background(), DeployRequest{OrgID: orgID, ProjectID: projID, DeploymentID: dep1, ImageRef: "127.0.0.1:5000/org/app:1", Preview: true, Spec: Spec{Port: 3000}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Instances[0].NodeID != "" || len(r.remote.Live()) != 0 {
		t.Fatalf("previews use Traefik's docker provider and always run on this server: %+v", res.Instances[0])
	}
}

func TestFriendlyImageErr_ExplainsArchitectureMismatch(t *testing.T) {
	raw := errors.New(`image x not available: docker api 404: {"message":"no matching manifest for linux/amd64/v3 in the manifest list entries"}`)
	got := friendlyImageErr("127.0.0.1:5000/o/p:1", raw)
	if got == nil || !strings.Contains(got.Error(), "linux/amd64") || !strings.Contains(got.Error(), "CPU architecture") || !strings.Contains(got.Error(), "multi-architecture") {
		t.Fatalf("message must explain the architecture problem: %v", got)
	}
	if !errors.Is(got, raw) {
		t.Fatal("the original error must stay wrapped")
	}
	other := errors.New("connection refused")
	if friendlyImageErr("x", other) != other {
		t.Fatal("unrelated errors must pass through unchanged")
	}
	if friendlyImageErr("x", nil) != nil {
		t.Fatal("nil stays nil")
	}
}

func TestPlacement_RestartKeepsPortAndProbeUsesNodeAddress(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	res, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	if err != nil {
		t.Fatal(err)
	}
	in := res.Instances[0]
	hp := in.HostPort

	// supervision probe: must use the node's private IP + published port, never the container's own IP
	r.prober.mu.Lock()
	r.prober.hits = nil
	r.prober.mu.Unlock()
	got, _ := r.repo.GetByID(context.Background(), in.ID)
	pr, err := r.m.Probe(context.Background(), got)
	if err != nil || !pr.Healthy {
		t.Fatalf("probe of a remote instance should be healthy: %+v %v", pr, err)
	}
	if len(r.prober.hits) != 1 || r.prober.hits[0] != net.JoinHostPort(nodePriv, strconv.Itoa(hp)) {
		t.Fatalf("remote probes must dial the published private-IP port: %v", r.prober.hits)
	}

	// restart in place: the published port must not change (a random daemon port would silently break the route)
	got, _ = r.repo.GetByID(context.Background(), in.ID)
	if err := r.m.Restart(context.Background(), got); err != nil {
		t.Fatalf("restart: %v", err)
	}
	after, _ := r.repo.GetByID(context.Background(), in.ID)
	if after.HostPort != hp {
		t.Fatalf("restart changed the host port %d -> %d", hp, after.HostPort)
	}
	st, err := r.remote.Engine().Inspect(context.Background(), after.ContainerID)
	if err != nil || st.HostPorts[3000] != hp {
		t.Fatalf("daemon port = %v (err %v), want %d", st.HostPorts, err, hp)
	}
}

func TestPlacement_PortCollisionRetriesWithAnotherPort(t *testing.T) {
	r := newNodeRig(t)
	r.place(nodeIDa)
	r.remote.StartErrors = []string{"driver failed programming external connectivity: Bind for 10.9.9.9:40000 failed: port is already allocated"}
	res, err := r.deploy(t, dep1, "127.0.0.1:5000/org/app:1")
	if err != nil {
		t.Fatalf("a taken port must be retried: %v", err)
	}
	if len(r.remote.Live()) != 1 {
		t.Fatalf("exactly one container must remain after the retry, got %d", len(r.remote.Live()))
	}
	if res.Instances[0].HostPort < hostPortMin {
		t.Fatalf("bad port after retry: %+v", res.Instances[0])
	}
	// a persistent non-port failure is not retried forever
	r2 := newNodeRig(t)
	r2.place(nodeIDa)
	r2.remote.StartErrors = []string{"boom", "boom", "boom"}
	if _, err := r2.deploy(t, dep1, "127.0.0.1:5000/org/app:1"); err == nil {
		t.Fatal("a start failure that is not a port collision must fail the deploy")
	}
}

func TestAllocHostPort_AvoidsUsedPortsInRange(t *testing.T) {
	r := newNodeRig(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = r.repo.Create(ctx, &store.Instance{OrgID: orgID, ProjectID: projID, Role: "app", ContainerID: "c" + strconv.Itoa(i), NodeID: nodeIDa, HostPort: hostPortMin + i, Port: 3000})
	}
	tried := map[int]bool{hostPortMax: true}
	for i := 0; i < 200; i++ {
		p, err := r.m.allocHostPort(ctx, nodeIDa, tried)
		if err != nil || p < hostPortMin+50 || p > hostPortMax || p == hostPortMax {
			t.Fatalf("allocation = %d %v", p, err)
		}
	}
	// other servers' ports are independent
	if p, err := r.m.allocHostPort(ctx, "other-node", nil); err != nil || p < hostPortMin || p > hostPortMax {
		t.Fatalf("other node: %d %v", p, err)
	}
}
