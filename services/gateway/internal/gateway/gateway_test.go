package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/revocation"
)

const (
	secret   = "0123456789abcdef0123456789abcdef0123"
	internal = "internal-token-for-tests-0123456789abcdef"
)

type env struct {
	t       *testing.T
	srv     *Server
	h       http.Handler
	jm      *jwtutil.Manager
	mr      *miniredis.Miniredis
	rdb     *redis.Client
	up      *httptest.Server
	mu      sync.Mutex
	roles   map[string]string // userID -> role
	orgDown bool
	pats    map[string]map[string]any
	seen    []seenReq
	pruned  int
	nodeErr string // when set, PUT /internal/runtime/nodes/* answers 502 with this message
}

type seenReq struct {
	Path    string
	Method  string
	Headers http.Header
	Body    []byte
}

func newEnv(t *testing.T, mut ...func(*Config)) *env {
	t.Helper()
	t.Setenv("INTERNAL_TOKEN", internal)
	e := &env{t: t, roles: map[string]string{}, pats: map[string]map[string]any{}}
	e.mr = miniredis.RunT(t)
	e.rdb = redis.NewClient(&redis.Options{Addr: e.mr.Addr()})
	t.Cleanup(func() { e.rdb.Close() })
	e.jm = jwtutil.NewManager(secret, 15*time.Minute, time.Hour)

	e.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/internal/orgs/") && strings.Contains(p, "/members/"):
			if e.orgDown {
				w.WriteHeader(500)
				return
			}
			uid := p[strings.LastIndex(p, "/")+1:]
			e.mu.Lock()
			role, ok := e.roles[uid]
			e.mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
		case p == "/internal/pats/verify":
			var b struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			info, ok := e.pats[b.Token]
			if !ok {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(info)
		case p == "/system/settings" && r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"settings": map[string]any{"smtp_host": map[string]any{"set": true}}, "editable_keys": []string{"smtp_host"}})
		case p == "/internal/security/stats":
			_ = json.NewEncoder(w).Encode(map[string]any{"users": 4, "admins": 1, "mfa_enabled": 1, "admins_mfa_enabled": 0,
				"bootstrap_initialized": true, "legacy_plaintext_totp": 0, "admins_default_password": []string{"admin@example.com"}})
		case p == "/internal/runtime/stats":
			_ = json.NewEncoder(w).Encode(map[string]any{"containers_running": 3, "containers_total": 5, "cpu_millicores_used": 250, "mem_used_bytes": 123456})
		case strings.HasPrefix(p, "/internal/runtime/nodes/") && r.Method == "PUT":
			body, _ := io.ReadAll(r.Body)
			e.mu.Lock()
			e.seen = append(e.seen, seenReq{Path: p, Method: r.Method, Headers: r.Header.Clone(), Body: body})
			msg := e.nodeErr
			e.mu.Unlock()
			if msg != "" {
				w.WriteHeader(502)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "node_unreachable", "message": msg}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "n", "docker_version": "29.9.3", "architecture": "x86_64", "control_architecture": "aarch64", "cpus": 2, "memory_total_bytes": 512 << 20, "latency_ms": 4, "reachable": true})
		case strings.HasPrefix(p, "/internal/runtime/nodes/") && r.Method == "DELETE":
			e.mu.Lock()
			e.seen = append(e.seen, seenReq{Path: p, Method: r.Method, Headers: r.Header.Clone()})
			e.mu.Unlock()
			w.WriteHeader(204)
		case p == "/internal/runtime/prune":
			e.mu.Lock()
			e.pruned++
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "pruned", "images_removed": 2})
		default:
			body, _ := io.ReadAll(r.Body)
			e.mu.Lock()
			e.seen = append(e.seen, seenReq{Path: p, Method: r.Method, Headers: r.Header.Clone(), Body: body})
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": p})
		}
	}))
	t.Cleanup(e.up.Close)

	ups := map[string]string{}
	for _, n := range []string{"identity", "organization", "project", "repository", "deployment", "build", "registry", "runtime", "domain",
		"certificate", "secret", "logging", "metrics", "storage", "database", "addon", "scheduler", "ai"} {
		ups[n] = e.up.URL
	}
	cfg := Config{Env: "development", Upstreams: ups, CORSOrigins: []string{"https://app.example.com"}, JWTSecret: secret,
		RegistrationMode: "invite", TrustedProxies: []string{"10.0.0.0/8"}}
	for _, m := range mut {
		m(&cfg)
	}
	srv, err := New(cfg, Deps{JWT: e.jm, Redis: e.rdb, Members: &membership.Client{OrgURL: e.up.URL, HTTP: http.DefaultClient}, HTTP: http.DefaultClient, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.srv, e.h = srv, srv.Handler()
	return e
}

type user struct{ id, token string }

func (e *env) user(role string, admin bool) user {
	id := uuid.NewString()
	if role != "" {
		e.roles[id] = role
	}
	tok, _, _ := e.jm.IssueAccessWith(id, id+"@example.com", "U", jwtutil.IssueOptions{Admin: admin})
	return user{id, tok}
}

func (e *env) do(method, path string, token string, body any, hdr ...string) *httptest.ResponseRecorder {
	var rdr io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "203.0.113.50:5555"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) last() seenReq {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 {
		e.t.Fatal("upstream was never called")
	}
	return e.seen[len(e.seen)-1]
}

func (e *env) calls() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.seen) }

