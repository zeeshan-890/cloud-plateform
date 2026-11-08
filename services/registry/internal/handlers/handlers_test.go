package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/registry/internal/regclient"
	"github.com/jp-cloud/registry/internal/store"
)

const (
	org  = "11111111-1111-4111-8111-111111111111"
	proj = "22222222-2222-4222-8222-222222222222"
	dig  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type fakeRegistry struct {
	mu        sync.Mutex
	manifests map[string]bool // "repo:tag"
	deleted   []string
	noDelete  bool
}

func (f *fakeRegistry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/v2/")
		i := strings.Index(p, "/manifests/")
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		repo, ref := p[:i], p[i+len("/manifests/"):]
		switch r.Method {
		case "HEAD":
			if !f.manifests[repo+":"+ref] {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Docker-Content-Digest", dig)
			w.WriteHeader(200)
		case "DELETE":
			if f.noDelete {
				w.WriteHeader(405)
				return
			}
			if ref != dig {
				w.WriteHeader(400)
				return
			}
			f.deleted = append(f.deleted, repo)
			w.WriteHeader(202)
		}
	})
}

type memRepo struct {
	imgs    []store.Image
	removed []string
}

func (m *memRepo) Create(context.Context, *store.Image) error                  { return nil }
func (m *memRepo) List(context.Context, string, string) ([]store.Image, error) { return m.imgs, nil }
func (m *memRepo) Candidates(context.Context, time.Time, int, map[string]struct{}) ([]store.Image, error) {
	return m.imgs, nil
}
func (m *memRepo) ListAllByProject(context.Context, string) ([]store.Image, error) {
	return m.imgs, nil
}
func (m *memRepo) Delete(_ context.Context, id string) error {
	m.removed = append(m.removed, id)
	return nil
}

func setup(t *testing.T, f *fakeRegistry, imgs []store.Image) (*Handler, *memRepo) {
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	repo := &memRepo{imgs: imgs}
	return &Handler{Store: repo, Registry: regclient.New(srv.URL, "registry:5000")}, repo
}

func TestCleanupDeletesFromRegistryThenMetadata(t *testing.T) {
	f := &fakeRegistry{manifests: map[string]bool{"abcd1234/efgh5678:old1": true}}
	h, repo := setup(t, f, []store.Image{
		{ID: "1", ImageRef: "registry:5000/abcd1234/efgh5678:old1"},
		{ID: "2", ImageRef: "registry:5000/abcd1234/efgh5678:gone"}, // already absent in registry
		{ID: "3", ImageRef: "docker.io/foreign/img:1"},              // not ours: never DELETEd
	})
	t.Setenv("INTERNAL_TOKEN", "t")
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest("POST", "/internal/cleanup/orphaned-images", strings.NewReader(`{"older_than_hours":1}`))
	req.Header.Set("X-Internal-Token", "t")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body.String())
	}
	if len(f.deleted) != 1 || f.deleted[0] != "abcd1234/efgh5678" {
		t.Fatalf("registry deletes = %v", f.deleted)
	}
	if len(repo.removed) != 3 {
		t.Fatalf("metadata removed = %v", repo.removed)
	}
	// without token: forbidden
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/internal/cleanup/orphaned-images", nil))
	if w.Code != 403 {
		t.Fatalf("no token = %d", w.Code)
	}
}

func TestDeleteDisabledKeepsMetadataAndReports(t *testing.T) {
	f := &fakeRegistry{manifests: map[string]bool{"abcd1234/efgh5678:t": true}, noDelete: true}
	h, repo := setup(t, f, []store.Image{{ID: "1", ImageRef: "registry:5000/abcd1234/efgh5678:t"}})
	rec := httptest.NewRecorder()
	h.CleanupOrphans(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
	if rec.Code != http.StatusMultiStatus || !strings.Contains(rec.Body.String(), "deletes disabled") {
		t.Fatalf("code %d %s", rec.Code, rec.Body.String())
	}
	if len(repo.removed) != 0 {
		t.Fatal("metadata must stay so the delete is retried")
	}
}

func TestProjectDeleteRemovesImages(t *testing.T) {
	f := &fakeRegistry{manifests: map[string]bool{"abcd1234/efgh5678:a": true, "abcd1234/efgh5678:b": true}}
	h, repo := setup(t, f, []store.Image{{ID: "1", ImageRef: "registry:5000/abcd1234/efgh5678:a"}, {ID: "2", ImageRef: "registry:5000/abcd1234/efgh5678:b"}})
	mux := http.NewServeMux()
	h.Routes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/internal/projects/"+proj, nil))
	if w.Code != 200 || len(f.deleted) != 2 || len(repo.removed) != 2 {
		t.Fatalf("code %d deleted %v removed %v", w.Code, f.deleted, repo.removed)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/internal/projects/zz", nil))
	if w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
}

func TestRefParsingRejectsTraversalAndForeignHosts(t *testing.T) {
	c := regclient.New("http://x", "registry:5000")
	good := map[string][2]string{
		"registry:5000/a1/b2:t1":      {"a1/b2", "t1"},
		"registry:5000/a1/b2":         {"a1/b2", "latest"},
		"registry:5000/team/app:v1.2": {"team/app", "v1.2"},
	}
	for ref, want := range good {
		r, tag, err := c.Parse(ref)
		if err != nil || r != want[0] || tag != want[1] {
			t.Errorf("%s => %s %s %v", ref, r, tag, err)
		}
	}
	for _, ref := range []string{"evil:5000/a/b:1", "registry:5000/../etc/passwd:1", "registry:5000/a/B:1", "registry:5000/a/b:-x", "registry:5000/a b/c:1", "nohost", "registry:5000/a/b:t?x=1", "registry:5000//a:1"} {
		if _, _, err := c.Parse(ref); err == nil {
			t.Errorf("%q must be rejected", ref)
		}
	}
}

func (m *memRepo) DeleteByRef(_ context.Context, _, ref string) error {
	m.removed = append(m.removed, "ref:"+ref)
	return nil
}

func TestDeleteSingleImage(t *testing.T) {
	f := &fakeRegistry{manifests: map[string]bool{"abcd1234/efgh5678:pr4-aaa": true}}
	h, repo := setup(t, f, nil)
	mux := http.NewServeMux()
	h.Routes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/internal/images/delete", strings.NewReader(`{"project_id":"`+proj+`","image_ref":"registry:5000/abcd1234/efgh5678:pr4-aaa"}`)))
	if w.Code != 204 || len(f.deleted) != 1 || len(repo.removed) != 1 {
		t.Fatalf("code %d deleted %v removed %v", w.Code, f.deleted, repo.removed)
	}
}

func TestImageListNeedsBuildsViewAccess(t *testing.T) {
	org2 := "11111111-1111-4111-8111-111111111111"
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveAccess(w, strings.Split(strings.Trim(r.URL.Path, "/"), "/")) {
			return
		}
		http.NotFound(w, r)
	}))
	defer orgSrv.Close()
	projSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) }))
	defer projSrv.Close()
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	h := &Handler{Store: &memRepo{}, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL)}
	mux := http.NewServeMux()
	h.Routes(mux)
	for user, want := range map[string]int{"viewer": 200, "admin": 200, "restricted": 403, "stranger": 403} {
		tok, _, _ := jm.IssueAccess(user, user+"@x", user)
		req := httptest.NewRequest("GET", "/orgs/"+org2+"/projects/"+proj+"/images", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("%s => %d, want %d", user, w.Code, want)
		}
	}
}
