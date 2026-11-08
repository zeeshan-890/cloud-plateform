package gateway

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func has(d []diagnostic, code string) *diagnostic {
	for i := range d {
		if d[i].Code == code {
			return &d[i]
		}
	}
	return nil
}

func TestDiagnose_PortBlockedOnOracle(t *testing.T) {
	self := serverEntry{Name: "api-1", Prefer: "private", PrivateAddr: "10.0.0.179"}
	peers := []serverEntry{{Name: "db-1", PrivateAddr: "10.0.0.88", Ports: []serverPort{{Port: 5432, Scope: "private"}, {Port: 6379, Scope: "private"}}}}
	rep := &joinReport{PrivateIP: "10.0.0.179", PrivateCIDR: "10.0.0.0/24", Provider: "oracle", Checks: []checkResult{
		{Peer: "db-1", Path: "private", Addr: "10.0.0.88", Port: 5432, OK: true, Ms: 1.2},
		{Peer: "db-1", Path: "private", Addr: "10.0.0.88", Port: 6379, Error: "timeout"},
	}}
	d := diagnose(self, rep, peers)
	b := has(d, "port_blocked")
	if b == nil || b.Level != "error" {
		t.Fatalf("want port_blocked error, got %+v", d)
	}
	if !strings.Contains(b.Message, "6379") || strings.Contains(b.Message, "Port(s) 5432") {
		t.Errorf("message must name only the blocked port: %s", b.Message)
	}
	if !strings.Contains(b.Fix, "Security List") || !strings.Contains(b.Fix, "10.0.0.0/24") {
		t.Errorf("Oracle fix must mention the Security List / NSG and the source network: %s", b.Fix)
	}
	if summarize(d) != "error" {
		t.Error("summary should be error")
	}
}

func TestDiagnose_AllBlockedIsUnreachable(t *testing.T) {
	self := serverEntry{Name: "a", Prefer: "private"}
	peers := []serverEntry{{Name: "b", PrivateAddr: "10.0.0.2", Ports: []serverPort{{Port: 80, Scope: "private"}}}}
	rep := &joinReport{PrivateIP: "10.0.0.1", PrivateCIDR: "10.0.0.0/24", Provider: "other", Checks: []checkResult{{Peer: "b", Path: "private", Addr: "10.0.0.2", Port: 80, Error: "timeout"}}}
	d := diagnose(self, rep, peers)
	if x := has(d, "peer_unreachable_private"); x == nil || !strings.Contains(x.Fix, "security group") {
		t.Fatalf("got %+v", d)
	}
}

func TestDiagnose_RefusedIsNotBlocked(t *testing.T) {
	self := serverEntry{Name: "a", Prefer: "private"}
	peers := []serverEntry{{Name: "b", PrivateAddr: "10.0.0.2", Ports: []serverPort{{Port: 5432, Scope: "private"}}}}
	rep := &joinReport{PrivateIP: "10.0.0.1", PrivateCIDR: "10.0.0.0/24", Checks: []checkResult{{Peer: "b", Path: "private", Addr: "10.0.0.2", Port: 5432, Error: "refused"}}}
	d := diagnose(self, rep, peers)
	if has(d, "port_blocked") != nil || has(d, "peer_unreachable_private") != nil {
		t.Fatalf("a refused connection means the path works; got %+v", d)
	}
	if x := has(d, "peer_port_closed"); x == nil || x.Level != "warn" || !strings.Contains(x.Fix, "Start the service") {
		t.Fatalf("want closed-port warning, got %+v", d)
	}
	if summarize(d) != "warn" {
		t.Errorf("summary = %s, want warn", summarize(d))
	}
}

