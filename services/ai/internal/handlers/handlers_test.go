package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/ai/internal/crypt"
	"github.com/jp-cloud/ai/internal/llm"
	"github.com/jp-cloud/ai/internal/store"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
)

const org, prj = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
const org2 = "55555555-5555-5555-5555-555555555555"

type memStore struct {
	mu sync.Mutex
	m  map[string]*store.Config
}

func (s *memStore) Get(_ context.Context, o string) (*store.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.m[o]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, store.ErrNotFound
}
func (s *memStore) Put(_ context.Context, c *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.m[c.OrgID] = &cp
	return nil
}
func (s *memStore) Delete(_ context.Context, o string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, o)
	return nil
}

type fakeLLM struct {
	mu    sync.Mutex
	calls []llm.Config
	users []string
	err   error
	reply string
}

func (f *fakeLLM) Chat(_ context.Context, c llm.Config, system, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	f.users = append(f.users, user)
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

type limiter struct {
	mu    sync.Mutex
	n     int
	limit int
}

func (l *limiter) Allow(context.Context, string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return l.n <= l.limit, nil
}

type env struct {
	srv   *httptest.Server
	jm    *jwtutil.Manager
	st    *memStore
	llm   *fakeLLM
	lim   *limiter
	h     *Handler
	tools *httptest.Server
	paths []string
	mu    sync.Mutex
	torg  *testOrg
}

func setup(t *testing.T) *env {
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	e := &env{jm: jm, st: &memStore{m: map[string]*store.Config{}}, llm: &fakeLLM{reply: "the root cause is X"}, lim: &limiter{limit: 100}}
	e.tools = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.paths = append(e.paths, r.URL.Path+"|"+r.Header.Get("Authorization"))
		e.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = w.Write([]byte(`{"deployments":[{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","status":"failed","error":"boom password=hunter2hunter2","build_id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}]}`))
		case strings.Contains(r.URL.Path, "/builds/"):
			_, _ = w.Write([]byte(`{"logs":"npm ERR! code ELIFECYCLE\nDATABASE_URL=postgres://u:supersecretpw@db:5432/x\nAuthorization: Bearer abcdef0123456789abcdef"}`))
		case strings.HasSuffix(r.URL.Path, "/runtime/logs"):
			_, _ = w.Write([]byte(`{"logs":"listening on 3000"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(e.tools.Close)
	box, _ := crypt.New("test-enc-key")
	to := newTestOrg(t, map[string]string{prj: org})
	e.lim = &limiter{limit: 100}
	e.torg = to
	e.h = &Handler{Store: e.st, Box: box, LLM: e.llm, Limit: e.lim, Authz: to.Client, JWT: jm,
		Tools: Tools{BuildURL: e.tools.URL, DeploymentURL: e.tools.URL, RuntimeURL: e.tools.URL}}
	mux := http.NewServeMux()
	e.h.Routes(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, user string, body any) (int, map[string]any, string) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(b))
	if user != "" {
		tok, _, _ := e.jm.IssueAccess(user, user+"@x", user)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

var cfgPath = "/orgs/" + org + "/ai/config"

func TestConfigIsAdminOnlyAndKeyNeverReturned(t *testing.T) {
	e := setup(t)
	body := map[string]any{"provider": "gemini", "model": "gemini-2.0-flash", "api_key": "AIzaSyDUMMYKEY-abcdefghijklmnopqr1234"}
	if c, _, _ := e.do(t, "PUT", cfgPath, "member", body); c != 403 {
		t.Fatalf("members must not change AI config: %d", c)
	}
	if c, _, _ := e.do(t, "PUT", cfgPath, "viewer", body); c != 403 {
		t.Fatalf("viewer: %d", c)
	}
	c, out, raw := e.do(t, "PUT", cfgPath, "admin", body)
	if c != 200 || out["provider"] != "gemini" || out["has_key"] != true || out["key_last4"] != "1234" || out["source"] != "org" {
		t.Fatalf("%d %s", c, raw)
	}
	if strings.Contains(raw, "AIzaSyDUMMYKEY") {
		t.Fatal("the key must never be returned")
	}
	for _, user := range []string{"viewer", "member", "admin"} {
		_, _, raw := e.do(t, "GET", cfgPath, user, nil)
		if strings.Contains(raw, "AIzaSyDUMMYKEY") || !strings.Contains(raw, "1234") {
			t.Fatalf("%s sees: %s", user, raw)
		}
	}
	// stored encrypted, bound to the org
	stored := e.st.m[org]
	if bytes.Contains(stored.EncryptedKey, []byte("AIzaSyDUMMYKEY")) || len(stored.EncryptedKey) < 30 {
		t.Fatal("key must be encrypted at rest")
	}
	// model-only change keeps the key
	if c, out, _ := e.do(t, "PUT", cfgPath, "admin", map[string]any{"model": "gemini-2.5-pro"}); c != 200 || out["has_key"] != true || out["model"] != "gemini-2.5-pro" {
		t.Fatalf("%d %v", c, out)
	}
	// switching provider without a key is refused
	if c, _, _ := e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "openai"}); c != 400 {
		t.Fatalf("provider switch without key: %d", c)
	}
	if c, _, _ := e.do(t, "DELETE", cfgPath, "member", nil); c != 403 {
		t.Fatal("delete must be admin only")
	}
	if c, _, _ := e.do(t, "DELETE", cfgPath, "admin", nil); c != 204 {
		t.Fatal("admin delete")
	}
	if _, out, _ := e.do(t, "GET", cfgPath, "admin", nil); out["has_key"] != false {
		t.Fatalf("%v", out)
	}
}

func TestConfigValidation(t *testing.T) {
	e := setup(t)
	bad := []map[string]any{
		{"provider": "claude", "api_key": "k-12345678"},
		{"provider": "gemini", "model": "../../evil", "api_key": "k-12345678"},
		{"provider": "gemini", "model": "m odel", "api_key": "k-12345678"},
		{"provider": "gemini", "api_key": "short"},
		{"provider": "gemini", "api_key": "has space inside key"},
		{"provider": "openai", "api_key": "sk-12345678", "base_url": "http://169.254.169.254/latest"},
		{"provider": "openai", "api_key": "sk-12345678", "base_url": "https://user:pw@evil.example/v1"},
		{"provider": "openai", "api_key": "sk-12345678", "base_url": "https://x.example/v1?a=b"},
	}
	for i, b := range bad {
		if c, _, raw := e.do(t, "PUT", cfgPath, "admin", b); c != 400 {
			t.Errorf("case %d accepted (%d): %v %s", i, c, b, raw)
		}
	}
	if c, out, raw := e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "openai", "api_key": "sk-12345678", "base_url": "https://llm.example.com/v1/"}); c != 200 || out["base_url"] != "https://llm.example.com/v1" {
		t.Fatalf("%d %s", c, raw)
	}
	e.h.Box = nil
	if c, _, _ := e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "k-123456789"}); c != 503 {
		t.Fatalf("without an encryption key org keys cannot be stored: %d", c)
	}
}

func TestOrgConfigIsolationAndPlatformFallback(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "ORG-KEY-0001-abcdef"})
	e.h.Platform = llm.Config{Provider: llm.ProviderGemini, Model: "platform-model", APIKey: "PLATFORM-KEY-xyz"}
	askPath := "/orgs/" + org + "/ai/ask"
	if c, _, _ := e.do(t, "POST", askPath, "member", map[string]any{"prompt": "hi"}); c != 200 {
		t.Fatal(c)
	}
	if got := e.llm.calls[0]; got.APIKey != "ORG-KEY-0001-abcdef" {
		t.Fatalf("org key must win: %+v", got)
	}
	// org without config uses the platform key; its status says so
	e.st.m = map[string]*store.Config{}
	_, out, _ := e.do(t, "GET", "/orgs/"+org+"/ai/status", "viewer", nil)
	if out["source"] != "platform" || out["has_key"] != true {
		t.Fatalf("%v", out)
	}
	if c, out, _ := e.do(t, "POST", askPath, "member", map[string]any{"prompt": "hi"}); c != 200 || out["source"] != "platform" || out["mode"] != "llm" {
		t.Fatalf("%d %v", c, out)
	}
	if got := e.llm.calls[len(e.llm.calls)-1]; got.APIKey != "PLATFORM-KEY-xyz" {
		t.Fatalf("%+v", got)
	}
}

func TestHeuristicFallbackIsLabelled(t *testing.T) {
	e := setup(t) // no org key, no platform key
	c, out, raw := e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/analyze-logs", "member", map[string]any{"logs": "Error: connect ECONNREFUSED 10.0.0.5:5432"})
	if c != 200 || out["mode"] != "heuristic" || out["notice"] == nil {
		t.Fatalf("%d %s", c, raw)
	}
	if len(e.llm.calls) != 0 {
		t.Fatal("no LLM call without a key")
	}
	if !strings.Contains(out["analysis"].(string), "Connection refused") {
		t.Fatalf("heuristic content: %v", out["analysis"])
	}
}

func TestProviderFailureFallsBackWithoutLeakingKey(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-SECRETKEY-9999abcdef"})
	e.llm.err = errors.New("gemini API returned status 429: quota")
	c, out, raw := e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/analyze-logs", "member", map[string]any{"logs": "timeout"})
	if c != 200 || out["mode"] != "heuristic" || !strings.Contains(out["warning"].(string), "429") {
		t.Fatalf("%d %s", c, raw)
	}
	if strings.Contains(raw, "SECRETKEY") {
		t.Fatal("key leaked")
	}
}

func TestSensitiveDataIsRedactedBeforeTheLLM(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-ORG-KEY-0000abcd"})
	logs := "start\nDATABASE_URL=postgres://u:supersecretpw@db:5432/x\nAuthorization: Bearer abcdef0123456789abcdef\nstripe sk-live-abcdefghijklmnop12345\nreal error: EADDRINUSE"
	c, _, raw := e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/analyze-logs", "member", map[string]any{"logs": logs})
	if c != 200 {
		t.Fatal(c, raw)
	}
	sent := e.llm.users[0]
	for _, s := range []string{"supersecretpw", "abcdef0123456789abcdef", "sk-live-abcdefghijklmnop12345"} {
		if strings.Contains(sent, s) {
			t.Fatalf("secret %q reached the LLM prompt:\n%s", s, sent)
		}
	}
	if !strings.Contains(sent, "EADDRINUSE") {
		t.Fatal("diagnostic content must be preserved")
	}
	// the user prompt is redacted as well
	e.do(t, "POST", "/orgs/"+org+"/ai/ask", "member", map[string]any{"prompt": "why does token=ZZZZsecretZZZZ fail?"})
	if strings.Contains(e.llm.users[len(e.llm.users)-1], "ZZZZsecretZZZZ") {
		t.Fatal("prompt not redacted")
	}
}

func TestSizeCaps(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-ORG-KEY-0000abcd"})
	huge := strings.Repeat("line of log output\n", 20000)
	e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/analyze-logs", "member", map[string]any{"logs": huge})
	if n := len(e.llm.users[0]); n > maxLogs+1000 {
		t.Fatalf("log payload must be capped, got %d bytes", n)
	}
	e.do(t, "POST", "/orgs/"+org+"/ai/ask", "member", map[string]any{"prompt": strings.Repeat("x ", 50000)})
	if n := len(e.llm.users[1]); n > maxPrompt+200 {
		t.Fatalf("prompt must be capped, got %d", n)
	}
}

func TestRateLimitPerOrg(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-ORG-KEY-0000abcd"})
	e.lim.limit = 2
	p := "/orgs/" + org + "/ai/ask"
	for i, want := range []int{200, 200, 429} {
		if c, _, _ := e.do(t, "POST", p, "member", map[string]any{"prompt": "hi"}); c != want {
			t.Fatalf("call %d -> %d want %d", i, c, want)
		}
	}
}

func TestProjectEndpointsNeedLogsAccess(t *testing.T) {
	e := setup(t)
	pa := "/orgs/" + org + "/projects/" + prj + "/ai/"
	e.torgUser(t)
	for _, tc := range []struct {
		ep, user string
		want     int
	}{
		{"analyze-logs", "viewer", 200}, {"analyze-logs", "onlydeploy", 403}, {"analyze-logs", "nogrant", 404}, {"analyze-logs", "stranger", 403},
		{"explain", "onlydeploy", 403}, {"overview-suggestions", "onlydeploy", 403}, {"overview-suggestions", "viewer", 200},
	} {
		if c, _, raw := e.do(t, "POST", pa+tc.ep, tc.user, map[string]any{}); c != tc.want {
			t.Errorf("%s as %s -> %d want %d (%s)", tc.ep, tc.user, c, tc.want, raw)
		}
	}
	// ask with a project the user may not see
	if c, _, _ := e.do(t, "POST", "/orgs/"+org+"/ai/ask", "onlydeploy", map[string]any{"prompt": "hi", "project_id": prj}); c != 403 {
		t.Fatalf("ask scoped to a project needs logs:view: %d", c)
	}
	if c, _, _ := e.do(t, "POST", "/orgs/"+org+"/ai/ask", "viewer", map[string]any{"prompt": "hi", "project_id": "not-a-uuid"}); c != 400 {
		t.Fatalf("bad project id: %d", c)
	}
}

func (e *env) torgUser(t *testing.T) {
	e.torg.Users["onlydeploy"] = membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{membership.AreaDeployments: membership.LevelManage}}
	e.torg.Users["onlylogs"] = membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{membership.AreaLogs: membership.LevelView}}
}

func TestToolsOnlyTouchTheRequestedProjectAndRespectAreas(t *testing.T) {
	e := setup(t)
	e.torgUser(t)
	body := map[string]any{"deployment_id": "cccccccc-cccc-cccc-cccc-cccccccccccc"}
	if c, _, _ := e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/explain", "viewer", body); c != 200 {
		t.Fatal(c)
	}
	e.mu.Lock()
	paths := append([]string(nil), e.paths...)
	e.mu.Unlock()
	if len(paths) == 0 {
		t.Fatal("tools were not used")
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/orgs/"+org+"/projects/"+prj+"/") {
			t.Errorf("tool call outside the requested project: %s", p)
		}
		if !strings.Contains(p, "|Bearer ") {
			t.Errorf("tool call must carry the caller's authorization so the target re-checks access: %s", p)
		}
	}
	// a user with logs:view but no builds/deployments access gets no build or deployment data
	e.mu.Lock()
	e.paths = nil
	e.mu.Unlock()
	e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/explain", "onlylogs", map[string]any{})
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.paths {
		if strings.Contains(p, "/builds") || strings.Contains(p, "/deployments") {
			t.Errorf("area without access was read: %s", p)
		}
	}
}

func TestToolOutputIsRedactedInContext(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-ORG-KEY-0000abcd"})
	e.torgUser(t)
	_, _, raw := e.do(t, "POST", "/orgs/"+org+"/projects/"+prj+"/ai/explain", "viewer", map[string]any{})
	for _, s := range []string{"supersecretpw", "hunter2hunter2", "abcdef0123456789abcdef"} {
		if strings.Contains(raw, s) || strings.Contains(e.llm.users[len(e.llm.users)-1], s) {
			t.Fatalf("secret %q leaked from tool output", s)
		}
	}
}

func TestNoGlobalConfigEndpointsForOtherOrgs(t *testing.T) {
	e := setup(t)
	e.do(t, "PUT", cfgPath, "admin", map[string]any{"provider": "gemini", "api_key": "AIza-ORG-KEY-0000abcd"})
	// the org-2 view of status must not reflect org 1's key
	if c, _, _ := e.do(t, "GET", "/orgs/"+org2+"/ai/status", "viewer", nil); c != 200 {
		t.Fatal(c)
	}
	if c, out, _ := e.do(t, "GET", cfgPath, "viewer", nil); c != 200 || out["has_key"] != true {
		t.Fatalf("%d %v", c, out)
	}
	if _, ok := e.st.m[org2]; ok {
		t.Fatal("config leaked across organisations")
	}
}
