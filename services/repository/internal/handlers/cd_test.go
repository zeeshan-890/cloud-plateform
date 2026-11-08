package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jp-cloud/repository/internal/cd"
	"github.com/jp-cloud/repository/internal/store"
)

const appSecret = "global-app-secret"

var sha40 = strings.Repeat("a", 40)

// connectCD connects a repository through the GitHub App installation `inst` with explicit CD settings.
func (r *rig) connectCD(t *testing.T, org, proj, full, inst string, s cd.Settings) *store.Repo {
	t.Helper()
	rp, err := r.repo.ConnectRepo(context.Background(), store.ConnectParams{OrgID: org, ProjectID: proj, FullName: full, CloneURL: "https://github.com/" + full + ".git",
		Branch: "main", AuthType: "app", InstallationID: inst, WebhookSecret: "own-secret-" + proj, CD: s})
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

func monoSettings(root string) cd.Settings {
	s := cd.Defaults()
	s.RootDir = root
	return s
}

// appPush sends an App-signed push (the delivery reaches every connection of the installation).
func (r *rig) appPush(branch, msg string, commits []map[string]any) *httptest.ResponseRecorder {
	p := map[string]any{"ref": "refs/heads/" + branch, "after": sha40, "installation": map[string]any{"id": 555},
		"repository": map[string]any{"full_name": "acme/mono", "default_branch": "main"}, "head_commit": map[string]any{"id": sha40, "message": msg}}
	if commits != nil {
		p["commits"] = commits
	}
	return r.hook("push", appSecret, delivery(), p)
}

func files(added, modified, removed []string) []map[string]any {
	return []map[string]any{{"id": sha40, "added": added, "modified": modified, "removed": removed}}
}

func (r *rig) deployedProjects() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, d := range r.deploys {
		out[d["project_id"].(string)]++
	}
	return out
}

