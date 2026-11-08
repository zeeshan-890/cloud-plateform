package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/domain/internal/certclient"
	"github.com/jp-cloud/domain/internal/dnscheck"
	"github.com/jp-cloud/domain/internal/hostname"
	"github.com/jp-cloud/domain/internal/store"
	"github.com/jp-cloud/domain/internal/traefikcfg"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
)

// ---------------------------------------------------------------- fakes

type memStore struct {
	mu      sync.Mutex
	domains map[string]*store.Domain
	routes  map[string]string
	ups     map[string][]string
}

func newMem() *memStore {
	return &memStore{domains: map[string]*store.Domain{}, routes: map[string]string{}}
}

func (m *memStore) clone(d *store.Domain) store.Domain { return *d }

func (m *memStore) routedConflict(d *store.Domain) bool {
	if !store.Routed(d.Status) {
		return false
	}
	for _, o := range m.domains {
		if o.ID != d.ID && o.Hostname == d.Hostname && o.Wildcard == d.Wildcard && store.Routed(o.Status) {
			return true
		}
	}
	return false
}

func (m *memStore) Create(_ context.Context, d *store.Domain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = store.StatusPending
	}
	if d.Kind == "" {
		d.Kind = store.KindCustom
	}
	for _, o := range m.domains {
		if o.ProjectID == d.ProjectID && o.Hostname == d.Hostname && o.Wildcard == d.Wildcard {
			return store.ErrConflict
		}
	}
	if m.routedConflict(d) {
		return store.ErrConflict
	}
	c := *d
	m.domains[d.ID] = &c
	return nil
}
func (m *memStore) filter(f func(*store.Domain) bool) []store.Domain {
	out := []store.Domain{}
	for _, d := range m.domains {
		if f(d) {
			out = append(out, *d)
		}
	}
	return out
}
func (m *memStore) List(_ context.Context, org, proj string) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(d *store.Domain) bool { return d.OrgID == org && d.ProjectID == proj }), nil
}
func (m *memStore) Get(_ context.Context, org, proj, id string) (*store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok || d.OrgID != org || d.ProjectID != proj {
		return nil, store.ErrNotFound
	}
	c := *d
	return &c, nil
}
func (m *memStore) GetByID(_ context.Context, id string) (*store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *d
	return &c, nil
}
func (m *memStore) Update(_ context.Context, d *store.Domain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[d.ID]; !ok {
		return store.ErrNotFound
	}
	if m.routedConflict(d) {
		return store.ErrConflict
	}
	c := *d
	m.domains[d.ID] = &c
	return nil
}
func (m *memStore) Delete(_ context.Context, org, proj, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok || d.OrgID != org || d.ProjectID != proj {
		return store.ErrNotFound
	}
	delete(m.domains, id)
	return nil
}
func (m *memStore) DeleteByID(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.domains, id)
	return nil
}
func (m *memStore) FindByDeploymentID(_ context.Context, dep string) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(d *store.Domain) bool { return d.DeploymentID != nil && *d.DeploymentID == dep }), nil
}
func (m *memStore) ListByProject(_ context.Context, p string) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(d *store.Domain) bool { return d.ProjectID == p }), nil
}
func (m *memStore) ListRouted(_ context.Context) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(d *store.Domain) bool { return store.Routed(d.Status) }), nil
}
func (m *memStore) ListReverify(_ context.Context) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(d *store.Domain) bool { return d.Kind == store.KindCustom && store.Routed(d.Status) }), nil
}
func (m *memStore) DeleteByProject(_ context.Context, p string) ([]store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.filter(func(d *store.Domain) bool { return d.ProjectID == p })
	for _, d := range out {
		delete(m.domains, d.ID)
	}
	delete(m.routes, p)
	return out, nil
}
func (m *memStore) FindSystemByProject(_ context.Context, p string) (*store.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.domains {
		if d.ProjectID == p && d.Kind == store.KindSystem {
			c := *d
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memStore) GetRoute(_ context.Context, p string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.routes[p]; ok {
		return s, nil
	}
	return "", store.ErrNotFound
}
func (m *memStore) SetRoute(_ context.Context, p, s string, ups []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[p] = s
	if m.ups == nil {
		m.ups = map[string][]string{}
	}
	if len(ups) == 0 {
		delete(m.ups, p)
	} else {
		m.ups[p] = append([]string(nil), ups...)
	}
	return nil
}
func (m *memStore) ListUpstreams(_ context.Context) (map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]string{}
	for k, v := range m.ups {
		out[k] = v
	}
	return out, nil
}
func (m *memStore) ListRoutes(_ context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.routes {
		out[k] = v
	}
	return out, nil
}
func (m *memStore) DeleteRoute(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.routes, p)
	return nil
}

