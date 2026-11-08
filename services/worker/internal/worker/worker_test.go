package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/worker/internal/gitx"
	"github.com/jp-cloud/worker/internal/redact"
	"github.com/jp-cloud/worker/internal/scan"
	"github.com/redis/go-redis/v9"
)

const (
	org   = "11111111-1111-4111-8111-111111111111"
	proj  = "22222222-2222-4222-8222-222222222222"
	build = "33333333-3333-4333-8333-333333333333"
	dep   = "44444444-4444-4444-8444-444444444444"
	repo  = "55555555-5555-4555-8555-555555555555"
	tok   = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func makeRepo(t *testing.T, files map[string]string) (bare, sha string) {
	root := t.TempDir()
	work := filepath.Join(root, "w")
	_ = os.MkdirAll(work, 0o755)
	git(t, work, "init", "-q", "-b", "main")
	for n, c := range files {
		p := filepath.Join(work, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(c), 0o644)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "init")
	sha = git(t, work, "rev-parse", "HEAD")
	bare = filepath.Join(root, "r.git")
	git(t, root, "clone", "-q", "--bare", work, bare)
	return
}

type fakeDocker struct {
	mu       sync.Mutex
	builds   []string
	pushes   []string
	buildErr error
	pushErr  error
	args     map[string]string
	files    []string
	ctxDirs  []string
	dfPaths  []string
}

func (f *fakeDocker) Available(context.Context) error { return nil }
func (f *fakeDocker) Build(_ context.Context, ref, df, ctxDir string, args map[string]string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds = append(f.builds, ref)
	f.ctxDirs = append(f.ctxDirs, ctxDir)
	f.dfPaths = append(f.dfPaths, df)
	f.args = args
	if b, err := os.ReadFile(df); err == nil {
		f.files = append(f.files, string(b))
	}
	_, _ = io.WriteString(out, "Step 1/3 : FROM x\nusing token "+tok+" in log\n")
	return f.buildErr
}
func (f *fakeDocker) Push(_ context.Context, ref string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, ref)
	return f.pushErr
}
func (f *fakeDocker) Remove(context.Context, string) {}

type services struct {
	mu          sync.Mutex
	buildStatus string
	buildPatch  []map[string]any
	depPatch    []map[string]any
	images      []map[string]any
	logs        strings.Builder
	cloneURL    string
	rootDir     string
	srv         *httptest.Server
}

func newServices(t *testing.T, cloneURL string) *services {
	s := &services{buildStatus: "queued", cloneURL: cloneURL}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "GET" && r.URL.Path == "/internal/builds/"+build:
			_ = json.NewEncoder(w).Encode(map[string]any{"build": map[string]any{"status": s.buildStatus}})
		case r.Method == "POST" && r.URL.Path == "/internal/builds/"+build+"/status":
			s.buildPatch = append(s.buildPatch, body)
			if l, ok := body["logs"].(string); ok {
				s.logs.WriteString(l)
			}
			if st, ok := body["status"].(string); ok {
				s.buildStatus = st
			}
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/internal/deployments/"+dep+"/status":
			s.depPatch = append(s.depPatch, body)
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == "/internal/repos/"+repo+"/clone-info":
			_ = json.NewEncoder(w).Encode(map[string]any{"clone_url": s.cloneURL, "auth_type": "token", "token": tok, "full_name": "o/r", "default_branch": "main", "root_dir": s.rootDir})
		case r.Method == "POST" && r.URL.Path == "/internal/images":
			s.images = append(s.images, body)
			w.WriteHeader(201)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *services) last(list []map[string]any) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func newWorker(t *testing.T, s *services, d *fakeDocker) *Worker {
	return &Worker{
		BuildURL: s.srv.URL, DeploymentURL: s.srv.URL, RepositoryURL: s.srv.URL, RegistryAPI: s.srv.URL,
		RegistryHost: "127.0.0.1:5000", HTTP: s.srv.Client(), Docker: d, WorkRoot: t.TempDir(), MinFreeBytes: 1,
		Redactor: redact.New(), AllowFileClone: true, CancelPoll: time.Hour,
		Scan: func(context.Context, string) *scan.Summary {
			return &scan.Summary{Scanner: "trivy", Status: scan.StatusClean, Counts: map[string]int{}, Findings: []scan.Finding{}}
		},
	}
}

func job(sha, branch string) Job {
	return Job{BuildID: build, OrgID: org, ProjectID: proj, DeploymentID: dep, RepoID: repo, GitSHA: sha, GitBranch: branch, FullName: "o/r"}
}

