package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/runtime/internal/store"
	"github.com/jp-cloud/runtime/internal/testutil"
)

type memNodes struct {
	mu         sync.Mutex
	nodes      map[string]*store.Node // by id
	placements map[string]string
	instOn     map[string]int
}

func newMemNodes() *memNodes {
	return &memNodes{nodes: map[string]*store.Node{}, placements: map[string]string{}, instOn: map[string]int{}}
}

func (m *memNodes) UpsertNode(_ context.Context, n *store.Node) (*store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.nodes {
		if x.Name == n.Name {
			x.Addr, x.PrivateIP, x.TokenSealed, x.Enabled = n.Addr, n.PrivateIP, n.TokenSealed, n.Enabled
			c := *x
			return &c, nil
		}
	}
	c := *n
	c.ID = uuid.NewString()
	m.nodes[c.ID] = &c
	r := c
	return &r, nil
}
func (m *memNodes) GetNode(_ context.Context, id string) (*store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.nodes[id]; ok {
		c := *n
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (m *memNodes) GetNodeByName(_ context.Context, name string) (*store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.nodes {
		if n.Name == name {
			c := *n
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memNodes) ListNodes(context.Context) ([]store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Node
	for _, n := range m.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *memNodes) DeleteNode(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, id)
	return nil
}
func (m *memNodes) CountInstancesOnNode(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instOn[id], nil
}
func (m *memNodes) CountPlacementsOnNode(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, v := range m.placements {
		if v == id {
			n++
		}
	}
	return n, nil
}
func (m *memNodes) Placement(_ context.Context, p string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.placements[p], nil
}
func (m *memNodes) SetPlacement(_ context.Context, p, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		delete(m.placements, p)
	} else {
		m.placements[p] = id
	}
	return nil
}
func (m *memNodes) DeletePlacement(_ context.Context, p string) error {
	return m.SetPlacement(nil, p, "")
}
func (m *memNodes) ListPlacements(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.placements {
		out[k] = v
	}
	return out, nil
}

const goodToken = "0123456789abcdef0123456789abcdef0123"

func nodeEnv(t *testing.T) (*env, *memNodes) {
	t.Helper()
	t.Setenv("INTERNAL_TOKEN", "int-token")
	allowLoopbackAgents = true // the fake node agent listens on 127.0.0.1
	t.Cleanup(func() { allowLoopbackAgents = false })
	e := newEnv(t)
	mn := newMemNodes()
	key := secretbox.DeriveKey("test-master", "runtime-nodes")
	e.h.Nodes, e.h.NodeKey = mn, key
	e.h.Manager.Nodes, e.h.Manager.NodeKey = mn, key
	return e, mn
}

func (e *env) internal(method, path string, body any) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("X-Internal-Token", "int-token")
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func agentAddr(fd *testutil.FakeDocker) string {
	u, _ := url.Parse(fd.Srv.URL)
	return u.Host
}

func TestNodesAPI_RequireInternalToken(t *testing.T) {
	e, _ := nodeEnv(t)
	for _, rt := range []struct{ m, p string }{
		{"GET", "/internal/runtime/nodes"}, {"PUT", "/internal/runtime/nodes/a"}, {"DELETE", "/internal/runtime/nodes/a"},
		{"GET", "/internal/runtime/placements/" + projID}, {"PUT", "/internal/runtime/placements/" + projID},
	} {
		req := httptest.NewRequest(rt.m, rt.p, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+e.tokens["u-admin"])
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s with a user JWT = %d, want 403", rt.m, rt.p, w.Code)
		}
	}
}

func TestNodesAPI_Validation(t *testing.T) {
	e, _ := nodeEnv(t)
	allowLoopbackAgents = false // validation must reject loopback/metadata here
	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{"bad name", "/internal/runtime/nodes/Bad_Name", map[string]any{"addr": "10.0.0.5:2377", "private_ip": "10.0.0.5", "token": goodToken}},
		{"hostname not allowed", "/internal/runtime/nodes/a", map[string]any{"addr": "example.com:2377", "private_ip": "10.0.0.5", "token": goodToken}},
		{"loopback", "/internal/runtime/nodes/a", map[string]any{"addr": "127.0.0.1:2377", "private_ip": "10.0.0.5", "token": goodToken}},
		{"metadata address", "/internal/runtime/nodes/a", map[string]any{"addr": "169.254.169.254:80", "private_ip": "10.0.0.5", "token": goodToken}},
		{"no port", "/internal/runtime/nodes/a", map[string]any{"addr": "10.0.0.5", "private_ip": "10.0.0.5", "token": goodToken}},
		{"bad private ip", "/internal/runtime/nodes/a", map[string]any{"addr": "10.0.0.5:2377", "private_ip": "nope", "token": goodToken}},
		{"short token", "/internal/runtime/nodes/a", map[string]any{"addr": "10.0.0.5:2377", "private_ip": "10.0.0.5", "token": "short"}},
		{"new node without token", "/internal/runtime/nodes/a", map[string]any{"addr": "10.0.0.5:2377", "private_ip": "10.0.0.5"}},
	}
	for _, c := range cases {
		if w := e.internal("PUT", c.path, c.body); w.Code != 400 {
			t.Errorf("%s = %d, want 400 (%s)", c.name, w.Code, w.Body)
		}
	}
}