type fakeVerifier struct {
	mu    sync.Mutex
	ok    map[string]bool
	calls int
}

func (f *fakeVerifier) Verify(_ context.Context, host, token, preferred string) dnscheck.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.ok[host] {
		return dnscheck.Result{OK: true, Method: dnscheck.MethodTXT, Detail: "ok"}
	}
	return dnscheck.Result{OK: false, Method: preferred, Detail: "no proof for " + host}
}

type fakeCerts struct {
	mu         sync.Mutex
	registered []string
	deleted    []string
	deletedPrj []string
	failPrj    bool
}

func (f *fakeCerts) Register(_ context.Context, org, proj, domainID, host string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = append(f.registered, host)
	return uuid.NewString(), nil
}
func (f *fakeCerts) DeleteByDomain(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeCerts) DeleteByProject(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPrj {
		return context.DeadlineExceeded
	}
	f.deletedPrj = append(f.deletedPrj, id)
	return nil
}
func (f *fakeCerts) ListByProject(_ context.Context, id string) (map[string]certclient.Info, error) {
	return map[string]certclient.Info{}, nil
}

// ---------------------------------------------------------------- harness

const rootDom = "jp.example.com"

type env struct {
	h        *Handler
	srv      *httptest.Server
	st       *memStore
	ver      *fakeVerifier
	certs    *fakeCerts
	dir      string
	jm       *jwtutil.Manager
	org, prj string
	torg     *testOrg
}

