package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/repository/internal/store"
)

const maxWebhookBody = 5 << 20

var deliveryRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// verifySignature checks X-Hub-Signature-256 against secret in constant time.
func verifySignature(secret string, body []byte, header string) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

type webhookEnvelope struct {
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// GitHubWebhook authenticates the delivery PER CONNECTION: a connection only receives an
// event signed with its own secret (or with the App-level secret for its own installation).
func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "payload too large")
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")

	var env webhookEnvelope
	_ = json.Unmarshal(body, &env)
	globalOK := verifySignature(h.GlobalWebhookSecret, body, sig)

	var verified []store.WebhookRepo
	if env.Repository.FullName != "" {
		repos, err := h.Store.FindForWebhook(r.Context(), env.Repository.FullName)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}
		only := r.URL.Query().Get("c") // a per-connection hook only ever serves that connection
		for _, c := range repos {
			if only != "" && c.ID != only {
				continue
			}
			own := verifySignature(c.WebhookSecret, body, sig)
			app := globalOK && c.InstallationID != nil && env.Installation.ID != 0 && *c.InstallationID == fmt.Sprint(env.Installation.ID)
			if own || app {
				verified = append(verified, c)
			}
		}
	}
	if len(verified) == 0 && !(event == "ping" && globalOK) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid webhook signature")
		return
	}

	// replay protection (only AFTER authentication so forged requests cannot burn delivery ids)
	if !deliveryRe.MatchString(delivery) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "missing X-GitHub-Delivery")
		return
	}
	if h.Redis == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "replay protection unavailable")
		return
	}
	first, err := h.Redis.SetNX(r.Context(), "jp:gh:delivery:"+delivery, "1", 24*time.Hour).Result()
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "replay protection unavailable")
		return
	}
	if !first {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true})
		return
	}

	switch event {
	case "ping":
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "pong": true})
	case "push":
		h.handlePush(w, r, body, verified)
	case "pull_request":
		h.handlePullRequest(w, r, body, verified)
	case "check_suite", "workflow_run":
		h.handleCI(w, r, event, body, verified)
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": event})
	}
}

// pushChanged returns the files a push touched. nil means "unknown" (no commit list, or GitHub's 20 commit cap
// was reached), which the CD rules treat as relevant to everyone.
func pushChanged(commits []pushCommit, head pushCommit, hasHead bool) []string {
	if len(commits) == 0 {
		if !hasHead {
			return nil
		}
		commits = []pushCommit{head}
	}
	if len(commits) >= 20 {
		return nil
	}
	out := []string{}
	for _, c := range commits {
		// GitHub always sends the three lists (possibly empty); absent lists mean the payload carries no file information
		if c.Added == nil && c.Modified == nil && c.Removed == nil {
			return nil
		}
		out = append(out, c.Added...)
		out = append(out, c.Modified...)
		out = append(out, c.Removed...)
	}
	return out
}

type pushCommit struct {
	ID       string   `json:"id"`
	Message  string   `json:"message"`
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

// ciHold is what is remembered between a push and the CI result when a connection waits for CI.
type ciHold struct {
	Branch  string `json:"branch"`
	Message string `json:"message"`
	Source  string `json:"source"`
	Preview bool   `json:"preview"`
}

func ciKey(repoID, sha string) string { return "jp:cd:ci:" + repoID + ":" + strings.ToLower(sha) }

func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request, body []byte, repos []store.WebhookRepo) {
	var p struct {
		Ref        string       `json:"ref"`
		After      string       `json:"after"`
		Deleted    bool         `json:"deleted"`
		HeadCommit pushCommit   `json:"head_commit"`
		Commits    []pushCommit `json:"commits"`
		Repository struct {
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if p.Deleted || !strings.HasPrefix(p.Ref, "refs/heads/") {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "deleted_or_not_a_branch"})
		return
	}
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")
	sha := p.After
	if sha == "" || strings.Trim(sha, "0") == "" {
		sha = p.HeadCommit.ID
	}
	if !branchRe.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") || !regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`).MatchString(sha) {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "invalid ref or sha"})
		return
	}
	// every commit message counts for "[skip ci]" only through the head commit, like GitHub Actions
	msg := truncate(p.HeadCommit.Message, 300)
	changed := pushChanged(p.Commits, p.HeadCommit, p.HeadCommit.ID != "")
	created, waiting := 0, 0
	skipped := []map[string]string{}
	for _, c := range repos {
		dec := c.Repo.CD().Decide(branch, firstNonEmpty(c.DefaultBranch, p.Repository.DefaultBranch), p.HeadCommit.Message, changed)
		if !dec.Deploy {
			skipped = append(skipped, map[string]string{"project_id": c.ProjectID, "reason": dec.Reason})
			continue
		}
		source := "webhook"
		if dec.Preview {
			source = "webhook-preview"
		}
		if dec.Wait {
			if h.holdForCI(r.Context(), c.Repo, sha, ciHold{Branch: branch, Message: msg, Source: source, Preview: dec.Preview}) {
				waiting++
			} else {
				skipped = append(skipped, map[string]string{"project_id": c.ProjectID, "reason": "could not remember the push while waiting for CI"})
			}
			continue
		}
		if h.createDeploy(r, c.Repo, sha, branch, 0, msg, source, dec.Preview, false) {
			created++
		}
	}
	h.emit(r.Context(), events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{"sha": sha, "branch": branch, "repos_matched": len(repos),
		"deployments_created": created, "skipped": len(skipped), "waiting_for_ci": waiting})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "repos_matched": len(repos), "deployments_created": created, "skipped": skipped, "waiting_for_ci": waiting})
}

