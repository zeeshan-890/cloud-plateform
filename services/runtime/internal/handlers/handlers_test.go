package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/runtime/internal/manager"
	"github.com/jp-cloud/runtime/internal/testutil"
)

const (
	orgID  = "11111111-1111-4111-8111-111111111111"
	projID = "22222222-2222-4222-8222-222222222222"
	otherP = "33333333-3333-4333-8333-333333333333"
	otherO = "44444444-4444-4444-8444-444444444444"
	secret = "test-secret-test-secret-test-secret-123"
)

type okSecrets struct{}

func (okSecrets) Env(context.Context, string, string, string) (map[string]string, error) {
	return map[string]string{}, nil
}

type okRoutes struct{}

func (okRoutes) SetService(context.Context, string, string) error             { return nil }
func (okRoutes) SetUpstreams(context.Context, string, string, []string) error { return nil }

type okProber struct{}

func (okProber) Probe(context.Context, string, int, string) (int, error) { return 200, nil }

type env struct {
	h      *Handler
	mux    *http.ServeMux
	fd     *testutil.FakeDocker
	repo   *testutil.MemRepo
	jwt    *jwtutil.Manager
	tokens map[string]string
}

// roles: user id -> role in org
func newEnv(t *testing.T) *env {
	t.Helper()
	roles := map[string]string{"u-viewer": "viewer", "u-member": "member", "u-admin": "admin"}
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // internal orgs {o} members {u}
		if serveAccess(w, parts) {
			return
		}
		if len(parts) != 5 || parts[2] != orgID {
			http.NotFound(w, r)
			return
		}
		role, ok := roles[parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
	}))
	t.Cleanup(orgSrv.Close)
	projSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/orgs/"+orgID+"/projects/"+projID {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(projSrv.Close)

	fd := testutil.NewFakeDocker()
	t.Cleanup(fd.Close)
	repo := testutil.NewMemRepo()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := manager.New(ctx, manager.Config{TraefikContainer: "jp-traefik-1", HealthTimeout: 200 * time.Millisecond, HealthInterval: 5 * time.Millisecond, StopTimeout: time.Second},
		fd.Engine(), repo, okSecrets{}, okRoutes{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mgr.Prober = okProber{}
	jm := jwtutil.NewManager(secret, time.Hour, time.Hour)
	h := &Handler{Store: repo, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL), Manager: mgr}
	mux := http.NewServeMux()
	h.Routes(mux)
	e := &env{h: h, mux: mux, fd: fd, repo: repo, jwt: jm, tokens: map[string]string{}}
	for _, u := range []string{"u-viewer", "u-member", "u-admin", "u-stranger", "u-restricted"} {
		tok, _, err := jm.IssueAccess(u, u+"@x.io", u)
		if err != nil {
			t.Fatal(err)
		}
		e.tokens[u] = tok
	}
	ro, _, _ := jm.IssueAccessWith("u-admin", "a@x.io", "a", jwtutil.IssueOptions{Scope: jwtutil.ScopeRead})
	e.tokens["ro-admin"] = ro
	return e
}