func TestMonorepoPushDeploysOnlyTheProjectWhoseFolderChanged(t *testing.T) {
	r := setup(t, false)
	backend := r.connectCD(t, orgA, projA, "acme/mono", "555", monoSettings("backend"))
	r.connectCD(t, orgA, projB, "acme/mono", "555", monoSettings("frontend"))

	got := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("push = %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	// backend-only change => only the backend project deploys; the frontend one reports why not
	out := got(r.appPush("main", "api: add endpoint", files(nil, []string{"backend/handler.go"}, nil)))
	if d := r.deployedProjects(); d[projA] != 1 || d[projB] != 0 {
		t.Fatalf("backend push deployed %v", d)
	}
	if skipped, _ := out["skipped"].([]any); len(skipped) != 1 || !strings.Contains(skipped[0].(map[string]any)["reason"].(string), "folder") {
		t.Fatalf("skipped = %v", out["skipped"])
	}
	if r.deploys[0]["repo_id"] != backend.ID || r.deploys[0]["preview"] != false {
		t.Fatalf("deploy = %v", r.deploys[0])
	}

	// frontend-only change => only the frontend project
	got(r.appPush("main", "ui", files([]string{"frontend/src/new.tsx"}, nil, nil)))
	if d := r.deployedProjects(); d[projA] != 1 || d[projB] != 1 {
		t.Fatalf("frontend push deployed %v", d)
	}

	// a change in both => both; a change in neither (docs at the root) => none
	got(r.appPush("main", "both", files(nil, []string{"backend/a.go", "frontend/b.ts"}, nil)))
	if d := r.deployedProjects(); d[projA] != 2 || d[projB] != 2 {
		t.Fatalf("combined push deployed %v", d)
	}
	got(r.appPush("main", "docs", files(nil, []string{"README.md"}, []string{"docs/old.md"})))
	if d := r.deployedProjects(); d[projA] != 2 || d[projB] != 2 {
		t.Fatalf("root-only push deployed %v", d)
	}
	// deleted files count as changes
	got(r.appPush("main", "cleanup", files(nil, nil, []string{"backend/old.go"})))
	if d := r.deployedProjects(); d[projA] != 3 {
		t.Fatalf("a removal in backend must deploy backend: %v", d)
	}

	// no commit list (or GitHub's 20 commit cap) => unknown => everyone deploys (never skip a needed deploy)
	got(r.appPush("main", "unknown", nil))
	many := make([]map[string]any, 20)
	for i := range many {
		many[i] = map[string]any{"id": sha40, "modified": []string{"docs/x.md"}}
	}
	got(r.appPush("main", "squash of twenty", many))
	if d := r.deployedProjects(); d[projA] != 5 || d[projB] != 4 {
		t.Fatalf("unknown change sets must deploy every project: %v", d)
	}
}

func TestWatchAndIgnorePathsAndSkipMessage(t *testing.T) {
	r := setup(t, false)
	s := monoSettings("services/api")
	s.WatchPaths = []string{"packages/shared/**"}
	s.IgnorePaths = []string{"**/*.md"}
	r.connectCD(t, orgA, projA, "acme/mono", "555", s)

	r.appPush("main", "shared", files(nil, []string{"packages/shared/util.go"}, nil))
	if r.deployedProjects()[projA] != 1 {
		t.Fatal("a change in a watched shared package must deploy")
	}
	r.appPush("main", "docs", files(nil, []string{"services/api/README.md"}, nil))
	if r.deployedProjects()[projA] != 1 {
		t.Fatal("an ignored file type must not deploy")
	}
	r.appPush("main", "real change [skip ci]", files(nil, []string{"services/api/main.go"}, nil))
	r.appPush("main", "another [skip deploy]", files(nil, []string{"services/api/main.go"}, nil))
	if r.deployedProjects()[projA] != 1 {
		t.Fatal("a skip marker in the commit message must not deploy")
	}
	r.appPush("main", "ship it", files(nil, []string{"services/api/main.go"}, nil))
	if r.deployedProjects()[projA] != 2 {
		t.Fatal("a normal change must deploy")
	}
}

func TestAutoDeployDeployBranchAndPreviewSwitches(t *testing.T) {
	r := setup(t, false)
	s := cd.Defaults()
	s.AutoDeploy = false
	r.connectCD(t, orgA, projA, "acme/mono", "555", s)
	r.appPush("main", "m", nil)
	if len(r.deploys) != 0 {
		t.Fatalf("auto-deploy off still deployed the production branch: %v", r.deploys)
	}
	r.appPush("feature/x", "m", nil)
	if len(r.deploys) != 1 || r.deploys[0]["preview"] != true {
		t.Fatalf("auto-deploy off must still preview other branches: %v", r.deploys)
	}

	// custom deploy branch: "release" is production, the repository default branch becomes a preview
	r2 := setup(t, false)
	s2 := cd.Defaults()
	s2.DeployBranch = "release"
	r2.connectCD(t, orgA, projA, "acme/mono", "555", s2)
	r2.appPush("release", "m", nil)
	r2.appPush("main", "m", nil)
	if len(r2.deploys) != 2 || r2.deploys[0]["preview"] != false || r2.deploys[1]["preview"] != true {
		t.Fatalf("deploys = %v", r2.deploys)
	}

	// previews off
	r3 := setup(t, false)
	s3 := cd.Defaults()
	s3.BranchPreviews, s3.PRPreviews = false, false
	r3.connectCD(t, orgA, projA, "acme/mono", "555", s3)
	r3.appPush("feature/y", "m", nil)
	r3.appPush("main", "m", nil)
	if len(r3.deploys) != 1 || r3.deploys[0]["git_branch"] != "main" {
		t.Fatalf("branch previews off: %v", r3.deploys)
	}
	pr := prPayload("opened", 3, "acme/mono", sha40)
	pr["repository"] = map[string]any{"full_name": "acme/mono"}
	pr["installation"] = map[string]any{"id": 555}
	if w := r3.hook("pull_request", appSecret, delivery(), pr); w.Code != 200 || len(r3.deploys) != 1 {
		t.Fatalf("PR previews off still deployed: %s", w.Body.String())
	}
}

func ciEvent(event, action, conclusion, slug string) map[string]any {
	p := map[string]any{"action": action, "installation": map[string]any{"id": 555}, "repository": map[string]any{"full_name": "acme/mono"}}
	if event == "check_suite" {
		p["check_suite"] = map[string]any{"head_sha": sha40, "conclusion": conclusion, "app": map[string]any{"slug": slug}}
	} else {
		p["workflow_run"] = map[string]any{"head_sha": sha40, "conclusion": conclusion}
	}
	return p
}

func TestRequireCIWaitsThenDeploysOnSuccess(t *testing.T) {
	r := setup(t, false)
	s := monoSettings("backend")
	s.RequireCI = true
	r.connectCD(t, orgA, projA, "acme/mono", "555", s)
	r.connectCD(t, orgA, projB, "acme/mono", "555", monoSettings("frontend")) // does not wait for CI

	w := r.appPush("main", "m", files(nil, []string{"backend/a.go", "frontend/b.ts"}, nil))
	if !strings.Contains(w.Body.String(), `"waiting_for_ci":1`) {
		t.Fatalf("push = %s", w.Body.String())
	}
	if d := r.deployedProjects(); d[projA] != 0 || d[projB] != 1 {
		t.Fatalf("only the project that does not wait for CI deploys at push time: %v", d)
	}

	// another app's check suite, an unfinished one, and a different commit change nothing
	r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "completed", "success", "codecov"))
	r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "requested", "", "github-actions"))
	other := ciEvent("check_suite", "completed", "success", "github-actions")
	other["check_suite"].(map[string]any)["head_sha"] = strings.Repeat("b", 40)
	r.hook("check_suite", appSecret, delivery(), other)
	if r.deployedProjects()[projA] != 0 {
		t.Fatal("deployed before the CI of this commit succeeded")
	}

	// a failed run drops the held deployment for good
	r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "completed", "failure", "github-actions"))
	r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "completed", "success", "github-actions"))
	if r.deployedProjects()[projA] != 0 {
		t.Fatal("a commit whose CI failed must never deploy")
	}

	// push again, CI succeeds => exactly one deployment of that commit, branch and source preserved
	r.appPush("main", "retry", files(nil, []string{"backend/a.go"}, nil))
	w = r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "completed", "success", "github-actions"))
	if w.Code != 200 || r.deployedProjects()[projA] != 1 {
		t.Fatalf("CI success = %d %s deploys=%v", w.Code, w.Body.String(), r.deployedProjects())
	}
	var d map[string]any
	for _, x := range r.deploys {
		if x["project_id"] == projA {
			d = x
		}
	}
	if d["git_sha"] != sha40 || d["git_branch"] != "main" || d["source"] != "webhook" || d["preview"] != false || d["message"] != "retry" {
		t.Fatalf("released deployment = %v", d)
	}
	// the same CI result again (or the workflow_run event of the same commit) deploys nothing more
	r.hook("workflow_run", appSecret, delivery(), ciEvent("workflow_run", "completed", "success", ""))
	r.hook("check_suite", appSecret, delivery(), ciEvent("check_suite", "completed", "success", "github-actions"))
	if r.deployedProjects()[projA] != 1 {
		t.Fatalf("CI result deployed twice: %v", r.deployedProjects())
	}

	// workflow_run works as the trigger too
	r.appPush("main", "again", files(nil, []string{"backend/c.go"}, nil))
	r.hook("workflow_run", appSecret, delivery(), ciEvent("workflow_run", "completed", "success", ""))
	if r.deployedProjects()[projA] != 2 {
		t.Fatalf("workflow_run did not release the deployment: %v", r.deployedProjects())
	}
}

