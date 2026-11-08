package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/addon/internal/clients"
	"github.com/jp-cloud/addon/internal/driver"
	"github.com/jp-cloud/addon/internal/service"
	"github.com/jp-cloud/addon/internal/store"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
)

const org, prj = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"

type rt struct{}

func (rt) Create(_ context.Context, r clients.CreateReq) (clients.CreateResp, error) {
	return clients.CreateResp{ContainerID: "cid-" + r.Name}, nil
}
func (rt) Status(context.Context, string) (clients.RuntimeStatus, error) {
	return clients.RuntimeStatus{State: "running", Running: true, Health: "ready"}, nil
}
func (rt) Action(context.Context, string, string) error { return nil }
func (rt) Delete(context.Context, string, bool) error   { return nil }
func (rt) Exec(context.Context, string, driver.ExecSpec, io.Reader, io.Writer, time.Duration) (int, string, error) {
	return 0, "", nil
}

type sec struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *sec) Upsert(_ context.Context, _, _, env, n, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[env+"/"+n] = v
	return nil
}
func (s *sec) Delete(_ context.Context, _, _, env, n string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, env+"/"+n)
	return nil
}
func (s *sec) Env(_ context.Context, _, _, env string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.m {
		if len(k) > len(env) && k[:len(env)+1] == env+"/" {
			out[k[len(env)+1:]] = v
		}
	}
	return out, nil
}

type mem struct {
	mu sync.Mutex
	m  map[string]*store.Addon
}

func (m *mem) Create(_ context.Context, a *store.Addon) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = "33333333-3333-3333-3333-333333333333"
	c := *a
	m.m[a.ID] = &c
	return nil
}
func (m *mem) Update(_ context.Context, a *store.Addon) error { c := *a; m.m[a.ID] = &c; return nil }
func (m *mem) List(_ context.Context, o, p, e string, inc bool) ([]store.Addon, error) {
	out := []store.Addon{}
	for _, a := range m.m {
		out = append(out, *a)
	}
	return out, nil
}
func (m *mem) ListByProject(context.Context, string) ([]store.Addon, error) { return nil, nil }
func (m *mem) Get(_ context.Context, o, p, id string) (*store.Addon, error) {
	a, ok := m.m[id]
	if !ok || a.OrgID != o || a.ProjectID != p {
		return nil, store.ErrNotFound
	}
	c := *a
	return &c, nil
}
func (m *mem) GetByID(ctx context.Context, id string) (*store.Addon, error) {
	return m.Get(ctx, org, prj, id)
}
func (m *mem) Delete(_ context.Context, id string) error { delete(m.m, id); return nil }
func (m *mem) ListStaleProvisioning(context.Context, time.Duration) ([]store.Addon, error) {
	return nil, nil
}
func (m *mem) CreateBackup(context.Context, *store.Backup) error           { return nil }
func (m *mem) UpdateBackup(context.Context, *store.Backup) error           { return nil }
func (m *mem) ListBackups(context.Context, string) ([]store.Backup, error) { return nil, nil }
func (m *mem) GetBackup(context.Context, string, string) (*store.Backup, error) {
	return nil, store.ErrNotFound
}
func (m *mem) DeleteBackup(context.Context, string) error { return nil }

var tOrg *testOrg

func setup(t *testing.T) (*httptest.Server, *jwtutil.Manager) {
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	svc := &service.Service{Store: &mem{m: map[string]*store.Addon{}}, Runtime: rt{}, Secrets: &sec{m: map[string]string{}},
		ReadyTimeout: time.Second, PollEvery: 5 * time.Millisecond}
	to := newTestOrg(t, map[string]string{prj: org})
	h := &Handler{Svc: svc, JWT: jm, Authz: to.Client}
	tOrg = to
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, jm
}