const dockerfileSrc = "FROM alpine:3.20\nEXPOSE 8080\nCMD [\"sleep\",\"1\"]\n"

func TestSuccessfulBuildPushesScansAndHandsOffToDeployment(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc, "jp.yaml": "name: x\nruntime: docker\ndeploy:\n  port: 8080\n  healthcheck: /healthz\n"})
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	w := newWorker(t, s, d)
	if out := w.Process(context.Background(), job(sha, "main")); out != Done {
		t.Fatalf("outcome = %v", out)
	}
	wantImage := "127.0.0.1:5000/11111111/22222222:" + sha[:12]
	if len(d.builds) != 1 || d.builds[0] != wantImage || len(d.pushes) != 1 || d.pushes[0] != wantImage {
		t.Fatalf("builds=%v pushes=%v", d.builds, d.pushes)
	}
	bp := s.last(s.buildPatch)
	if bp["status"] != "succeeded" || bp["image_ref"] != wantImage || bp["scan_status"] != "clean" {
		t.Fatalf("build patch = %v", bp)
	}
	dp := s.last(s.depPatch)
	if dp["status"] != "deploying" || dp["image_ref"] != wantImage || dp["git_sha"] != sha {
		t.Fatalf("deployment patch = %v", dp)
	}
	if !strings.Contains(string(mustJSON(dp["jp_config"])), "/healthz") {
		t.Fatalf("jp.yaml not forwarded: %v", dp["jp_config"])
	}
	if len(s.images) != 1 {
		t.Fatal("image not registered")
	}
	// no secrets in build args, token scrubbed from every log line
	if len(d.args) != 0 {
		t.Fatalf("build args = %v", d.args)
	}
	if strings.Contains(s.logs.String(), tok) || !strings.Contains(s.logs.String(), "Step 1/3") {
		t.Fatalf("logs leak token or are empty:\n%s", s.logs.String())
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestFailedCloneFailsDeploymentWithoutAnyFallbackBuild(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc})
	for name, j := range map[string]Job{
		"missing branch": job(sha, "no-such-branch"),
		"unknown sha":    job(strings.Repeat("ab", 20), "main"),
		"bad repo":       job(sha, "main"),
	} {
		url := "file://" + bare
		if name == "bad repo" {
			url = "file://" + bare + "-missing"
		}
		s, d := newServices(t, url), &fakeDocker{}
		w := newWorker(t, s, d)
		if out := w.Process(context.Background(), j); out != Done {
			t.Fatalf("%s: outcome %v", name, out)
		}
		if len(d.builds) != 0 || len(d.pushes) != 0 {
			t.Fatalf("%s: docker was used after a failed clone (mock fallback)", name)
		}
		if bp := s.last(s.buildPatch); bp["status"] != "failed" || !strings.Contains(bp["error"].(string), "clone failed") {
			t.Fatalf("%s: build patch = %v", name, bp)
		}
		if dp := s.last(s.depPatch); dp["status"] != "failed" {
			t.Fatalf("%s: deployment patch = %v", name, dp)
		}
	}
}

func TestBuildAndPushFailuresFailTheDeployment(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc})
	for name, d := range map[string]*fakeDocker{
		"build": {buildErr: errors.New("exit status 1")},
		"push":  {pushErr: errors.New("connection refused to " + tok)},
	} {
		s := newServices(t, "file://"+bare)
		w := newWorker(t, s, d)
		w.Process(context.Background(), job(sha, "main"))
		bp, dp := s.last(s.buildPatch), s.last(s.depPatch)
		if bp["status"] != "failed" || dp["status"] != "failed" {
			t.Fatalf("%s: %v / %v", name, bp, dp)
		}
		if strings.Contains(bp["error"].(string), tok) || strings.Contains(s.logs.String(), tok) {
			t.Fatalf("%s: token leaked", name)
		}
		if name == "push" && !strings.Contains(bp["error"].(string), "not in the registry") {
			t.Fatalf("push error = %v", bp["error"])
		}
	}
}

