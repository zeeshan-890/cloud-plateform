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

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/metrics/internal/store"
)

type fakeRepo struct {
	mu      sync.Mutex
	samples []store.Sample
}

func (f *fakeRepo) IngestBatch(_ context.Context, ss []*store.Sample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range ss {
		f.samples = append(f.samples, *s)
	}
	return nil
}
func (f *fakeRepo) Summary(_ context.Context, org, proj string) ([]store.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Summary{}
	for _, s := range f.samples {
		if s.OrgID == org && s.ProjectID == proj {
			out = append(out, store.Summary{Name: s.Name, Latest: s.Value, Count: 1})
		}
	}
	return out, nil
}
func (f *fakeRepo) Series(_ context.Context, org, proj, name string, since time.Time, limit int) ([]store.Point, error) {
	return []store.Point{{Value: 1, RecordedAt: time.Now()}}, nil
}
func (f *fakeRepo) DeleteProject(_ context.Context, proj string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = nil
	return nil
}
func (f *fakeRepo) DeleteOlderThan(context.Context, time.Time, int) (int64, error) { return 0, nil }

var (
	orgA  = uuid.NewString()
	projA = uuid.NewString()
)

type harness struct {
	mux   *http.ServeMux
	repo  *fakeRepo
	h     *Handler
	jm    *jwtutil.Manager
	roles map[string]string
}

func newHarness(t *testing.T) *harness {
	hs := &harness{repo: &fakeRepo{}, jm: jwtutil.NewManager("0123456789abcdef0123456789abcdef0123", time.Minute, time.Hour), roles: map[string]string{}}
	org := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		role, ok := hs.roles[parts[len(parts)-1]]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(r.URL.Path, "/access/") {
			switch role {
			case "owner", "admin":
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "full": true})
			case "viewer":
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "areas": map[string]string{"metrics": "view"}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "areas": map[string]string{"metrics": "manage"}})
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
	}))
	proj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/projects/"+projA) && strings.Contains(r.URL.Path, "/orgs/"+orgA+"/") {
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(org.Close)
	t.Cleanup(proj.Close)
	hs.h = &Handler{Store: hs.repo, JWT: hs.jm, Members: &membership.Client{OrgURL: org.URL, ProjectURL: proj.URL, HTTP: http.DefaultClient}}
	hs.mux = http.NewServeMux()
	hs.h.Routes(hs.mux)
	return hs
}

func (hs *harness) user(role string) string {
	id := uuid.NewString()
	if role != "" {
		hs.roles[id] = role
	}
	tok, _, _ := hs.jm.IssueAccess(id, "u@example.com", "U")
	return tok
}

func (hs *harness) do(method, path string, body any, token string, hdr ...string) *httptest.ResponseRecorder {
	var rdr io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
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

func mp(org, proj string) string { return "/orgs/" + org + "/projects/" + proj + "/metrics" }

func TestIngestRolesAndProjectBinding(t *testing.T) {
	hs := newHarness(t)
	body := map[string]any{"name": "http_requests_total", "value": 3}
	for role, want := range map[string]int{"viewer": 403, "": 403, "member": 201, "owner": 201} {
		if rec := hs.do("POST", mp(orgA, projA), body, hs.user(role)); rec.Code != want {
			t.Errorf("role %q = %d want %d", role, rec.Code, want)
		}
	}
	if rec := hs.do("POST", mp(orgA, uuid.NewString()), body, hs.user("member")); rec.Code != 404 {
		t.Errorf("foreign project: %d", rec.Code)
	}
}

func TestIngestValidation(t *testing.T) {
	hs := newHarness(t)
	m := hs.user("member")
	for _, bad := range []map[string]any{{"name": "bad name!", "value": 1}, {"name": "", "value": 1}, {"name": "ok", "labels": map[string]string{strings.Repeat("k", 100): "v"}}} {
		if rec := hs.do("POST", mp(orgA, projA), bad, m); rec.Code != 400 {
			t.Errorf("%v => %d", bad, rec.Code)
		}
	}
}

func TestReadIsLiveByDefaultAndReportsMode(t *testing.T) {
	hs := newHarness(t)
	rec := hs.do("GET", mp(orgA, projA), nil, hs.user("viewer"))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["mode"] != "live" || len(hs.repo.samples) != 0 {
		t.Fatalf("default must be live with no fabricated data: %d %v samples=%d", rec.Code, out, len(hs.repo.samples))
	}
	if _, has := out["targets"]; has {
		t.Fatal("scrape targets were removed")
	}
	hs.h.Simulate = true
	rec = hs.do("GET", mp(orgA, projA), nil, hs.user("viewer"))
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["mode"] != "simulate" {
		t.Fatalf("simulation must be visible: %v", out)
	}
	if SimulateAllowed("production") || !SimulateAllowed("development") {
		t.Fatal("simulate only outside production")
	}
}

func TestInternalEndpointsProtected(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	hs := newHarness(t)
	body := map[string]any{"samples": []map[string]any{{"org_id": orgA, "project_id": projA, "name": "cpu_millicores", "value": 5}}}
	if rec := hs.do("POST", "/internal/metrics", body, hs.user("owner")); rec.Code != 403 {
		t.Errorf("user JWT on internal ingest: %d", rec.Code)
	}
	if rec := hs.do("POST", "/internal/metrics", body, "", internalauth.Header, "internal-token-for-tests"); rec.Code != 201 {
		t.Errorf("internal ingest: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/internal/projects/"+projA, nil, ""); rec.Code != 403 {
		t.Errorf("internal delete unauth: %d", rec.Code)
	}
}

func TestSelfMetricsHaveNoTenantLabels(t *testing.T) {
	hs := newHarness(t)
	hs.do("POST", mp(orgA, projA), map[string]any{"name": "x", "value": 1}, hs.user("member"))
	rec := hs.do("GET", "/metrics", nil, "")
	if strings.Contains(rec.Body.String(), orgA) || strings.Contains(rec.Body.String(), projA) || strings.Contains(rec.Body.String(), "{") {
		t.Fatalf("self metrics leak tenant data: %s", rec.Body)
	}
}