// holdForCI remembers a push whose deployment waits for the repository's CI.
func (h *Handler) holdForCI(ctx context.Context, repo store.Repo, sha string, hold ciHold) bool {
	if h.Redis == nil {
		return false
	}
	raw, _ := json.Marshal(hold)
	return h.Redis.Set(ctx, ciKey(repo.ID, sha), raw, 24*time.Hour).Err() == nil
}

// handleCI releases the deployments that wait for CI: a successful check_suite (GitHub Actions) or workflow_run of the
// pushed commit deploys it; a failed one drops it. Connections that do not wait for CI are not affected.
func (h *Handler) handleCI(w http.ResponseWriter, r *http.Request, event string, body []byte, repos []store.WebhookRepo) {
	var p struct {
		Action     string `json:"action"`
		CheckSuite struct {
			HeadSHA    string `json:"head_sha"`
			Conclusion string `json:"conclusion"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_suite"`
		WorkflowRun struct {
			HeadSHA    string `json:"head_sha"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	sha, conclusion := p.WorkflowRun.HeadSHA, p.WorkflowRun.Conclusion
	if event == "check_suite" {
		sha, conclusion = p.CheckSuite.HeadSHA, p.CheckSuite.Conclusion
		// only the GitHub Actions suite counts: other apps (coverage bots, ...) finish at unrelated times
		if p.CheckSuite.App.Slug != "github-actions" {
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "check suite of another app"})
			return
		}
	}
	if p.Action != "completed" || !regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`).MatchString(sha) {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "not a completed run of a commit"})
		return
	}
	pass := conclusion == "success" || (event == "check_suite" && (conclusion == "neutral" || conclusion == "skipped"))
	released, dropped := 0, 0
	for _, c := range repos {
		if !c.RequireCI || h.Redis == nil {
			continue
		}
		raw, err := h.Redis.GetDel(r.Context(), ciKey(c.ID, sha)).Bytes()
		if err != nil {
			continue
		}
		if !pass {
			dropped++
			h.log().Info("deployment dropped: CI did not succeed", "repo", c.FullName, "sha", sha, "conclusion", conclusion)
			continue
		}
		var hold ciHold
		if json.Unmarshal(raw, &hold) != nil {
			continue
		}
		if h.createDeploy(r, c.Repo, sha, hold.Branch, 0, hold.Message, hold.Source, hold.Preview, false) {
			released++
		}
	}
	h.emit(r.Context(), events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{"sha": sha, "ci": event, "conclusion": conclusion, "released": released, "dropped": dropped})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ci": event, "conclusion": conclusion, "deployments_created": released, "dropped": dropped})
}

func (h *Handler) handlePullRequest(w http.ResponseWriter, r *http.Request, body []byte, repos []store.WebhookRepo) {
	var p struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Title string `json:"title"`
			Head  struct {
				SHA  string `json:"sha"`
				Ref  string `json:"ref"`
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if p.Number <= 0 || p.Number > 10_000_000 {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "invalid pull request number"})
		return
	}
	switch p.Action {
	case "closed":
		torn := 0
		for _, c := range repos {
			code, err := h.internalPost(r.Context(), "/internal/preview/teardown", map[string]any{
				"org_id": c.OrgID, "project_id": c.ProjectID, "full_name": c.FullName, "pr_number": p.Number})
			if err == nil && code < 300 {
				torn++
			} else {
				h.log().Warn("preview teardown failed", "repo", c.FullName, "pr", p.Number, "status", code, "err", err)
			}
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "teardown": true, "pr": p.Number, "repos_matched": len(repos), "teardown_calls": torn})
		return
	case "opened", "synchronize", "reopened":
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": p.Action})
		return
	}
	sha := p.PullRequest.Head.SHA
	if !regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`).MatchString(sha) {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "invalid head sha"})
		return
	}
	// code from a fork is never built unless the connection explicitly allows it, and then
	// it never receives any secret (fork=true is enforced by the runtime).
	fork := p.PullRequest.Head.Repo == nil || !strings.EqualFold(p.PullRequest.Head.Repo.FullName, p.Repository.FullName)
	ref := p.PullRequest.Head.Ref
	if !branchRe.MatchString(ref) || strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") {
		ref = fmt.Sprintf("pr-%d", p.Number)
	}
	msg := truncate(fmt.Sprintf("PR #%d: %s", p.Number, p.PullRequest.Title), 300)
	created, skipped := 0, 0
	for _, c := range repos {
		if !c.PRPreviews {
			skipped++
			continue
		}
		if fork && !c.AllowForks {
			skipped++
			continue
		}
		if h.createDeploy(r, c.Repo, sha, ref, p.Number, msg, "webhook-preview", true, fork) {
			created++
		}
	}
	h.emit(r.Context(), events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{"sha": sha, "pr": p.Number, "preview": true, "fork": fork,
		"repos_matched": len(repos), "deployments_created": created, "skipped_fork": skipped})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "preview": true, "pr": p.Number, "fork": fork, "repos_matched": len(repos),
		"deployments_created": created, "skipped_fork": skipped})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// createDeploy asks the deployment service for a deployment of THIS connection only.
func (h *Handler) createDeploy(r *http.Request, repo store.Repo, sha, branch string, pr int, msg, source string, preview, fork bool) bool {
	if h.DeploymentURL == "" {
		return false
	}
	code, err := h.internalPost(r.Context(), "/internal/deployments/from-git", map[string]any{
		"org_id": repo.OrgID, "project_id": repo.ProjectID, "repo_id": repo.ID, "git_sha": sha, "git_branch": branch,
		"pr_number": pr, "message": msg, "source": source, "preview": preview, "fork": fork,
	})
	if err != nil || code >= 300 {
		h.log().Warn("create deployment from webhook failed", "repo", repo.FullName, "status", code, "err", err)
		return false
	}
	return true
}
