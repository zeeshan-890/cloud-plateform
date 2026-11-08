package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/project/internal/store"
)

const testSecret = "0123456789abcdef0123456789abcdef0123"

type fakeRepo struct {
	mu       sync.Mutex
	projects map[string]*store.Project
}

func newFakeRepo() *fakeRepo { return &fakeRepo{projects: map[string]*store.Project{}} }

func (f *fakeRepo) Create(_ context.Context, orgID, name, slug, desc string) (*store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.OrgID == orgID && p.Slug == slug {
			return nil, store.ErrAlreadyExists
		}
	}
	p := &store.Project{ID: uuid.NewString(), OrgID: orgID, Name: name, Slug: slug, Description: desc, Status: store.StatusActive}
	f.projects[p.ID] = p
	return p, nil
}
func (f *fakeRepo) List(_ context.Context, orgID string) ([]store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Project{}
	for _, p := range f.projects {
		if p.OrgID == orgID {
			out = append(out, *p)
		}
	}
	return out, nil
}
func (f *fakeRepo) Count(ctx context.Context, orgID string) (int, error) {
	l, _ := f.List(ctx, orgID)
	return len(l), nil
}
func (f *fakeRepo) Get(_ context.Context, orgID, id string) (*store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok || p.OrgID != orgID {
		return nil, store.ErrNotFound
	}
	c := *p
	return &c, nil
}
func (f *fakeRepo) Update(_ context.Context, orgID, id string, name, desc *string) (*store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok || p.OrgID != orgID {
		return nil, store.ErrNotFound
	}
	if p.Status == store.StatusDeleting {
		return nil, store.ErrDeleting
	}
	if name != nil {
		p.Name = *name
	}
	if desc != nil {
		p.Description = *desc
	}
	c := *p
	return &c, nil
}
func (f *fakeRepo) ApplyConfig(_ context.Context, orgID, id, raw, hash string, cfg map[string]any) (*store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok || p.OrgID != orgID {
		return nil, store.ErrNotFound
	}
	b, _ := json.Marshal(cfg)
	p.JPConfig, p.JPConfigRaw, p.JPConfigHash = b, raw, hash
	c := *p
	return &c, nil
}
func (f *fakeRepo) MarkDeleting(_ context.Context, orgID, id string) (*store.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok || p.OrgID != orgID {
		return nil, store.ErrNotFound
	}
	p.Status = store.StatusDeleting
	c := *p
	return &c, nil
}
func (f *fakeRepo) SetDeleteError(_ context.Context, orgID, id, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[id]; ok {
		p.DeleteError = msg
	}
	return nil
}
func (f *fakeRepo) Delete(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.projects[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.projects, id)
	return nil
}

type grantSet struct {
	projects map[string]bool
	areas    map[string]string
}