func TestDiagnose_PrivateNetworkMismatch(t *testing.T) {
	self := serverEntry{Name: "a", Prefer: "private"}
	peers := []serverEntry{{Name: "b", PrivateAddr: "192.168.5.9"}, {Name: "c", PrivateAddr: "10.0.7.4"}}
	rep := &joinReport{PrivateIP: "10.0.0.1", PrivateCIDR: "10.0.0.0/24"}
	d := diagnose(self, rep, peers)
	if x := has(d, "private_network_mismatch"); x == nil || x.Level != "error" || !strings.Contains(x.Message, "192.168.5.9") {
		t.Fatalf("different private network must be an error: %+v", d)
	}
	if x := has(d, "different_subnet"); x == nil || x.Level != "warn" || !strings.Contains(x.Message, "10.0.7.4") {
		t.Fatalf("same /16, other subnet should only warn: %+v", d)
	}
}

func TestDiagnose_NoPrivateAddressAndFailover(t *testing.T) {
	self := serverEntry{Name: "a", Prefer: "private"}
	peers := []serverEntry{{Name: "b", PrivateAddr: "10.0.0.2", PublicAddr: "203.0.113.9", Ports: []serverPort{{Port: 80, Scope: "both"}}}}
	d := diagnose(self, &joinReport{}, peers)
	if has(d, "no_private_address") == nil {
		t.Fatalf("missing private address should be reported: %+v", d)
	}
	rep := &joinReport{PrivateIP: "10.0.0.1", PrivateCIDR: "10.0.0.0/24", Checks: []checkResult{
		{Peer: "b", Path: "private", Addr: "10.0.0.2", Port: 80, Error: "timeout"},
		{Peer: "b", Path: "public", Addr: "203.0.113.9", Port: 80, OK: true, Ms: 9},
	}}
	if has(diagnose(self, rep, peers), "would_fail_over_to_public") == nil {
		t.Error("private broken + public fine with prefer=private must warn about failover")
	}
}

func TestDiagnose_AllGood(t *testing.T) {
	self := serverEntry{Name: "a", Prefer: "private", PrivateAddr: "10.0.0.1", Ports: []serverPort{{Port: 22, Scope: "private"}}}
	peers := []serverEntry{{Name: "b", PrivateAddr: "10.0.0.2", Ports: []serverPort{{Port: 22, Scope: "private"}}}}
	rep := &joinReport{PrivateIP: "10.0.0.1", PrivateCIDR: "10.0.0.0/24", Firewall: "firewalld", Opened: []int{22}, Listening: []int{22},
		Checks: []checkResult{{Peer: "b", Path: "private", Addr: "10.0.0.2", Port: 22, OK: true, Ms: 2}}}
	d := diagnose(self, rep, peers)
	if summarize(d) != "ok" || has(d, "reachable") == nil {
		t.Fatalf("got %+v", d)
	}
	if txt := textReport("a", &joinReport{Diagnostics: d, Status: "ok"}); !strings.Contains(txt, "everything checked is working") {
		t.Errorf("text report: %s", txt)
	}
}

func TestDialable_BlocksMetadataAndLoopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "::1", "224.0.0.1"} {
		if dialable(net.ParseIP(ip)) {
			t.Errorf("%s must not be dialable", ip)
		}
	}
	for _, ip := range []string{"10.0.0.5", "203.0.113.4", "92.4.70.208"} {
		if !dialable(net.ParseIP(ip)) {
			t.Errorf("%s should be dialable", ip)
		}
	}
	if !isPrivateIP(net.ParseIP("10.1.2.3")) || !isPrivateIP(net.ParseIP("172.16.0.1")) || isPrivateIP(net.ParseIP("172.32.0.1")) || isPrivateIP(net.ParseIP("8.8.8.8")) {
		t.Error("isPrivateIP wrong")
	}
}

// ---- full flow through the gateway