func TestNodesAPI_UnreachableAgentIsNotRegistered(t *testing.T) {
	e, mn := nodeEnv(t)
	dead := testutil.NewFakeDocker()
	addr := agentAddr(dead)
	dead.Close()
	w := e.internal("PUT", "/internal/runtime/nodes/node-a", map[string]any{"addr": addr, "private_ip": "10.0.0.5", "token": goodToken})
	if w.Code != 502 || !strings.Contains(w.Body.String(), "cannot reach the node agent") || !strings.Contains(w.Body.String(), "cloud firewall") {
		t.Fatalf("unreachable agent must give a clear 502, got %d %s", w.Code, w.Body)
	}
	if n, _ := mn.ListNodes(context.Background()); len(n) != 0 {
		t.Fatal("a node that cannot be reached must not be saved")
	}
}

func TestNodesAPI_RegisterListPlaceAndGuards(t *testing.T) {
	e, mn := nodeEnv(t)
	agent := testutil.NewFakeDocker()
	t.Cleanup(agent.Close)
	w := e.internal("PUT", "/internal/runtime/nodes/node-a", map[string]any{"addr": agentAddr(agent), "private_ip": "10.0.0.5", "token": goodToken})
	if w.Code != 200 {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), goodToken) || strings.Contains(w.Body.String(), "enc:v1") {
		t.Fatal("the token (plain or sealed) must never be returned")
	}
	nodes, _ := mn.ListNodes(context.Background())
	if len(nodes) != 1 || !strings.HasPrefix(nodes[0].TokenSealed, "enc:v1:") || strings.Contains(nodes[0].TokenSealed, goodToken) {
		t.Fatalf("token must be stored sealed: %+v", nodes)
	}
	if len(agent.Tokens) == 0 || agent.Tokens[0] != goodToken {
		t.Fatalf("registration must prove the agent answers with the token: %v", agent.Tokens)
	}

	// edit without a token keeps the stored one
	if w := e.internal("PUT", "/internal/runtime/nodes/node-a", map[string]any{"addr": agentAddr(agent), "private_ip": "10.0.0.6"}); w.Code != 200 {
		t.Fatalf("edit without token = %d %s", w.Code, w.Body)
	}
	if n, _ := mn.GetNodeByName(context.Background(), "node-a"); n.PrivateIP != "10.0.0.6" || n.TokenSealed == "" {
		t.Fatalf("edit lost data: %+v", n)
	}

	w = e.internal("GET", "/internal/runtime/nodes", nil)
	var list struct {
		Nodes []struct {
			Name      string `json:"name"`
			Reachable bool   `json:"reachable"`
		} `json:"nodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Nodes) != 1 || list.Nodes[0].Name != "node-a" || !list.Nodes[0].Reachable {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}

	// placement
	if w := e.internal("PUT", "/internal/runtime/placements/"+projID, map[string]any{"node": "nope"}); w.Code != 404 {
		t.Errorf("unknown node = %d, want 404", w.Code)
	}
	if w := e.internal("PUT", "/internal/runtime/placements/not-a-uuid", map[string]any{"node": "node-a"}); w.Code != 400 {
		t.Errorf("bad project id = %d, want 400", w.Code)
	}
	if w := e.internal("PUT", "/internal/runtime/placements/"+projID, map[string]any{"node": "node-a"}); w.Code != 200 {
		t.Fatalf("placement = %d %s", w.Code, w.Body)
	}
	if w := e.internal("GET", "/internal/runtime/placements/"+projID, nil); !strings.Contains(w.Body.String(), `"node":"node-a"`) {
		t.Fatalf("get placement = %s", w.Body)
	}

	// a node with placed projects or containers cannot be deleted
	if w := e.internal("DELETE", "/internal/runtime/nodes/node-a", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "placed") {
		t.Fatalf("delete with placements = %d %s", w.Code, w.Body)
	}
	if w := e.internal("PUT", "/internal/runtime/placements/"+projID, map[string]any{"node": ""}); w.Code != 200 {
		t.Fatalf("back to local = %d", w.Code)
	}
	n, _ := mn.GetNodeByName(context.Background(), "node-a")
	mn.instOn[n.ID] = 2
	if w := e.internal("DELETE", "/internal/runtime/nodes/node-a", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "containers still run") {
		t.Fatalf("delete with containers = %d %s", w.Code, w.Body)
	}
	mn.instOn[n.ID] = 0
	if w := e.internal("DELETE", "/internal/runtime/nodes/node-a", nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
}

func TestNodesAPI_PlacementRefusesWhenAddonsAreOnAnotherServer(t *testing.T) {
	e, mn := nodeEnv(t)
	agent := testutil.NewFakeDocker()
	t.Cleanup(agent.Close)
	if w := e.internal("PUT", "/internal/runtime/nodes/node-a", map[string]any{"addr": agentAddr(agent), "private_ip": "10.0.0.5", "token": goodToken}); w.Code != 200 {
		t.Fatalf("register = %d", w.Code)
	}
	// a running add-on on this server (node id "")
	if err := e.repo.Create(context.Background(), &store.Instance{OrgID: orgID, ProjectID: projID, Role: "addon", Kind: "addon", DesiredState: "running", Status: "running", ContainerName: "addon-redis"}); err != nil {
		t.Fatal(err)
	}
	w := e.internal("PUT", "/internal/runtime/placements/"+projID, map[string]any{"node": "node-a"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "add-ons") {
		t.Fatalf("moving a project away from its add-ons must be refused: %d %s", w.Code, w.Body)
	}
	if id, _ := mn.Placement(context.Background(), projID); id != "" {
		t.Fatal("placement must not change when refused")
	}
	// staying where the add-ons are is fine
	if w := e.internal("PUT", "/internal/runtime/placements/"+projID, map[string]any{"node": ""}); w.Code != 200 {
		t.Fatalf("placing on the add-ons' server = %d %s", w.Code, w.Body)
	}
}