type harness struct {
	t       *testing.T
	h       *Handler
	mux     *http.ServeMux
	repo    *fakeRepo
	jm      *jwtutil.Manager
	roles   map[string]string // userID -> role (org service)
	orgDown bool
	grants  map[string]*grantSet // userID -> grants

	mu          sync.Mutex
	cleanupLog  []string
	cleanupFail map[string]int
	domainCalls []map[string]string
	domainFail  bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hs := &harness{t: t, repo: newFakeRepo(), jm: jwtutil.NewManager(testSecret, time.Minute, time.Hour), roles: map[string]string{}, grants: map[string]*grantSet{}, cleanupFail: map[string]int{}}

	org := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hs.orgDown {
			w.WriteHeader(500)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 5 && parts[3] == "members": // /internal/orgs/{o}/members/{u}
			role, ok := hs.roles[parts[4]]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
		case len(parts) == 7 && parts[3] == "projects" && parts[5] == "access": // /internal/orgs/{o}/projects/{p}/access/{u}
			role, ok := hs.roles[parts[6]]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if role == "owner" || role == "admin" {
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "full": true})
				return
			}
			areas := map[string]string{}
			if gp := hs.grants[parts[6]]; gp != nil && gp.projects[parts[4]] {
				areas = gp.areas
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "full": false, "areas": areas, "secrets_reveal": false})
		case len(parts) == 6 && parts[3] == "users" && parts[5] == "projects": // /internal/orgs/{o}/users/{u}/projects
			role, ok := hs.roles[parts[4]]
			if !ok {
				w.WriteHeader(404)
				return
			}
			ids := []string{}
			if gp := hs.grants[parts[4]]; gp != nil {
				for id := range gp.projects {
					ids = append(ids, id)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"full": role == "owner" || role == "admin", "project_ids": ids})
		case len(parts) == 3: // /internal/orgs/{o}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": parts[2], "slug": "acme"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(org.Close)

	mkSvc := func(name string) string {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "DELETE" || !strings.HasPrefix(r.URL.Path, "/internal/projects/") {
				w.WriteHeader(404)
				return
			}
			hs.mu.Lock()
			hs.cleanupLog = append(hs.cleanupLog, name)
			fail := hs.cleanupFail[name]
			hs.mu.Unlock()
			if fail != 0 {
				w.WriteHeader(fail)
				_, _ = w.Write([]byte("boom"))
				return
			}
			w.WriteHeader(204)
		}))
		t.Cleanup(s.Close)
		return s.URL
	}

	domain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/domains/system" && r.Method == "POST" {
			var m map[string]string
			_ = json.NewDecoder(r.Body).Decode(&m)
			hs.mu.Lock()
			hs.domainCalls = append(hs.domainCalls, m)
			fail := hs.domainFail
			hs.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(domain.Close)

	hs.h = &Handler{
		Store: hs.repo, JWT: hs.jm, Members: &membership.Client{OrgURL: org.URL, HTTP: http.DefaultClient},
		HTTP: http.DefaultClient, OrganizationURL: org.URL, DomainURL: domain.URL,
		Cleanup: []CleanupTarget{{"addon", mkSvc("addon")}, {"runtime", mkSvc("runtime")}, {"domain", mkSvc("domain")}, {"secret", mkSvc("secret")}},
	}
	hs.mux = http.NewServeMux()
	hs.h.Routes(hs.mux)
	return hs
}

func (hs *harness) user(role string) (id, token string) {
	id = uuid.NewString()
	token, _, _ = hs.jm.IssueAccess(id, id+"@example.com", "U")
	if role != "" {
		hs.roles[id] = role
	}
	return
}

func (hs *harness) do(method, path string, body any, token string, hdr ...string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	return rec
}

