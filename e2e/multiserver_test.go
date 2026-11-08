package e2e

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Multi-server tests need a second server that already joined the cluster as a project server (the join
// command with "Run projects on this server"), running the SAME CPU architecture images as the test fixtures:
//
//	E2E_NODE=node-a                       name of the joined server (required, else the tests skip)
//	E2E_NODE_IMAGE=localhost:5000/e2e/addon-probe-x86   fixture repository built for that server's architecture
//
// Build the image on the node (docker build + push through the node's 127.0.0.1:5000 forwarder), tags :1 and :2
// (:2 is :1 plus ENV APP_VERSION=2).

func needNode(t *testing.T) (name, image string) {
	t.Helper()
	name = os.Getenv("E2E_NODE")
	if name == "" {
		t.Skip("E2E_NODE not set: no joined project server to test against")
	}
	image = os.Getenv("E2E_NODE_IMAGE")
	if image == "" {
		t.Skip("E2E_NODE_IMAGE not set: need a fixture image built for the node's CPU architecture")
	}
	return name, image
}

func nodeInfo(t *testing.T, admin *Client, name string) map[string]any {
	t.Helper()
	r := admin.Must(t, "GET", "/cluster/remote-nodes", nil, 200).Map(t)
	for _, n := range Arr(r, "nodes") {
		if Str(n, "name") == name {
			return n.(map[string]any)
		}
	}
	t.Fatalf("node %q is not registered with the runtime: %v", name, r)
	return nil
}

func TestMultiServer_ProjectRunsOnRemoteNode(t *testing.T) {
	node, image := needNode(t)
	e := NeedSSH(t)
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)

	info := nodeInfo(t, admin, node)
	if !Bool(info, "reachable") {
		t.Fatalf("node %s is not reachable: %v", node, Dig(info, "error"))
	}
	nodeIP := Str(info, "private_ip")
	before := int(Num(info, "projects"))
	beforeInst := int(Num(info, "instances"))

	// place the project on the node, read it back
	admin.Must(t, "PUT", "/cluster/placements/"+p.ID, map[string]any{"node": node}, 200)
	if got := Str(admin.Must(t, "GET", "/cluster/placements/"+p.ID, nil, 200).Map(t), "node"); got != node {
		t.Fatalf("placement = %q, want %q", got, node)
	}
	if after := int(Num(nodeInfo(t, admin, node), "projects")); after != before+1 {
		t.Errorf("node reports %d placed projects, want %d", after, before+1)
	}

	// --- deploy: the app must run on the node, not on the control plane
	res := DeployApp(t, admin, p, image+":1", nil)
	inst := Dig(res, "instance")
	if Str(inst, "status") != "running" {
		t.Fatalf("remote deploy not running: %v", truncate(fmt.Sprint(res), 400))
	}
	port := int(Num(inst, "host_port"))
	if Str(inst, "node_id") == "" || port < 32768 || port > 60999 {
		t.Fatalf("instance must record its node and a published port in 32768-60999: node=%q port=%d", Str(inst, "node_id"), port)
	}
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)
	if local := strings.TrimSpace(e.Docker(t, "ps -aq --filter label=jp.project="+p.ID)); local != "" {
		t.Errorf("containers of a node-placed project exist on the control plane: %s", local)
	}

	// --- routing is an upstream URL on the node's private address, never a local docker service
	grep := e.TraefikDynamicGrep(t, nodeIP+":"+fmt.Sprint(port))
	if !strings.Contains(grep, "url: http://"+net.JoinHostPort(nodeIP, fmt.Sprint(port))) {
		t.Errorf("traefik has no upstream for %s:%d: %q", nodeIP, port, truncate(grep, 300))
	}

	// --- the add-on lives next to the app and is reachable by it over the project network
	admin.Timeout = 10 * time.Minute
	defer func() { admin.Timeout = 60 * time.Second }()
	a := Dig(admin.Must(t, "POST", p.Path("/addons"), map[string]any{"engine": "redis", "name": "e2e-redis", "secret_name": "REDIS_URL"}, 201).Map(t), "addon")
	if Str(a, "status") != "ready" {
		t.Fatalf("remote add-on status = %q error=%q", Str(a, "status"), Str(a, "error"))
	}
	if Dig(a, "public_url") != nil {
		t.Errorf("add-on exposes a public_url: %v", Dig(a, "public_url"))
	}
	if local := strings.TrimSpace(e.Docker(t, "ps -aq --filter label=jp.project="+p.ID)); local != "" {
		t.Errorf("the add-on landed on the control plane: %s", local)
	}
	// moving the project away from its add-on is refused with a clear reason
	r := admin.Do("PUT", "/cluster/placements/"+p.ID, map[string]any{"node": ""})
	if r.Status != 409 || !strings.Contains(string(r.Body), "add-ons") {
		t.Errorf("moving a project away from its add-ons: want 409 mentioning add-ons, got %d %s", r.Status, truncate(string(r.Body), 200))
	}
	// redeploy so the app receives REDIS_URL, then talk to Redis through the public edge
	DeployApp(t, admin, p, image+":1", nil)
	Eventually(t, 60*time.Second, 2*time.Second, "app reaches its remote Redis", func() (bool, string) {
		code, body, err := e.EdgeGet(host, "/probe?only=redis")
		if err != nil {
			return false, err.Error()
		}
		return code == 200 && strings.Contains(body, `"ok":true`), fmt.Sprintf("%d %s", code, truncate(body, 160))
	})

	// --- zero-downtime rolling redeploy across the private link
	poll := StartPoller(e, host, "/health")
	time.Sleep(1500 * time.Millisecond)
	DeployApp(t, admin, p, image+":2", nil)
	time.Sleep(12 * time.Second)
	total, bad := poll.Stop()
	if total < 50 {
		t.Fatalf("poller ran only %d requests", total)
	}
	if len(bad) > 0 {
		t.Errorf("downtime during a remote rolling redeploy: %d/%d requests failed: %v", len(bad), total, bad[:minInt(len(bad), 8)])
	}
	if poll.Seen("2") == 0 {
		t.Errorf("version 2 never served (v1=%d v2=%d)", poll.Seen("1"), poll.Seen("2"))
	}

	// --- project delete cleans the node and the placement
	admin.Must(t, "DELETE", p.Path(""), nil, 200, 202, 204)
	Eventually(t, 60*time.Second, 2*time.Second, "node released the project", func() (bool, string) {
		n := nodeInfo(t, admin, node)
		return int(Num(n, "instances")) == beforeInst && int(Num(n, "projects")) == before, fmt.Sprintf("instances=%v (want %d) projects=%v (want %d)", Dig(n, "instances"), beforeInst, Dig(n, "projects"), before)
	})
}