func tokenFor(t *testing.T, jm *jwtutil.Manager, uid string) string {
	t.Helper()
	tok, _, err := jm.IssueAccess(uid, uid+"@x.io", uid)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func newEnv(t *testing.T, tls bool) *env {
	t.Helper()
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	to := newTestOrg(t, map[string]string{})
	e := &env{torg: to, st: newMem(), ver: &fakeVerifier{ok: map[string]bool{}}, certs: &fakeCerts{}, dir: t.TempDir(), jm: jm,
		org: uuid.NewString(), prj: uuid.NewString()}
	e.h = &Handler{
		Store: e.st, JWT: jm,
		Authz:          to.Client,
		Files:          traefikcfg.New(e.dir, "letsencrypt"),
		Policy:         hostname.Policy{RootDomain: rootDom, ManagementDomain: "panel.example.org", PreviewBase: "preview.jp.example.com"},
		VerifyCooldown: time.Nanosecond, Verifier: e.ver, Certs: e.certs, TLSEnabled: tls, CNAMETarget: "edge.jp.example.net", ATargets: []string{"203.0.113.10"},
	}
	to.Projects[e.prj] = e.org
	mux := http.NewServeMux()
	e.h.Routes(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, user string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, e.jm, user))
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

func (e *env) domainsPath() string { return "/orgs/" + e.org + "/projects/" + e.prj + "/domains" }

func (e *env) files(t *testing.T) []string {
	t.Helper()
	// only router files; jp-middlewares.yml is the shared retry middleware definition
	m, _ := filepath.Glob(filepath.Join(e.dir, "domain-*.yml"))
	return m
}

func (e *env) createDomain(t *testing.T, host string) (string, map[string]any) {
	t.Helper()
	code, out := e.do(t, "POST", e.domainsPath(), "member", map[string]string{"hostname": host})
	if code != http.StatusCreated {
		t.Fatalf("create %s: %d %v", host, code, out)
	}
	return out["domain"].(map[string]any)["id"].(string), out
}

// ---------------------------------------------------------------- tests

func TestCreateValidatesHostnames(t *testing.T) {
	e := newEnv(t, false)
	bad := []string{
		"", "localhost", "Foo.COM.evil`)||Host(`x", "a.com\nhttp:", "1.2.3.4", "jp.example.com", "www.jp.example.com",
		"x.jp.example.com", "panel.example.org", "pr-1.preview.jp.example.com", "bücher.de", "*.example.com", "a..b.com",
		"-a.com", "a b.com", "http://a.com", "a.com/path", "a.com:80",
	}
	for _, h := range bad {
		code, out := e.do(t, "POST", e.domainsPath(), "member", map[string]string{"hostname": h})
		if code != http.StatusBadRequest {
			t.Errorf("hostname %q: status %d %v", h, code, out)
		}
	}
	if len(e.st.domains) != 0 {
		t.Fatalf("nothing may be stored for invalid hostnames, have %d", len(e.st.domains))
	}
	if len(e.files(t)) != 0 {
		t.Fatal("no router files may exist")
	}
}

func TestCreateNormalizesAndReturnsInstructions(t *testing.T) {
	e := newEnv(t, false)
	_, out := e.createDomain(t, "  Shop.Example.COM ")
	d := out["domain"].(map[string]any)
	if d["hostname"] != "shop.example.com" || d["status"] != "pending" || d["routed"] != false {
		t.Fatalf("domain=%v", d)
	}
	v := out["verification"].(map[string]any)
	tok := v["token"].(string)
	if len(tok) != 32 {
		t.Fatalf("token must be 128-bit hex, got %q", tok)
	}
	recs := v["records"].([]any)
	first := recs[0].(map[string]any)
	if first["type"] != "TXT" || first["name"] != "_jp-challenge.shop.example.com" || first["value"] != "jp-verify="+tok {
		t.Fatalf("txt record=%v", first)
	}
	var sawCNAME, sawA bool
	for _, r := range recs {
		rm := r.(map[string]any)
		if rm["type"] == "CNAME" && rm["value"] == "edge.jp.example.net" {
			sawCNAME = true
		}
		if rm["type"] == "A" && rm["value"] == "203.0.113.10" {
			sawA = true
		}
	}
	if !sawCNAME || !sawA {
		t.Fatalf("records=%v", recs)
	}
	// unverified: no router, no certificate
	if len(e.files(t)) != 0 || len(e.certs.registered) != 0 {
		t.Fatalf("unverified domain must have no router/cert: files=%v certs=%v", e.files(t), e.certs.registered)
	}
	// duplicate in same project
	code, _ := e.do(t, "POST", e.domainsPath(), "member", map[string]string{"hostname": "shop.example.com"})
	if code != http.StatusConflict {
		t.Fatalf("duplicate: %d", code)
	}
}

func TestVerifyFailureLeavesDomainUnrouted(t *testing.T) {
	e := newEnv(t, true)
	id, _ := e.createDomain(t, "shop.example.com")
	code, out := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", map[string]any{"force": true})
	if code != http.StatusBadRequest || out["verified"] != false {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if len(e.files(t)) != 0 || len(e.certs.registered) != 0 {
		t.Fatal("failed verification must not create router or certificate")
	}
	d, _ := e.st.Get(context.Background(), e.org, e.prj, id)
	if d.Status != store.StatusPending || d.DNSError == "" || d.DNSLastChecked == nil {
		t.Fatalf("domain=%+v", d)
	}
}

func TestVerifyIgnoresForceFlag(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "shop.example.com")
	body := map[string]any{"force": true}
	code, out := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", body)
	if code == http.StatusOK {
		t.Fatalf("force must not bypass DNS verification: %v", out)
	}
	if e.ver.calls == 0 {
		t.Fatal("DNS verifier must have been consulted")
	}
}

func TestVerifySuccessWritesRouterToCurrentService(t *testing.T) {
	e := newEnv(t, true)
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true
	code, out := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	if code != http.StatusOK || out["verified"] != true {
		t.Fatalf("code=%d out=%v", code, out)
	}
	dom := out["domain"].(map[string]any)
	if dom["status"] != "verified" || dom["routed"] != true {
		t.Fatalf("domain=%v", dom)
	}
	files := e.files(t)
	if len(files) != 1 {
		t.Fatalf("files=%v", files)
	}
	if filepath.Base(files[0]) != traefikcfg.FileName("shop.example.com", false) {
		t.Fatalf("non canonical file name %s", files[0])
	}
	body, _ := os.ReadFile(files[0])
	s := string(body)
	if !strings.Contains(s, "Host(`shop.example.com`)") || !strings.Contains(s, "service: jp-app-"+ShortID(e.prj)+"@docker") ||
		!strings.Contains(s, "certResolver: letsencrypt") || !strings.Contains(s, "websecure") {
		t.Fatalf("router file:\n%s", s)
	}
	if strings.Contains(s, "gateway") || strings.Contains(s, "loadBalancer") {
		t.Fatalf("custom domains must no longer be routed through the gateway:\n%s", s)
	}
	if len(e.certs.registered) != 1 || e.certs.registered[0] != "shop.example.com" {
		t.Fatalf("certs=%v", e.certs.registered)
	}
}

func TestInternalRoutesRewritesRouters(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "shop.example.com")
	pending, _ := e.createDomain(t, "pending.example.com")
	_ = pending
	e.ver.ok["shop.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)

	green := "jp-app-" + ShortID(e.prj) + "-green"
	code, out := e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]string{"service": green})
	if code != http.StatusOK {
		t.Fatalf("code=%d %v", code, out)
	}
	files := e.files(t)
	if len(files) != 1 {
		t.Fatalf("only the verified domain may have a router: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "service: "+green+"@docker") {
		t.Fatalf("not switched:\n%s", b)
	}
	if got, _ := e.st.GetRoute(context.Background(), e.prj); got != green {
		t.Fatalf("project_routes=%q", got)
	}
	// invalid payloads
	for _, svc := range []string{"", "evil@file", "jp-app-x\nfoo", "gateway", "JP-APP-1"} {
		if code, _ := e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]string{"service": svc}); code != http.StatusBadRequest {
			t.Errorf("service %q accepted: %d", svc, code)
		}
	}
	if code, _ := e.do(t, "PUT", "/internal/routes/not-a-uuid", "", map[string]string{"service": green}); code != http.StatusBadRequest {
		t.Errorf("bad project id accepted: %d", code)
	}
}

