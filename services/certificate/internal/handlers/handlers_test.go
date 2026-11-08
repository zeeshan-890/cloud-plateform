package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/certificate/internal/prober"
	"github.com/jp-cloud/certificate/internal/store"
	jwtutil "github.com/jp-cloud/go-common/jwt"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]*store.Certificate
}

func (s *memStore) Upsert(_ context.Context, c *store.Certificate) (*store.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.m {
		if c.DomainID != nil && o.DomainID != nil && *o.DomainID == *c.DomainID {
			cp := *o
			return &cp, nil
		}
	}
	c.ID = uuid.NewString()
	c.CreatedAt = time.Now().UTC()
	cp := *c
	s.m[c.ID] = &cp
	return c, nil
}
func (s *memStore) all(f func(*store.Certificate) bool) []store.Certificate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.Certificate{}
	for _, c := range s.m {
		if f(c) {
			out = append(out, *c)
		}
	}
	return out
}
func (s *memStore) List(_ context.Context, o, p string) ([]store.Certificate, error) {
	return s.all(func(c *store.Certificate) bool { return c.OrgID == o && c.ProjectID == p }), nil
}
func (s *memStore) ListByProject(_ context.Context, p string) ([]store.Certificate, error) {
	return s.all(func(c *store.Certificate) bool { return c.ProjectID == p }), nil
}
func (s *memStore) ListAll(context.Context) ([]store.Certificate, error) {
	return s.all(func(*store.Certificate) bool { return true }), nil
}
func (s *memStore) Get(_ context.Context, o, p, id string) (*store.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[id]
	if !ok || c.OrgID != o || c.ProjectID != p {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}
func (s *memStore) Update(_ context.Context, c *store.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.m[c.ID] = &cp
	return nil
}
func (s *memStore) DeleteByDomain(_ context.Context, d string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, c := range s.m {
		if c.DomainID != nil && *c.DomainID == d {
			delete(s.m, id)
			n++
		}
	}
	return n, nil
}
func (s *memStore) DeleteByProject(_ context.Context, p string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, c := range s.m {
		if c.ProjectID == p {
			delete(s.m, id)
			n++
		}
	}
	return n, nil
}

type fakeProber struct{ res prober.Result }

func (f *fakeProber) Probe(context.Context, string) prober.Result { return f.res }

type fakeReissuer struct{ calls []string }

func (f *fakeReissuer) Reissue(_ context.Context, id string) error {
	f.calls = append(f.calls, id)
	return nil
}

type env struct {
	h   *Handler
	srv *httptest.Server
	st  *memStore
	pr  *fakeProber
	ri  *fakeReissuer
	jm  *jwtutil.Manager
	org string
	prj string
}

func newEnv(t *testing.T, tls bool) *env {
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	to := newTestOrg(t, map[string]string{})
	e := &env{st: &memStore{m: map[string]*store.Certificate{}}, pr: &fakeProber{}, ri: &fakeReissuer{}, jm: jm, org: uuid.NewString(), prj: uuid.NewString()}
	to.Projects[e.prj] = e.org
	e.h = &Handler{Store: e.st, JWT: jm, Prober: e.pr, Reissuer: e.ri, TLSEnabled: tls, RenewCool: time.Nanosecond,
		Authz: to.Client}
	mux := http.NewServeMux()
	e.h.Routes(mux)
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
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *env) register(t *testing.T) string {
	code, out := e.do(t, "POST", "/internal/certificates", "", map[string]string{"org_id": e.org, "project_id": e.prj, "domain_id": uuid.NewString(), "hostname": "app.example.com"})
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	return out["certificate"].(map[string]any)["id"].(string)
}

func goodResult(days int) prober.Result {
	return prober.Result{Reachable: true, ChainOK: true, Issuer: "CN=R3", Serial: "ab", DNSNames: []string{"app.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Duration(days) * 24 * time.Hour)}
}

func TestRegisterIsPendingNeverIssued(t *testing.T) {
	e := newEnv(t, true)
	id := e.register(t)
	c, _ := e.st.Get(context.Background(), e.org, e.prj, id)
	if c.Status != "pending" || c.IssuedAt != nil || c.ExpiresAt != nil {
		t.Fatalf("a fresh certificate must be pending with no dates: %+v", c)
	}
}

func TestTLSDisabledIsReportedHonestly(t *testing.T) {
	e := newEnv(t, false)
	id := e.register(t)
	c, _ := e.st.Get(context.Background(), e.org, e.prj, id)
	if c.Status != prober.StatusTLSDisabled {
		t.Fatalf("status=%s", c.Status)
	}
	if code, out := e.do(t, "POST", "/orgs/"+e.org+"/projects/"+e.prj+"/certificates/"+id+"/renew", "member", nil); code != http.StatusConflict {
		t.Fatalf("renew with TLS off: %d %v", code, out)
	}
	e.h.Scan(context.Background())
	c, _ = e.st.Get(context.Background(), e.org, e.prj, id)
	if c.Status != prober.StatusTLSDisabled || c.ExpiresAt != nil {
		t.Fatalf("%+v", c)
	}
}

func TestScanDerivesStatusFromProbe(t *testing.T) {
	e := newEnv(t, true)
	id := e.register(t)
	get := func() *store.Certificate { c, _ := e.st.Get(context.Background(), e.org, e.prj, id); return c }

	e.pr.res = goodResult(60)
	e.h.Scan(context.Background())
	if c := get(); c.Status != "issued" || c.ExpiresAt == nil || c.Issuer != "CN=R3" || c.LastProbedAt == nil {
		t.Fatalf("%+v", c)
	}
	// valid + probed <1h ago: skipped on next scan, so force by clearing LastProbedAt
	c := get()
	c.LastProbedAt = nil
	_ = e.st.Update(context.Background(), c)
	e.pr.res = goodResult(10)
	e.h.Scan(context.Background())
	if c := get(); c.Status != "expiring" {
		t.Fatalf("%+v", c)
	}
	c = get()
	c.LastProbedAt = nil
	_ = e.st.Update(context.Background(), c)
	e.pr.res = prober.Result{Reachable: false, Err: "connection refused"}
	e.h.Scan(context.Background())
	if c := get(); c.Status != "failed" || c.Error == "" {
		t.Fatalf("%+v", c)
	}
}

func TestRenewActions(t *testing.T) {
	e := newEnv(t, true)
	id := e.register(t)
	path := "/orgs/" + e.org + "/projects/" + e.prj + "/certificates/" + id + "/renew"

	e.pr.res = prober.Result{Reachable: true, DefaultCrt: true}
	code, out := e.do(t, "POST", path, "member", nil)
	if code != http.StatusAccepted || out["action"] != "reissue_requested" || len(e.ri.calls) != 1 {
		t.Fatalf("pending cert must trigger a re-issue: %d %v calls=%v", code, out, e.ri.calls)
	}
	e.pr.res = goodResult(80)
	code, out = e.do(t, "POST", path, "member", nil)
	if code != http.StatusOK || out["action"] != "none" || len(e.ri.calls) != 1 {
		t.Fatalf("valid cert must not be re-issued: %d %v", code, out)
	}
	if code, _ := e.do(t, "POST", path, "viewer", nil); code != http.StatusForbidden {
		t.Fatalf("viewer renew: %d", code)
	}
	if code, _ := e.do(t, "POST", path, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anon renew: %d", code)
	}
}

func TestRenewRateLimited(t *testing.T) {
	e := newEnv(t, true)
	e.h.RenewCool = time.Hour
	id := e.register(t)
	e.pr.res = prober.Result{Reachable: true, DefaultCrt: true}
	path := "/orgs/" + e.org + "/projects/" + e.prj + "/certificates/" + id + "/renew"
	e.do(t, "POST", path, "member", nil)
	if code, _ := e.do(t, "POST", path, "member", nil); code != http.StatusTooManyRequests {
		t.Fatalf("code=%d", code)
	}
}

func TestInternalCleanupAndTokens(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "tok")
	e := newEnv(t, true)
	req, _ := http.NewRequest("POST", e.srv.URL+"/internal/certificates/scan", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("internal without token: %d", resp.StatusCode)
	}
	t.Setenv("INTERNAL_TOKEN", "")
	e.register(t)
	if code, out := e.do(t, "DELETE", "/internal/projects/"+e.prj, "", nil); code != 200 || out["removed"].(float64) != 1 {
		t.Fatalf("%d %v", code, out)
	}
}
