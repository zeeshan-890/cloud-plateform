package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/database/internal/addonclient"
	"github.com/jp-cloud/database/internal/store"
	jwtutil "github.com/jp-cloud/go-common/jwt"
)

const org, prj = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"

type mem struct {
	mu sync.Mutex
	m  map[string]*store.Database
}

func (s *mem) Create(_ context.Context, d *store.Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.m {
		if o.ProjectID == d.ProjectID && o.Name == d.Name {
			return store.ErrConflict
		}
	}
	d.ID = uuid.NewString()
	c := *d
	s.m[d.ID] = &c
	return nil
}
func (s *mem) Update(_ context.Context, d *store.Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *d
	s.m[d.ID] = &c
	return nil
}
func (s *mem) List(_ context.Context, o, p string) ([]store.Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.Database{}
	for _, d := range s.m {
		out = append(out, *d)
	}
	return out, nil
}
func (s *mem) ListByProject(ctx context.Context, p string) ([]store.Database, error) {
	return s.List(ctx, org, p)
}
func (s *mem) Get(_ context.Context, o, p, id string) (*store.Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.m[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *d
	return &c, nil
}
func (s *mem) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}
func (s *mem) LegacyRoles(context.Context) ([]string, error) { return nil, nil }

type fakeAddons struct {
	mu        sync.Mutex
	provision []addonclient.ProvisionReq
	deleted   []string
	purges    []bool
	sqls      []string
	sqlErr    error
	provErr   error
	slowOut   string
	backups   int
}

func (f *fakeAddons) Provision(_ context.Context, r addonclient.ProvisionReq) (*addonclient.Addon, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provErr != nil {
		return nil, f.provErr
	}
	f.provision = append(f.provision, r)
	return &addonclient.Addon{ID: "aaaaaaaa-0000-0000-0000-000000000001", Status: "ready", SecretRef: "POSTGRES_URL_DB_X", Hint: "hint"}, nil
}
func (f *fakeAddons) Get(context.Context, string) (*addonclient.Addon, error) {
	return &addonclient.Addon{ID: "a", Status: "ready", Endpoint: "addon-db-x:5432", Live: &addonclient.Live{Status: "running"}}, nil
}
func (f *fakeAddons) Connection(context.Context, string, bool) (json.RawMessage, error) {
	return json.RawMessage(`{"url":"postgres://app:xxxxx@addon-db-x:5432/app","masked":true}`), nil
}
func (f *fakeAddons) Delete(_ context.Context, id string, purge bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	f.purges = append(f.purges, purge)
	return nil
}
func (f *fakeAddons) SQL(_ context.Context, id, sql string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sqls = append(f.sqls, sql)
	if f.sqlErr != nil {
		return "", f.sqlErr
	}
	if strings.Contains(sql, "pg_stat_activity") {
		return "4\t100\n", nil
	}
	return f.slowOut, nil
}
func (f *fakeAddons) Backup(context.Context, string, string) (json.RawMessage, error) {
	f.backups++
	return json.RawMessage(`{"backup":{"status":"completed"}}`), nil
}
func (f *fakeAddons) ListBackups(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"backups":[]}`), nil
}
func (f *fakeAddons) Restore(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"restored":true,"verified":true}`), nil
}

type env struct {
	srv *httptest.Server
	jm  *jwtutil.Manager
	st  *mem
	fa  *fakeAddons
}

func setup(t *testing.T) *env {
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	e := &env{jm: jm, st: &mem{m: map[string]*store.Database{}}, fa: &fakeAddons{}}
	h := &Handler{Store: e.st, Addons: e.fa, JWT: jm, Authz: newTestOrg(t, map[string]string{prj: org}).Client}
	mux := http.NewServeMux()
	h.Routes(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, user string, body any) (int, map[string]any) {
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
	return resp.StatusCode, out
}

var base = "/orgs/" + org + "/projects/" + prj + "/databases"

func TestCreateProvisionsDedicatedPostgres(t *testing.T) {
	e := setup(t)
	c, out := e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	if c != 201 {
		t.Fatalf("%d %v", c, out)
	}
	if len(e.fa.provision) != 1 {
		t.Fatal("exactly one add-on must be provisioned")
	}
	p := e.fa.provision[0]
	if p.Engine != "postgres" || p.Name != "db-main" || p.Kind != "managed_db" || !p.PGStatStatements || p.OrgID != org || p.ProjectID != prj {
		t.Fatalf("provision request: %+v", p)
	}
	if len(e.fa.sqls) != 1 || !strings.Contains(e.fa.sqls[0], "CREATE EXTENSION IF NOT EXISTS pg_stat_statements") {
		t.Fatalf("pg_stat_statements must be enabled: %v", e.fa.sqls)
	}
	d := out["database"].(map[string]any)
	if d["status"] != "ready" || d["addon_id"] == nil || d["public_url"] != nil {
		t.Fatalf("%v", d)
	}
	if _, leaked := d["schema_name"]; leaked {
		t.Fatal("legacy shared-schema fields must be gone")
	}
	if d["mode"] == "simulate" {
		t.Fatal("no simulate mode")
	}
}

func TestCreateFailureIsFailureNoSimulation(t *testing.T) {
	e := setup(t)
	e.fa.provErr = &addonclient.Error{Status: 504, Code: "not_ready", Message: "readiness probe did not pass within 2m0s"}
	c, out := e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	if c != 504 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "readiness probe") {
		t.Fatalf("the real error must be surfaced: %d %v", c, out)
	}
	if len(e.st.m) != 0 {
		t.Fatalf("the reserved row must be removed: %v", e.st.m)
	}
	// name is free again
	e.fa.provErr = nil
	if c, _ := e.do(t, "POST", base, "member", map[string]any{"name": "main"}); c != 201 {
		t.Fatalf("retry: %d", c)
	}
}