func jbody(rec *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

var org1 = uuid.NewString()

func (hs *harness) create(token, slug string) (int, string) {
	rec := hs.do("POST", "/orgs/"+org1+"/projects", map[string]string{"name": "My App", "slug": slug}, token)
	if rec.Code != 201 {
		return rec.Code, ""
	}
	return rec.Code, jbody(rec)["project"].(map[string]any)["id"].(string)
}

func TestCreateValidationAndSystemDomain(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.user("admin")
	for slug, want := range map[string]int{
		"my-app": 201, "a": 201, "x1": 201, "-bad": 400, "bad-": 400, "UPPER_x": 400, "has space": 400,
		strings.Repeat("a", 31): 400, "../x": 400, "a.b": 400,
	} {
		if code, _ := hs.create(tok, slug); code != want {
			t.Errorf("slug %q: got %d want %d", slug, code, want)
		}
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.domainCalls) == 0 {
		t.Fatal("system domain must be registered on create")
	}
	c := hs.domainCalls[0]
	if c["org_slug"] != "acme" || c["project_slug"] == "" || c["project_id"] == "" || c["org_id"] != org1 {
		t.Fatalf("domain call: %v", c)
	}
	if strings.Contains(strings.Join(mapValues(c), " "), "My App") {
		t.Fatal("the display name must never reach the domain service")
	}
}

func mapValues(m map[string]string) []string {
	out := []string{}
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestCreateNameSanitisedAndRolesEnforced(t *testing.T) {
	hs := newHarness(t)
	_, viewer := hs.user("viewer")
	_, outsider := hs.user("")
	_, member := hs.user("admin")
	if code, _ := hs.create(viewer, "v-app"); code != 403 {
		t.Errorf("viewer create: %d", code)
	}
	if code, _ := hs.create(outsider, "o-app"); code != 403 {
		t.Errorf("non-member create: %d", code)
	}
	rec := hs.do("POST", "/orgs/"+org1+"/projects", map[string]string{"name": "  evil\x00\x07  name\n\t\tx ", "slug": "san-app"}, member)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	name := jbody(rec)["project"].(map[string]any)["name"].(string)
	if strings.ContainsAny(name, "\x00\x07\n\t") || strings.Contains(name, "  ") {
		t.Fatalf("name not sanitised: %q", name)
	}
	if rec := hs.do("POST", "/orgs/"+org1+"/projects", map[string]string{"name": "dup", "slug": "san-app"}, member); rec.Code != 409 {
		t.Errorf("duplicate slug: %d", rec.Code)
	}
	if rec := hs.do("POST", "/orgs/"+org1+"/projects", map[string]string{"name": "   ", "slug": "empty-name"}, member); rec.Code != 400 {
		t.Errorf("blank name: %d", rec.Code)
	}
}

func TestCreateRollsBackWhenDomainRegistrationFails(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.user("admin")
	hs.domainFail = true
	if code, _ := hs.create(tok, "fail-app"); code != 502 {
		t.Fatalf("domain failure must surface as 502, got %d", code)
	}
	if n, _ := hs.repo.Count(context.Background(), org1); n != 0 {
		t.Fatal("project must be rolled back when its domain cannot be registered")
	}
}

func TestMembershipFailsClosed(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.user("owner")
	hs.orgDown = true
	if rec := hs.do("GET", "/orgs/"+org1+"/projects", nil, tok); rec.Code != 502 {
		t.Fatalf("org service down must not grant access, got %d", rec.Code)
	}
}

func TestDeleteRequiresAdmin(t *testing.T) {
	hs := newHarness(t)
	_, owner := hs.user("owner")
	_, id := hs.create(owner, "del-app")
	for role, want := range map[string]int{"viewer": 404, "member": 404, "": 403} { // no grant: the project is not even revealed
		_, tok := hs.user(role)
		if rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+id, nil, tok); rec.Code != want {
			t.Errorf("role %q delete = %d want %d", role, rec.Code, want)
		}
	}
	hs.mu.Lock()
	if len(hs.cleanupLog) != 0 {
		t.Fatal("unauthorised delete must not trigger any cleanup")
	}
	hs.mu.Unlock()
	if _, err := hs.repo.Get(context.Background(), org1, id); err != nil {
		t.Fatal("project must still exist")
	}
	_, admin := hs.user("admin")
	if rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+id, nil, admin); rec.Code != 204 {
		t.Fatalf("admin delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := hs.repo.Get(context.Background(), org1, id); err == nil {
		t.Fatal("project should be gone")
	}
	if strings.Join(hs.cleanupLog, ",") != "addon,runtime,domain,secret" {
		t.Fatalf("cleanup order/coverage: %v", hs.cleanupLog)
	}
}

func TestCascadeDeleteAggregatesFailuresAndIsRetryable(t *testing.T) {
	hs := newHarness(t)
	_, owner := hs.user("owner")
	_, id := hs.create(owner, "casc-app")
	hs.cleanupFail["runtime"] = 500
	hs.cleanupFail["secret"] = 503

	rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+id, nil, owner)
	if rec.Code != 502 {
		t.Fatalf("partial failure must be 502, got %d %s", rec.Code, rec.Body)
	}
	out := jbody(rec)
	fails := out["failures"].([]any)
	if len(fails) != 2 || !strings.Contains(rec.Body.String(), "runtime") || !strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("both failures must be reported: %s", rec.Body)
	}
	// every target was still attempted
	if strings.Join(hs.cleanupLog, ",") != "addon,runtime,domain,secret" {
		t.Fatalf("all targets must be attempted: %v", hs.cleanupLog)
	}
	p, err := hs.repo.Get(context.Background(), org1, id)
	if err != nil || p.Status != store.StatusDeleting || p.DeleteError == "" {
		t.Fatalf("project must stay in 'deleting' with the error recorded: %+v %v", p, err)
	}
	// status is exposed to the user, and the binding oracle stops vouching for it
	get := hs.do("GET", "/orgs/"+org1+"/projects/"+id, nil, owner)
	if jbody(get)["project"].(map[string]any)["status"] != "deleting" {
		t.Fatalf("status must be exposed: %s", get.Body)
	}
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+id, map[string]string{"name": "x"}, owner); rec.Code != 409 {
		t.Fatalf("a deleting project cannot be modified: %d", rec.Code)
	}
	// retry after fixing
	delete(hs.cleanupFail, "runtime")
	delete(hs.cleanupFail, "secret")
	if rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+id, nil, owner); rec.Code != 204 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	if _, err := hs.repo.Get(context.Background(), org1, id); err == nil {
		t.Fatal("project should be gone after successful retry")
	}
}