func TestSecurityBlockOnCritical(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc, "jp.yaml": "security:\n  block_on_critical: true\n"})
	vuln := func(context.Context, string) *scan.Summary {
		return &scan.Summary{Scanner: "trivy", Status: scan.StatusVulnerable, Critical: 2, Counts: map[string]int{"CRITICAL": 2}, Findings: []scan.Finding{}}
	}
	s := newServices(t, "file://"+bare)
	w := newWorker(t, s, &fakeDocker{})
	w.Scan = vuln
	w.Process(context.Background(), job(sha, "main"))
	if bp := s.last(s.buildPatch); bp["status"] != "failed" || !strings.Contains(bp["error"].(string), "block_on_critical") {
		t.Fatalf("critical not blocking: %v", bp)
	}
	// scan unavailable + block => blocked (fail closed), never treated as clean
	s2 := newServices(t, "file://"+bare)
	w2 := newWorker(t, s2, &fakeDocker{})
	w2.Scan = func(context.Context, string) *scan.Summary {
		return &scan.Summary{Status: scan.StatusUnavailable, Reason: "db download failed", Counts: map[string]int{}, Findings: []scan.Finding{}}
	}
	w2.Process(context.Background(), job(sha, "main"))
	if bp := s2.last(s2.buildPatch); bp["status"] != "failed" || !strings.Contains(bp["error"].(string), "could not run") {
		t.Fatalf("unavailable scan with block: %v", bp)
	}
	// without block_on_critical the deployment proceeds and the vulnerabilities are recorded
	bare2, sha2 := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc})
	s3 := newServices(t, "file://"+bare2)
	w3 := newWorker(t, s3, &fakeDocker{})
	w3.Scan = vuln
	w3.Process(context.Background(), job(sha2, "main"))
	if bp := s3.last(s3.buildPatch); bp["status"] != "succeeded" || bp["scan_status"] != "vulnerabilities" {
		t.Fatalf("not blocked by default: %v", bp)
	}
}

func TestGeneratedDockerfileAndNoDetectionFails(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"package.json": `{"scripts":{"start":"node s.js"},"dependencies":{"express":"4"}}`, "s.js": "x"})
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	newWorker(t, s, d).Process(context.Background(), job(sha, "main"))
	if len(d.files) != 1 || !strings.Contains(d.files[0], `CMD ["npm","start"]`) {
		t.Fatalf("generated = %v", d.files)
	}
	bare2, sha2 := makeRepo(t, map[string]string{"README.md": "nothing runnable"})
	s2, d2 := newServices(t, "file://"+bare2), &fakeDocker{}
	newWorker(t, s2, d2).Process(context.Background(), job(sha2, "main"))
	if len(d2.builds) != 0 || s2.last(s2.buildPatch)["status"] != "failed" {
		t.Fatalf("undetectable project must fail, not build a placeholder: %v", s2.last(s2.buildPatch))
	}
}

func TestJpYamlDockerfileAndContextAreHonouredAndConfined(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"app/docker/Prod.Dockerfile": dockerfileSrc, "jp.yaml": "build:\n  dockerfile: app/docker/Prod.Dockerfile\n  context: app\n"})
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	newWorker(t, s, d).Process(context.Background(), job(sha, "main"))
	if len(d.builds) != 1 {
		t.Fatalf("jp.yaml dockerfile not used: %v", s.last(s.buildPatch))
	}
	bare2, sha2 := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc, "jp.yaml": "build:\n  dockerfile: ../../../etc/passwd\n"})
	s2, d2 := newServices(t, "file://"+bare2), &fakeDocker{}
	newWorker(t, s2, d2).Process(context.Background(), job(sha2, "main"))
	if len(d2.builds) != 0 || s2.last(s2.buildPatch)["status"] != "failed" {
		t.Fatal("escaping dockerfile path accepted")
	}
}

func TestPreviewBuildUsesPRHeadAndTag(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc})
	git(t, bare, "update-ref", "refs/pull/9/head", sha)
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	j := job(sha, "")
	j.PRNumber = 9
	newWorker(t, s, d).Process(context.Background(), j)
	if len(d.builds) != 1 || !strings.HasSuffix(d.builds[0], ":pr9-"+sha[:12]) {
		t.Fatalf("builds = %v / %v", d.builds, s.last(s.buildPatch))
	}
}