func call(t *testing.T, srv *httptest.Server, jm *jwtutil.Manager, method, path, user string, body any) (int, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
	if user != "" {
		tok, _, _ := jm.IssueAccess(user, user+"@x", user)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRBACAndShapes(t *testing.T) {
	srv, jm := setup(t)
	base := "/orgs/" + org + "/projects/" + prj + "/addons"
	body := map[string]any{"engine": "postgres", "name": "primary"}

	if c, _ := call(t, srv, jm, "POST", base, "", body); c != 401 {
		t.Fatalf("anon create %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base, "viewer", body); c != 403 {
		t.Fatalf("viewer create %d", c)
	}
	c, out := call(t, srv, jm, "POST", base, "member", body)
	if c != 201 {
		t.Fatalf("member create %d %v", c, out)
	}
	a := out["addon"].(map[string]any)
	if v, present := a["public_url"]; !present || v != nil {
		t.Fatalf("public_url must be present and null: %v", a)
	}
	if a["live"].(map[string]any)["status"] != "running" {
		t.Fatalf("live status: %v", a["live"])
	}
	id := a["id"].(string)
	conn := a["connection"].(map[string]any)
	if conn["masked"] != true {
		t.Fatalf("connection must be masked: %v", conn)
	}
	// legacy custom-image bypass is refused
	if c, _ := call(t, srv, jm, "POST", base, "member", map[string]any{"engine": "evil", "name": "evil1", "docker_image": "evil/img"}); c != 400 {
		t.Fatalf("docker_image on catalog endpoint %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base, "member", map[string]any{"engine": "not-in-catalog", "name": "evil2"}); c != 400 {
		t.Fatalf("unknown engine %d", c)
	}
	// reveal needs admin
	if c, _ := call(t, srv, jm, "GET", base+"/"+id+"/connection?reveal=true", "member", nil); c != 403 {
		t.Fatalf("member reveal %d", c)
	}
	c, out = call(t, srv, jm, "GET", base+"/"+id+"/connection?reveal=true", "admin", nil)
	if c != 200 || out["connection"].(map[string]any)["masked"] != false {
		t.Fatalf("admin reveal %d %v", c, out)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/"+id+"/connection?reveal=true", "viewer", nil); c != 403 {
		t.Fatalf("restricted viewer reveal %d", c)
	}
	if c, out := call(t, srv, jm, "GET", base+"/"+id+"/connection?reveal=true", "revealer", nil); c != 200 || out["connection"].(map[string]any)["masked"] != false {
		t.Fatalf("grant with secrets_reveal %d %v", c, out)
	}
	if c, out := call(t, srv, jm, "GET", base+"/"+id+"/connection", "viewer", nil); c != 200 || out["connection"].(map[string]any)["masked"] != true {
		t.Fatalf("restricted viewer gets masked info %d %v", c, out)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/"+id, "nogrant", nil); c != 404 {
		t.Fatalf("no grant must hide the project %d", c)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/"+id, "stranger", nil); c != 403 {
		t.Fatalf("non member %d", c)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/"+id, "viewer", nil); c != 200 {
		t.Fatalf("viewer get %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base+"/"+id+"/stop", "viewer", nil); c != 403 {
		t.Fatalf("viewer stop %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base+"/"+id+"/restart", "member", nil); c != 200 {
		t.Fatalf("member restart %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base+"/"+id+"/backups", "viewer", nil); c != 403 {
		t.Fatalf("member backup %d", c)
	}
	if c, _ := call(t, srv, jm, "DELETE", base+"/"+id, "viewer", nil); c != 403 {
		t.Fatalf("member delete %d", c)
	}
	if c, _ := call(t, srv, jm, "DELETE", base+"/"+id, "member", nil); c != 204 {
		t.Fatalf("admin delete %d", c)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/"+id, "admin", nil); c != 404 {
		t.Fatalf("deleted get %d", c)
	}
	if c, _ := call(t, srv, jm, "GET", base+"/not-a-uuid", "admin", nil); c != 400 {
		t.Fatalf("bad id %d", c)
	}
}

func TestCatalogEndpointReportsProvisionableCounts(t *testing.T) {
	srv, jm := setup(t)
	c, out := call(t, srv, jm, "GET", "/orgs/"+org+"/addons/catalog", "viewer", nil)
	if c != 200 || out["total"].(float64) != 241 || out["provisionable"].(float64) < 15 {
		t.Fatalf("%d total=%v prov=%v", c, out["total"], out["provisionable"])
	}
	first := out["catalog"].([]any)[0].(map[string]any)
	if _, ok := first["provisionable"]; !ok {
		t.Fatalf("provisionable flag missing: %v", first)
	}
	if _, ok := first["arch_ok"]; !ok {
		t.Fatal("arch_ok missing")
	}
}

func TestCustomEndpointValidates(t *testing.T) {
	srv, jm := setup(t)
	base := "/orgs/" + org + "/projects/" + prj + "/addons/custom"
	if c, _ := call(t, srv, jm, "POST", base, "member", map[string]any{"name": "app1", "image": "bad image", "port": 80}); c != 400 {
		t.Fatalf("bad image %d", c)
	}
	if c, _ := call(t, srv, jm, "POST", base, "viewer", map[string]any{"name": "app1", "image": "nginx", "port": 80}); c != 403 {
		t.Fatalf("viewer %d", c)
	}
	if c, out := call(t, srv, jm, "POST", base, "member", map[string]any{"name": "app1", "image": "nginx:1.27", "port": 80}); c != 201 {
		t.Fatalf("%d %v", c, out)
	}
}

func TestInternalRequiresToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "tok")
	srv, _ := setup(t)
	for _, p := range [][2]string{{"POST", "/internal/addons/provision"}, {"GET", "/internal/addons/" + prj}, {"DELETE", "/internal/projects/" + prj}, {"POST", "/internal/addons/" + prj + "/sql"}} {
		req, _ := http.NewRequest(p[0], srv.URL+p[1], nil)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%v -> %d", p, resp.StatusCode)
		}
	}
}

func TestOrgListFilteredToGrantedProjects(t *testing.T) {
	srv, jm := setup(t)
	other := "44444444-4444-4444-4444-444444444444"
	tOrg.Projects[other] = org
	tOrg.Users["onlyother"] = membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{membership.AreaAddons: membership.LevelView}}
	// give "onlyother" an addons grant on `other` but nothing on prj by using a custom org server answer: emulate through areas of the user
	// (the fake org returns the same Access for every project, so restrict by project via a second user without addon area)
	tOrg.Users["noaddons"] = membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{membership.AreaLogs: membership.LevelView}}
	if c, _ := call(t, srv, jm, "POST", "/orgs/"+org+"/projects/"+prj+"/addons", "member", map[string]any{"engine": "redis", "name": "cache1"}); c != 201 {
		t.Fatalf("create %d", c)
	}
	list := func(user string) int {
		c, out := call(t, srv, jm, "GET", "/orgs/"+org+"/addons", user, nil)
		if c != 200 {
			t.Fatalf("%s: %d", user, c)
		}
		return len(out["addons"].([]any))
	}
	if n := list("admin"); n != 1 {
		t.Fatalf("admin sees all: %d", n)
	}
	if n := list("viewer"); n != 1 {
		t.Fatalf("viewer with addons:view sees it: %d", n)
	}
	if n := list("noaddons"); n != 0 {
		t.Fatalf("a user without addons access must see none: %d", n)
	}
	if n := list("nogrant"); n != 0 {
		t.Fatalf("a user without any grant must see none: %d", n)
	}
}