func TestCleanupTreats404AsNothingToDelete(t *testing.T) {
	hs := newHarness(t)
	_, owner := hs.user("owner")
	_, id := hs.create(owner, "nf-app")
	hs.cleanupFail["addon"] = 404
	if rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+id, nil, owner); rec.Code != 204 {
		t.Fatalf("404 from a cleanup target is not a failure: %d", rec.Code)
	}
}

func TestInternalEndpointsProtectedAndBinding(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	hs := newHarness(t)
	_, owner := hs.user("owner")
	_, id := hs.create(owner, "int-app")
	itok := []string{internalauth.Header, "internal-token-for-tests"}

	if rec := hs.do("GET", "/internal/orgs/"+org1+"/projects/"+id, nil, owner); rec.Code != 403 {
		t.Fatalf("user JWT must not reach internal endpoints: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/internal/orgs/"+org1+"/projects/"+id, nil, owner); rec.Code != 403 {
		t.Fatalf("internal delete without token: %d", rec.Code)
	}
	if rec := hs.do("GET", "/internal/orgs/"+org1+"/projects/"+id, nil, "", itok...); rec.Code != 200 {
		t.Fatalf("binding ok: %d", rec.Code)
	}
	if rec := hs.do("GET", "/internal/orgs/"+uuid.NewString()+"/projects/"+id, nil, "", itok...); rec.Code != 404 {
		t.Fatalf("project from a different org must be 404: %d", rec.Code)
	}
	if rec := hs.do("GET", "/internal/orgs/"+org1+"/projects/"+uuid.NewString(), nil, "", itok...); rec.Code != 404 {
		t.Fatalf("unknown project: %d", rec.Code)
	}
	list := hs.do("GET", "/internal/orgs/"+org1+"/projects", nil, "", itok...)
	if list.Code != 200 || !strings.Contains(list.Body.String(), id) {
		t.Fatalf("internal list: %d %s", list.Code, list.Body)
	}
	// being deleted -> binding oracle says 404
	hs.cleanupFail["runtime"] = 500
	hs.do("DELETE", "/internal/orgs/"+org1+"/projects/"+id, nil, "", itok...)
	if rec := hs.do("GET", "/internal/orgs/"+org1+"/projects/"+id, nil, "", itok...); rec.Code != 404 {
		t.Fatalf("deleting project must not validate: %d", rec.Code)
	}
}

func TestSlugImmutable(t *testing.T) {
	hs := newHarness(t)
	_, member := hs.user("admin")
	_, id := hs.create(member, "keep-me")
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+id, map[string]string{"slug": "other"}, member); rec.Code != 400 {
		t.Fatalf("slug change must be refused: %d", rec.Code)
	}
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+id, map[string]string{"slug": "keep-me", "name": "Renamed"}, member); rec.Code != 200 {
		t.Fatalf("same slug + rename should work: %d %s", rec.Code, rec.Body)
	}
	_, viewer := hs.user("viewer")
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+id, map[string]string{"name": "x"}, viewer); rec.Code != 404 {
		t.Fatalf("viewer update: %d", rec.Code)
	}
}

