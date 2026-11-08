package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jp-cloud/deployment/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/redis/go-redis/v9"
)

const (
	org   = "11111111-1111-4111-8111-111111111111"
	proj  = "22222222-2222-4222-8222-222222222222"
	repoA = "55555555-5555-4555-8555-555555555555"
)

type memRepo struct {
	mu sync.Mutex
	m  map[string]*store.Deployment
}

func (r *memRepo) Create(_ context.Context, d *store.Deployment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	d.CreatedAt = time.Now().Add(time.Duration(len(r.m)) * time.Millisecond)
	d.CommitStatus = store.CommitStateFor(d.Status)
	c := *d
	r.m[d.ID] = &c
	return nil
}
func (r *memRepo) all(f func(*store.Deployment) bool) []store.Deployment {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []store.Deployment{}
	for _, d := range r.m {
		if f(d) {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (r *memRepo) List(_ context.Context, o, p string) ([]store.Deployment, error) {
	return r.all(func(d *store.Deployment) bool { return d.OrgID == o && d.ProjectID == p }), nil
}
func (r *memRepo) Get(_ context.Context, o, p, id string) (*store.Deployment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.m[id]; ok && d.OrgID == o && d.ProjectID == p {
		c := *d
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (r *memRepo) GetByID(_ context.Context, id string) (*store.Deployment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.m[id]; ok {
		c := *d
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (r *memRepo) RollbackCandidates(_ context.Context, o, p string, n int) ([]store.Deployment, error) {
	l := r.all(func(d *store.Deployment) bool {
		return d.OrgID == o && d.ProjectID == p && !d.IsPreview && (d.Status == "ready" || d.Status == "superseded") && d.ImageRef != ""
	})
	if len(l) > n {
		l = l[:n]
	}
	return l, nil
}
func (r *memRepo) Apply(_ context.Context, id string, p store.Patch) (*store.Deployment, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return nil, false, store.ErrNotFound
	}
	if p.Status != "" && !store.CanTransition(d.Status, p.Status) {
		c := *d
		return &c, false, store.ErrBadTransition
	}
	changed := p.Status != "" && p.Status != d.Status
	if p.Status != "" {
		d.Status = p.Status
		d.CommitStatus = store.CommitStateFor(p.Status)
	}
	if p.ImageRef != "" {
		d.ImageRef = p.ImageRef
	}
	if p.Error != "" {
		d.Error = p.Error
	}
	if p.BuildID != "" {
		b := p.BuildID
		d.BuildID = &b
	}
	if p.GitSHA != "" {
		d.GitSHA = p.GitSHA
	}
	if len(p.JPConfig) > 0 {
		d.JPConfig = p.JPConfig
	}
	if p.PreviewURL != nil {
		d.PreviewURL = *p.PreviewURL
	}
	c := *d
	return &c, changed, nil
}
func (r *memRepo) SupersedeOlder(_ context.Context, nd *store.Deployment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.m {
		if d.ProjectID == nd.ProjectID && !d.IsPreview && d.Status == "ready" && d.ID != nd.ID && d.CreatedAt.Before(nd.CreatedAt) {
			d.Status = "superseded"
		}
	}
	return nil
}
func (r *memRepo) ActivePreviews(_ context.Context, o, p string, pr int, branch string) ([]store.Deployment, error) {
	return r.all(func(d *store.Deployment) bool {
		return d.ProjectID == p && d.IsPreview && !store.IsTerminal(d.Status) && d.Status != "superseded" && ((pr > 0 && d.PRNumber == pr) || (pr == 0 && d.PRNumber == 0 && d.GitBranch == branch))
	}), nil
}
func (r *memRepo) PreviewsByPR(_ context.Context, o, p, fn string, pr int) ([]store.Deployment, error) {
	return r.all(func(d *store.Deployment) bool {
		return d.ProjectID == p && d.IsPreview && d.PRNumber == pr && !store.IsTerminal(d.Status) && d.Status != "superseded"
	}), nil
}
func (r *memRepo) ExpiredPreviews(_ context.Context, cutoff time.Time) ([]store.Deployment, error) {
	return r.all(func(d *store.Deployment) bool {
		return d.IsPreview && d.CreatedAt.Before(cutoff) && !store.IsTerminal(d.Status)
	}), nil
}
func (r *memRepo) DeleteProject(_ context.Context, p string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for id, d := range r.m {
		if d.ProjectID == p {
			delete(r.m, id)
			n++
		}
	}
	return n, nil
}
func (r *memRepo) LiveByProject(_ context.Context, p string) ([]store.Deployment, error) {
	return r.all(func(d *store.Deployment) bool { return d.ProjectID == p }), nil
}

type calls struct {
	mu   sync.Mutex
	list []string
	body map[string]map[string]any
	fail map[string]int // path substring -> status
}

func (c *calls) has(sub string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, l := range c.list {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

type rig struct {
	h    *Handler
	mux  *http.ServeMux
	repo *memRepo
	rdb  *redis.Client
	c    *calls
	tok  map[string]string
}

func setup(t *testing.T) *rig {
	t.Helper()
	roles := map[string]string{"viewer": "viewer", "member": "member"}
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if serveAccess(w, p) {
			return
		}
		if len(p) == 5 && p[2] == org && roles[p[4]] != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": roles[p[4]]})
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
	c := &calls{body: map[string]map[string]any{}, fail: map[string]int{}}
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.list = append(c.list, r.Method+" "+r.URL.Path)
		c.body[r.Method+" "+r.URL.Path] = body
		for sub, st := range c.fail {
			if strings.Contains(r.URL.Path, sub) {
				c.mu.Unlock()
				w.WriteHeader(st)
				return
			}
		}
		c.mu.Unlock()
		switch {
		case r.URL.Path == "/internal/orgs/"+org+"/projects/"+proj+"/repos":
			_, _ = w.Write([]byte(`{"repos":[{"id":"` + repoA + `","full_name":"acme/web","default_branch":"trunk"}]}`))
		case r.URL.Path == "/internal/builds":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"build":{"id":"` + uuid.NewString() + `"}}`))
		default:
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(svc.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	repo := &memRepo{m: map[string]*store.Deployment{}}
	h := &Handler{Store: repo, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL), BuildURL: svc.URL, RepositoryURL: svc.URL, RuntimeURL: svc.URL,
		DomainURL: svc.URL, RegistryURL: svc.URL, PublicBaseURL: "https://jp.example", HTTP: internalauth.NewClient(5 * time.Second), Redis: rdb}
	mux := http.NewServeMux()
	h.Routes(mux)
	tk := map[string]string{}
	for u := range map[string]bool{"viewer": true, "member": true, "stranger": true} {
		tk[u], _, _ = jm.IssueAccess(u, u+"@x", u)
	}
	return &rig{h: h, mux: mux, repo: repo, rdb: rdb, c: c, tok: tk}
}

func (r *rig) do(user, method, path, body string, internal bool) *httptest.ResponseRecorder {
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

const dbase = "/orgs/" + org + "/projects/" + proj + "/deployments"

func decodeDep(t *testing.T, w *httptest.ResponseRecorder) store.Deployment {
	var out struct {
		Deployment store.Deployment `json:"deployment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out.Deployment
}

func (r *rig) status(id, body string) int {
	return r.do("", "POST", "/internal/deployments/"+id+"/status", body, true).Code
}

func (r *rig) events(status string) int {
	msgs, _ := r.rdb.XRange(context.Background(), events.TopicDeploy, "-", "+").Result()
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Values["payload"].(string), `"status":"`+status+`"`) {
			n++
		}
	}
	return n
}

func TestCreateIgnoresClientCloneURLAndRBAC(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	body := `{"git_branch":"feat","clone_url":"https://x-access-token:SECRET@evil.example/steal.git","full_name":"evil/repo","jp_config":{"deploy":{"strategy":"blue_green"}}}`
	if w := r.do("viewer", "POST", dbase, body, false); w.Code != 403 {
		t.Fatalf("viewer deploy = %d", w.Code)
	}
	if w := r.do("stranger", "POST", dbase, body, false); w.Code != 403 {
		t.Fatalf("stranger deploy = %d", w.Code)
	}
	w := r.do("member", "POST", dbase, body, false)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	d := decodeDep(t, w)
	if d.FullName != "acme/web" || d.Strategy != "blue_green" || d.Status != "building" || d.BuildID == nil {
		t.Fatalf("deployment = %+v", d)
	}
	if strings.Contains(w.Body.String(), "evil") || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "clone_url") {
		t.Fatalf("response leaks client-supplied url: %s", w.Body.String())
	}
	bb := r.c.body["POST /internal/builds"]
	if bb["full_name"] != "acme/web" || bb["repo_id"] != repoA || bb["git_branch"] != "feat" {
		t.Fatalf("build request = %v", bb)
	}
	if _, has := bb["clone_url"]; has {
		t.Fatal("clone_url forwarded to the build queue")
	}
	// invalid inputs
	for _, b := range []string{`{"git_branch":"--upload-pack=x"}`, `{"git_sha":"zzzz"}`, `{"repo_id":"` + uuid.NewString() + `"}`, `{"jp_config":"{"}`} {
		if w := r.do("member", "POST", dbase, b, false); w.Code != 400 {
			t.Errorf("%s => %d", b, w.Code)
		}
	}
	// default branch comes from the stored repo
	w = r.do("member", "POST", dbase, `{}`, false)
	if d := decodeDep(t, w); d.GitBranch != "trunk" {
		t.Fatalf("branch = %s", d.GitBranch)
	}
	if w := r.do("viewer", "GET", dbase, "", false); w.Code != 200 {
		t.Fatalf("viewer list = %d", w.Code)
	}
	if w := r.do("member", "GET", "/orgs/"+org+"/projects/"+uuid.NewString()+"/deployments", "", false); w.Code != 404 {
		t.Fatalf("foreign project = %d", w.Code)
	}
}

func TestStateMachineIdempotencyAndSingleEvent(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	d := decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
	img := "127.0.0.1:5000/11111111/22222222:abc"
	if c := r.status(d.ID, `{"status":"deploying","image_ref":"`+img+`","git_sha":"abcdef1234","jp_config":{"deploy":{"port":4000,"healthcheck":"/h"},"resources":{"memory":"1g","cpus":2}}}`); c != 204 {
		t.Fatalf("deploying = %d", c)
	}
	for i := 0; i < 3; i++ { // retried delivery of the same transition
		if c := r.status(d.ID, `{"status":"deploying"}`); c != 204 {
			t.Fatalf("repeat = %d", c)
		}
	}
	if n := r.events("deploying"); n != 1 {
		t.Fatalf("deploying event published %d times, want exactly once", n)
	}
	msgs, _ := r.rdb.XRange(context.Background(), events.TopicDeploy, "-", "+").Result()
	payload := msgs[len(msgs)-1].Values["payload"].(string)
	if !strings.Contains(payload, `"port":4000`) || !strings.Contains(payload, `"memory_mb":1024`) || !strings.Contains(payload, `"health_path":"/h"`) {
		t.Fatalf("event spec missing: %s", payload)
	}
	if c := r.status(d.ID, `{"status":"building"}`); c != 409 {
		t.Fatalf("backwards transition = %d", c)
	}
	if c := r.status(d.ID, `{"status":"bogus"}`); c != 400 {
		t.Fatalf("unknown status = %d", c)
	}
	if c := r.status(d.ID, `{"status":"ready"}`); c != 204 {
		t.Fatalf("ready = %d", c)
	}
	if c := r.status(d.ID, `{"status":"failed"}`); c != 204 { // ready -> failed allowed (runtime lost it)
		t.Fatalf("ready->failed = %d", c)
	}
	if c := r.status(d.ID, `{"status":"ready"}`); c != 409 {
		t.Fatalf("failed->ready = %d", c)
	}
	if c := r.status(uuid.NewString(), `{"status":"ready"}`); c != 404 {
		t.Fatalf("unknown deployment = %d", c)
	}
	if r.do("", "POST", "/internal/deployments/"+d.ID+"/status", `{"status":"ready"}`, false).Code != 403 {
		t.Fatal("internal endpoint reachable without the internal token")
	}
	// commit statuses used real states
	st := r.c.body["POST /internal/github/commit-status"]
	if st == nil || st["full_name"] != "acme/web" {
		t.Fatalf("commit status body = %v", st)
	}
}

func TestCommitStatusStatesAreReal(t *testing.T) {
	for status, want := range map[string]string{"queued": "pending", "building": "pending", "deploying": "pending", "ready": "success", "failed": "failure", "canceled": "failure"} {
		if got := store.CommitStateFor(status); got != want {
			t.Errorf("%s => %s, want %s", status, got, want)
		}
	}
}

func TestRollbackStartsPreviousImageAndIsNotMarkedReadyUpFront(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	mk := func(img string) string {
		d := decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
		r.status(d.ID, `{"status":"deploying","image_ref":"`+img+`"}`)
		r.status(d.ID, `{"status":"ready"}`)
		time.Sleep(3 * time.Millisecond)
		return d.ID
	}
	first := mk("reg/a:1")
	second := mk("reg/a:2")
	if g, _ := r.repo.GetByID(context.Background(), first); g.Status != "superseded" {
		t.Fatalf("older ready deployment should be superseded, is %s", g.Status)
	}
	w := r.do("member", "POST", dbase+"/rollback", "", false)
	if w.Code != 201 {
		t.Fatalf("rollback = %d %s", w.Code, w.Body.String())
	}
	rb := decodeDep(t, w)
	if rb.ImageRef != "reg/a:1" || rb.Status != "deploying" || rb.RollbackOf == nil || *rb.RollbackOf != first || rb.Source != "rollback" {
		t.Fatalf("rollback = %+v (second=%s)", rb, second)
	}
	if n := r.events("deploying"); n < 3 {
		t.Fatalf("rollback must go through scheduler/runtime (deploying events = %d)", n)
	}
	// verified by the runtime => ready
	if c := r.status(rb.ID, `{"status":"ready"}`); c != 204 {
		t.Fatalf("ready = %d", c)
	}
	// explicit target, viewer forbidden, preview/failed targets rejected
	if r.do("viewer", "POST", dbase+"/"+first+"/rollback", "", false).Code != 403 {
		t.Fatal("viewer rollback")
	}
	bad := decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
	if r.do("member", "POST", dbase+"/"+bad.ID+"/rollback", "", false).Code != 400 {
		t.Fatal("rollback to unfinished deployment accepted")
	}
}

func TestCancelAndFromGitValidation(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	d := decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
	if r.do("viewer", "POST", dbase+"/"+d.ID+"/cancel", "", false).Code != 403 {
		t.Fatal("viewer cancel")
	}
	w := r.do("member", "POST", dbase+"/"+d.ID+"/cancel", "", false)
	if w.Code != 200 || decodeDep(t, w).Status != "canceled" || r.c.has("/cancel") != 1 {
		t.Fatalf("cancel = %d %s (build cancels=%d)", w.Code, w.Body.String(), r.c.has("/cancel"))
	}
	if r.do("member", "POST", dbase+"/"+d.ID+"/cancel", "", false).Code != 409 {
		t.Fatal("double cancel")
	}
	// a late worker report cannot resurrect a canceled deployment
	if r.status(d.ID, `{"status":"deploying"}`) != 409 {
		t.Fatal("canceled deployment resurrected")
	}
	base := `"org_id":"` + org + `","project_id":"` + proj + `","repo_id":"` + repoA + `"`
	for name, b := range map[string]string{
		"clone_url":      `{` + base + `,"git_sha":"abcdef1","git_branch":"main","clone_url":"https://evil/x"}`,
		"head sha":       `{` + base + `,"git_sha":"HEAD","git_branch":"main"}`,
		"bad branch":     `{` + base + `,"git_sha":"abcdef1","git_branch":"-x"}`,
		"foreign repo":   `{"org_id":"` + org + `","project_id":"` + proj + `","repo_id":"` + uuid.NewString() + `","git_sha":"abcdef1","git_branch":"main"}`,
		"pr w/o preview": `{` + base + `,"git_sha":"abcdef1","git_branch":"main","pr_number":3}`,
	} {
		if w := r.do("", "POST", "/internal/deployments/from-git", b, true); w.Code != 400 {
			t.Errorf("%s => %d %s", name, w.Code, w.Body.String())
		}
	}
	ok := r.do("", "POST", "/internal/deployments/from-git", `{`+base+`,"git_sha":"abcdef1234567","git_branch":"head-ref","pr_number":8,"preview":true,"source":"webhook-preview","fork":true}`, true)
	if ok.Code != 201 {
		t.Fatalf("from-git = %d %s", ok.Code, ok.Body.String())
	}
	pd := decodeDep(t, ok)
	if !pd.IsPreview || pd.PRNumber != 8 || !pd.Fork || pd.FullName != "acme/web" {
		t.Fatalf("preview deployment = %+v", pd)
	}
}

func TestPreviewTeardownIsRealAndReportsFailures(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	base := `"org_id":"` + org + `","project_id":"` + proj + `","repo_id":"` + repoA + `","git_branch":"h","preview":true,"source":"webhook-preview"`
	first := decodeDep(t, r.do("", "POST", "/internal/deployments/from-git", `{`+base+`,"git_sha":"aaaaaaa1","pr_number":4}`, true))
	r.status(first.ID, `{"status":"deploying","image_ref":"reg/p:pr4-aaaaaaa1"}`)
	r.status(first.ID, `{"status":"ready","preview_url":"https://pr-4.preview.example"}`)
	// new push to the same PR replaces (tears down) the previous preview
	second := decodeDep(t, r.do("", "POST", "/internal/deployments/from-git", `{`+base+`,"git_sha":"bbbbbbb2","pr_number":4}`, true))
	g, _ := r.repo.GetByID(context.Background(), first.ID)
	if g.Status != "stopped" {
		t.Fatalf("superseded preview status = %s", g.Status)
	}
	if r.c.has("/internal/runtime/stop-by-deployment") != 1 || r.c.has("/internal/domains/by-deployment/"+first.ID) != 1 || r.c.has("/internal/images/delete") != 1 {
		t.Fatalf("teardown did not hit runtime/domain/registry: %v", r.c.list)
	}
	if g.PreviewURL != "" {
		t.Fatal("preview url not cleared")
	}
	// PR closed => teardown of the live one; domain failure is reported, not swallowed
	r.c.mu.Lock()
	r.c.fail["/internal/domains/by-deployment"] = 500
	r.c.mu.Unlock()
	w := r.do("", "POST", "/internal/preview/teardown", `{"org_id":"`+org+`","project_id":"`+proj+`","full_name":"acme/web","pr_number":4}`, true)
	if w.Code != http.StatusMultiStatus || !strings.Contains(w.Body.String(), "preview domain removal failed") {
		t.Fatalf("teardown = %d %s", w.Code, w.Body.String())
	}
	s2, _ := r.repo.GetByID(context.Background(), second.ID)
	if !store.IsTerminal(s2.Status) {
		t.Fatalf("second preview status = %s", s2.Status)
	}
	// production deployments are never touched by preview teardown
	prod := decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
	r.do("", "POST", "/internal/preview/teardown", `{"org_id":"`+org+`","project_id":"`+proj+`","pr_number":4}`, true)
	if g, _ := r.repo.GetByID(context.Background(), prod.ID); g.Status == "stopped" || g.Status == "canceled" {
		t.Fatalf("production deployment torn down: %s", g.Status)
	}
}

func TestProjectDeleteCascade(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t)
	decodeDep(t, r.do("member", "POST", dbase, `{}`, false))
	w := r.do("", "DELETE", "/internal/projects/"+proj, "", true)
	if w.Code != 200 || len(r.repo.m) != 0 || r.c.has("/cancel") != 1 {
		t.Fatalf("cascade = %d %s left=%d", w.Code, w.Body.String(), len(r.repo.m))
	}
}

func TestSpecFromConfig(t *testing.T) {
	spec := SpecFromConfig(json.RawMessage(`{"env":{"A":"1"},"deploy":{"port":3000,"healthcheck":"/up","replicas":2,"restart":"always","env":{"B":2}},"resources":{"memory":"256MB","cpus":"0.5"}}`))
	b, _ := json.Marshal(spec)
	for _, want := range []string{`"port":3000`, `"health_path":"/up"`, `"replicas":2`, `"restart_policy":"always"`, `"memory_mb":256`, `"cpus":0.5`, `"A":"1"`, `"B":"2"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if len(SpecFromConfig(json.RawMessage(`not json`))) != 0 {
		t.Error("garbage must yield empty spec")
	}
	for in, want := range map[any]int{"512": 512, "1g": 1024, "2GiB": 2048, "128Mi": 128, "bad": 0, float64(300): 300} {
		if got := parseMemoryMB(in); got != want {
			t.Errorf("parseMemoryMB(%v) = %d want %d", in, got, want)
		}
	}
}

func TestNormalizeStrategy(t *testing.T) {
	for in, want := range map[string]string{"blue_green": "blue_green", "BLUE-GREEN": "blue_green", "bluegreen": "blue_green", "rolling": "rolling", "": "rolling", "x": "rolling"} {
		if got := normalizeStrategy(in); got != want {
			t.Errorf("%q => %q", in, got)
		}
	}
}

func TestTransitionTable(t *testing.T) {
	ok := [][2]string{{"queued", "building"}, {"building", "deploying"}, {"deploying", "ready"}, {"ready", "superseded"}, {"ready", "stopped"}, {"queued", "canceled"}, {"deploying", "failed"}, {"ready", "ready"}}
	bad := [][2]string{{"ready", "building"}, {"failed", "ready"}, {"canceled", "deploying"}, {"queued", "ready"}, {"stopped", "ready"}, {"building", "ready"}}
	for _, p := range ok {
		if !store.CanTransition(p[0], p[1]) {
			t.Errorf("%v should be allowed", p)
		}
	}
	for _, p := range bad {
		if store.CanTransition(p[0], p[1]) {
			t.Errorf("%v should be rejected", p)
		}
	}
}
