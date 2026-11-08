package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/repository/internal/cd"
	"github.com/jp-cloud/repository/internal/githubapp"
	"github.com/jp-cloud/repository/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	orgA  = "11111111-1111-4111-8111-111111111111"
	orgB  = "66666666-6666-4666-8666-666666666666"
	projA = "22222222-2222-4222-8222-222222222222"
	projB = "77777777-7777-4777-8777-777777777777"
)

type conn struct {
	store.Repo
	token, secret string
}

type memRepo struct {
	mu    sync.Mutex
	repos []*conn
	inst  map[string]string // installation id -> org
	keys  map[string]string // project -> private
	pubs  map[string]string
}

func newMem() *memRepo {
	return &memRepo{inst: map[string]string{}, keys: map[string]string{}, pubs: map[string]string{}}
}

func (m *memRepo) ListInstallations(_ context.Context, org string) ([]store.Installation, error) {
	out := []store.Installation{}
	for id, o := range m.inst {
		if o == org {
			out = append(out, store.Installation{InstallationID: id, OrgID: o})
		}
	}
	return out, nil
}
func (m *memRepo) HasInstallation(_ context.Context, org, id string) (bool, error) {
	return m.inst[id] == org, nil
}
func (m *memRepo) UpsertInstallation(_ context.Context, org, id, login string) (*store.Installation, error) {
	if o, ok := m.inst[id]; ok && o != org {
		return nil, store.ErrInstallationOwned
	}
	m.inst[id] = org
	return &store.Installation{InstallationID: id, OrgID: org, AccountLogin: login}, nil
}
func (m *memRepo) LatestInstallation(_ context.Context, org string) (*store.Installation, error) {
	for id, o := range m.inst {
		if o == org {
			return &store.Installation{InstallationID: id, OrgID: o}, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memRepo) ConnectRepo(_ context.Context, p store.ConnectParams) (*store.Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.repos {
		if c.OrgID == p.OrgID && c.ProjectID == p.ProjectID && c.FullName == p.FullName {
			return nil, store.ErrAlreadyExists
		}
	}
	r := store.Repo{ID: uuid.NewString(), OrgID: p.OrgID, ProjectID: p.ProjectID, FullName: p.FullName, CloneURL: p.CloneURL,
		DefaultBranch: p.Branch, AuthType: p.AuthType, AllowForks: p.AllowForks,
		RootDir: p.CD.RootDir, AutoDeploy: p.CD.AutoDeploy, DeployBranch: p.CD.DeployBranch, BranchPreviews: p.CD.BranchPreviews, PRPreviews: p.CD.PRPreviews,
		WatchPaths: p.CD.WatchPaths, IgnorePaths: p.CD.IgnorePaths, RequireCI: p.CD.RequireCI}
	if p.InstallationID != "" {
		i := p.InstallationID
		r.InstallationID = &i
	}
	m.repos = append(m.repos, &conn{Repo: r, token: p.Token, secret: p.WebhookSecret})
	c := r
	return &c, nil
}
func (m *memRepo) ListRepos(_ context.Context, org, proj string) ([]store.Repo, error) {
	out := []store.Repo{}
	for _, c := range m.repos {
		if c.OrgID == org && c.ProjectID == proj {
			out = append(out, c.Repo)
		}
	}
	return out, nil
}
func (m *memRepo) GetRepo(_ context.Context, org, proj, id string) (*store.Repo, error) {
	for _, c := range m.repos {
		if c.ID == id && c.OrgID == org && c.ProjectID == proj {
			r := c.Repo
			return &r, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memRepo) UpdateCD(_ context.Context, org, proj, id string, c cd.Settings) (*store.Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.repos {
		if x.ID == id && x.OrgID == org && x.ProjectID == proj {
			x.RootDir, x.AutoDeploy, x.DeployBranch, x.BranchPreviews, x.PRPreviews = c.RootDir, c.AutoDeploy, c.DeployBranch, c.BranchPreviews, c.PRPreviews
			x.WatchPaths, x.IgnorePaths, x.RequireCI = c.WatchPaths, c.IgnorePaths, c.RequireCI
			r := x.Repo
			return &r, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memRepo) SetWebhookSecret(_ context.Context, org, proj, id, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.repos {
		if x.ID == id && x.OrgID == org && x.ProjectID == proj {
			x.secret = secret
			return nil
		}
	}
	return store.ErrNotFound
}
func (m *memRepo) WebhookSecretOf(_ context.Context, org, proj, id string) (string, error) {
	for _, x := range m.repos {
		if x.ID == id && x.OrgID == org && x.ProjectID == proj {
			return x.secret, nil
		}
	}
	return "", store.ErrNotFound
}
func (m *memRepo) DeleteRepo(_ context.Context, org, proj, id string) error {
	for i, c := range m.repos {
		if c.ID == id && c.OrgID == org && c.ProjectID == proj {
			m.repos = append(m.repos[:i], m.repos[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}
func (m *memRepo) FindForWebhook(_ context.Context, full string) ([]store.WebhookRepo, error) {
	var out []store.WebhookRepo
	for _, c := range m.repos {
		if strings.EqualFold(c.FullName, full) {
			out = append(out, store.WebhookRepo{Repo: c.Repo, WebhookSecret: c.secret})
		}
	}
	return out, nil
}
func (m *memRepo) Credentials(_ context.Context, id string) (*store.Repo, string, error) {
	for _, c := range m.repos {
		if c.ID == id {
			r := c.Repo
			return &r, c.token, nil
		}
	}
	return nil, "", store.ErrNotFound
}
func (m *memRepo) InstallationForRepo(_ context.Context, full string) (string, error) {
	for _, c := range m.repos {
		if c.InstallationID != nil && strings.EqualFold(c.FullName, full) {
			return *c.InstallationID, nil
		}
	}
	return "", store.ErrNotFound
}
func (m *memRepo) DeleteProject(_ context.Context, proj string) (int64, error) {
	var n int64
	kept := m.repos[:0]
	for _, c := range m.repos {
		if c.ProjectID == proj {
			n++
		} else {
			kept = append(kept, c)
		}
	}
	m.repos = kept
	return n, nil
}
func (m *memRepo) GetDeployKey(_ context.Context, proj string) (*store.DeployKey, error) {
	if pub, ok := m.pubs[proj]; ok {
		return &store.DeployKey{ProjectID: proj, PublicKey: pub, KeyType: "ed25519"}, nil
	}
	return nil, store.ErrNotFound
}
func (m *memRepo) GetOrCreateDeployKey(ctx context.Context, org, proj string) (*store.DeployKey, error) {
	if _, ok := m.pubs[proj]; !ok {
		m.pubs[proj], m.keys[proj] = "ssh-ed25519 AAAApub jp", "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRETKEY\n-----END OPENSSH PRIVATE KEY-----"
	}
	return m.GetDeployKey(ctx, proj)
}
func (m *memRepo) RotateDeployKey(ctx context.Context, org, proj string) (*store.DeployKey, error) {
	m.pubs[proj], m.keys[proj] = "ssh-ed25519 AAAAnew jp", "-----BEGIN OPENSSH PRIVATE KEY-----\nNEWSECRET\n-----END OPENSSH PRIVATE KEY-----"
	return m.GetDeployKey(ctx, proj)
}
func (m *memRepo) DeployKeyPrivate(_ context.Context, proj string) (string, error) {
	if k, ok := m.keys[proj]; ok {
		return k, nil
	}
	return "", store.ErrNotFound
}

type rig struct {
	h        *Handler
	mux      *http.ServeMux
	repo     *memRepo
	rdb      *redis.Client
	tok      map[string]string
	mu       sync.Mutex
	deploys  []map[string]any
	teardown []map[string]any
	ghRepos  map[string]ghRepoInfo // fake GitHub: repo -> info (requires token "good-token" unless public)
	ghHooks  []map[string]any      // fake GitHub: webhooks created through POST/PATCH (body + method)
	ghHookIn string                // fake GitHub: JSON array answered to GET .../hooks
	ghTree   map[string]string     // fake GitHub: "owner/repo/contents/<dir>" -> JSON answered to GET (token "good-token" required)
	ghDeny   bool                  // fake GitHub: answer 403 to hook calls
	ghNoRef  string                // fake GitHub: answer 404 to contents requests for this ref (a branch that does not exist)
	instOK   map[string]string     // fake GitHub App: installation id -> login
}

func setup(t *testing.T, withApp bool) *rig {
	t.Helper()
	r := &rig{repo: newMem(), tok: map[string]string{}, ghRepos: map[string]ghRepoInfo{}, instOK: map[string]string{}}
	roles := map[string]string{"viewer": "viewer", "member": "member", "admin": "admin", "restricted": "member"}
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		if serveAccess(w, p) {
			return
		}
		if len(p) == 5 && p[2] == orgA && roles[p[4]] != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": roles[p[4]]})
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(orgSrv.Close)
	projSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/internal/orgs/"+orgA+"/projects/"+projA {
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(projSrv.Close)
	dep := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		defer r.mu.Unlock()
		switch req.URL.Path {
		case "/internal/deployments/from-git":
			r.deploys = append(r.deploys, body)
			w.WriteHeader(201)
		case "/internal/preview/teardown":
			r.teardown = append(r.teardown, body)
			w.WriteHeader(200)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(dep.Close)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/repos/") && strings.Contains(req.URL.Path, "/hooks"):
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.ghDeny || req.Header.Get("Authorization") != "Bearer good-token" {
				w.WriteHeader(403)
				return
			}
			if req.Method == http.MethodGet {
				_, _ = w.Write([]byte(firstNonEmpty(r.ghHookIn, "[]")))
				return
			}
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			body["_method"], body["_path"] = req.Method, req.URL.Path
			r.ghHooks = append(r.ghHooks, body)
			if req.Method == http.MethodPost {
				w.WriteHeader(201)
			}
			_, _ = w.Write([]byte(`{"id":4711}`))
		case strings.HasPrefix(req.URL.Path, "/repos/") && strings.Contains(req.URL.Path, "/contents"):
			if req.Header.Get("Authorization") != "Bearer good-token" {
				w.WriteHeader(404)
				return
			}
			if r.ghNoRef != "" && req.URL.Query().Get("ref") == r.ghNoRef {
				w.WriteHeader(404)
				return
			}
			key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/repos/"), "/")
			out, ok := r.ghTree[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(out))
		case strings.HasPrefix(req.URL.Path, "/repos/"):
			full := strings.TrimPrefix(req.URL.Path, "/repos/")
			info, ok := r.ghRepos[full]
			if !ok || (info.Private && req.Header.Get("Authorization") != "Bearer good-token") {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(info)
		case strings.HasPrefix(req.URL.Path, "/app/installations/") && !strings.HasSuffix(req.URL.Path, "/access_tokens"):
			id := strings.TrimPrefix(req.URL.Path, "/app/installations/")
			login, ok := r.instOK[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]string{"login": login}})
		case strings.HasSuffix(req.URL.Path, "/access_tokens"):
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation_token_value", "expires_at": time.Now().Add(time.Hour)})
		case req.URL.Path == "/installation/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"acme/app","default_branch":"main","private":true}]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(gh.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	r.rdb = rdb
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	r.h = &Handler{Store: r.repo, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL), DeploymentURL: dep.URL, GlobalWebhookSecret: "global-app-secret",
		PublicBaseURL: "https://jp.example", DashboardURL: "https://dash.example", GitHubAPI: gh.URL, HTTP: gh.Client(), Redis: rdb}
	if withApp {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		pemData := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
		c, err := githubapp.New(1, "jp-test", pemData, gh.Client())
		if err != nil {
			t.Fatal(err)
		}
		c.APIBase = gh.URL
		r.h.GitHub = c
	}
	r.mux = http.NewServeMux()
	r.h.Routes(r.mux)
	for u := range roles {
		r.tok[u], _, _ = jm.IssueAccess(u, u+"@x", u)
	}
	r.tok["stranger"], _, _ = jm.IssueAccess("stranger", "s@x", "s")
	return r
}

func (r *rig) do(user, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+r.tok[user])
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (r *rig) hook(event, secret, delivery string, payload any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(payload)
	hdr := []string{"X-GitHub-Event", event, "X-Hub-Signature-256", sign(secret, b)}
	if delivery != "" {
		hdr = append(hdr, "X-GitHub-Delivery", delivery)
	}
	return r.do("", "POST", "/webhooks/github", string(b), hdr...)
}

func (r *rig) connect(t *testing.T, org, proj, full, secret string, forks bool, inst string) *store.Repo {
	p := store.ConnectParams{OrgID: org, ProjectID: proj, FullName: full, CloneURL: "https://github.com/" + full + ".git", Branch: "main", AuthType: "none",
		WebhookSecret: secret, AllowForks: forks, InstallationID: inst, CD: cd.Defaults()}
	rp, err := r.repo.ConnectRepo(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

func delivery() string { return uuid.NewString() }

func TestWebhookIsAuthenticatedPerConnectionAndScoped(t *testing.T) {
	r := setup(t, false)
	a := r.connect(t, orgA, projA, "acme/web", "secret-of-A", false, "")
	r.connect(t, orgB, projB, "acme/web", "secret-of-B", false, "")
	push := map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("a", 40), "repository": map[string]any{"full_name": "acme/web", "default_branch": "main"},
		"head_commit": map[string]any{"id": strings.Repeat("a", 40), "message": "m"}}

	if w := r.hook("push", "wrong-secret", delivery(), push); w.Code != 401 {
		t.Fatalf("bad signature = %d", w.Code)
	}
	if w := r.hook("push", "secret-of-A", "", push); w.Code != 400 {
		t.Fatalf("missing delivery id = %d", w.Code)
	}
	w := r.hook("push", "secret-of-A", delivery(), push)
	if w.Code != 200 || len(r.deploys) != 1 {
		t.Fatalf("push = %d %s deploys=%d", w.Code, w.Body.String(), len(r.deploys))
	}
	d := r.deploys[0]
	if d["org_id"] != orgA || d["project_id"] != projA || d["repo_id"] != a.ID || d["preview"] != false || d["git_branch"] != "main" {
		t.Fatalf("deploy = %v (must only target the connection whose secret signed the event)", d)
	}
	if _, has := d["clone_url"]; has {
		t.Fatal("clone_url must not be sent")
	}
	// unknown repository: indistinguishable 401
	other := map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("a", 40), "repository": map[string]any{"full_name": "nobody/nothing"}}
	if w := r.hook("push", "secret-of-A", delivery(), other); w.Code != 401 {
		t.Fatalf("unknown repo = %d", w.Code)
	}
	// oversized body
	big := bytes.Repeat([]byte("x"), maxWebhookBody+10)
	if w := r.do("", "POST", "/webhooks/github", string(big), "X-GitHub-Event", "push"); w.Code != 413 {
		t.Fatalf("oversized = %d", w.Code)
	}
}

func TestWebhookReplayIsRejected(t *testing.T) {
	r := setup(t, false)
	r.connect(t, orgA, projA, "acme/web", "s1", false, "")
	push := map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("b", 40), "repository": map[string]any{"full_name": "acme/web"}}
	id := delivery()
	if w := r.hook("push", "s1", id, push); w.Code != 200 || len(r.deploys) != 1 {
		t.Fatal("first delivery failed")
	}
	w := r.hook("push", "s1", id, push)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) || len(r.deploys) != 1 {
		t.Fatalf("replay = %d %s deploys=%d", w.Code, w.Body.String(), len(r.deploys))
	}
	// a forged request cannot burn a legit delivery id
	id2 := delivery()
	if r.hook("push", "bad", id2, push).Code != 401 {
		t.Fatal("forged accepted")
	}
	if w := r.hook("push", "s1", id2, push); w.Code != 200 || len(r.deploys) != 2 {
		t.Fatalf("legit delivery after forged attempt = %d deploys=%d", w.Code, len(r.deploys))
	}
}

func TestPushToNonDefaultBranchIsPreview(t *testing.T) {
	r := setup(t, false)
	r.connect(t, orgA, projA, "acme/web", "s1", false, "")
	r.hook("push", "s1", delivery(), map[string]any{"ref": "refs/heads/feature/x", "after": strings.Repeat("c", 40), "repository": map[string]any{"full_name": "acme/web"}})
	if len(r.deploys) != 1 || r.deploys[0]["preview"] != true || r.deploys[0]["git_branch"] != "feature/x" {
		t.Fatalf("deploys = %v", r.deploys)
	}
	// tags, deletions and hostile refs are ignored
	for _, ref := range []string{"refs/tags/v1", "refs/heads/--upload-pack=x", "refs/heads/a..b"} {
		r.hook("push", "s1", delivery(), map[string]any{"ref": ref, "after": strings.Repeat("c", 40), "repository": map[string]any{"full_name": "acme/web"}})
	}
	r.hook("push", "s1", delivery(), map[string]any{"ref": "refs/heads/gone", "deleted": true, "after": strings.Repeat("0", 40), "repository": map[string]any{"full_name": "acme/web"}})
	if len(r.deploys) != 1 {
		t.Fatalf("ignored refs created deployments: %v", r.deploys)
	}
}

func prPayload(action string, n int, headRepo string, sha string) map[string]any {
	head := map[string]any{"sha": sha, "ref": "topic"}
	if headRepo != "" {
		head["repo"] = map[string]any{"full_name": headRepo}
	}
	return map[string]any{"action": action, "number": n, "pull_request": map[string]any{"title": "T", "head": head}, "repository": map[string]any{"full_name": "acme/web"}}
}

func TestPullRequestPreviewForksAndTeardown(t *testing.T) {
	r := setup(t, false)
	r.connect(t, orgA, projA, "acme/web", "s1", false, "")
	sha := strings.Repeat("d", 40)
	// same-repo PR => preview of the PR head
	if w := r.hook("pull_request", "s1", delivery(), prPayload("opened", 7, "acme/web", sha)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	d := r.deploys[0]
	if d["git_sha"] != sha || d["pr_number"] != float64(7) || d["preview"] != true || d["fork"] != false {
		t.Fatalf("deploy = %v", d)
	}
	// fork PR is ignored by default
	if w := r.hook("pull_request", "s1", delivery(), prPayload("synchronize", 8, "evil/web", sha)); !strings.Contains(w.Body.String(), `"skipped_fork":1`) || len(r.deploys) != 1 {
		t.Fatalf("fork PR was built: %s", w.Body.String())
	}
	// deleted fork (head.repo == null) is a fork too
	r.hook("pull_request", "s1", delivery(), prPayload("opened", 9, "", sha))
	if len(r.deploys) != 1 {
		t.Fatal("deleted-fork PR was built")
	}
	// allow_forks => built, flagged fork=true so the runtime withholds secrets
	r2 := setup(t, false)
	r2.connect(t, orgA, projA, "acme/web", "s1", true, "")
	r2.hook("pull_request", "s1", delivery(), prPayload("opened", 8, "evil/web", sha))
	if len(r2.deploys) != 1 || r2.deploys[0]["fork"] != true {
		t.Fatalf("allowed fork deploy = %v", r2.deploys)
	}
	// closed => teardown by PR number
	r.hook("pull_request", "s1", delivery(), prPayload("closed", 7, "acme/web", sha))
	if len(r.teardown) != 1 || r.teardown[0]["pr_number"] != float64(7) || r.teardown[0]["project_id"] != projA {
		t.Fatalf("teardown = %v", r.teardown)
	}
	// ignored actions and invalid SHAs
	r.hook("pull_request", "s1", delivery(), prPayload("labeled", 7, "acme/web", sha))
	r.hook("pull_request", "s1", delivery(), prPayload("opened", 10, "acme/web", "not-a-sha"))
	if len(r.deploys) != 1 {
		t.Fatalf("deploys = %d", len(r.deploys))
	}
}

func TestGlobalAppSecretOnlyReachesOwnInstallation(t *testing.T) {
	r := setup(t, false)
	r.connect(t, orgA, projA, "acme/web", "s-A", false, "111")
	r.connect(t, orgB, projB, "acme/web", "s-B", false, "222")
	push := map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("e", 40), "installation": map[string]any{"id": 111}, "repository": map[string]any{"full_name": "acme/web"}}
	if w := r.hook("push", "global-app-secret", delivery(), push); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(r.deploys) != 1 || r.deploys[0]["org_id"] != orgA {
		t.Fatalf("deploys = %v (installation 111 must only reach org A)", r.deploys)
	}
	// app-level secret + installation matching nobody => rejected
	push["installation"] = map[string]any{"id": 999}
	if w := r.hook("push", "global-app-secret", delivery(), push); w.Code != 401 {
		t.Fatalf("foreign installation = %d", w.Code)
	}
}

func TestInstallFlowStateIsMandatorySingleUseAndBound(t *testing.T) {
	r := setup(t, true)
	r.instOK["4242"] = "acme-org"
	start := func(user string) string {
		w := r.do(user, "POST", "/orgs/"+orgA+"/github/install/start", "")
		if w.Code != 200 {
			t.Fatalf("start = %d %s", w.Code, w.Body.String())
		}
		var out struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.State
	}
	cb := func(user, body string) *httptest.ResponseRecorder {
		return r.do(user, "POST", "/orgs/"+orgA+"/github/install/callback", body)
	}
	if r.do("member", "POST", "/orgs/"+orgA+"/github/install/start", "").Code != 403 {
		t.Fatal("member started an install")
	}
	// no state / unknown state
	if cb("admin", `{"installation_id":"4242"}`).Code != 400 || cb("admin", `{"installation_id":"4242","state":"`+strings.Repeat("a", 48)+`"}`).Code != 400 {
		t.Fatal("callback without a valid state accepted")
	}
	// state bound to the user who started it
	s := start("admin")
	if cb("member", `{"installation_id":"4242","state":"`+s+`"}`).Code != 403 { // member lacks admin
		t.Fatal("member completed install")
	}
	// the state was NOT consumed by the rejected (unauthorised) attempt? it is consumed only after authz
	if w := cb("admin", `{"installation_id":"4242","state":"`+s+`"}`); w.Code != 201 {
		t.Fatalf("legit callback = %d %s", w.Code, w.Body.String())
	}
	// single use
	if cb("admin", `{"installation_id":"4242","state":"`+s+`"}`).Code != 400 {
		t.Fatal("state reused")
	}
	// installation that GitHub does not know => rejected
	s2 := start("admin")
	if w := cb("admin", `{"installation_id":"31337","state":"`+s2+`"}`); w.Code != 400 {
		t.Fatalf("unverifiable installation = %d", w.Code)
	}
	// non numeric id never reaches GitHub
	s3 := start("admin")
	if w := cb("admin", `{"installation_id":"../../x","state":"`+s3+`"}`); w.Code != 400 {
		t.Fatalf("hostile installation id = %d", w.Code)
	}
	// installation already owned by another org => 409 (IDOR)
	r.repo.inst["777"] = orgB
	r.instOK["777"] = "victim"
	s4 := start("admin")
	if w := cb("admin", `{"installation_id":"777","state":"`+s4+`"}`); w.Code != 409 {
		t.Fatalf("stealing another org's installation = %d", w.Code)
	}
	// GitHub setup redirect: needs the state too
	s5 := start("admin")
	w := r.do("", "GET", "/github/setup?installation_id=4242&state="+s5, "")
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "installed=1") {
		t.Fatalf("setup redirect = %d %s", w.Code, w.Header().Get("Location"))
	}
	w = r.do("", "GET", "/github/setup?installation_id=4242&state="+s5, "")
	if !strings.Contains(w.Header().Get("Location"), "error=invalid_state") {
		t.Fatalf("setup state reuse -> %s", w.Header().Get("Location"))
	}
	w = r.do("", "GET", "/github/setup?installation_id=4242", "")
	if !strings.Contains(w.Header().Get("Location"), "error=invalid_state") {
		t.Fatalf("setup without state -> %s", w.Header().Get("Location"))
	}
}

func TestNoStubInstallUnlessExplicit(t *testing.T) {
	r := setup(t, false)
	if w := r.do("admin", "POST", "/orgs/"+orgA+"/github/install/start", ""); w.Code != http.StatusNotImplemented {
		t.Fatalf("install without app and without GITHUB_MODE=stub = %d", w.Code)
	}
	r.h.StubMode = true
	w := r.do("admin", "POST", "/orgs/"+orgA+"/github/install/start", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"stub"`) {
		t.Fatalf("explicit stub = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if c := r.do("admin", "POST", "/orgs/"+orgA+"/github/install/callback", `{"state":"`+out.State+`"}`); c.Code != 201 || !strings.Contains(c.Body.String(), "stub-") {
		t.Fatalf("stub callback = %d %s", c.Code, c.Body.String())
	}
}

func TestConnectRepoRBACTokenHandlingAndNoLeaks(t *testing.T) {
	r := setup(t, false)
	r.ghRepos["acme/private"] = ghRepoInfo{DefaultBranch: "develop", Private: true}
	r.ghRepos["acme/public"] = ghRepoInfo{DefaultBranch: "main"}
	base := "/orgs/" + orgA + "/projects/" + projA
	body := `{"full_name":"acme/private","token":"good-token","clone_url":"https://evil.example/steal.git"}`
	if r.do("member", "POST", base+"/repos", body).Code != 403 || r.do("viewer", "POST", base+"/repos", body).Code != 403 {
		t.Fatal("non-admin connected a repo")
	}
	if r.do("admin", "POST", base+"/repos", `{"full_name":"acme/private","token":"wrong"}`).Code != 400 {
		t.Fatal("unverified token accepted")
	}
	w := r.do("admin", "POST", base+"/repos", body)
	if w.Code != 201 {
		t.Fatalf("connect = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "good-token") || strings.Contains(w.Body.String(), "evil.example") {
		t.Fatalf("response leaks: %s", w.Body.String())
	}
	var out struct {
		Repo          store.Repo `json:"repo"`
		WebhookSecret string     `json:"webhook_secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Repo.CloneURL != "https://github.com/acme/private.git" || out.Repo.DefaultBranch != "develop" || out.Repo.AuthType != "token" || len(out.WebhookSecret) != 64 {
		t.Fatalf("repo = %+v secret=%q", out.Repo, out.WebhookSecret)
	}
	for _, c := range r.repo.repos {
		if strings.Contains(c.CloneURL, "good-token") || strings.Contains(c.CloneURL, "@") {
			t.Fatal("token stored in clone_url")
		}
	}
	// listings never show credentials or webhook secrets
	l := r.do("viewer", "GET", base+"/repos", "")
	if l.Code != 200 || strings.Contains(l.Body.String(), "good-token") || strings.Contains(l.Body.String(), out.WebhookSecret) || strings.Contains(l.Body.String(), "\"token\":") {
		t.Fatalf("list leaks: %s", l.Body.String())
	}
	if r.do("admin", "POST", base+"/repos", body).Code != 409 {
		t.Fatal("duplicate connection accepted")
	}
	// the same repository may be connected to another project
	r.repo.repos = append(r.repo.repos[:0:0], r.repo.repos...)
	r.connect(t, orgB, projB, "acme/private", "x", false, "")
	// public repo without credentials; private without credentials rejected; foreign hosts rejected
	if r.do("admin", "POST", base+"/repos", `{"full_name":"https://github.com/acme/public"}`).Code != 201 {
		t.Fatal("public repo")
	}
	if r.do("admin", "POST", base+"/repos", `{"full_name":"acme/private"}`).Code != 400 {
		t.Fatal("private repo without credentials accepted")
	}
	for _, bad := range []string{"https://evil.example/acme/x", "https://user:pw@github.com/a/b", "a/b/c", "../x/y", "a/b;rm", "-x/y"} {
		if r.do("admin", "POST", base+"/repos", `{"full_name":"`+bad+`"}`).Code != 400 {
			t.Errorf("%q accepted", bad)
		}
	}
	// disconnect: admin only
	if r.do("member", "DELETE", base+"/repos/"+out.Repo.ID, "").Code != 403 {
		t.Fatal("member disconnected")
	}
	if r.do("admin", "DELETE", base+"/repos/"+out.Repo.ID, "").Code != 204 {
		t.Fatal("admin disconnect")
	}
}

func TestDeployKeyPublicOnlyAndInternalCloneInfo(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t, false)
	base := "/orgs/" + orgA + "/projects/" + projA
	if r.do("restricted", "GET", base+"/deploy-key", "").Code != 403 {
		t.Fatal("restricted user reached the deploy key")
	}
	if r.do("viewer", "GET", base+"/deploy-key", "").Code != 404 {
		t.Fatal("viewer must not generate a key")
	}
	if r.do("member", "GET", base+"/deploy-key", "").Code != 404 {
		t.Fatal("member must not generate keys")
	}
	w := r.do("admin", "GET", base+"/deploy-key", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ssh-ed25519") || strings.Contains(strings.ToLower(w.Body.String()), "private") || strings.Contains(w.Body.String(), "SECRETKEY") {
		t.Fatalf("deploy key response: %d %s", w.Code, w.Body.String())
	}
	if r.do("member", "POST", base+"/deploy-key/generate", "").Code != 403 {
		t.Fatal("member rotated key")
	}
	if w := r.do("admin", "POST", base+"/deploy-key/generate", ""); w.Code != 200 || strings.Contains(w.Body.String(), "NEWSECRET") {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}

	tokRepo, _ := r.repo.ConnectRepo(context.Background(), store.ConnectParams{OrgID: orgA, ProjectID: projA, FullName: "acme/t", CloneURL: "https://github.com/acme/t.git", Branch: "main", AuthType: "token", Token: "tok-123456789", WebhookSecret: "w"})
	keyRepo, _ := r.repo.ConnectRepo(context.Background(), store.ConnectParams{OrgID: orgA, ProjectID: projA, FullName: "acme/k", CloneURL: "git@github.com:acme/k.git", Branch: "main", AuthType: "deploy_key", WebhookSecret: "w"})
	internal := func(path string, token bool) *httptest.ResponseRecorder {
		if token {
			return r.do("", "GET", path, "", "X-Internal-Token", "int")
		}
		return r.do("admin", "GET", path, "")
	}
	if internal("/internal/repos/"+tokRepo.ID+"/clone-info", false).Code != 403 {
		t.Fatal("clone-info reachable with a user JWT")
	}
	w = internal("/internal/repos/"+tokRepo.ID+"/clone-info?org_id="+orgA+"&project_id="+projA, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token":"tok-123456789"`) || strings.Contains(w.Body.String(), "@github.com") {
		t.Fatalf("token clone-info: %d %s", w.Code, w.Body.String())
	}
	w = internal("/internal/repos/"+keyRepo.ID+"/clone-info", true)
	if !strings.Contains(w.Body.String(), "private_key") || !strings.Contains(w.Body.String(), "git@github.com:acme/k.git") {
		t.Fatalf("deploy key clone-info: %s", w.Body.String())
	}
	if internal("/internal/repos/"+tokRepo.ID+"/clone-info?org_id="+orgB, true).Code != 404 {
		t.Fatal("clone-info ignores org scope")
	}
	if internal("/internal/projects/"+projA+"/deploy-key", false).Code != 403 {
		t.Fatal("internal private-key endpoint reachable without internal token")
	}
	// repo list for the deployment service has no credentials
	w = internal("/internal/orgs/"+orgA+"/projects/"+projA+"/repos", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "tok-123") || strings.Contains(w.Body.String(), "clone_url") {
		t.Fatalf("internal repos: %s", w.Body.String())
	}
	// cascade
	req := r.do("", "DELETE", "/internal/projects/"+projA, "", "X-Internal-Token", "int")
	if req.Code != 200 || len(r.repo.repos) != 0 {
		t.Fatalf("cascade: %d", req.Code)
	}
}

func TestParseFullName(t *testing.T) {
	good := map[string]string{"acme/web": "acme/web", "https://github.com/acme/web.git": "acme/web", "git@github.com:acme/web.git": "acme/web", "ssh://git@github.com/acme/web": "acme/web", "https://github.com/acme/web/": "acme/web"}
	for in, want := range good {
		if got, err := parseFullName(in); err != nil || got != want {
			t.Errorf("%q => %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "a", "https://gitlab.com/a/b", "http://github.com.evil.com/a/b", "a/b/c", "a/..", "a b/c"} {
		if _, err := parseFullName(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestSignatureVerification(t *testing.T) {
	b := []byte(`{"a":1}`)
	if !verifySignature("s", b, sign("s", b)) {
		t.Fatal("valid signature rejected")
	}
	for _, h := range []string{"", "sha256=", "sha256=zz", "sha1=" + hex.EncodeToString(make([]byte, 20)), sign("other", b)} {
		if verifySignature("s", b, h) {
			t.Errorf("%q accepted", h)
		}
	}
	if verifySignature("", b, sign("", b)) {
		t.Fatal("empty secret must never verify")
	}
}