func TestConfigApplyRoleAndDrift(t *testing.T) {
	hs := newHarness(t)
	_, owner := hs.user("owner")
	_, id := hs.create(owner, "cfg-app")
	cfg := map[string]any{"config": map[string]any{"name": "cfg-app", "runtime": "node"}}
	_, member := hs.user("member")
	if rec := hs.do("PUT", "/orgs/"+org1+"/projects/"+id+"/config", cfg, member); rec.Code != 404 {
		t.Fatalf("member without a grant must not apply config: %d", rec.Code)
	}
	rec := hs.do("PUT", "/orgs/"+org1+"/projects/"+id+"/config", cfg, owner)
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	hash := jbody(rec)["hash"].(string)

	d := hs.do("GET", "/orgs/"+org1+"/projects/"+id+"/config/drift?desired_hash="+hash, nil, owner)
	if jbody(d)["drift"] != false || jbody(d)["stub"] != nil {
		t.Fatalf("same hash -> no drift: %s", d.Body)
	}
	d = hs.do("GET", "/orgs/"+org1+"/projects/"+id+"/config/drift?desired_hash=abc", nil, owner)
	if jbody(d)["drift"] != true {
		t.Fatalf("different hash -> drift: %s", d.Body)
	}
	if d := hs.do("GET", "/orgs/"+org1+"/projects/"+id+"/config/drift", nil, owner); d.Code != 400 {
		t.Fatalf("no desired input is an error, not a fake answer: %d", d.Code)
	}
	d = hs.do("POST", "/orgs/"+org1+"/projects/"+id+"/config/drift", map[string]any{"config": map[string]any{"name": "cfg-app", "runtime": "go"}}, owner)
	if jbody(d)["drift"] != true || !strings.Contains(d.Body.String(), "runtime") {
		t.Fatalf("body compare must detect changed key: %s", d.Body)
	}
}

func (hs *harness) grant(role string, projectID string, areas map[string]string) string {
	id, tok := hs.user(role)
	hs.grants[id] = &grantSet{projects: map[string]bool{projectID: true}, areas: areas}
	return tok
}

func TestRestrictedUserSeesOnlyGrantedProjectsAndAreas(t *testing.T) {
	hs := newHarness(t)
	_, admin := hs.user("admin")
	_, p1 := hs.create(admin, "granted-app")
	_, p2 := hs.create(admin, "other-app")

	viewer := hs.grant("member", p1, map[string]string{"overview": "view", "logs": "view"})
	// list is filtered
	rec := hs.do("GET", "/orgs/"+org1+"/projects", nil, viewer)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, p1) || strings.Contains(body, p2) {
		t.Fatalf("restricted list must contain only the granted project: %d %s", rec.Code, body)
	}
	all := hs.do("GET", "/orgs/"+org1+"/projects", nil, admin).Body.String()
	if !strings.Contains(all, p1) || !strings.Contains(all, p2) {
		t.Fatalf("admin sees everything: %s", all)
	}
	// granted project: overview view OK
	if rec := hs.do("GET", "/orgs/"+org1+"/projects/"+p1, nil, viewer); rec.Code != 200 {
		t.Fatalf("granted overview: %d", rec.Code)
	}
	// another project: 404 (not revealed)
	if rec := hs.do("GET", "/orgs/"+org1+"/projects/"+p2, nil, viewer); rec.Code != 404 {
		t.Fatalf("non-granted project must be 404, got %d", rec.Code)
	}
	// settings area not granted -> 403 on config, rename, delete
	for _, c := range []struct{ m, path string }{{"GET", "/config"}, {"PATCH", ""}, {"DELETE", ""}, {"PUT", "/config"}} {
		if rec := hs.do(c.m, "/orgs/"+org1+"/projects/"+p1+c.path, map[string]any{"name": "x"}, viewer); rec.Code != 403 {
			t.Errorf("%s %s without settings access = %d, want 403", c.m, c.path, rec.Code)
		}
	}
	// settings:view allows reading config but not changing it
	reader := hs.grant("member", p1, map[string]string{"overview": "view", "settings": "view"})
	if rec := hs.do("GET", "/orgs/"+org1+"/projects/"+p1+"/config", nil, reader); rec.Code != 200 {
		t.Errorf("settings view -> config read: %d", rec.Code)
	}
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+p1, map[string]string{"name": "x"}, reader); rec.Code != 403 {
		t.Errorf("settings view must not rename: %d", rec.Code)
	}
	// settings:manage allows rename and delete
	mgr := hs.grant("member", p1, map[string]string{"overview": "view", "settings": "manage"})
	if rec := hs.do("PATCH", "/orgs/"+org1+"/projects/"+p1, map[string]string{"name": "Renamed"}, mgr); rec.Code != 200 {
		t.Errorf("settings manage rename: %d", rec.Code)
	}
	// restricted users cannot create projects
	if code, _ := hs.create(mgr, "mine"); code != 403 {
		t.Errorf("project creation is org-admin only: %d", code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org1+"/projects/"+p1, nil, mgr); rec.Code != 204 {
		t.Errorf("settings manage delete: %d", rec.Code)
	}
}