func (e *env) do(user, method, path string, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if tok := e.tokens[user]; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func base(org, proj string) string { return "/orgs/" + org + "/projects/" + proj + "/runtime" }

func (e *env) deployOne(t *testing.T) string {
	t.Helper()
	w := e.do("u-member", "POST", base(orgID, projID)+"/instances", map[string]any{"image_ref": "registry.local/app:1"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Instance struct {
			ID string `json:"id"`
		} `json:"instance"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Instance.ID
}

func TestRBACMatrix(t *testing.T) {
	e := newEnv(t)
	id := e.deployOne(t)
	inst := base(orgID, projID) + "/instances/" + id

	cases := []struct {
		user, method, path string
		want               int
	}{
		{"u-viewer", "GET", base(orgID, projID) + "/instances", 200},
		{"u-viewer", "GET", inst, 200},
		{"u-viewer", "GET", inst + "/logs", 200},
		{"u-viewer", "GET", base(orgID, projID) + "/containers", 200},
		{"u-viewer", "POST", inst + "/stop", 403},
		{"u-viewer", "POST", inst + "/restart", 403},
		{"u-viewer", "POST", base(orgID, projID) + "/instances", 403},
		{"u-viewer", "DELETE", inst, 403},
		{"u-restricted", "DELETE", inst, 403},
		{"u-restricted", "GET", base(orgID, projID) + "/instances", 403},
		{"u-stranger", "GET", base(orgID, projID) + "/instances", 403},
		{"", "GET", base(orgID, projID) + "/instances", 401},
		{"ro-admin", "POST", inst + "/stop", 403}, // read-only PAT token
		{"ro-admin", "GET", inst, 200},
		{"u-member", "POST", inst + "/stop", 200},
		{"u-member", "POST", inst + "/start", 200},
		{"u-member", "POST", inst + "/restart", 200},
		{"u-admin", "DELETE", inst, 204},
		{"u-admin", "GET", inst, 404},
	}
	for _, c := range cases {
		w := e.do(c.user, c.method, c.path, nil)
		if w.Code != c.want {
			t.Errorf("%s %s %s = %d, want %d (%s)", c.user, c.method, c.path, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestProjectBindingAndIDValidation(t *testing.T) {
	e := newEnv(t)
	// project that does not belong to the org => 404 even for a legit member
	if w := e.do("u-admin", "GET", base(orgID, otherP)+"/instances", nil); w.Code != 404 {
		t.Errorf("foreign project = %d", w.Code)
	}
	// non-UUID ids never reach membership / docker
	if w := e.do("u-admin", "GET", base("not-a-uuid", projID)+"/instances", nil); w.Code != 400 {
		t.Errorf("bad org id = %d", w.Code)
	}
	if w := e.do("u-admin", "GET", base(orgID, projID)+"/instances/%2e%2e%2f", nil); w.Code != 404 {
		t.Errorf("bad instance id = %d", w.Code)
	}
}

func TestInstanceOfAnotherProjectIsNotReachable(t *testing.T) {
	e := newEnv(t)
	id := e.deployOne(t)
	// same instance id addressed through a different org/project pair must 404 (store scoping)
	in, _ := e.repo.GetByID(context.Background(), id)
	in.ProjectID = otherP
	_ = e.repo.Update(context.Background(), in)
	if w := e.do("u-admin", "GET", base(orgID, projID)+"/instances/"+id, nil); w.Code != 404 {
		t.Errorf("cross-project instance = %d", w.Code)
	}
}

func TestContainersListIsTenantScoped(t *testing.T) {
	e := newEnv(t)
	e.deployOne(t)
	// another tenant's container exists on the same daemon
	_, err := e.fd.Engine().Create(context.Background(), dockerSpec("jp-foreign", otherO, otherP))
	if err != nil {
		t.Fatal(err)
	}
	w := e.do("u-viewer", "GET", base(orgID, projID)+"/containers", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Containers []struct {
			Names []string `json:"Names"`
		} `json:"containers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Containers) != 1 || strings.Contains(out.Containers[0].Names[0], "foreign") {
		t.Fatalf("containers = %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), otherP) {
		t.Error("response leaks another project's data")
	}
}

func TestInternalEndpointsRequireInternalToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int-token")
	e := newEnv(t)
	routes := []struct{ method, path string }{
		{"POST", "/internal/runtime/start"},
		{"POST", "/internal/runtime/stop-by-deployment"},
		{"GET", "/internal/runtime/instances/" + orgID},
		{"POST", "/internal/runtime/instances/" + orgID + "/probe"},
		{"POST", "/internal/runtime/instances/" + orgID + "/restart"},
		{"GET", "/internal/runtime/desired"},
		{"POST", "/internal/runtime/jobs"},
		{"POST", "/internal/runtime/addon"},
		{"DELETE", "/internal/runtime/addon/abc"},
		{"GET", "/internal/runtime/stats"},
		{"POST", "/internal/runtime/prune"},
		{"POST", "/internal/runtime/reconcile"},
		{"DELETE", "/internal/projects/" + projID},
		{"GET", "/internal/runtime/nodes"},
		{"PUT", "/internal/runtime/nodes/a"},
		{"DELETE", "/internal/runtime/nodes/a"},
		{"GET", "/internal/runtime/placements/" + projID},
		{"PUT", "/internal/runtime/placements/" + projID},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s without token = %d, want 403", rt.method, rt.path, w.Code)
		}
		// a user JWT is not an internal credential
		req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+e.tokens["u-admin"])
		w = httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s with user JWT = %d, want 403", rt.method, rt.path, w.Code)
		}
	}
	req := httptest.NewRequest("GET", "/internal/runtime/stats", nil)
	req.Header.Set("X-Internal-Token", "int-token")
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "docker_version") {
		t.Errorf("stats with token = %d %s", w.Code, w.Body.String())
	}
}

func TestInternalStartDeployFailureReturns422WithStage(t *testing.T) {
	e := newEnv(t)
	e.fd.CrashImages["registry.local/app:crash"] = true
	body := `{"org_id":"` + orgID + `","project_id":"` + projID + `","deployment_id":"aaaaaaaa-1111-4111-8111-111111111111","image_ref":"registry.local/app:crash"}`
	req := httptest.NewRequest("POST", "/internal/runtime/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"stage":"health_gate"`) || !strings.Contains(w.Body.String(), "deploy_failed") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	// invalid image => 400 and nothing created
	req = httptest.NewRequest("POST", "/internal/runtime/start", strings.NewReader(`{"org_id":"`+orgID+`","project_id":"`+projID+`","image_ref":"--privileged"}`))
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("bad image = %d", w.Code)
	}
}