func TestOneHostnameOneProject(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "shared.example.com")
	e.ver.ok["shared.example.com"] = true
	if code, _ := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil); code != http.StatusOK {
		t.Fatalf("first verify: %d", code)
	}
	// another project in another org registers the same (pending) hostname
	org2, prj2 := uuid.NewString(), uuid.NewString()
	e.torg.Projects[prj2] = org2
	p2 := "/orgs/" + org2 + "/projects/" + prj2 + "/domains"
	code, out := e.do(t, "POST", p2, "member", map[string]string{"hostname": "shared.example.com"})
	if code != http.StatusCreated {
		t.Fatalf("pending claims by others are allowed: %d %v", code, out)
	}
	id2 := out["domain"].(map[string]any)["id"].(string)
	code, out = e.do(t, "POST", p2+"/"+id2+"/verify", "member", nil)
	if code != http.StatusConflict {
		t.Fatalf("second project must not take over a verified hostname: %d %v", code, out)
	}
	// release on delete
	if code, _ := e.do(t, "DELETE", e.domainsPath()+"/"+id, "admin", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if len(e.files(t)) != 0 {
		t.Fatal("router must be removed on delete")
	}
	if code, out = e.do(t, "POST", p2+"/"+id2+"/verify", "member", nil); code != http.StatusOK {
		t.Fatalf("hostname should be free after delete: %d %v", code, out)
	}
	if len(e.certs.deleted) != 1 {
		t.Fatalf("certificate cleanup not requested: %v", e.certs.deleted)
	}
}

func TestRBAC(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true

	check := func(name, method, path, user string, body any, want int) {
		t.Helper()
		if code, out := e.do(t, method, path, user, body); code != want {
			t.Errorf("%s: %s %s as %q -> %d (want %d) %v", name, method, path, user, code, want, out)
		}
	}
	check("anon list", "GET", e.domainsPath(), "", nil, http.StatusUnauthorized)
	check("stranger list", "GET", e.domainsPath(), "nobody", nil, http.StatusForbidden)
	check("viewer list", "GET", e.domainsPath(), "viewer", nil, http.StatusOK)
	check("viewer get", "GET", e.domainsPath()+"/"+id, "viewer", nil, http.StatusOK)
	check("viewer create", "POST", e.domainsPath(), "viewer", map[string]string{"hostname": "v.example.com"}, http.StatusForbidden)
	check("viewer verify", "POST", e.domainsPath()+"/"+id+"/verify", "viewer", nil, http.StatusForbidden)
	check("member verify", "POST", e.domainsPath()+"/"+id+"/verify", "member", nil, http.StatusOK)
	check("viewer delete", "DELETE", e.domainsPath()+"/"+id, "viewer", nil, http.StatusForbidden)
	e.torg.Users["onlylogs"] = membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{membership.AreaLogs: membership.LevelManage}}
	check("other area only", "GET", e.domainsPath(), "onlylogs", nil, http.StatusForbidden)
	check("no grant hides project", "GET", e.domainsPath(), "nogrant", nil, http.StatusNotFound)
	check("not a member", "GET", e.domainsPath(), "stranger", nil, http.StatusForbidden)
	check("manage delete", "DELETE", e.domainsPath()+"/"+id, "member", nil, http.StatusNoContent)

	// project must belong to the org
	other := uuid.NewString()
	check("project/org mismatch", "GET", "/orgs/"+other+"/projects/"+e.prj+"/domains", "viewer", nil, http.StatusNotFound)
	check("bad id", "GET", "/orgs/zzz/projects/"+e.prj+"/domains", "viewer", nil, http.StatusBadRequest)
}

func TestInternalRequiresToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "s3cr3t-internal")
	e := newEnv(t, false)
	body, _ := json.Marshal(map[string]string{"service": "jp-app-12345678"})
	for _, tc := range []struct{ method, path string }{
		{"PUT", "/internal/routes/" + e.prj},
		{"POST", "/internal/domains/preview"},
		{"DELETE", "/internal/domains/by-deployment/" + uuid.NewString()},
		{"POST", "/internal/domains/system"},
		{"DELETE", "/internal/domains/system/" + e.prj},
		{"DELETE", "/internal/projects/" + e.prj},
	} {
		req, _ := http.NewRequest(tc.method, e.srv.URL+tc.path, bytes.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s without token -> %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest("PUT", e.srv.URL+"/internal/routes/"+e.prj, bytes.NewReader(body))
	req.Header.Set("X-Internal-Token", "s3cr3t-internal")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token -> %d", resp.StatusCode)
	}
}

func TestSystemDomain(t *testing.T) {
	e := newEnv(t, true)
	body := map[string]string{"org_id": e.org, "project_id": e.prj, "project_slug": "My Shop", "org_slug": "Acme"}
	code, out := e.do(t, "POST", "/internal/domains/system", "", body)
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	d := out["domain"].(map[string]any)
	if d["hostname"] != "my-shop-acme."+rootDom || d["status"] != "active" || d["kind"] != "system" || d["routed"] != true {
		t.Fatalf("domain=%v", d)
	}
	if out["url"] != "https://my-shop-acme."+rootDom {
		t.Fatalf("url=%v", out["url"])
	}
	files := e.files(t)
	if len(files) != 1 {
		t.Fatalf("files=%v", files)
	}
	// idempotent
	code, out = e.do(t, "POST", "/internal/domains/system", "", body)
	if code != http.StatusOK || len(e.st.domains) != 1 {
		t.Fatalf("idempotency: %d %v", code, out)
	}
	// collision with another project using the same slugs gets a disambiguated host
	org2, prj2 := uuid.NewString(), uuid.NewString()
	code, out = e.do(t, "POST", "/internal/domains/system", "", map[string]string{"org_id": org2, "project_id": prj2, "project_slug": "my-shop", "org_slug": "acme"})
	if code != http.StatusCreated {
		t.Fatalf("collision: %d %v", code, out)
	}
	h2 := out["domain"].(map[string]any)["hostname"].(string)
	if h2 == "my-shop-acme."+rootDom || !strings.HasSuffix(h2, "."+rootDom) {
		t.Fatalf("host2=%s", h2)
	}
	// system domains cannot be deleted through the tenant API
	sysID := d["id"].(string)
	if code, _ := e.do(t, "DELETE", e.domainsPath()+"/"+sysID, "admin", nil); code != http.StatusBadRequest {
		t.Fatalf("tenant delete of system domain: %d", code)
	}
	// delete
	if code, _ := e.do(t, "DELETE", "/internal/domains/system/"+e.prj, "", nil); code != http.StatusNoContent {
		t.Fatalf("system delete: %d", code)
	}
	if len(e.files(t)) != 1 {
		t.Fatalf("only project 2 router should remain: %v", e.files(t))
	}
	// invalid inputs
	for _, b := range []map[string]string{
		{"org_id": e.org, "project_id": "nope", "project_slug": "a", "org_slug": "b"},
		{"org_id": e.org, "project_id": uuid.NewString(), "project_slug": "", "org_slug": "b"},
		{"org_id": e.org, "project_id": uuid.NewString(), "project_slug": "!!!", "org_slug": "b"},
	} {
		if code, _ := e.do(t, "POST", "/internal/domains/system", "", b); code != http.StatusBadRequest {
			t.Errorf("%v -> %d", b, code)
		}
	}
}

func TestSystemHostLongSlugs(t *testing.T) {
	e := newEnv(t, false)
	host, err := e.h.SystemHost(strings.Repeat("p", 50), strings.Repeat("o", 50), e.prj, false)
	if err != nil {
		t.Fatal(err)
	}
	if label := strings.TrimSuffix(host, "."+rootDom); len(label) > 63 || strings.Contains(label, ".") {
		t.Fatalf("label too long: %s", label)
	}
	if _, err := e.h.SystemHost("www", "", e.prj, false); err == nil {
		t.Fatal("empty org slug must fail")
	}
}