func TestRedeliveryOfFinishedBuildIsSkippedAndShutdownLeavesJobPending(t *testing.T) {
	bare, sha := makeRepo(t, map[string]string{"Dockerfile": dockerfileSrc})
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	s.buildStatus = "succeeded"
	if out := newWorker(t, s, d).Process(context.Background(), job(sha, "main")); out != Done || len(d.builds) != 0 {
		t.Fatalf("redelivery re-ran the build: %v %v", out, d.builds)
	}
	// shutdown mid-clone => Retry (not marked failed)
	s2 := newServices(t, "file://"+bare)
	w := newWorker(t, s2, &fakeDocker{})
	ctx, cancel := context.WithCancel(context.Background())
	w.Git = func(c context.Context, o gitx.Options) (*gitx.Result, error) {
		cancel()
		<-c.Done()
		return nil, c.Err()
	}
	if out := w.Process(ctx, job(sha, "main")); out != Retry {
		t.Fatalf("outcome = %v", out)
	}
	if bp := s2.last(s2.buildPatch); bp["status"] == "failed" {
		t.Fatal("interrupted build must not be marked failed")
	}
}

func TestSimulateIsExplicitAndLabelled(t *testing.T) {
	s, d := newServices(t, "x"), &fakeDocker{}
	w := newWorker(t, s, d)
	w.Simulate = true
	w.Process(context.Background(), job("", "main"))
	if !strings.Contains(s.logs.String(), "SIMULATED") || len(d.builds) != 0 {
		t.Fatalf("logs = %s", s.logs.String())
	}
	if bp := s.last(s.buildPatch); bp["framework"] != "simulated" || bp["scan_status"] != "simulated" {
		t.Fatalf("patch = %v", bp)
	}
}

func TestParseJobRejectsHostilePayloads(t *testing.T) {
	ok := map[string]any{"build_id": build, "org_id": org, "project_id": proj, "git_branch": "main", "git_sha": "abcdef1", "pr_number": float64(3)}
	if _, err := ParseJob(events.Envelope{Payload: ok}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"git_branch": "--upload-pack=x", "git_sha": "HEAD; rm", "build_id": "../x", "org_id": "", "pr_number": float64(-1), "deployment_id": "zz"} {
		p := map[string]any{}
		for kk, vv := range ok {
			p[kk] = vv
		}
		p[k] = v
		if _, err := ParseJob(events.Envelope{Payload: p}); err == nil {
			t.Errorf("%s=%v accepted", k, v)
		}
	}
}

func TestDeadLetterAfterTooManyAttempts(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s, d := newServices(t, "x"), &fakeDocker{}
	w := newWorker(t, s, d)
	rc := RunConfig{Redis: rdb, Consumer: "t", MaxAttempts: 2}
	env := events.New(events.TopicBuild, events.TypeBuildQueued, "", org, map[string]any{"build_id": build, "org_id": org, "project_id": proj, "deployment_id": dep, "repo_id": repo})
	raw, _ := json.Marshal(env)
	msg := redis.XMessage{ID: "1-0", Values: map[string]any{"payload": string(raw)}}
	rdb.Set(context.Background(), "jp:worker:attempts:1-0", 2, time.Hour)
	_ = events.EnsureGroup(context.Background(), rdb, events.TopicBuild, events.BuildConsumerGroup)
	w.handle(context.Background(), rc, msg)
	if bp := s.last(s.buildPatch); bp["status"] != "failed" || !strings.Contains(bp["error"].(string), "abandoned") {
		t.Fatalf("build patch = %v", bp)
	}
	if dp := s.last(s.depPatch); dp["status"] != "failed" {
		t.Fatalf("deployment patch = %v", dp)
	}
	if len(d.builds) != 0 {
		t.Fatal("dead-lettered job was built")
	}
}