func TestSettingsAPIValidationRBACAndPartialUpdate(t *testing.T) {
	r := setup(t, false)
	r.ghRepos["acme/mono"] = ghRepoInfo{DefaultBranch: "main"}
	base := "/orgs/" + orgA + "/projects/" + projA

	// connect with settings
	w := r.do("admin", "POST", base+"/repos", `{"full_name":"acme/mono","root_dir":"./backend/","watch_paths":["packages/shared/**"],"require_ci":true,"branch_previews":false}`)
	if w.Code != 201 {
		t.Fatalf("connect = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Repo       store.Repo `json:"repo"`
		WebhookURL string     `json:"webhook_url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	rp := out.Repo
	if rp.RootDir != "backend" || !rp.RequireCI || rp.BranchPreviews || !rp.AutoDeploy || !rp.PRPreviews || len(rp.WatchPaths) != 1 {
		t.Fatalf("connected settings = %+v", rp)
	}
	if !strings.HasSuffix(out.WebhookURL, "/api/v1/webhooks/github?c="+rp.ID) {
		t.Fatalf("webhook url = %q (must identify the connection)", out.WebhookURL)
	}
	// defaults for a plain connect
	r.ghRepos["acme/plain"] = ghRepoInfo{DefaultBranch: "main"}
	w = r.do("admin", "POST", base+"/repos", `{"full_name":"acme/plain"}`)
	var plain struct {
		Repo store.Repo `json:"repo"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &plain)
	if p := plain.Repo; p.RootDir != "" || !p.AutoDeploy || !p.BranchPreviews || !p.PRPreviews || p.RequireCI || p.WatchPaths == nil || p.IgnorePaths == nil {
		t.Fatalf("defaults = %+v (lists must never be null)", p)
	}

	// hostile values are rejected, on connect and on update
	for _, bad := range []string{`{"root_dir":"../etc"}`, `{"root_dir":"a/../../b"}`, `{"root_dir":".git/hooks"}`, `{"deploy_branch":"--upload-pack=x"}`,
		`{"deploy_branch":"a..b"}`, `{"watch_paths":["../x"]}`, `{"ignore_paths":["a/../b"]}`} {
		if c := r.do("admin", "PATCH", base+"/repos/"+rp.ID, bad); c.Code != 400 {
			t.Errorf("PATCH %s = %d", bad, c.Code)
		}
	}
	if r.do("admin", "POST", base+"/repos", `{"full_name":"acme/mono2","root_dir":"../x"}`).Code != 400 {
		t.Error("connect accepted a hostile root_dir")
	}
	// admin only
	if r.do("member", "PATCH", base+"/repos/"+rp.ID, `{"auto_deploy":false}`).Code != 403 || r.do("viewer", "PATCH", base+"/repos/"+rp.ID, `{"auto_deploy":false}`).Code != 403 {
		t.Error("non-admin changed CD settings")
	}
	if r.do("admin", "PATCH", base+"/repos/"+sha40[:8]+"-0000-4000-8000-000000000000", `{}`).Code != 404 || r.do("admin", "PATCH", base+"/repos/not-a-uuid", `{}`).Code != 404 {
		t.Error("unknown connection must be 404")
	}

	// partial update keeps everything else
	w = r.do("admin", "PATCH", base+"/repos/"+rp.ID, `{"auto_deploy":false,"deploy_branch":"release","root_dir":"apps/api","ignore_paths":["**/*.md"]}`)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	var upd struct {
		Repo store.Repo `json:"repo"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &upd)
	u := upd.Repo
	if u.AutoDeploy || u.DeployBranch != "release" || u.RootDir != "apps/api" || len(u.IgnorePaths) != 1 || !u.RequireCI || u.BranchPreviews || len(u.WatchPaths) != 1 {
		t.Fatalf("partial update = %+v", u)
	}
	// emptying the root directory goes back to the repository root; lists can be cleared
	w = r.do("admin", "PATCH", base+"/repos/"+rp.ID, `{"root_dir":"","watch_paths":[]}`)
	_ = json.Unmarshal(w.Body.Bytes(), &upd)
	if upd.Repo.RootDir != "" || upd.Repo.WatchPaths == nil || len(upd.Repo.WatchPaths) != 0 {
		t.Fatalf("clearing = %+v", upd.Repo)
	}
	// visible in the listing for viewers, with no secrets
	l := r.do("viewer", "GET", base+"/repos", "")
	if l.Code != 200 || !strings.Contains(l.Body.String(), `"deploy_branch":"release"`) || strings.Contains(l.Body.String(), "webhook_secret") {
		t.Fatalf("list = %s", l.Body.String())
	}
}

func TestCloneInfoCarriesTheRootDirectory(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	r := setup(t, false)
	rp := r.connectCD(t, orgA, projA, "acme/mono", "555", monoSettings("backend"))
	w := r.do("", "GET", "/internal/repos/"+rp.ID+"/clone-info?org_id="+orgA, "", "X-Internal-Token", "int")
	if w.Code != 200 {
		// "app" connections need a configured GitHub App to mint tokens: use a token connection for the assertion
		rp2, _ := r.repo.ConnectRepo(context.Background(), store.ConnectParams{OrgID: orgA, ProjectID: projA, FullName: "acme/tok", CloneURL: "https://github.com/acme/tok.git",
			Branch: "main", AuthType: "token", Token: "tok-123456789", WebhookSecret: "w", CD: monoSettings("services/web")})
		w = r.do("", "GET", "/internal/repos/"+rp2.ID+"/clone-info?org_id="+orgA, "", "X-Internal-Token", "int")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"root_dir":"services/web"`) && !strings.Contains(w.Body.String(), `"root_dir":"backend"`) {
		t.Fatalf("clone-info = %d %s", w.Code, w.Body.String())
	}
}

func TestRotateWebhookSecretInvalidatesTheOldOne(t *testing.T) {
	r := setup(t, false)
	rp := r.connect(t, orgA, projA, "acme/web", "old-secret", false, "")
	base := "/orgs/" + orgA + "/projects/" + projA + "/repos/" + rp.ID + "/webhook/rotate"
	if r.do("member", "POST", base, "").Code != 403 {
		t.Fatal("member rotated a secret")
	}
	w := r.do("admin", "POST", base, "")
	var out struct {
		Secret string `json:"webhook_secret"`
		URL    string `json:"webhook_url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Secret) != 64 || !strings.HasSuffix(out.URL, "?c="+rp.ID) {
		t.Fatalf("rotate = %d %s", w.Code, w.Body.String())
	}
	push := map[string]any{"ref": "refs/heads/main", "after": sha40, "repository": map[string]any{"full_name": "acme/web", "default_branch": "main"}}
	if r.hook("push", "old-secret", delivery(), push).Code != 401 {
		t.Fatal("the old secret still works")
	}
	if w := r.hook("push", out.Secret, delivery(), push); w.Code != 200 || len(r.deploys) != 1 {
		t.Fatalf("new secret = %d %s", w.Code, w.Body.String())
	}
}

func TestPerConnectionHookOnlyServesItsConnection(t *testing.T) {
	r := setup(t, false)
	a := r.connect(t, orgA, projA, "acme/mono", "secret-A", false, "")
	b := r.connect(t, orgA, projB, "acme/mono", "secret-B", false, "")
	push := map[string]any{"ref": "refs/heads/main", "after": sha40, "repository": map[string]any{"full_name": "acme/mono", "default_branch": "main"}}
	send := func(secret, conn string) int {
		body, _ := json.Marshal(push)
		req := httptest.NewRequest("POST", "/webhooks/github?c="+conn, strings.NewReader(string(body)))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", delivery())
		req.Header.Set("X-Hub-Signature-256", sign(secret, body))
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		return w.Code
	}
	if send("secret-A", a.ID) != 200 || r.deployedProjects()[projA] != 1 || r.deployedProjects()[projB] != 0 {
		t.Fatalf("hook A must serve only A: %v", r.deployedProjects())
	}
	// hook B's secret on hook A's URL is rejected, and the other way round
	if send("secret-B", a.ID) != 401 || send("secret-A", b.ID) != 401 {
		t.Fatal("a per-connection hook accepted another connection's secret")
	}
	if send("secret-B", b.ID) != 200 || r.deployedProjects()[projB] != 1 {
		t.Fatalf("hook B must serve B: %v", r.deployedProjects())
	}
}

func TestInstallWebhookCreatesThenUpdatesItsOwnHook(t *testing.T) {
	r := setup(t, false)
	r.ghRepos["acme/mono"] = ghRepoInfo{DefaultBranch: "main", Private: true}
	base := "/orgs/" + orgA + "/projects/" + projA
	w := r.do("admin", "POST", base+"/repos", `{"full_name":"acme/mono","token":"good-token"}`)
	var c struct {
		Repo   store.Repo `json:"repo"`
		Secret string     `json:"webhook_secret"`
		URL    string     `json:"webhook_url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	inst := base + "/repos/" + c.Repo.ID + "/webhook/install"

	if r.do("member", "POST", inst, "").Code != 403 {
		t.Fatal("member installed a webhook")
	}
	// first call creates the hook with this connection's secret and the events CD needs
	if w := r.do("admin", "POST", inst, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatalf("install = %d %s", w.Code, w.Body.String())
	}
	h := r.ghHooks[0]
	cfg := h["config"].(map[string]any)
	if h["_method"] != "POST" || cfg["secret"] != c.Secret || cfg["url"] != c.URL || cfg["content_type"] != "json" || h["active"] != true {
		t.Fatalf("hook = %v", h)
	}
	ev := map[string]bool{}
	for _, e := range h["events"].([]any) {
		ev[e.(string)] = true
	}
	for _, need := range []string{"push", "pull_request", "check_suite", "workflow_run"} {
		if !ev[need] {
			t.Errorf("hook lacks event %s: %v", need, h["events"])
		}
	}
	// GitHub now lists a hook with this connection's URL (and one of ANOTHER connection): only ours is updated
	r.ghHookIn = `[{"id":1,"config":{"url":"https://jp.example/api/v1/webhooks/github?c=00000000-0000-4000-8000-000000000000"}},{"id":4711,"config":{"url":"` + c.URL + `"}}]`
	if w := r.do("admin", "POST", inst, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"created":false`) {
		t.Fatalf("second install = %d %s", w.Code, w.Body.String())
	}
	if last := r.ghHooks[len(r.ghHooks)-1]; last["_method"] != "PATCH" || !strings.HasSuffix(last["_path"].(string), "/hooks/4711") {
		t.Fatalf("second install must update its own hook, got %v", last)
	}
	// a token without hook permission => a clear 400 (not a 500)
	r.ghDeny = true
	if w := r.do("admin", "POST", inst, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "admin") {
		t.Fatalf("denied = %d %s", w.Code, w.Body.String())
	}
	// public/no-token and GitHub App connections cannot auto-install
	pub := r.connect(t, orgA, projA, "acme/other", "s", false, "")
	if w := r.do("admin", "POST", base+"/repos/"+pub.ID+"/webhook/install", ""); w.Code != 400 {
		t.Fatalf("no-token install = %d", w.Code)
	}
}

func TestRepoFoldersBrowser(t *testing.T) {
	r := setup(t, false)
	r.ghRepos["acme/mono"] = ghRepoInfo{DefaultBranch: "main", Private: true}
	r.ghTree = map[string]string{
		"acme/mono/contents": `[{"name":"backend","type":"dir"},{"name":"frontend","type":"dir"},{"name":".git","type":"dir"},{"name":"README.md","type":"file"},{"name":"package.json","type":"file"}]`,
		"acme/mono/contents/backend": `[{"name":"cmd","type":"dir"},{"name":"Dockerfile","type":"file"},{"name":"go.mod","type":"file"},{"name":"main.go","type":"file"}]`,
		"acme/mono/contents/README.md": `{"name":"README.md","type":"file"}`,
	}
	base := "/orgs/" + orgA + "/projects/" + projA
	w := r.do("admin", "POST", base+"/repos", `{"full_name":"acme/mono","token":"good-token"}`)
	var c struct {
		Repo store.Repo `json:"repo"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	f := base + "/repos/" + c.Repo.ID + "/folders"

	type res struct {
		Path    string              `json:"path"`
		Folders []map[string]string `json:"folders"`
		Markers []string            `json:"markers"`
	}
	var top res
	w = r.do("admin", "GET", f, "")
	_ = json.Unmarshal(w.Body.Bytes(), &top)
	if w.Code != 200 || len(top.Folders) != 2 || top.Folders[0]["path"] != "backend" || top.Folders[1]["path"] != "frontend" || len(top.Markers) != 1 || top.Markers[0] != "package.json" {
		t.Fatalf("top level = %d %s", w.Code, w.Body.String())
	}
	var sub res
	w = r.do("admin", "GET", f+"?path=backend", "")
	_ = json.Unmarshal(w.Body.Bytes(), &sub)
	if w.Code != 200 || len(sub.Folders) != 1 || sub.Folders[0]["path"] != "backend/cmd" || len(sub.Markers) != 2 {
		t.Fatalf("backend = %d %s", w.Code, w.Body.String())
	}
	// the production branch does not exist on GitHub: browsing falls back to the default branch instead of failing
	if w := r.do("admin", "PATCH", base+"/repos/"+c.Repo.ID, `{"deploy_branch":"release"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r.ghNoRef = "release"
	var fb res
	w = r.do("admin", "GET", f, "")
	_ = json.Unmarshal(w.Body.Bytes(), &fb)
	if w.Code != 200 || len(fb.Folders) != 2 || !strings.Contains(w.Body.String(), `"ref":"main"`) {
		t.Fatalf("fallback to the default branch = %d %s", w.Code, w.Body.String())
	}
	// an explicit ref is honoured as given (no silent fallback)
	if w := r.do("admin", "GET", f+"?ref=release", ""); w.Code != 404 {
		t.Fatalf("explicit missing ref = %d", w.Code)
	}
	r.ghNoRef = ""
	// errors: missing path, a file, a traversal, non-admin
	if r.do("admin", "GET", f+"?path=nope", "").Code != 404 {
		t.Error("missing folder must be 404")
	}
	if w := r.do("admin", "GET", f+"?path=README.md", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "file") {
		t.Errorf("a file = %d %s", w.Code, w.Body.String())
	}
	if r.do("admin", "GET", f+"?path=../etc", "").Code != 400 || r.do("admin", "GET", f+"?ref=--x", "").Code != 400 {
		t.Error("traversal / hostile ref accepted")
	}
	if r.do("member", "GET", f, "").Code != 403 {
		t.Error("member browsed the repository")
	}
	// deploy-key connections cannot use the API
	dk, _ := r.repo.ConnectRepo(context.Background(), store.ConnectParams{OrgID: orgA, ProjectID: projA, FullName: "acme/k", CloneURL: "git@github.com:acme/k.git", Branch: "main", AuthType: "deploy_key", WebhookSecret: "w", CD: cd.Defaults()})
	if w := r.do("admin", "GET", base+"/repos/"+dk.ID+"/folders", ""); w.Code != 400 {
		t.Errorf("deploy key browse = %d", w.Code)
	}
}
