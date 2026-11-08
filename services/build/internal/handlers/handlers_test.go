package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jp-cloud/build/internal/store"
	"github.com/jp-cloud/events"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/redis/go-redis/v9"
)

const (
	org  = "11111111-1111-4111-8111-111111111111"
	proj = "22222222-2222-4222-8222-222222222222"
)

type memRepo struct{ m map[string]*store.Build }

func (r *memRepo) Create(_ context.Context, b *store.Build) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	c := *b
	r.m[b.ID] = &c
	return nil
}
func (r *memRepo) List(_ context.Context, o, p string) ([]store.Build, error) {
	out := []store.Build{}
	for _, b := range r.m {
		if b.OrgID == o && b.ProjectID == p {
			out = append(out, *b)
		}
	}
	return out, nil
}
func (r *memRepo) DeleteProject(_ context.Context, p string) (int64, error) {
	var n int64
	for id, b := range r.m {
		if b.ProjectID == p {
			delete(r.m, id)
			n++
		}
	}
	return n, nil
}
func (r *memRepo) Get(_ context.Context, id string) (*store.Build, error) {
	if b, ok := r.m[id]; ok {
		c := *b
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (r *memRepo) Apply(_ context.Context, id string, u store.Update) (*store.Build, bool, error) {
	b, ok := r.m[id]
	if !ok {
		return nil, false, store.ErrNotFound
	}
	if store.IsTerminal(b.Status) {
		c := *b
		if u.Status == b.Status || u.Status == "" {
			return &c, false, nil
		}
		return &c, false, store.ErrTerminal
	}
	if !store.ValidTransition(b.Status, u.Status) {
		return nil, false, store.ErrTerminal
	}
	if u.Status != "" {
		b.Status = u.Status
	}
	b.Logs += u.LogsAppend
	if u.ImageRef != "" {
		b.ImageRef = u.ImageRef
	}
	if u.ScanStatus != "" {
		b.ScanStatus = u.ScanStatus
		b.Scan = u.Scan
	}
	c := *b
	return &c, store.IsTerminal(u.Status), nil
}

type rig struct {
	mux  *http.ServeMux
	repo *memRepo
	rdb  *redis.Client
	tok  map[string]string
}

func setup(t *testing.T) *rig {
	t.Helper()
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if serveAccess(w, p) {
			return
		}
		if len(p) == 5 && p[2] == org && p[4] == "viewer" {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": "viewer"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(orgSrv.Close)
	projSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/orgs/"+org+"/projects/"+proj {
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(projSrv.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	repo := &memRepo{m: map[string]*store.Build{}}
	h := &Handler{Store: repo, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL), Redis: rdb}
	mux := http.NewServeMux()
	h.Routes(mux)
	tk := map[string]string{}
	for _, u := range []string{"viewer", "stranger"} {
		tk[u], _, _ = jm.IssueAccess(u, u+"@x", u)
	}
	return &rig{mux: mux, repo: repo, rdb: rdb, tok: tk}
}

func (r *rig) call(method, path, body, user string, internal bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+r.tok[user])
	}
	if internal {
		req.Header.Set("X-Internal-Token", "int")
	}
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w
}

func TestInternalCreateValidation(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	bad := []string{
		`{}`,
		`{"org_id":"` + org + `","project_id":"x"}`,
		`{"org_id":"` + org + `","project_id":"` + proj + `","git_branch":"--upload-pack=evil"}`,
		`{"org_id":"` + org + `","project_id":"` + proj + `","git_branch":"a b"}`,
		`{"org_id":"` + org + `","project_id":"` + proj + `","git_sha":"zzz"}`,
		`{"org_id":"` + org + `","project_id":"` + proj + `","full_name":"../../etc"}`,
		`{"org_id":"` + org + `","project_id":"` + proj + `","clone_url":"https://x:tok@github.com/a/b"}`, // unknown field
	}
	for _, b := range bad {
		if w := r.call("POST", "/internal/builds", b, "", true); w.Code != 400 {
			t.Errorf("%s => %d", b, w.Code)
		}
	}
	if w := r.call("POST", "/internal/builds", `{}`, "", false); w.Code != 403 {
		t.Errorf("no internal token => %d", w.Code)
	}
}

func TestBuildLifecycleScanRBACAndSingleOutcomeEvent(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	w := r.call("POST", "/internal/builds", `{"org_id":"`+org+`","project_id":"`+proj+`","deployment_id":"`+uuid.NewString()+`","git_sha":"abcdef1","git_branch":"main","full_name":"o/r","pr_number":5}`, "", true)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Build store.Build `json:"build"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	id := out.Build.ID
	if strings.Contains(w.Body.String(), "clone_url") {
		t.Fatal("build must not expose clone_url")
	}
	// the queued job reached the stream with the PR number
	msgs, _ := r.rdb.XRange(context.Background(), events.TopicBuild, "-", "+").Result()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Values["payload"].(string), `"pr_number":5`) {
		t.Fatalf("stream = %v", msgs)
	}
	// worker progress
	if w := r.call("POST", "/internal/builds/"+id+"/status", `{"status":"running","logs":"step 1\n"}`, "", true); w.Code != 204 {
		t.Fatalf("running = %d", w.Code)
	}
	scan := `{"critical":1,"high":2,"findings":[{"id":"CVE-1"}]}`
	done := `{"status":"succeeded","image_ref":"reg/a:1","logs":"done\n","scan_status":"scanned","scan":` + scan + `}`
	for i := 0; i < 3; i++ { // idempotent repeat of the terminal update
		if w := r.call("POST", "/internal/builds/"+id+"/status", done, "", true); w.Code != 204 {
			t.Fatalf("succeeded #%d = %d %s", i, w.Code, w.Body.String())
		}
	}
	deploy, _ := r.rdb.XRange(context.Background(), events.TopicDeploy, "-", "+").Result()
	if len(deploy) != 1 {
		t.Fatalf("build.succeeded must be published exactly once, got %d", len(deploy))
	}
	// terminal state is final
	if w := r.call("POST", "/internal/builds/"+id+"/status", `{"status":"failed"}`, "", true); w.Code != 409 {
		t.Fatalf("terminal->failed = %d", w.Code)
	}
	if w := r.call("POST", "/internal/builds/"+id+"/status", `{"status":"bogus"}`, "", true); w.Code != 400 {
		t.Fatalf("bogus = %d", w.Code)
	}
	// viewer can read, scan summary is exposed on GET
	base := "/orgs/" + org + "/projects/" + proj + "/builds"
	g := r.call("GET", base+"/"+id, "", "viewer", false)
	if g.Code != 200 || !strings.Contains(g.Body.String(), `"scan_status":"scanned"`) || !strings.Contains(g.Body.String(), "CVE-1") {
		t.Fatalf("get = %d %s", g.Code, g.Body.String())
	}
	if l := r.call("GET", base, "", "viewer", false); l.Code != 200 || strings.Contains(l.Body.String(), "CVE-1") || strings.Contains(l.Body.String(), `"logs"`) {
		t.Fatalf("list must be a summary: %d %s", l.Code, l.Body.String())
	}
	if w := r.call("GET", base+"/"+id, "", "stranger", false); w.Code != 403 {
		t.Fatalf("stranger = %d", w.Code)
	}
	if w := r.call("GET", base, "", "", false); w.Code != 401 {
		t.Fatalf("anon = %d", w.Code)
	}
	// a build of this project is not visible through another project's URL
	if w := r.call("GET", "/orgs/"+org+"/projects/33333333-3333-4333-8333-333333333333/builds/"+id, "", "viewer", false); w.Code == 200 {
		t.Fatal("cross-project read")
	}
}

func TestCancel(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	w := r.call("POST", "/internal/builds", `{"org_id":"`+org+`","project_id":"`+proj+`"}`, "", true)
	var out struct {
		Build store.Build `json:"build"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	c := r.call("POST", "/internal/builds/"+out.Build.ID+"/cancel", "", "", true)
	if c.Code != 200 || !strings.Contains(c.Body.String(), `"canceled":true`) {
		t.Fatalf("cancel = %d %s", c.Code, c.Body.String())
	}
	if w := r.call("POST", "/internal/builds/"+out.Build.ID+"/status", `{"status":"running"}`, "", true); w.Code != 409 {
		t.Fatalf("running after cancel = %d", w.Code)
	}
}

func TestStateMachine(t *testing.T) {
	ok := [][2]string{{"queued", "running"}, {"queued", "failed"}, {"queued", "canceled"}, {"running", "succeeded"}, {"running", "failed"}, {"running", "canceled"}, {"running", "running"}}
	for _, p := range ok {
		if !store.ValidTransition(p[0], p[1]) {
			t.Errorf("%v should be valid", p)
		}
	}
	for _, p := range [][2]string{{"running", "queued"}, {"succeeded", "running"}, {"failed", "succeeded"}} {
		if store.ValidTransition(p[0], p[1]) {
			t.Errorf("%v should be invalid", p)
		}
	}
}

func TestProjectCascadeNeedsTokenAndRemovesBuilds(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	r.call("POST", "/internal/builds", `{"org_id":"`+org+`","project_id":"`+proj+`"}`, "", true)
	if w := r.call("DELETE", "/internal/projects/"+proj, "", "", false); w.Code != 403 {
		t.Fatalf("no token = %d", w.Code)
	}
	if w := r.call("DELETE", "/internal/projects/"+proj, "", "", true); w.Code != 200 || len(r.repo.m) != 0 {
		t.Fatalf("cascade = %d left=%d", w.Code, len(r.repo.m))
	}
}