func TestRunDrainsOnShutdown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := newServices(t, "x")
	w := newWorker(t, s, &fakeDocker{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, RunConfig{Redis: rdb, Consumer: "t", ReadBlock: 20 * time.Millisecond, DrainTimeout: time.Second})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// A monorepo: two projects of ONE repository, each building its own folder.
func monorepo(t *testing.T) (bare, sha string) {
	return makeRepo(t, map[string]string{
		"jp.yaml":                 "deploy:\n  port: 1111\nbuild:\n  dockerfile: nowhere/Dockerfile\n", // the repo-root manifest must NOT leak into the sub-projects
		"backend/Dockerfile":      dockerfileSrc,
		"backend/jp.yaml":         "deploy:\n  port: 8080\n",
		"backend/main.go":         "package main",
		"frontend/package.json":   `{"scripts":{"start":"node s.js"},"dependencies":{"express":"4"}}`,
		"frontend/s.js":           "x",
		"services/api/Dockerfile": dockerfileSrc,
		"services/api/docker/Prod.Dockerfile": "FROM alpine:3.20\nCMD [\"prod\"]\n",
		"services/api/jp.yaml":    "build:\n  dockerfile: docker/Prod.Dockerfile\n  context: .\n",
		"notes.txt":               "a file, not a folder",
	})
}

func TestRootDirectoryBuildsOnlyThatFolderOfAMonorepo(t *testing.T) {
	bare, sha := monorepo(t)

	// backend: its own Dockerfile, its own jp.yaml (port 8080), build context = backend/
	s, d := newServices(t, "file://"+bare), &fakeDocker{}
	s.rootDir = "backend"
	if out := newWorker(t, s, d).Process(context.Background(), job(sha, "main")); out != Done {
		t.Fatalf("outcome = %v", out)
	}
	bp := s.last(s.buildPatch)
	if bp["status"] != "succeeded" || len(d.builds) != 1 {
		t.Fatalf("backend build = %v / %v", bp, d.builds)
	}
	if filepath.Base(d.ctxDirs[0]) != "backend" || filepath.Base(filepath.Dir(d.dfPaths[0])) != "backend" || filepath.Base(d.dfPaths[0]) != "Dockerfile" {
		t.Fatalf("context=%s dockerfile=%s (must both be inside backend/)", d.ctxDirs[0], d.dfPaths[0])
	}
	if cfg := string(mustJSON(s.last(s.depPatch)["jp_config"])); !strings.Contains(cfg, "8080") || strings.Contains(cfg, "1111") {
		t.Fatalf("jp.yaml of the root dir must be used, not the repository's: %s", cfg)
	}
	if !strings.Contains(s.logs.String(), "root directory: backend") {
		t.Fatalf("log does not mention the root directory: %s", s.logs.String())
	}

	// frontend: no Dockerfile => one is generated for Node, inside frontend/ (never at the repo root)
	s2, d2 := newServices(t, "file://"+bare), &fakeDocker{}
	s2.rootDir = "frontend"
	newWorker(t, s2, d2).Process(context.Background(), job(sha, "main"))
	if len(d2.builds) != 1 || filepath.Base(d2.ctxDirs[0]) != "frontend" || !strings.Contains(d2.files[0], `CMD ["npm","start"]`) {
		t.Fatalf("frontend build: ctx=%v files=%v patch=%v", d2.ctxDirs, d2.files, s2.last(s2.buildPatch))
	}

	// nested folder + jp.yaml paths are relative to the root directory
	s3, d3 := newServices(t, "file://"+bare), &fakeDocker{}
	s3.rootDir = "services/api"
	newWorker(t, s3, d3).Process(context.Background(), job(sha, "main"))
	if len(d3.builds) != 1 || !strings.Contains(d3.files[0], `"prod"`) || !strings.HasSuffix(filepath.ToSlash(d3.dfPaths[0]), "services/api/docker/Prod.Dockerfile") {
		t.Fatalf("nested root dir: dockerfiles=%v paths=%v patch=%v", d3.files, d3.dfPaths, s3.last(s3.buildPatch))
	}

	// without a root directory the repository root is built (and its jp.yaml applies)
	s4, d4 := newServices(t, "file://"+bare), &fakeDocker{}
	newWorker(t, s4, d4).Process(context.Background(), job(sha, "main"))
	if len(d4.builds) != 0 || s4.last(s4.buildPatch)["status"] != "failed" || !strings.Contains(s4.last(s4.buildPatch)["error"].(string), "nowhere") {
		t.Fatalf("repository root build must use the root jp.yaml: %v", s4.last(s4.buildPatch))
	}
}

func TestRootDirectoryFailuresAreClearAndNeverBuild(t *testing.T) {
	bare, sha := monorepo(t)
	for _, c := range []struct{ root, want string }{
		{"gone", `root directory "gone" does not exist`},
		{"notes.txt", "is a file, not a folder"},
		{"../outside", "root directory"},
		{"backend/../../x", "root directory"},
	} {
		s, d := newServices(t, "file://"+bare), &fakeDocker{}
		s.rootDir = c.root
		if out := newWorker(t, s, d).Process(context.Background(), job(sha, "main")); out != Done {
			t.Fatalf("%s: outcome %v", c.root, out)
		}
		bp, dp := s.last(s.buildPatch), s.last(s.depPatch)
		if len(d.builds) != 0 || bp["status"] != "failed" || !strings.Contains(bp["error"].(string), c.want) || dp["status"] != "failed" {
			t.Errorf("%q: builds=%v build=%v deployment=%v", c.root, d.builds, bp, dp)
		}
	}
}