var orgID = uuid.NewString()

func projects() string { return "/api/v1/orgs/" + orgID + "/projects" }

func TestSpoofedIdentityHeadersAreStrippedAndReplaced(t *testing.T) {
	e := newEnv(t)
	u := e.user("member", false)
	rec := e.do("GET", projects(), u.token, nil,
		"X-User-ID", "attacker", "X-Org-ID", "other-org", "X-Org-Role", "owner", "X-Internal-Token", internal,
		"X-User-Email", "evil@example.com", "X-Forwarded-For", "6.6.6.6", "X-Jp-Anything", "1", "X-JP-Client-IP", "7.7.7.7", "Forwarded", "for=8.8.8.8")
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	h := e.last().Headers
	if h.Get("X-User-ID") != u.id || h.Get("X-Org-ID") != orgID || h.Get("X-Org-Role") != "member" || h.Get("X-User-Email") != u.id+"@example.com" {
		t.Fatalf("identity headers must come from verified claims/membership: %v", h)
	}
	if h.Get("X-Internal-Token") != "" || h.Get("X-Jp-Anything") != "" || h.Get("X-JP-Client-IP") != "" || h.Get("Forwarded") != "" {
		t.Fatalf("spoofed internal headers leaked upstream: %v", h)
	}
	if h.Get("X-Forwarded-For") != "203.0.113.50" {
		t.Fatalf("XFF from an untrusted peer must be replaced by the peer address, got %q", h.Get("X-Forwarded-For"))
	}
}

func TestSpoofedHeadersStrippedOnPublicRoutes(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/auth/register", "", map[string]string{"email": "a@b.c"}, "X-User-ID", "attacker", "X-Org-Role", "owner")
	h := e.last().Headers
	if h.Get("X-User-ID") != "" || h.Get("X-Org-Role") != "" {
		t.Fatalf("public routes must not forward identity headers: %v", h)
	}
}

func TestTrustedProxyXForwardedFor(t *testing.T) {
	e := newEnv(t)
	u := e.user("member", false)
	req := httptest.NewRequest("GET", projects(), nil)
	req.RemoteAddr = "10.1.2.3:4444" // Traefik
	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.9")
	e.h.ServeHTTP(httptest.NewRecorder(), req)
	if got := e.last().Headers.Get("X-Forwarded-For"); got != "198.51.100.9" {
		t.Fatalf("got %q", got)
	}
}