func TestPreviewLifecycle(t *testing.T) {
	e := newEnv(t, true)
	dep := uuid.NewString()
	host := "pr-7-" + ShortID(e.prj) + ".preview.jp.example.com"
	req := map[string]string{"org_id": e.org, "project_id": e.prj, "deployment_id": dep, "hostname": host}
	code, out := e.do(t, "POST", "/internal/domains/preview", "", req)
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	if out["url"] != "http://"+host {
		t.Fatalf("url=%v", out["url"])
	}
	files := e.files(t)
	if len(files) != 1 {
		t.Fatalf("files=%v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "service: jp-prev-"+ShortID(dep)+"@docker") {
		t.Fatalf("preview must route to its own deployment service:\n%s", b)
	}
	if strings.Contains(string(b), "websecure") {
		t.Fatalf("previews are plain HTTP unless PREVIEW_TLS:\n%s", b)
	}
	// previews are not affected by project route switches
	e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]string{"service": "jp-app-" + ShortID(e.prj) + "-blue"})
	b, _ = os.ReadFile(e.files(t)[0])
	if !strings.Contains(string(b), "jp-prev-"+ShortID(dep)) {
		t.Fatalf("route switch must not alter preview:\n%s", b)
	}
	// delete by deployment
	if code, out = e.do(t, "DELETE", "/internal/domains/by-deployment/"+dep, "", nil); code != http.StatusOK || out["removed"].(float64) != 1 {
		t.Fatalf("%d %v", code, out)
	}
	if len(e.files(t)) != 0 {
		t.Fatal("preview router must be removed")
	}
}

func TestPreviewValidation(t *testing.T) {
	e := newEnv(t, false)
	dep := uuid.NewString()
	for _, host := range []string{
		"evil.com", "pr-1.jp.example.com", "preview.jp.example.com", "x`).preview.jp.example.com",
		"a.preview.jp.example.com\nfoo: bar",
	} {
		code, _ := e.do(t, "POST", "/internal/domains/preview", "", map[string]string{"org_id": e.org, "project_id": e.prj, "deployment_id": dep, "hostname": host})
		if code != http.StatusBadRequest {
			t.Errorf("preview host %q -> %d", host, code)
		}
	}
	// deployment id must be a uuid (service name derives from it)
	code, _ := e.do(t, "POST", "/internal/domains/preview", "", map[string]string{"org_id": e.org, "project_id": e.prj, "deployment_id": "../x", "hostname": "a.preview.jp.example.com"})
	if code != http.StatusBadRequest {
		t.Errorf("bad deployment id -> %d", code)
	}
	// wildcard preview
	code, out := e.do(t, "POST", "/internal/domains/preview", "", map[string]string{"org_id": e.org, "project_id": e.prj, "deployment_id": dep, "hostname": "*.preview.jp.example.com"})
	if code != http.StatusCreated {
		t.Fatalf("wildcard: %d %v", code, out)
	}
	b, _ := os.ReadFile(e.files(t)[0])
	if !strings.Contains(string(b), "HostRegexp") {
		t.Fatalf("wildcard preview must use HostRegexp:\n%s", b)
	}
}

func TestUserCannotClaimPreviewNamespaceButPlatformCan(t *testing.T) {
	e := newEnv(t, false)
	code, _ := e.do(t, "POST", e.domainsPath(), "owner", map[string]string{"hostname": "x.preview.jp.example.com"})
	if code != http.StatusBadRequest {
		t.Fatalf("users must not create preview-namespace domains: %d", code)
	}
}

func TestDeleteProjectCascade(t *testing.T) {
	e := newEnv(t, true)
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	e.do(t, "POST", "/internal/domains/system", "", map[string]string{"org_id": e.org, "project_id": e.prj, "project_slug": "shop", "org_slug": "acme"})
	e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]string{"service": "jp-app-" + ShortID(e.prj)})
	if len(e.files(t)) != 2 {
		t.Fatalf("setup: %v", e.files(t))
	}
	code, out := e.do(t, "DELETE", "/internal/projects/"+e.prj, "", nil)
	if code != http.StatusOK || out["removed"].(float64) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	if len(e.files(t)) != 0 || len(e.st.domains) != 0 || len(e.st.routes) != 0 {
		t.Fatalf("cascade incomplete: files=%v domains=%d routes=%d", e.files(t), len(e.st.domains), len(e.st.routes))
	}
	if len(e.certs.deletedPrj) != 1 {
		t.Fatal("certificate cleanup must be requested")
	}
	// partial failures are reported, never swallowed
	e.certs.failPrj = true
	if code, out := e.do(t, "DELETE", "/internal/projects/"+e.prj, "", nil); code != http.StatusBadGateway {
		t.Fatalf("partial failure must surface: %d %v", code, out)
	}
}