func TestCreateRollsBackWhenExtensionFails(t *testing.T) {
	e := setup(t)
	e.fa.sqlErr = &addonclient.Error{Status: 422, Code: "sql_error", Message: "could not open extension control file"}
	c, _ := e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	if c != 422 {
		t.Fatalf("%d", c)
	}
	if len(e.fa.deleted) != 1 || !e.fa.purges[0] || len(e.st.m) != 0 {
		t.Fatalf("container must be removed with its volume and the row dropped: %v %v", e.fa.deleted, e.st.m)
	}
}

func TestCreateValidation(t *testing.T) {
	e := setup(t)
	for _, n := range []string{"", "ab", "1db", "has space", "x;drop", strings.Repeat("a", 30)} {
		if c, _ := e.do(t, "POST", base, "member", map[string]any{"name": n}); c != 400 {
			t.Errorf("name %q -> %d", n, c)
		}
	}
	e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	if c, _ := e.do(t, "POST", base, "member", map[string]any{"name": "main"}); c != 409 {
		t.Errorf("duplicate -> %d", c)
	}
}

func TestRBACAndLifecycle(t *testing.T) {
	e := setup(t)
	if c, _ := e.do(t, "POST", base, "viewer", map[string]any{"name": "main"}); c != 403 {
		t.Fatalf("viewer create %d", c)
	}
	if c, _ := e.do(t, "GET", base, "", nil); c != 401 {
		t.Fatalf("anon %d", c)
	}
	_, out := e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	id := out["database"].(map[string]any)["id"].(string)
	for _, tc := range []struct {
		m, p, u string
		want    int
	}{
		{"GET", base, "viewer", 200}, {"GET", base + "/" + id, "viewer", 200}, {"GET", base + "/" + id + "/connection", "viewer", 200},
		{"GET", base + "/" + id + "/connection?reveal=true", "member", 403}, {"GET", base + "/" + id + "/connection?reveal=true", "viewer", 403},
		{"GET", base + "/" + id + "/connection?reveal=true", "admin", 200}, {"GET", base + "/" + id + "/connection?reveal=true", "revealer", 200},
		{"GET", base + "/" + id, "nogrant", 404}, {"GET", base + "/" + id, "stranger", 403},
		{"GET", base + "/" + id + "/slow-queries", "viewer", 200}, {"GET", base + "/" + id + "/slow-queries", "nogrant", 404},
		{"POST", base + "/" + id + "/backups", "viewer", 403}, {"POST", base + "/" + id + "/backups", "member", 201},
		{"POST", base + "/" + id + "/backups/" + uuid.NewString() + "/restore", "viewer", 403},
		{"POST", base + "/" + id + "/backups/" + uuid.NewString() + "/restore", "member", 200},
		{"DELETE", base + "/" + id, "viewer", 403}, {"DELETE", base + "/" + id, "admin", 204}, {"GET", base + "/" + id, "admin", 404},
	} {
		if c, o := e.do(t, tc.m, tc.p, tc.u, nil); c != tc.want {
			t.Errorf("%s %s as %s -> %d want %d (%v)", tc.m, tc.p, tc.u, c, tc.want, o)
		}
	}
	if len(e.fa.deleted) != 1 {
		t.Fatalf("delete must remove the container: %v", e.fa.deleted)
	}
}

func TestSlowQueriesReadPgStatStatements(t *testing.T) {
	e := setup(t)
	e.fa.slowOut = "10\t900.5\t90.05\t10\tSELECT * FROM t WHERE email = 'a@b.c'\n"
	_, out := e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	id := out["database"].(map[string]any)["id"].(string)
	c, res := e.do(t, "GET", base+"/"+id+"/diagnostics/slow-queries", "member", nil)
	if c != 200 || res["source"] != "pg_stat_statements" {
		t.Fatalf("%d %v", c, res)
	}
	q := res["slow_queries"].([]any)[0].(map[string]any)
	if strings.Contains(q["query"].(string), "a@b.c") || q["calls"].(float64) != 10 {
		t.Fatalf("%v", q)
	}
	stats := res["stats"].(map[string]any)
	if stats["connections"].(float64) != 4 || stats["max_connections"].(float64) != 100 {
		t.Fatalf("%v", stats)
	}
	// a failing container surfaces as an error, never as fake data
	e.fa.sqlErr = &addonclient.Error{Status: 502, Code: "runtime_error", Message: "container down"}
	if c, _ := e.do(t, "GET", base+"/"+id+"/slow-queries", "member", nil); c != 502 {
		t.Fatalf("%d", c)
	}
}

func TestProjectCascade(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "tok")
	e := setup(t)
	req, _ := http.NewRequest("DELETE", e.srv.URL+"/internal/projects/"+prj, nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("internal without token: %d", resp.StatusCode)
	}
	t.Setenv("INTERNAL_TOKEN", "")
	e.do(t, "POST", base, "member", map[string]any{"name": "main"})
	c, out := e.do(t, "DELETE", "/internal/projects/"+prj, "", nil)
	if c != 200 || out["removed"].(float64) != 1 || len(e.st.m) != 0 || !e.fa.purges[0] {
		t.Fatalf("%d %v purges=%v", c, out, e.fa.purges)
	}
}