func TestMembershipFailsClosed(t *testing.T) {
	e := newEnv(t)
	nonMember := e.user("", false)
	if rec := e.do("GET", projects(), nonMember.token, nil); rec.Code != 403 {
		t.Fatalf("non-member must get 403, got %d", rec.Code)
	}
	if e.calls() != 0 {
		t.Fatal("request from a non-member reached the upstream")
	}
	member := e.user("owner", false)
	e.orgDown = true
	rec := e.do("GET", projects(), member.token, nil)
	if rec.Code != 502 {
		t.Fatalf("membership lookup failure must not fail open: %d", rec.Code)
	}
	if e.calls() != 0 {
		t.Fatal("request reached the upstream despite failed membership lookup")
	}
}

func TestViewerIsReadOnlyButCanLeave(t *testing.T) {
	e := newEnv(t)
	v := e.user("viewer", false)
	if rec := e.do("GET", projects(), v.token, nil); rec.Code != 200 {
		t.Fatalf("viewer read: %d", rec.Code)
	}
	if rec := e.do("POST", projects(), v.token, map[string]string{"name": "x"}); rec.Code != 403 {
		t.Fatalf("viewer write must be 403, got %d", rec.Code)
	}
	if rec := e.do("DELETE", projects()+"/"+uuid.NewString(), v.token, nil); rec.Code != 403 {
		t.Fatalf("viewer delete: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/v1/orgs/"+orgID+"/members/"+v.id, v.token, nil); rec.Code != 200 {
		t.Fatalf("viewer may leave the org (service decides): %d", rec.Code)
	}
	if got := e.last().Headers.Get("X-Org-Role"); got != "viewer" {
		t.Fatalf("role must be passed downstream, got %q", got)
	}
}

func TestRevokedAccessTokenRejected(t *testing.T) {
	e := newEnv(t)
	u := e.user("owner", false)
	if rec := e.do("GET", projects(), u.token, nil); rec.Code != 200 {
		t.Fatalf("pre-check: %d", rec.Code)
	}
	c, _ := e.jm.Parse(u.token)
	rv := &revocation.Store{Redis: e.rdb}
	_ = rv.DenyToken(context.Background(), c.ID, time.Minute)
	if rec := e.do("GET", projects(), u.token, nil); rec.Code != 401 {
		t.Fatalf("revoked jti must be 401, got %d", rec.Code)
	}
	// user-wide revoke-before (logout-all / password change)
	u2 := e.user("owner", false)
	time.Sleep(3 * time.Millisecond)
	_ = rv.RevokeUserBefore(context.Background(), u2.id, time.Hour)
	if rec := e.do("GET", projects(), u2.token, nil); rec.Code != 401 {
		t.Fatalf("revoke-before must be 401, got %d", rec.Code)
	}
	// session deny
	tok, _, _ := e.jm.IssueAccessWith(u.id, "x@example.com", "U", jwtutil.IssueOptions{SessionID: "sess-1"})
	_ = rv.DenySession(context.Background(), "sess-1", time.Minute)
	if rec := e.do("GET", projects(), tok, nil); rec.Code != 401 {
		t.Fatalf("revoked session must be 401, got %d", rec.Code)
	}
	// Redis down => fail closed
	u3 := e.user("owner", false)
	e.mr.Close()
	if rec := e.do("GET", projects(), u3.token, nil); rec.Code != 503 {
		t.Fatalf("revocation store outage must fail closed, got %d", rec.Code)
	}
}

func TestRefreshTokensAndGarbageRejected(t *testing.T) {
	e := newEnv(t)
	u := e.user("owner", false)
	refresh, _, _ := e.jm.IssueRefresh(u.id, "a@b.c", "A")
	for _, tok := range []string{refresh, "garbage", ""} {
		if rec := e.do("GET", projects(), tok, nil); rec.Code != 401 {
			t.Errorf("token %.12q: %d", tok, rec.Code)
		}
	}
}

func addPAT(e *env, token, scope string, isAdmin bool) user {
	id := uuid.NewString()
	e.roles[id] = "owner"
	e.pats[token] = map[string]any{"user": map[string]any{"id": id, "email": "pat@example.com", "name": "P", "is_admin": isAdmin}, "scope": scope, "pat_id": "p1"}
	return user{id: id}
}

func TestPATScopesEnforcedAtGateway(t *testing.T) {
	e := newEnv(t)
	ro := "jp_pat_readonly"
	rw := "jp_pat_readwrite"
	addPAT(e, ro, "read", false)
	addPAT(e, rw, "write", false)
	if rec := e.do("GET", projects(), ro, nil); rec.Code != 200 {
		t.Fatalf("read PAT GET: %d", rec.Code)
	}
	if rec := e.do("POST", projects(), ro, map[string]string{"name": "x"}); rec.Code != 403 || !strings.Contains(rec.Body.String(), "read-only") {
		t.Fatalf("read-only PAT must not mutate: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("DELETE", projects()+"/"+uuid.NewString(), ro, nil); rec.Code != 403 {
		t.Fatalf("read-only PAT delete: %d", rec.Code)
	}
	calls := e.calls()
	if rec := e.do("POST", projects(), rw, map[string]string{"name": "x"}); rec.Code != 200 {
		t.Fatalf("write PAT POST: %d", rec.Code)
	}
	if e.calls() != calls+1 {
		t.Fatal("write PAT must reach upstream")
	}
	// the minted downstream JWT carries the scope
	auth := e.last().Headers.Get("Authorization")
	c, err := e.jm.Parse(strings.TrimPrefix(auth, "Bearer "))
	if err != nil || c.Scope != "write" || c.Admin {
		t.Fatalf("minted token: %+v %v", c, err)
	}
	if rec := e.do("GET", projects(), "jp_pat_unknown", nil); rec.Code != 401 {
		t.Fatalf("unknown PAT: %d", rec.Code)
	}
}

func TestPATAdminOnlyWithAdminScope(t *testing.T) {
	e := newEnv(t)
	addPAT(e, "jp_pat_adminuser_write", "write", true)
	addPAT(e, "jp_pat_adminuser_admin", "admin", true)
	if rec := e.do("GET", "/api/v1/system/cluster", "jp_pat_adminuser_write", nil); rec.Code != 403 {
		t.Fatalf("write-scope PAT of an admin must not reach admin routes: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/system/cluster", "jp_pat_adminuser_admin", nil); rec.Code != 200 {
		t.Fatalf("admin-scope PAT of an admin: %d", rec.Code)
	}
}

func TestSystemRoutesPlatformAdminOnly(t *testing.T) {
	e := newEnv(t)
	normal := e.user("owner", false)
	admin := e.user("owner", true)
	routes := [][2]string{{"GET", "/api/v1/system/settings"}, {"PATCH", "/api/v1/system/settings"}, {"GET", "/api/v1/system/security"},
		{"GET", "/api/v1/system/cluster"}, {"GET", "/api/v1/cluster/nodes"}, {"POST", "/api/v1/system/maintenance/cleanup"}}
	for _, rt := range routes {
		if rec := e.do(rt[0], rt[1], normal.token, map[string]string{}); rec.Code != 403 {
			t.Errorf("%s %s as normal user = %d, want 403", rt[0], rt[1], rec.Code)
		}
		if rec := e.do(rt[0], rt[1], "", nil); rec.Code != 401 {
			t.Errorf("%s %s anonymous = %d, want 401", rt[0], rt[1], rec.Code)
		}
	}
	e.mu.Lock()
	if e.pruned != 0 {
		t.Fatal("non-admin must not trigger prune")
	}
	e.mu.Unlock()
	for _, rt := range routes {
		if rt[1] == "/api/v1/system/settings" && rt[0] == "PATCH" {
			continue // proxied to identity (echo upstream)
		}
		if rec := e.do(rt[0], rt[1], admin.token, map[string]string{}); rec.Code != 200 {
			t.Errorf("%s %s as admin = %d (%s)", rt[0], rt[1], rec.Code, rec.Body)
		}
	}
	if rec := e.do("PATCH", "/api/v1/system/settings", admin.token, map[string]string{"smtp_host": "x"}); rec.Code != 200 {
		t.Fatalf("admin patch: %d", rec.Code)
	}
}

func TestMaintenanceCleanupCallsRuntimePruneForReal(t *testing.T) {
	e := newEnv(t)
	admin := e.user("owner", true)
	rec := e.do("POST", "/api/v1/system/maintenance/cleanup", admin.token, nil)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["images_removed"] != float64(2) {
		t.Fatalf("response must be the runtime's real result: %d %v", rec.Code, out)
	}
	if e.pruned != 1 {
		t.Fatalf("runtime prune calls = %d", e.pruned)
	}
}

func TestClusterIsSingleRealNode(t *testing.T) {
	e := newEnv(t)
	admin := e.user("owner", true)
	rec := e.do("GET", "/api/v1/system/cluster", admin.token, nil)
	var out struct {
		Mode string `json:"mode"`
		Node struct {
			Hostname   string         `json:"hostname"`
			CPU        map[string]any `json:"cpu"`
			Containers map[string]any `json:"containers"`
		} `json:"node"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Mode != "single-node" || out.Node.Hostname == "" || out.Node.CPU["cores"] == nil {
		t.Fatalf("cluster: %s", rec.Body)
	}
	if out.Node.Containers["running"] != float64(3) || out.Node.Containers["total"] != float64(5) {
		t.Fatalf("container counts must come from the runtime: %v", out.Node.Containers)
	}
	nodes := e.do("GET", "/api/v1/cluster/nodes", admin.token, nil)
	var n struct{ Nodes []any }
	_ = json.Unmarshal(nodes.Body.Bytes(), &n)
	if len(n.Nodes) != 1 {
		t.Fatalf("exactly one node: %s", nodes.Body)
	}
	// legacy fake endpoints are gone
	if rec := e.do("POST", "/api/v1/cluster/nodes", admin.token, map[string]string{}); rec.Code != 404 {
		t.Fatalf("node enrolment does not exist: %d", rec.Code)
	}
}

func TestSecurityReportReflectsRealState(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.Env = "production"
		c.JWTSecret = "short"
		c.RegistrationMode = "open"
		c.CORSOrigins = []string{"*"}
	})
	t.Setenv("TLS_ENABLED", "")
	t.Setenv("INFRA_BIND_ADDR", "")
	admin := e.user("owner", true)
	rec := e.do("GET", "/api/v1/system/security", admin.token, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Checks  []Check        `json:"checks"`
		Summary map[string]int `json:"summary"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	by := map[string]Check{}
	for _, c := range out.Checks {
		by[c.ID] = c
		if c.ID == "" || c.Title == "" || c.Detail == "" || (c.Status != "pass" && c.Status != "warn" && c.Status != "fail") {
			t.Errorf("malformed check %+v", c)
		}
	}
	want := map[string]string{"internal_token": "pass", "jwt_secret": "fail", "environment": "pass", "registration": "warn", "tls": "fail",
		"cors": "fail", "trusted_proxies": "pass", "infra_ports": "warn", "token_revocation": "pass", "admin_mfa": "fail",
		"default_credentials": "fail", "bootstrap": "pass", "smtp": "pass"}
	for id, st := range want {
		if by[id].Status != st {
			t.Errorf("check %s = %q (%s), want %q", id, by[id].Status, by[id].Detail, st)
		}
	}
	if !strings.Contains(by["mfa_adoption"].Detail, "1 of 4") {
		t.Errorf("mfa adoption detail: %q", by["mfa_adoption"].Detail)
	}
	if out.Summary["fail"] == 0 || out.Summary["pass"] == 0 {
		t.Errorf("summary: %v", out.Summary)
	}
	// flipping real state flips the result
	t.Setenv("TLS_ENABLED", "true")
	t.Setenv("INFRA_BIND_ADDR", "127.0.0.1")
	rec = e.do("GET", "/api/v1/system/security", admin.token, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	for _, c := range out.Checks {
		if c.ID == "tls" && c.Status != "pass" || c.ID == "infra_ports" && c.Status != "pass" {
			t.Errorf("%s should now pass: %+v", c.ID, c)
		}
	}
	e.mr.Close()
	rec = e.do("GET", "/api/v1/system/security", admin.token, nil)
	if rec.Code != 503 { // admin's own token can no longer be checked: fail closed
		t.Fatalf("redis down: %d", rec.Code)
	}
}

func TestSystemSettingsNeverContainsSecrets(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "super-secret-smtp")
	t.Setenv("OPENAI_API_KEY", "sk-live-secret")
	e := newEnv(t)
	admin := e.user("owner", true)
	rec := e.do("GET", "/api/v1/system/settings", admin.token, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "super-secret-smtp") || strings.Contains(rec.Body.String(), "sk-live-secret") {
		t.Fatalf("settings must not leak secrets: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "editable_keys") || !strings.Contains(rec.Body.String(), "platform") {
		t.Fatalf("missing sections: %s", rec.Body)
	}
}

func TestLoginRateLimited(t *testing.T) {
	e := newEnv(t)
	var last int
	for i := 0; i < 101; i++ {
		last = e.do("POST", "/api/v1/auth/login", "", map[string]string{"email": "a@b.c", "password": "x"}).Code
	}
	if last != 429 {
		t.Fatalf("login must be rate limited per IP, got %d", last)
	}
	rec := e.do("POST", "/api/v1/auth/login", "", map[string]string{})
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 needs Retry-After")
	}
	// another client is unaffected
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader("{}"))
	req.RemoteAddr = "198.51.100.77:1"
	rec2 := httptest.NewRecorder()
	e.h.ServeHTTP(rec2, req)
	if rec2.Code == 429 {
		t.Fatal("rate limit must be per client address")
	}
}

func TestUpstreamDownIs502WithErrorShape(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Upstreams["domain"] = "http://127.0.0.1:1" })
	u := e.user("owner", false)
	rec := e.do("GET", projects()+"/"+uuid.NewString()+"/domains", u.token, nil)
	if rec.Code != 502 {
		t.Fatalf("got %d", rec.Code)
	}
	var out struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "bad_gateway" || strings.Contains(rec.Body.String(), "domains\":[]") {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestNoInternalRoutesExposed(t *testing.T) {
	for _, rt := range Routes() {
		if strings.Contains(rt.Pattern, "/internal") {
			t.Errorf("route %s exposes an internal path", rt.Pattern)
		}
	}
	e := newEnv(t)
	u := e.user("owner", true)
	for _, p := range []string{"/api/v1/internal/pats/verify", "/internal/users/x", "/api/v1/orgs/" + orgID + "/internal/anything"} {
		if rec := e.do("GET", p, u.token, nil); rec.Code != 404 {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
	}
	if rec := e.do("GET", "/api/v1/does-not-exist", "", nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("unknown api path: %d %s", rec.Code, rec.Body)
	}
}

func TestCORSNotReflectedForUnlistedOrigins(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/healthz", "", nil, "Origin", "https://evil.example")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("arbitrary origins must not be reflected")
	}
	rec = e.do("GET", "/healthz", "", nil, "Origin", "https://app.example.com")
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatal("allowlisted origin should be allowed")
	}
}

func TestRequestBodyCap(t *testing.T) {
	e := newEnv(t)
	u := e.user("owner", false)
	req := httptest.NewRequest("POST", projects(), bytes.NewReader(make([]byte, 5<<20)))
	req.Header.Set("Authorization", "Bearer "+u.token)
	req.ContentLength = 5 << 20
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("oversized body: %d", rec.Code)
	}
}

// Every user-facing route registered by a service must be reachable through the gateway.
func TestGatewayCoversEveryServiceRoute(t *testing.T) {
	param := regexp.MustCompile(`\{[^}]+\}`)
	norm := func(s string) string { return param.ReplaceAllString(s, "{}") }
	have := map[string]bool{}
	for _, rt := range Routes() {
		have[norm(rt.Pattern)] = true
	}
	have["GET /system/settings"], have["GET /system/security"], have["GET /system/cluster"] = true, true, true
	have["POST /system/maintenance/cleanup"], have["GET /cluster/nodes"] = true, true

	ignore := map[string]bool{
		"GET /status":  true, // scheduler self status
		"POST /notify": true, // notification: internal-only (INTERNAL_TOKEN)
		// scheduler's former multi-node routes: removed from the platform
		"GET /cluster/nodes": true, "POST /cluster/nodes": true, "POST /cluster/nodes/{}/drain": true, "POST /cluster/nodes/{}/heartbeat": true,
		"GET /api/v1/github/setup": true, // repository registers both spellings
	}
	re := regexp.MustCompile(`"((?:GET|POST|PUT|PATCH|DELETE) /[^"]*)"`)
	// Routes built from a per-file prefix variable: "METHOD "+p+"/suffix"
	preRe := regexp.MustCompile(`(?m)^\s*(?:const |var )?p\s*:?=\s*"([^"]*)"`)
	varRe := regexp.MustCompile(`"((?:GET|POST|PUT|PATCH|DELETE)) "\+p(?:\+"([^"]*)")?`)
	actRe := regexp.MustCompile(`for _, act := range \[\]string\{([^}]*)\}`)
	files, _ := filepath.Glob("../../../*/internal/handlers/*.go")
	if len(files) < 10 {
		t.Fatalf("expected to find service handler sources next to the gateway, found %d", len(files))
	}
	check := func(svc, pat string) {
		if strings.Contains(pat, "/internal") || strings.Contains(pat, "/healthz") || pat == "GET /metrics" || ignore[norm(pat)] {
			return
		}
		if !have[norm(pat)] {
			t.Errorf("%s registers %q which the gateway does not expose", svc, pat)
		}
	}
	for _, f := range files {
		if strings.Contains(f, "/gateway/") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		svc := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			check(svc, m[1])
		}
		if pm := preRe.FindStringSubmatch(src); pm != nil {
			for _, m := range varRe.FindAllStringSubmatch(src, -1) {
				suffix := m[2]
				if strings.HasSuffix(suffix, "/") && strings.Contains(src, `+act`) { // action routes: .../{id}/<act>
					for _, am := range actRe.FindAllStringSubmatch(src, -1) {
						for _, a := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(am[1], -1) {
							check(svc, m[1]+" "+pm[1]+suffix+a[1])
						}
					}
					continue
				}
				check(svc, m[1]+" "+pm[1]+suffix)
			}
		}
	}
}

func TestPasswordChangeTokenIsLimited(t *testing.T) {
	e := newEnv(t)
	id := uuid.NewString()
	e.roles[id] = "owner"
	tokn, _, _ := e.jm.IssueAccessWith(id, "", "M", jwtutil.IssueOptions{Scope: jwtutil.ScopePWChange, Managed: true})
	if rec := e.do("GET", projects(), tokn, nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), "password_change_required") {
		t.Fatalf("limited token on a normal route: %d %s", rec.Code, rec.Body)
	}
	if e.calls() != 0 {
		t.Fatal("must not reach upstream")
	}
	if rec := e.do("GET", "/api/v1/auth/me", tokn, nil); rec.Code != 200 {
		t.Fatalf("me: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/auth/password", tokn, map[string]string{"current_password": "a", "new_password": "b"}); rec.Code != 200 {
		t.Fatalf("password change: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/auth/pats", tokn, nil); rec.Code != 403 {
		t.Fatalf("pats: %d", rec.Code)
	}
}