func TestReconcileDerivesEverythingFromDB(t *testing.T) {
	e := newEnv(t, true)
	// state in DB: one verified custom, one pending, one failed, one system, one preview
	mk := func(host, kind, status string, svc *string) *store.Domain {
		d := &store.Domain{OrgID: e.org, ProjectID: e.prj, Hostname: host, Kind: kind, Status: status}
		if err := e.st.Create(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		d.Service = svc
		_ = e.st.Update(context.Background(), d)
		return d
	}
	mk("verified.example.com", store.KindCustom, store.StatusVerified, nil)
	mk("pending.example.com", store.KindCustom, store.StatusPending, nil)
	mk("failed.example.com", store.KindCustom, store.StatusFailed, nil)
	mk("sys-acme."+rootDom, store.KindSystem, store.StatusActive, nil)
	prev := "jp-prev-abcdef12"
	mk("pr-1.preview.jp.example.com", store.KindPreview, store.StatusActive, &prev)
	// garbage on disk: legacy gateway routers, orphan canonical file, unrelated file
	_ = os.WriteFile(filepath.Join(e.dir, "domain-evil-example-com.yml"), []byte("http:\n  routers: {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(e.dir, traefikcfg.FileName("orphan.example.com", false)), []byte("http:\n  routers: {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(e.dir, "gateway.yml"), []byte("http: {}\n"), 0o644)

	res, err := e.h.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 3 || res.Removed != 2 || len(res.Errors) != 0 {
		t.Fatalf("res=%+v", res)
	}
	got := map[string]bool{}
	for _, f := range e.files(t) {
		got[filepath.Base(f)] = true
	}
	for _, h := range []string{"verified.example.com", "sys-acme." + rootDom, "pr-1.preview.jp.example.com"} {
		if !got[traefikcfg.FileName(h, false)] {
			t.Errorf("missing router for %s", h)
		}
	}
	for _, h := range []string{"pending.example.com", "failed.example.com", "orphan.example.com"} {
		if got[traefikcfg.FileName(h, false)] {
			t.Errorf("unexpected router for %s", h)
		}
	}
	if len(got) != 3 {
		t.Fatalf("router files=%v", got)
	}
	for _, keep := range []string{"gateway.yml", "jp-middlewares.yml"} {
		if _, err := os.Stat(filepath.Join(e.dir, keep)); err != nil {
			t.Fatalf("%s must survive reconcile: %v", keep, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(e.dir, traefikcfg.FileName("pr-1.preview.jp.example.com", false)))
	if !strings.Contains(string(b), prev+"@docker") {
		t.Fatalf("preview service override lost:\n%s", b)
	}
}

func TestReverifyDowngradesAfterTwoFailures(t *testing.T) {
	e := newEnv(t, true)
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	if len(e.files(t)) != 1 {
		t.Fatal("setup failed")
	}
	// DNS still valid
	if c, d := e.h.ReverifyAll(context.Background()); c != 1 || d != 0 {
		t.Fatalf("checked=%d downgraded=%d", c, d)
	}
	// DNS broken: first failure is tolerated (transient), second downgrades
	delete(e.ver.ok, "shop.example.com")
	if _, d := e.h.ReverifyAll(context.Background()); d != 0 {
		t.Fatal("one failure must not downgrade")
	}
	dom, _ := e.st.Get(context.Background(), e.org, e.prj, id)
	if dom.Status != store.StatusVerified || dom.DNSFailCount != 1 || dom.DNSError == "" {
		t.Fatalf("after 1 failure: %+v", dom)
	}
	if _, d := e.h.ReverifyAll(context.Background()); d != 1 {
		t.Fatal("second failure must downgrade")
	}
	dom, _ = e.st.Get(context.Background(), e.org, e.prj, id)
	if dom.Status != store.StatusFailed {
		t.Fatalf("status=%s", dom.Status)
	}
	if len(e.files(t)) != 0 {
		t.Fatal("router must be removed after downgrade")
	}
	if len(e.certs.deleted) != 1 {
		t.Fatal("certificate must be dropped after downgrade")
	}
	// recovers via manual verify
	e.ver.ok["shop.example.com"] = true
	time.Sleep(5 * time.Millisecond)
	if code, _ := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil); code != http.StatusOK {
		t.Fatalf("re-verify: %d", code)
	}
	if len(e.files(t)) != 1 {
		t.Fatal("router must come back")
	}
}

func TestListShowsSSLAndRoutedFields(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "a.example.com")
	e.createDomain(t, "b.example.com")
	e.ver.ok["a.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	code, out := e.do(t, "GET", e.domainsPath(), "viewer", nil)
	if code != 200 {
		t.Fatal(code)
	}
	byHost := map[string]map[string]any{}
	for _, d := range out["domains"].([]any) {
		m := d.(map[string]any)
		byHost[m["hostname"].(string)] = m
	}
	a, b := byHost["a.example.com"], byHost["b.example.com"]
	if a["routed"] != true || a["ssl_status"] != "tls_disabled" || a["url"] != "http://a.example.com" {
		t.Fatalf("a=%v", a)
	}
	if b["routed"] != false || b["ssl_status"] != "none" || b["dns_last_checked"] != nil {
		t.Fatalf("b=%v", b)
	}
	if _, ok := b["verification"].(map[string]any); !ok {
		t.Fatalf("pending domains must carry verification instructions: %v", b)
	}
	if _, leaked := a["verification_token"]; leaked {
		t.Fatal("raw token field must not be serialized outside verification block")
	}
	if cfg := out["config"].(map[string]any); cfg["tls_enabled"] != false {
		t.Fatalf("config=%v", cfg)
	}
}

func TestVerifyThrottle(t *testing.T) {
	e := newEnv(t, false)
	e.h.VerifyCooldown = time.Minute
	id, _ := e.createDomain(t, "a.example.com")
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	if code, _ := e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil); code != http.StatusTooManyRequests {
		t.Fatalf("code=%d", code)
	}
}

func TestInternalReissueRewritesRouter(t *testing.T) {
	e := newEnv(t, true)
	e.h.ReissueGap = time.Millisecond
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	if code, out := e.do(t, "POST", "/internal/domains/"+id+"/reissue", "", nil); code != http.StatusAccepted {
		t.Fatalf("%d %v", code, out)
	}
	time.Sleep(150 * time.Millisecond) // let the remove+rewrite cycle run
	deadline := time.Now().Add(3 * time.Second)
	for len(e.files(t)) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(e.files(t)) != 1 {
		t.Fatal("router must be rewritten after the gap")
	}
	pend, _ := e.createDomain(t, "pending.example.com")
	if code, _ := e.do(t, "POST", "/internal/domains/"+pend+"/reissue", "", nil); code != http.StatusConflict {
		t.Fatalf("unverified domains never get certificates: %d", code)
	}
	if code, _ := e.do(t, "POST", "/internal/domains/"+uuid.NewString()+"/reissue", "", nil); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
}

func TestInternalRoutesUpstreams(t *testing.T) {
	e := newEnv(t, false)
	id, _ := e.createDomain(t, "shop.example.com")
	e.ver.ok["shop.example.com"] = true
	e.do(t, "POST", e.domainsPath()+"/"+id+"/verify", "member", nil)
	svc := "jp-app-" + ShortID(e.prj)
	read := func() string {
		fs := e.files(t)
		if len(fs) != 1 {
			t.Fatalf("files=%v", fs)
		}
		b, _ := os.ReadFile(fs[0])
		return string(b)
	}
	ups := []string{"http://10.0.0.179:32768", "http://10.0.0.180:32768"}
	code, out := e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]any{"service": svc, "upstreams": ups})
	if code != http.StatusOK {
		t.Fatalf("code=%d %v", code, out)
	}
	body := read()
	if !strings.Contains(body, "url: http://10.0.0.179:32768") || !strings.Contains(body, "url: http://10.0.0.180:32768") ||
		!strings.Contains(body, "@file") || strings.Contains(body, "@docker") {
		t.Fatalf("not upstream routed:\n%s", body)
	}
	// persistence: reconcile and re-render paths keep the upstreams
	if _, err := e.h.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b2 := read(); b2 != body {
		t.Fatalf("reconcile reverted upstreams:\n%s", b2)
	}
	if err := e.h.SyncProject(context.Background(), e.prj); err != nil {
		t.Fatal(err)
	}
	if b2 := read(); b2 != body {
		t.Fatalf("sync reverted upstreams:\n%s", b2)
	}
	// invalid upstreams are rejected and change nothing
	for _, bad := range [][]string{{"https://10.0.0.1:80"}, {"http://169.254.169.254:80"}, {"http://example.com:80"}, {"http://127.0.0.1:80"}, {"http://10.0.0.1:80", "http://10.0.0.1:80"}} {
		code, _ := e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]any{"service": svc, "upstreams": bad})
		if code != http.StatusBadRequest {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}
	if read() != body {
		t.Fatal("rejected request changed state")
	}
	// clearing: no upstreams -> back to the docker service, no services block
	code, _ = e.do(t, "PUT", "/internal/routes/"+e.prj, "", map[string]any{"service": svc})
	if code != http.StatusOK {
		t.Fatal(code)
	}
	b3 := read()
	if !strings.Contains(b3, "service: "+svc+"@docker") || strings.Contains(b3, "services:") {
		t.Fatalf("not cleared:\n%s", b3)
	}
	if _, err := e.h.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if read() != b3 {
		t.Fatal("reconcile resurrected upstreams")
	}
}