func TestJoin_FullFlow(t *testing.T) {
	old := dialTimeout
	dialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })
	e := newEnv(t)
	admin := e.user("", true)
	member := e.user("member", false)

	// peer already recorded
	if rec := e.do("PUT", "/api/v1/cluster/servers/db-1", admin.token, map[string]any{"name": "db-1", "role": "database", "private_addr": "10.0.0.88",
		"public_addr": "203.0.113.88", "ports": []map[string]any{{"port": 5432, "scope": "private"}, {"port": 443, "scope": "public"}}}); rec.Code != 200 {
		t.Fatalf("seed peer = %d %s", rec.Code, rec.Body)
	}

	// token creation is admin only
	if rec := e.do("POST", "/api/v1/cluster/join-tokens", member.token, map[string]any{"name": "api-1"}); rec.Code != 403 {
		t.Errorf("member creating token = %d, want 403", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": "Bad Name!"}); rec.Code != 400 {
		t.Errorf("bad name = %d, want 400", rec.Code)
	}
	rec := e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": "api-1", "role": "api", "prefer": "private",
		"ports": []map[string]any{{"port": 8080, "scope": "private"}}})
	if rec.Code != 201 {
		t.Fatalf("token = %d %s", rec.Code, rec.Body)
	}
	var tk struct {
		Token   string `json:"token"`
		Command string `json:"command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if !strings.HasPrefix(tk.Command, "curl -fsSL http") || !strings.HasSuffix(tk.Command, "/script | sudo bash") || !strings.Contains(tk.Command, tk.Token) {
		t.Fatalf("command = %q", tk.Command)
	}

	// the token is the only credential on the public endpoints
	if rec := e.do("GET", "/api/v1/cluster/join/"+strings.Repeat("a", 43)+"/script", "", nil); rec.Code != 404 {
		t.Errorf("unknown token script = %d, want 404", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/cluster/join/short/plan", "", nil); rec.Code != 404 {
		t.Errorf("malformed token = %d, want 404", rec.Code)
	}
	script := e.do("GET", "/api/v1/cluster/join/"+tk.Token+"/script", "", nil)
	if script.Code != 200 || !strings.HasPrefix(script.Body.String(), "#!/usr/bin/env bash") || strings.Contains(script.Body.String(), "__TOKEN__") || !strings.Contains(script.Body.String(), tk.Token) {
		t.Fatalf("script = %d", script.Code)
	}
	plan := e.do("GET", "/api/v1/cluster/join/"+tk.Token+"/plan", "", nil).Body.String()
	for _, want := range []string{"name=api-1", "open=8080 private", "test=db-1 private 10.0.0.88 5432", "test=db-1 public 203.0.113.88 443"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan missing %q:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, "test=db-1 public 203.0.113.88 5432") {
		t.Error("a private-only port must not be tested over the public network")
	}

	// the script reports back
	report := map[string]any{"hostname": "api-1", "private_ip": "10.0.0.179", "private_cidr": "10.0.0.179/24", "provider": "oracle", "os": "Oracle Linux 9",
		"firewall": "firewalld", "opened_ports": []int{8080}, "listening_ports": []int{22},
		"checks": []map[string]any{
			{"peer": "db-1", "path": "private", "addr": "10.0.0.88", "port": 5432, "ok": false, "error": "timeout"},
			{"peer": "db-1", "path": "public", "addr": "203.0.113.88", "port": 443, "ok": true, "ms": 12.5},
			{"peer": "x", "path": "private", "addr": "bad host; rm", "port": 1, "ok": true}, // dropped: invalid address
			{"peer": "x", "path": "sideways", "addr": "10.0.0.9", "port": 1, "ok": true},    // dropped: invalid path
		}}
	rec = e.do("POST", "/api/v1/cluster/join/"+tk.Token+"/report?format=text", "", report)
	if rec.Code != 200 {
		t.Fatalf("report = %d %s", rec.Code, rec.Body)
	}
	txt := rec.Body.String()
	if !strings.Contains(txt, "[FAIL]") || !strings.Contains(txt, "5432") || !strings.Contains(txt, "Security List") || !strings.Contains(txt, "problems found") {
		t.Errorf("text report should name the blocked port and the Oracle fix:\n%s", txt)
	}

	// single use
	if rec := e.do("POST", "/api/v1/cluster/join/"+tk.Token+"/report", "", report); rec.Code != 404 {
		t.Errorf("token reuse = %d, want 404", rec.Code)
	}

	// the server now exists with addresses taken from the report / observed client address
	var list struct{ Servers []serverEntry }
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	var api *serverEntry
	for i := range list.Servers {
		if list.Servers[i].Name == "api-1" {
			api = &list.Servers[i]
		}
	}
	if api == nil {
		t.Fatalf("joined server missing: %+v", list.Servers)
	}
	if api.PrivateAddr != "10.0.0.179" || api.PublicAddr != "203.0.113.50" || api.Role != "api" || api.Report == nil || api.Report.Status != "error" || len(api.Report.Checks) != 2 {
		t.Fatalf("joined server = %+v report=%+v", api, api.Report)
	}

	// editing the configuration keeps the report
	if rec := e.do("PUT", "/api/v1/cluster/servers/api-1", admin.token, map[string]any{"name": "api-1", "role": "api", "private_addr": "10.0.0.179", "prefer": "private", "notes": "edited"}); rec.Code != 200 {
		t.Fatalf("edit = %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	for _, s := range list.Servers {
		if s.Name == "api-1" && (s.Report == nil || s.Notes != "edited") {
			t.Errorf("edit must keep the report: %+v", s)
		}
	}

	// Re-check on an existing server issues a token bound to it; "check now" refreshes the control-plane side
	rec = e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": "api-1"})
	var tk2 struct {
		Token    string `json:"token"`
		Existing bool   `json:"existing"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk2)
	if rec.Code != 201 || !tk2.Existing {
		t.Fatalf("re-check token = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/cluster/servers/api-1/check", member.token, nil); rec.Code != 403 {
		t.Errorf("member check = %d, want 403", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/cluster/servers/nope/check", admin.token, nil); rec.Code != 404 {
		t.Errorf("check unknown = %d, want 404", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/cluster/servers/api-1/check", admin.token, nil); rec.Code != 200 {
		t.Errorf("check = %d %s", rec.Code, rec.Body)
	}
}

func TestJoin_RejectsLinkLocalAndLoopbackTargets(t *testing.T) {
	res := dialServer(context.Background(), serverEntry{Name: "x", PrivateAddr: "169.254.169.254", PublicAddr: "127.0.0.1", Ports: []serverPort{{Port: 80, Scope: "both"}}})
	for _, r := range res {
		if r.OK || r.Error != "address not allowed" {
			t.Errorf("%s %s must be refused, got %+v", r.Path, r.Addr, r)
		}
	}
}

func TestDiagnose_InboundRefusedIsNotBlocked(t *testing.T) {
	self := serverEntry{Name: "api-1", Prefer: "private", PrivateAddr: "10.0.0.179", Ports: []serverPort{{Port: 8080, Scope: "private"}, {Port: 9090, Scope: "private"}}}
	rep := &joinReport{PrivateIP: "10.0.0.179", PrivateCIDR: "10.0.0.0/24", Provider: "oracle", Inbound: []checkResult{
		{Peer: "api-1", Path: "private", Addr: "10.0.0.179", Port: 8080, Error: "refused"},
		{Peer: "api-1", Path: "private", Addr: "10.0.0.179", Port: 9090, Error: "timeout"},
	}}
	d := diagnose(self, rep, nil)
	if x := has(d, "inbound_private_closed"); x == nil || x.Level != "info" || !strings.Contains(x.Message, "8080") || strings.Contains(x.Message, "9090") {
		t.Fatalf("refused port should be an info note about a stopped service: %+v", d)
	}
	b := has(d, "inbound_private_blocked")
	if b == nil || b.Level != "warn" || !strings.Contains(b.Message, "9090") || strings.Contains(b.Message, "8080") || strings.Contains(b.Message, "private.") {
		t.Fatalf("only the unanswered port is blocked: %+v", d)
	}
	if strings.Contains(b.Fix, "address=the control plane") {
		t.Errorf("fix must not splice a phrase into the firewall command: %s", b.Fix)
	}
}

func TestJoinScript_IsValidBash(t *testing.T) {
	// Guard against a syntax slip in the shell template (it is only ever exercised on a real server otherwise).
	for _, must := range []string{"#!/usr/bin/env bash", "__BASE__", "__TOKEN__", "BASE_US", "firewall-cmd", "EOF_TESTS"} {
		if !strings.Contains(joinScriptTemplate, must) {
			t.Errorf("template lost %q", must)
		}
	}
	if strings.Contains(joinScriptTemplate, "`") {
		t.Error("template must not contain backticks")
	}
}

// ---- node mode (servers that run projects)

func (e *env) seenFor(prefix, method string) []seenReq {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []seenReq
	for _, s := range e.seen {
		if strings.HasPrefix(s.Path, prefix) && s.Method == method {
			out = append(out, s)
		}
	}
	return out
}

func nodeToken(t *testing.T, e *env, admin user, name string) (token string, plan string) {
	t.Helper()
	rec := e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": name, "role": "worker", "node": true})
	if rec.Code != 201 {
		t.Fatalf("node token = %d %s", rec.Code, rec.Body)
	}
	var tk struct {
		Token string `json:"token"`
		Node  bool   `json:"node"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if !tk.Node {
		t.Fatal("token must say node=true")
	}
	return tk.Token, e.do("GET", "/api/v1/cluster/join/"+tk.Token+"/plan", "", nil).Body.String()
}

func TestJoinNode_PlanAgentRegistrationAndRotation(t *testing.T) {
	old := dialTimeout
	dialTimeout = 100 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })
	t.Setenv("CONTROL_PLANE_PRIVATE_IP", "10.0.0.41")
	t.Setenv("NODE_REGISTRY_UPSTREAM", "10.0.0.41:5000")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node-linux-amd64"), []byte("AGENT-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_AGENT_DIR", dir)
	e := newEnv(t)
	admin := e.user("", true)

	tok, plan := nodeToken(t, e, admin, "node-a")
	for _, want := range []string{"node=1", "agent_port=2377", "agent_source=10.0.0.41", "registry=10.0.0.41:5000", "agent_url=", "/join/" + tok + "/agent", "agent_token="} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan missing %q:\n%s", want, plan)
		}
	}
	agentTok := ""
	for _, l := range strings.Split(plan, "\n") {
		if strings.HasPrefix(l, "agent_token=") {
			agentTok = strings.TrimPrefix(l, "agent_token=")
		}
	}
	if len(agentTok) < 32 {
		t.Fatalf("agent token too short: %q", agentTok)
	}

	// the agent download is gated by the join token and only for node joins
	if rec := e.do("GET", "/api/v1/cluster/join/"+tok+"/agent?arch=amd64", "", nil); rec.Code != 200 || rec.Body.String() != "AGENT-BINARY" {
		t.Fatalf("agent download = %d %q", rec.Code, rec.Body.String())
	}
	if rec := e.do("GET", "/api/v1/cluster/join/"+tok+"/agent?arch=arm64", "", nil); rec.Code != 404 {
		t.Errorf("missing build = %d, want 404", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/cluster/join/"+tok+"/agent?arch=../../etc/passwd", "", nil); rec.Code != 400 {
		t.Errorf("bad arch = %d, want 400", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/cluster/join/"+strings.Repeat("x", 43)+"/agent?arch=amd64", "", nil); rec.Code != 404 {
		t.Errorf("unknown token = %d, want 404", rec.Code)
	}
	plain := e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": "plain-1"})
	var pt struct{ Token string }
	_ = json.Unmarshal(plain.Body.Bytes(), &pt)
	if rec := e.do("GET", "/api/v1/cluster/join/"+pt.Token+"/agent?arch=amd64", "", nil); rec.Code != 404 {
		t.Errorf("a non-node join must not hand out the agent: %d", rec.Code)
	}
	if strings.Contains(e.do("GET", "/api/v1/cluster/join/"+pt.Token+"/plan", "", nil).Body.String(), "agent_token") {
		t.Error("a non-node plan must not contain an agent token")
	}

	// the script reports in: the server is registered with the runtime using the agent token
	rep := map[string]any{"hostname": "node-a", "private_ip": "10.0.0.179", "private_cidr": "10.0.0.179/24", "provider": "oracle", "firewall": "firewalld", "checks": []any{}}
	rec := e.do("POST", "/api/v1/cluster/join/"+tok+"/report?format=text", "", rep)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "projects can now run on this server") {
		t.Fatalf("report = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "[WARN] This server is x86_64 but the control plane is aarch64") {
		t.Errorf("mixed CPU architectures must be called out at join time:\n%s", rec.Body)
	}
	puts := e.seenFor("/internal/runtime/nodes/node-a", "PUT")
	if len(puts) != 1 {
		t.Fatalf("runtime registrations = %d", len(puts))
	}
	var sent struct {
		Addr      string `json:"addr"`
		PrivateIP string `json:"private_ip"`
		Token     string `json:"token"`
	}
	_ = json.Unmarshal(puts[0].Body, &sent)
	if sent.Addr != "10.0.0.179:2377" || sent.PrivateIP != "10.0.0.179" || sent.Token != agentTok {
		t.Fatalf("registration body = %+v", sent)
	}
	var list struct{ Servers []serverEntry }
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	if len(list.Servers) != 1 || !list.Servers[0].Node {
		t.Fatalf("server must be flagged as a node: %+v", list.Servers)
	}
	// a client edit can neither grant nor remove the node flag
	e.do("PUT", "/api/v1/cluster/servers/node-a", admin.token, map[string]any{"name": "node-a", "private_addr": "10.0.0.179", "node": false, "notes": "edit"})
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	if !list.Servers[0].Node {
		t.Error("editing must keep the node flag")
	}
	e.do("PUT", "/api/v1/cluster/servers/plain-2", admin.token, map[string]any{"name": "plain-2", "private_addr": "10.0.0.5", "node": true})
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	for _, s := range list.Servers {
		if s.Name == "plain-2" && s.Node {
			t.Error("a client must not be able to flag a server as a node")
		}
	}

	// re-running the command rotates the agent token
	_, plan2 := nodeToken(t, e, admin, "node-a")
	if strings.Contains(plan2, "agent_token="+agentTok) {
		t.Error("each join must issue a fresh agent token")
	}
}

func TestJoinNode_RegistrationFailureIsAClearError(t *testing.T) {
	old := dialTimeout
	dialTimeout = 100 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })
	e := newEnv(t)
	e.nodeErr = "cannot reach the node agent at 10.0.0.179:2377: the agent may be stopped, or the network (cloud firewall / security group) blocks TCP 2377 from the control plane"
	admin := e.user("", true)
	tok, _ := nodeToken(t, e, admin, "node-b")
	rec := e.do("POST", "/api/v1/cluster/join/"+tok+"/report?format=text", "", map[string]any{"hostname": "node-b", "private_ip": "10.0.0.179", "private_cidr": "10.0.0.179/24", "provider": "oracle", "checks": []any{}})
	txt := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(txt, "[FAIL] Could not register this server to run projects") || !strings.Contains(txt, "blocks TCP 2377") || !strings.Contains(txt, "Oracle Security List") {
		t.Fatalf("registration failure must be explained:\n%s", txt)
	}
	var list struct{ Servers []serverEntry }
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	if len(list.Servers) != 1 || list.Servers[0].Node || list.Servers[0].Report.Status != "error" {
		t.Fatalf("a server whose agent cannot be reached must not be marked as a node: %+v", list.Servers)
	}
	// ...but the intent is remembered: the Re-check command (no flags) is a node join again and now succeeds
	e.nodeErr = ""
	rt := e.do("POST", "/api/v1/cluster/join-tokens", admin.token, map[string]any{"name": "node-b"})
	var rtk struct {
		Token string `json:"token"`
		Node  bool   `json:"node"`
	}
	_ = json.Unmarshal(rt.Body.Bytes(), &rtk)
	if !rtk.Node {
		t.Fatal("re-check after a failed node registration must still be a node join")
	}
	txt = e.do("POST", "/api/v1/cluster/join/"+rtk.Token+"/report?format=text", "", map[string]any{"hostname": "node-b", "private_ip": "10.0.0.179", "private_cidr": "10.0.0.179/24", "provider": "oracle", "checks": []any{}}).Body.String()
	if !strings.Contains(txt, "projects can now run on this server") {
		t.Fatalf("re-check should register the node now:\n%s", txt)
	}
	_ = json.Unmarshal(e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.Bytes(), &list)
	if !list.Servers[0].Node {
		t.Fatal("node flag should now be set")
	}
	// no private address -> clear error without calling the runtime
	e2 := newEnv(t)
	admin2 := e2.user("", true)
	tok2, _ := nodeToken(t, e2, admin2, "node-c")
	txt = e2.do("POST", "/api/v1/cluster/join/"+tok2+"/report?format=text", "", map[string]any{"hostname": "c", "checks": []any{}}).Body.String()
	if !strings.Contains(txt, "no private address") || len(e2.seenFor("/internal/runtime/nodes/", "PUT")) != 0 {
		t.Fatalf("no private address:\n%s", txt)
	}
}

func TestClusterNodes_AdminPassthrough(t *testing.T) {
	e := newEnv(t)
	admin := e.user("", true)
	member := e.user("member", false)
	pid := uuid.NewString()
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/cluster/remote-nodes"}, {"DELETE", "/api/v1/cluster/remote-nodes/node-a"},
		{"GET", "/api/v1/cluster/placements/" + pid}, {"PUT", "/api/v1/cluster/placements/" + pid},
	} {
		if rec := e.do(c.m, c.p, "", nil); rec.Code != 401 {
			t.Errorf("%s %s anonymous = %d", c.m, c.p, rec.Code)
		}
		if rec := e.do(c.m, c.p, member.token, map[string]any{"node": "x"}); rec.Code != 403 {
			t.Errorf("%s %s member = %d, want 403", c.m, c.p, rec.Code)
		}
	}
	if rec := e.do("PUT", "/api/v1/cluster/placements/"+pid, admin.token, map[string]any{"node": "node-a"}); rec.Code != 200 {
		t.Fatalf("placement = %d %s", rec.Code, rec.Body)
	}
	last := e.last()
	if last.Method != "PUT" || last.Path != "/internal/runtime/placements/"+pid || !strings.Contains(string(last.Body), "node-a") {
		t.Fatalf("forwarded request = %+v body=%s", last, last.Body)
	}
	if rec := e.do("GET", "/api/v1/cluster/remote-nodes", admin.token, nil); rec.Code != 200 || e.last().Path != "/internal/runtime/nodes" {
		t.Fatalf("list = %d path=%s", rec.Code, e.last().Path)
	}
	// deleting a node clears the node flag in the inventory
	e.do("PUT", "/api/v1/cluster/servers/node-a", admin.token, map[string]any{"name": "node-a", "private_addr": "10.0.0.179"})
	srv, _ := e.srv.getServer(context.Background(), "node-a")
	srv.Node = true
	_ = e.srv.saveServer(context.Background(), *srv)
	if rec := e.do("DELETE", "/api/v1/cluster/remote-nodes/node-a", admin.token, nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	srv, _ = e.srv.getServer(context.Background(), "node-a")
	if srv.Node {
		t.Error("node flag must be cleared after the node is removed")
	}
}

func TestServerInventory_ListsAreNeverNull(t *testing.T) {
	old := dialTimeout
	dialTimeout = 100 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })
	e := newEnv(t)
	admin := e.user("", true)
	// a server that joined without any ports (and a report with no checks) must not serialise null lists
	tok, _ := nodeToken(t, e, admin, "no-ports")
	e.do("POST", "/api/v1/cluster/join/"+tok+"/report", "", map[string]any{"hostname": "x", "private_ip": "10.0.0.7", "private_cidr": "10.0.0.7/24"})
	body := e.do("GET", "/api/v1/cluster/servers", admin.token, nil).Body.String()
	for _, bad := range []string{`"ports":null`, `"checks":null`, `"inbound":null`, `"diagnostics":null`} {
		if strings.Contains(body, bad) {
			t.Errorf("the API must never return %s: %s", bad, body)
		}
	}
}