func TestMultiServer_NodeAPIsAreAdminOnlyAndValidated(t *testing.T) {
	admin := AdminClient(t)
	o := NewOrg(t, admin)
	member := CreateUser(t, admin, o, "member", false).Login(t)
	for _, c := range [][2]string{{"GET", "/cluster/remote-nodes"}, {"GET", "/cluster/placements/" + o.ID}, {"PUT", "/cluster/placements/" + o.ID}, {"DELETE", "/cluster/remote-nodes/x"}} {
		if r := member.Do(c[0], c[1], map[string]any{"node": "x"}); r.Status != 403 {
			t.Errorf("%s %s as a member = %d, want 403", c[0], c[1], r.Status)
		}
		if r := Load(t).NewClient().Do(c[0], c[1], nil); r.Status != 401 {
			t.Errorf("%s %s anonymous = %d, want 401", c[0], c[1], r.Status)
		}
	}
	// unknown node / malformed project
	if r := admin.Do("PUT", "/cluster/placements/00000000-0000-4000-8000-000000000000", map[string]any{"node": "no-such-node"}); r.Status != 404 {
		t.Errorf("placing on an unknown node = %d, want 404", r.Status)
	}
	if r := admin.Do("PUT", "/cluster/placements/not-a-uuid", map[string]any{"node": ""}); r.Status != 400 {
		t.Errorf("malformed project id = %d, want 400", r.Status)
	}
	if r := admin.Do("DELETE", "/cluster/remote-nodes/no-such-node", nil); r.Status != 404 {
		t.Errorf("deleting an unknown node = %d, want 404", r.Status)
	}
}

func TestMultiServer_NodeAgentPortNotPublic(t *testing.T) {
	node, _ := needNode(t)
	admin := AdminClient(t)
	info := nodeInfo(t, admin, node)
	pub := os.Getenv("E2E_NODE_PUBLIC_IP")
	if pub == "" {
		t.Skip("E2E_NODE_PUBLIC_IP not set")
	}
	// the agent speaks only on the private address, and the cloud firewall allows only the control plane in
	for _, port := range []string{"2377", "5000"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(pub, port), 4*time.Second)
		if err == nil {
			c.Close()
			t.Errorf("node %s port %s is reachable from the internet (%s); the agent must be private only", node, port, pub)
		}
	}
	if Str(info, "private_ip") == "" {
		t.Error("node has no private ip")
	}
}
