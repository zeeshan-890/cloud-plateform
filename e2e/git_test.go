package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func gitRepo() string { return getenv("E2E_GIT_REPO", "heroku/node-js-getting-started") }

func waitDeployment(t *testing.T, c *Client, p *Project, id string, timeout time.Duration) map[string]any {
	t.Helper()
	var last map[string]any
	Eventually(t, timeout, 5*time.Second, "deployment "+id+" to finish", func() (bool, string) {
		r := c.Do("GET", p.Path("/deployments/"+id), nil)
		if r.Status != 200 {
			return false, fmt.Sprintf("status %d", r.Status)
		}
		last, _ = Dig(r.Map(t), "deployment").(map[string]any)
		st := Str(last, "status")
		switch st {
		case "ready", "failed", "canceled", "superseded", "stopped":
			return true, st
		}
		return false, st
	})
	return last
}

func postWebhook(e *Env, secret, event, delivery string, body []byte, sig string) (int, string) {
	req, _ := http.NewRequest("POST", e.API+"/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	if sig == "" && secret != "" {
		sig = HMACSig(secret, body)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, string(b)
}

// remoteHead resolves the default branch and its head sha with git ls-remote on the server (or the test host).
func remoteHead(t *testing.T, e *Env) (branch, sha string) {
	t.Helper()
	out, err := e.TryRun("git ls-remote --symref https://github.com/" + gitRepo() + " HEAD")
	if err != nil {
		t.Skipf("cannot reach GitHub from the server: %v %s", err, truncate(out, 200))
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "ref:" {
			branch = strings.TrimPrefix(f[1], "refs/heads/")
		} else if len(f) == 2 && f[1] == "HEAD" {
			sha = f[0]
		}
	}
	if branch == "" || sha == "" {
		t.Skipf("could not resolve HEAD of %s: %s", gitRepo(), out)
	}
	return
}

func TestGit_ConnectValidationRejectsDangerousURLs(t *testing.T) {
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	bad := []string{
		"-oProxyCommand=touch/pwned", "--upload-pack=evil/x", "file:///etc/passwd", "file://localhost/tmp/repo.git",
		"https://10.0.0.5/a/b.git", "https://127.0.0.1/a/b.git", "https://169.254.169.254/latest/meta-data", "http://localhost/a/b",
		"https://[::1]/a/b", "git@evil.example.com:a/b.git", "ssh://git@evil.example.com/a/b", "https://github.com.evil.example.com/a/b",
		"https://user:pw@github.com/a/b", "ext::sh -c touch% /tmp/pwned", "a/../../etc", "a b/c", "owner/repo;touch /tmp/x", "", "/etc/passwd",
		"$(touch /tmp/pwned)/x",
	}
	for _, b := range bad {
		for _, field := range []string{"full_name", "clone_url"} {
			r := admin.Do("POST", p.Path("/repos"), map[string]string{field: b})
			if r.Status < 400 || r.Status >= 500 {
				t.Errorf("repo %s=%q: want 4xx, got %d %s", field, b, r.Status, truncate(string(r.Body), 120))
			}
		}
	}
	// branch names that look like options
	for _, br := range []string{"-x", "--upload-pack=x", "a..b", "a b", "a;b"} {
		r := admin.Do("POST", p.Path("/repos"), map[string]string{"full_name": gitRepo(), "default_branch": br})
		if r.Status != 400 {
			t.Errorf("default_branch %q: want 400, got %d", br, r.Status)
		}
	}
	if r := admin.Do("POST", p.Path("/deployments"), map[string]string{"git_branch": "--upload-pack=x"}); r.Status != 400 {
		t.Errorf("deployment git_branch '--upload-pack=x': want 400, got %d", r.Status)
	}
	if r := admin.Do("POST", p.Path("/deployments"), map[string]string{"git_sha": "-1; rm -rf /"}); r.Status != 400 {
		t.Errorf("deployment git_sha with shell chars: want 400, got %d", r.Status)
	}
	// without a connected repository a deployment cannot be created
	if r := admin.Do("POST", p.Path("/deployments"), map[string]string{"git_sha": "HEAD"}); r.Status != 400 {
		t.Errorf("deployment without repo: want 400 no_repository, got %d", r.Status)
	}
	// forged webhooks for unknown repos
	e := Load(t)
	body := []byte(`{"ref":"refs/heads/main","after":"0123456789abcdef0123456789abcdef01234567","repository":{"full_name":"nobody/never-connected"}}`)
	if code, _ := postWebhook(e, "", "push", "e2e-"+randHex(6), body, "sha256=00"); code != 401 {
		t.Errorf("webhook for unknown repo with bogus signature: want 401, got %d", code)
	}
	if code, _ := postWebhook(e, "", "push", "e2e-"+randHex(6), body, ""); code != 401 {
		t.Errorf("unsigned webhook: want 401, got %d", code)
	}
}

func TestGit_PublicRepoBuildDeployWebhook(t *testing.T) {
	e := NeedSSH(t)
	if os.Getenv("E2E_SKIP_GIT") != "" {
		t.Skip("E2E_SKIP_GIT set")
	}
	branch, sha := remoteHead(t, e)
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)

	// secrets for the preview isolation check at the end
	admin.Must(t, "POST", p.Path("/environments/production/secrets"), map[string]string{"name": "E2E_PROD_ONLY", "value": "prod-secret"}, 201)
	admin.Must(t, "POST", p.Path("/environments/preview/secrets"), map[string]string{"name": "E2E_PREVIEW_ONLY", "value": "preview-secret"}, 201)

	// --- connect the public repo
	cr := admin.Must(t, "POST", p.Path("/repos"), map[string]string{"full_name": "https://github.com/" + gitRepo()}, 201).Map(t)
	repoID, whSecret := Str(cr, "repo", "id"), Str(cr, "webhook_secret")
	if repoID == "" || len(whSecret) < 16 {
		t.Fatalf("connect response missing repo id / per-repo webhook secret: %s", mustJSON(cr))
	}
	if Str(cr, "repo", "clone_url") != "https://github.com/"+gitRepo()+".git" {
		t.Errorf("clone_url = %q", Str(cr, "repo", "clone_url"))
	}
	if r := admin.Do("POST", p.Path("/repos"), map[string]string{"full_name": gitRepo()}); r.Status != 409 {
		t.Errorf("connecting the same repo twice: want 409, got %d", r.Status)
	}
	lr := admin.Must(t, "GET", p.Path("/repos"), nil, 200)
	if strings.Contains(string(lr.Body), whSecret) || strings.Contains(strings.ToLower(string(lr.Body)), `"token"`) {
		t.Errorf("repo list leaks webhook secret / token: %s", truncate(string(lr.Body), 300))
	}

	// --- first deployment (rolling)
	dep := admin.Must(t, "POST", p.Path("/deployments"), map[string]string{"git_sha": "HEAD", "strategy": "rolling", "message": "e2e first"}, 201).Map(t)
	id1 := Str(dep, "deployment", "id")
	d1 := waitDeployment(t, admin, p, id1, 20*time.Minute)
	if Str(d1, "status") != "ready" {
		t.Fatalf("first deployment finished as %q: %s", Str(d1, "status"), Str(d1, "error"))
	}
	body := WaitEdge(t, e, host, "/", 200, 90*time.Second)
	if len(body) < 20 {
		t.Errorf("app answered with a suspiciously small body: %q", body)
	}
	if bl := admin.Must(t, "GET", p.Path("/builds"), nil, 200).Map(t); len(Arr(bl, "builds")) == 0 {
		t.Errorf("no build recorded: %v", bl)
	}
	app := e.RunningApp(t, p.ID)
	ins := e.Inspect(t, app.ID)
	if len(ins.HostConfig.CapDrop) == 0 || ins.HostConfig.Memory <= 0 {
		t.Errorf("git-deployed container is not hardened: %+v", ins.HostConfig)
	}
	if v, _ := ins.EnvValue("E2E_PROD_ONLY"); v != "prod-secret" {
		t.Errorf("production secret not injected into the git deployed container")
	}
	if ins.EnvHas("E2E_PREVIEW_ONLY") {
		t.Errorf("preview secret leaked into the production container")
	}

	// --- blue/green flips the colour and keeps serving without a single non-2xx
	colorOf := func() string {
		for _, i := range Arr(admin.Must(t, "GET", p.Path("/runtime/instances"), nil, 200).Map(t), "instances") {
			if Str(i, "status") == "running" && Str(i, "role") == "app" && !Bool(i, "is_preview") {
				return Str(i, "color")
			}
		}
		return "?"
	}
	bg := func(msg string) map[string]any {
		r := admin.Must(t, "POST", p.Path("/deployments"), map[string]string{"git_sha": "HEAD", "strategy": "blue_green", "message": msg}, 201).Map(t)
		return waitDeployment(t, admin, p, Str(r, "deployment", "id"), 20*time.Minute)
	}
	poll := StartPoller(e, host, "/")
	d2 := bg("e2e bg 1")
	c2 := colorOf()
	d3 := bg("e2e bg 2")
	c3 := colorOf()
	time.Sleep(12 * time.Second)
	total, bad := poll.Stop()
	if Str(d2, "status") != "ready" || Str(d3, "status") != "ready" {
		t.Fatalf("blue/green deployments: %s / %s (%s %s)", Str(d2, "status"), Str(d3, "status"), Str(d2, "error"), Str(d3, "error"))
	}
	if c2 == c3 || (c2 != "blue" && c2 != "green") || (c3 != "blue" && c3 != "green") {
		t.Errorf("blue/green did not flip colours: %q -> %q", c2, c3)
	}
	if len(bad) > 0 {
		t.Errorf("downtime during blue/green flips: %d/%d bad: %v", len(bad), total, bad[:minInt(len(bad), 6)])
	}
	// the router file for the project hostname exists and is current (staged routing keeps exactly one router per host)
	if g := e.TraefikDynamicGrep(t, host); strings.TrimSpace(g) == "" {
		t.Errorf("no Traefik router for %s after blue/green flips", host)
	}
	WaitEdge(t, e, host, "/", 200, 30*time.Second)

	// --- rollback to the first deployment
	rb := admin.Must(t, "POST", p.Path("/deployments/"+id1+"/rollback"), nil, 201).Map(t)
	rd := waitDeployment(t, admin, p, Str(rb, "deployment", "id"), 10*time.Minute)
	if Str(rd, "status") != "ready" {
		t.Errorf("rollback deployment finished as %q: %s", Str(rd, "status"), Str(rd, "error"))
	}
	WaitEdge(t, e, host, "/", 200, 60*time.Second)

	// --- webhook
	push := func(ref string) []byte {
		b, _ := json.Marshal(map[string]any{"ref": ref, "after": sha, "head_commit": map[string]string{"id": sha, "message": "e2e push"},
			"repository": map[string]string{"full_name": gitRepo(), "default_branch": branch}})
		return b
	}
	countDeploys := func() int {
		return len(Arr(admin.Must(t, "GET", p.Path("/deployments"), nil, 200).Map(t), "deployments"))
	}
	body0 := push("refs/heads/" + branch)
	if code, out := postWebhook(e, "", "push", "e2e-"+randHex(8), body0, "sha256=deadbeef"); code != 401 {
		t.Errorf("forged signature: want 401, got %d %s", code, out)
	}
	if code, _ := postWebhook(e, "a-different-secret-entirely", "push", "e2e-"+randHex(8), body0, ""); code != 401 {
		t.Errorf("signature with a wrong secret: want 401, got %d", code)
	}
	if code, _ := postWebhook(e, "", "push", "e2e-"+randHex(8), body0, ""); code != 401 {
		t.Errorf("missing signature: want 401, got %d", code)
	}
	before := countDeploys()
	delivery := "e2e-" + randHex(10)
	code, out := postWebhook(e, whSecret, "push", delivery, body0, "")
	if code != 200 {
		t.Fatalf("signed push: want 200, got %d %s", code, out)
	}
	var wr map[string]any
	_ = json.Unmarshal([]byte(out), &wr)
	if Num(wr, "deployments_created") < 1 {
		t.Fatalf("signed push created no deployment: %s", out)
	}
	if after := countDeploys(); after != before+1 {
		t.Errorf("deployments %d -> %d after one push", before, after)
	}
	// replay of the same delivery id is ignored
	code, out = postWebhook(e, whSecret, "push", delivery, body0, "")
	if code != 200 || !strings.Contains(out, "duplicate") {
		t.Errorf("replayed delivery: want 200 duplicate, got %d %s", code, out)
	}
	if after := countDeploys(); after != before+1 {
		t.Errorf("replay created another deployment (%d -> %d)", before, after)
	}
	// ping
	if code, out := postWebhook(e, whSecret, "ping", "e2e-"+randHex(8), []byte(`{"zen":"x","repository":{"full_name":"`+gitRepo()+`"}}`), ""); code != 200 {
		t.Errorf("signed ping: want 200, got %d %s", code, out)
	}
	// the deployment from the webhook must actually build and go live
	var wh map[string]any
	for _, d := range Arr(admin.Must(t, "GET", p.Path("/deployments"), nil, 200).Map(t), "deployments") {
		if Str(d, "source") == "webhook" {
			wh, _ = d.(map[string]any)
			break
		}
	}
	if wh == nil {
		t.Fatalf("no deployment with source=webhook in the list")
	}
	if Str(wh, "git_sha") != sha {
		t.Errorf("webhook deployment sha = %q, want %q", Str(wh, "git_sha"), sha)
	}
	wd := waitDeployment(t, admin, p, Str(wh, "id"), 20*time.Minute)
	if Str(wd, "status") != "ready" {
		t.Errorf("webhook deployment finished as %q: %s", Str(wd, "status"), Str(wd, "error"))
	}
	WaitEdge(t, e, host, "/", 200, 60*time.Second)

	// --- preview deployment (non-default branch) never gets production secrets
	t.Run("preview isolation", func(t *testing.T) {
		br := otherBranch(t, e)
		if br == "" {
			t.Skip("repository has no branch besides the default one")
		}
		out, err := e.TryRun("git ls-remote https://github.com/" + gitRepo() + " refs/heads/" + br)
		f := strings.Fields(out)
		if err != nil || len(f) < 1 {
			t.Skipf("cannot resolve branch %s: %s", br, out)
		}
		psha := f[0]
		b, _ := json.Marshal(map[string]any{"ref": "refs/heads/" + br, "after": psha, "head_commit": map[string]string{"id": psha, "message": "e2e preview"},
			"repository": map[string]string{"full_name": gitRepo(), "default_branch": branch}})
		code, out := postWebhook(e, whSecret, "push", "e2e-"+randHex(8), b, "")
		if code != 200 {
			t.Fatalf("preview push: %d %s", code, out)
		}
		var prev map[string]any
		Eventually(t, 2*time.Minute, 3*time.Second, "preview deployment record", func() (bool, string) {
			for _, d := range Arr(admin.Must(t, "GET", p.Path("/deployments"), nil, 200).Map(t), "deployments") {
				if Bool(d, "is_preview") && Str(d, "git_branch") == br {
					prev, _ = d.(map[string]any)
					return true, ""
				}
			}
			return false, "none yet"
		})
		pd := waitDeployment(t, admin, p, Str(prev, "id"), 20*time.Minute)
		if Str(pd, "status") != "ready" {
			t.Fatalf("preview deployment finished as %q: %s", Str(pd, "status"), Str(pd, "error"))
		}
		found := false
		for _, c := range e.ProjectContainers(t, p.ID) {
			ci := e.Inspect(t, c.ID)
			if ci.Config.Labels["jp.deployment"] == Str(pd, "id") {
				found = true
				if !ci.EnvHas("E2E_PREVIEW_ONLY") {
					t.Errorf("preview container misses the preview secret")
				}
				if ci.EnvHas("E2E_PROD_ONLY") {
					t.Errorf("PRODUCTION secret leaked into the preview container")
				}
			}
		}
		if !found {
			t.Errorf("no container labelled with preview deployment %s", Str(pd, "id"))
		}
		// production keeps serving the production deployment
		WaitEdge(t, e, host, "/", 200, 30*time.Second)
	})
}

func otherBranch(t *testing.T, e *Env) string {
	out, err := e.TryRun("git ls-remote --heads https://github.com/" + gitRepo())
	if err != nil {
		return ""
	}
	br, _ := remoteHead(t, e)
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			name := strings.TrimPrefix(f[1], "refs/heads/")
			if name != br && !strings.HasPrefix(name, "-") {
				return name
			}
		}
	}
	return ""
}