func TestInternalAddonAndProjectDelete(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("POST", "/internal/runtime/addon", strings.NewReader(
		`{"org_id":"`+orgID+`","project_id":"`+projID+`","name":"redis","image":"redis:7","port":6379,"env":{"A":"b"}}`))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("addon start = %d %s", w.Code, w.Body.String())
	}
	var res struct {
		ContainerID string `json:"container_id"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Host != "addon-redis" || res.Port != 6379 || res.ContainerID == "" {
		t.Fatalf("res = %+v", res)
	}
	// duplicate -> 409
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("POST", "/internal/runtime/addon", strings.NewReader(
		`{"org_id":"`+orgID+`","project_id":"`+projID+`","name":"redis","image":"redis:7","port":6379}`)))
	if w.Code != 409 {
		t.Fatalf("dup = %d", w.Code)
	}
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/internal/runtime/addon/"+res.ContainerID+"?purge=true", nil))
	if w.Code != 204 {
		t.Fatalf("addon delete = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/internal/projects/"+projID, nil))
	if w.Code != 200 {
		t.Fatalf("project delete = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/internal/projects/not-a-uuid", nil))
	if w.Code != 400 {
		t.Fatalf("bad project id = %d", w.Code)
	}
}

func TestJobEndpointRBAC(t *testing.T) {
	e := newEnv(t)
	if w := e.do("u-viewer", "POST", base(orgID, projID)+"/jobs", map[string]any{"image_ref": "registry.local/job:1"}); w.Code != 403 {
		t.Errorf("viewer run job = %d", w.Code)
	}
	w := e.do("u-member", "POST", base(orgID, projID)+"/jobs", map[string]any{"image_ref": "registry.local/job:1", "command": []string{"echo", "x"}})
	if w.Code != 202 {
		t.Fatalf("member run job = %d %s", w.Code, w.Body.String())
	}
	e.h.Manager.Wait()
	if w := e.do("u-viewer", "GET", base(orgID, projID)+"/jobs", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "succeeded") {
		t.Errorf("list jobs = %d %s", w.Code, w.Body.String())
	}
}

// goldenAddonRequest is the JSON the add-on service's clients.CreateReq marshals with every field set
// (services/addon/internal/clients/clients.go + driver.Health). Keep in sync with that type.
const goldenAddonRequest = `{"org_id":"` + orgID + `","project_id":"` + projID + `","name":"pg-main","image":"postgres:16","port":5432,
"env":{"POSTGRES_PASSWORD":"x"},"volume":"/var/lib/postgresql/data","command":["postgres","-c","max_connections=50"],
"health":{"type":"cmd","port":5432,"path":"/","cmd":["pg_isready"],"timeout_seconds":30}}`

func TestAddonContractExecAndLifecycle(t *testing.T) {
	e := newEnv(t)
	e.h.Manager.TCP = func(context.Context, string, int) error { return nil }
	do := func(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		return w
	}
	w := do("POST", "/internal/runtime/addon", goldenAddonRequest)
	if w.Code != 201 {
		t.Fatalf("addon client payload rejected: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		ContainerID string `json:"container_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	// an unknown field is reported by name
	w = do("POST", "/internal/runtime/addon", `{"org_id":"`+orgID+`","bogus_field":1}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "bogus_field") {
		t.Fatalf("400 must name the field: %d %s", w.Code, w.Body.String())
	}
	base := "/internal/runtime/addon/" + res.ContainerID
	if w := do("GET", base, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	if do("POST", base+"/stop", "").Code != 204 {
		t.Fatal("stop")
	}
	if w := do("GET", base, ""); !strings.Contains(w.Body.String(), `"running":false`) {
		t.Fatalf("status after stop = %s", w.Body.String())
	}
	if do("POST", base+"/start", "").Code != 204 || do("POST", base+"/restart", "").Code != 204 || do("POST", base+"/explode", "").Code != 400 {
		t.Fatal("start/restart/unknown action")
	}
	if do("GET", "/internal/runtime/addon/nope", "").Code != 404 {
		t.Fatal("unknown id")
	}
	// exec streaming: stdin echoed, exit code + stderr as trailers
	cmd := base64.StdEncoding.EncodeToString([]byte(`["pg_dump","-U","x"]`))
	w = do("POST", base+"/exec", "SELECT 1", "X-Exec-Cmd", cmd)
	if w.Code != 200 || w.Body.String() != "out:SELECT 1" {
		t.Fatalf("exec = %d %q", w.Code, w.Body.String())
	}
	tr := w.Result().Trailer
	if tr.Get("X-Exec-Exit-Code") != "7" {
		t.Fatalf("trailers = %v", w.Result().Header)
	}
	if do("POST", base+"/exec", "", "X-Exec-Cmd", "%%%").Code != 400 {
		t.Fatal("bad exec header")
	}
	// app containers can never be addressed through the add-on endpoints
	app := e.deployOne(t)
	in, _ := e.repo.GetByID(context.Background(), app)
	if do("POST", "/internal/runtime/addon/"+in.ContainerID+"/stop", "").Code != 400 {
		t.Fatal("add-on endpoint touched an app container")
	}
}
