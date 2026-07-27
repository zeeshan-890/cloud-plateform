package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/repository/internal/githubapp"
	"github.com/jp-cloud/repository/internal/store"
	"github.com/redis/go-redis/v9"
)

const installStateTTL = 10 * time.Minute

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	DeploymentURL   string
	WebhookSecret   string
	PublicBaseURL   string
	DashboardURL    string
	HTTP            *http.Client
	Redis           *redis.Client
	GitHub          *githubapp.Client
	Log             *slog.Logger
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs/{orgId}/github/install/start", auth(http.HandlerFunc(h.InstallStart)))
	mux.Handle("POST /orgs/{orgId}/github/install/callback", auth(http.HandlerFunc(h.InstallCallback)))
	mux.Handle("GET /orgs/{orgId}/github/installations", auth(http.HandlerFunc(h.ListInstallations)))
	mux.Handle("GET /orgs/{orgId}/github/repos", auth(http.HandlerFunc(h.ListAvailableRepos)))

	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/repos", auth(http.HandlerFunc(h.ConnectRepo)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/repos", auth(http.HandlerFunc(h.ListRepos)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/repos/{repoId}", auth(http.HandlerFunc(h.DisconnectRepo)))

	// Public webhook (HMAC verified)
	mux.HandleFunc("POST /webhooks/github", h.GitHubWebhook)

	// GitHub App setup redirect (public; state verified via Redis)
	mux.HandleFunc("GET /github/setup", h.GitHubSetup)
	mux.HandleFunc("GET /api/v1/github/setup", h.GitHubSetup)

	// Internal: commit status for deployment service
	mux.HandleFunc("POST /internal/github/commit-status", h.InternalCommitStatus)
}

func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request, orgID, userID string) bool {
	if h.OrganizationURL == "" {
		return true
	}
	url := fmt.Sprintf("%s/internal/orgs/%s/members/%s", strings.TrimRight(h.OrganizationURL, "/"), orgID, userID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "membership check failed")
		return false
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "organization service unavailable")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return false
	}
	if resp.StatusCode != http.StatusOK {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "organization service error")
		return false
	}
	return true
}

func (h *Handler) appConfigured() bool {
	return h.GitHub != nil && h.GitHub.Configured()
}

func (h *Handler) InstallStart(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	state := uuid.NewString()

	if h.appConfigured() {
		h.saveInstallState(r.Context(), state, orgID, uid)
		httpx.JSON(w, http.StatusOK, map[string]any{
			"install_url": h.GitHub.InstallURL(state),
			"state":       state,
			"mode":        "github_app",
			"message":     "Open install_url to install the GitHub App, then return to the dashboard",
		})
		return
	}

	// Stub mode for local demos without App credentials
	installURL := fmt.Sprintf("%s/api/v1/orgs/%s/github/install/callback?state=%s&stub=1",
		strings.TrimRight(h.PublicBaseURL, "/"), orgID, state)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"install_url": installURL,
		"state":       state,
		"mode":        "stub",
		"message":     "Open install_url or POST /github/install/callback to complete the stub install",
	})
}

func (h *Handler) InstallCallback(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		InstallationID string `json:"installation_id"`
		AccountLogin   string `json:"account_login"`
		State          string `json:"state"`
		SetupAction    string `json:"setup_action"`
	}
	_ = httpx.Decode(r, &req)

	if req.State != "" {
		if stOrg, _, ok := h.loadInstallState(r.Context(), req.State); ok && stOrg != orgID {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "install state org mismatch")
			return
		}
	}

	if req.InstallationID == "" {
		req.InstallationID = "stub-" + uuid.NewString()[:8]
	}
	if req.AccountLogin == "" {
		if h.appConfigured() && !strings.HasPrefix(req.InstallationID, "stub-") {
			if login, err := h.GitHub.GetInstallationAccount(r.Context(), req.InstallationID); err == nil && login != "" {
				req.AccountLogin = login
			}
		}
		if req.AccountLogin == "" {
			req.AccountLogin = "stub-github-org"
		}
	}

	inst, err := h.Store.UpsertInstallation(r.Context(), orgID, req.InstallationID, req.AccountLogin)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "installation already exists")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save installation")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"installation": inst})
}

// GitHubSetup handles GitHub App setup_url redirect: ?installation_id=&setup_action=&state=
func (h *Handler) GitHubSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	installationID := strings.TrimSpace(q.Get("installation_id"))
	state := strings.TrimSpace(q.Get("state"))
	setupAction := strings.TrimSpace(q.Get("setup_action"))

	dash := strings.TrimRight(h.DashboardURL, "/")
	if dash == "" {
		dash = "http://localhost:3000"
	}

	if installationID == "" {
		http.Redirect(w, r, dash+"/git?error=missing_installation", http.StatusFound)
		return
	}

	orgID, userID, ok := h.loadInstallState(r.Context(), state)
	if !ok || orgID == "" {
		// Still try to show success page asking user to complete from dashboard
		http.Redirect(w, r, fmt.Sprintf("%s/git?installation_id=%s&setup_action=%s&error=missing_state",
			dash, url.QueryEscape(installationID), url.QueryEscape(setupAction)), http.StatusFound)
		return
	}

	login := ""
	if h.appConfigured() {
		if l, err := h.GitHub.GetInstallationAccount(r.Context(), installationID); err == nil {
			login = l
		}
	}
	if login == "" {
		login = "github"
	}
	_, err := h.Store.UpsertInstallation(r.Context(), orgID, installationID, login)
	if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
		http.Redirect(w, r, dash+"/git?error=save_failed", http.StatusFound)
		return
	}
	_ = userID
	http.Redirect(w, r, dash+"/git?installed=1", http.StatusFound)
}

func (h *Handler) saveInstallState(ctx context.Context, state, orgID, userID string) {
	if h.Redis == nil || state == "" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"org_id": orgID, "user_id": userID})
	_ = h.Redis.Set(ctx, "jp:gh:install:"+state, payload, installStateTTL).Err()
}

func (h *Handler) loadInstallState(ctx context.Context, state string) (orgID, userID string, ok bool) {
	if h.Redis == nil || state == "" {
		return "", "", false
	}
	raw, err := h.Redis.Get(ctx, "jp:gh:install:"+state).Bytes()
	if err != nil {
		return "", "", false
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil {
		return "", "", false
	}
	_ = h.Redis.Del(ctx, "jp:gh:install:"+state).Err()
	return m["org_id"], m["user_id"], m["org_id"] != ""
}

func (h *Handler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.ListInstallations(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list installations")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"installations": list})
}

// ListAvailableRepos prefers installation repos, then GITHUB_TOKEN, then mock.
func (h *Handler) ListAvailableRepos(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}

	if h.appConfigured() {
		if inst, err := h.Store.LatestInstallation(r.Context(), orgID); err == nil {
			if repos, err := h.GitHub.ListInstallationRepos(r.Context(), inst.InstallationID); err == nil {
				out := make([]map[string]any, 0, len(repos))
				for _, repo := range repos {
					out = append(out, map[string]any{
						"full_name": repo.FullName, "clone_url": repo.CloneURL,
						"default_branch": repo.DefaultBranch, "private": repo.Private,
					})
				}
				httpx.JSON(w, http.StatusOK, map[string]any{"repos": out, "mode": "github_app"})
				return
			}
		}
	}

	if repos, ok := h.fetchGitHubRepos(r); ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"repos": repos, "mode": "github"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"mode": "mock",
		"repos": []map[string]any{
			{"full_name": "acme/web-app", "clone_url": "https://github.com/acme/web-app.git", "default_branch": "main", "private": false},
			{"full_name": "acme/api-go", "clone_url": "https://github.com/acme/api-go.git", "default_branch": "main", "private": true},
			{"full_name": "acme/dashboard", "clone_url": "https://github.com/acme/dashboard.git", "default_branch": "main", "private": false},
			{"full_name": "acme/fastapi-svc", "clone_url": "https://github.com/acme/fastapi-svc.git", "default_branch": "main", "private": false},
		},
	})
}

func (h *Handler) fetchGitHubRepos(r *http.Request) ([]map[string]any, bool) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" || h.HTTP == nil {
		return nil, false
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/user/repos?per_page=30&sort=updated", nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := h.HTTP.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, false
	}
	defer resp.Body.Close()
	var raw []struct {
		FullName      string `json:"full_name"`
		CloneURL      string `json:"clone_url"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, false
	}
	out := make([]map[string]any, 0, len(raw))
	for _, repo := range raw {
		out = append(out, map[string]any{
			"full_name": repo.FullName, "clone_url": repo.CloneURL,
			"default_branch": repo.DefaultBranch, "private": repo.Private,
		})
	}
	return out, true
}

func (h *Handler) ConnectRepo(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		FullName       string `json:"full_name"`
		CloneURL       string `json:"clone_url"`
		DefaultBranch  string `json:"default_branch"`
		InstallationID string `json:"installation_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.FullName = strings.TrimSpace(req.FullName)
	if req.FullName == "" || !strings.Contains(req.FullName, "/") {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "full_name required (owner/repo)")
		return
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.CloneURL == "" {
		req.CloneURL = "https://github.com/" + req.FullName + ".git"
	}
	// Prefer GitHub installation_id from org if caller omitted it
	if req.InstallationID == "" {
		if inst, err := h.Store.LatestInstallation(r.Context(), orgID); err == nil {
			req.InstallationID = inst.InstallationID
		}
	}
	secret := h.WebhookSecret
	if secret == "" {
		secret = "dev-webhook-secret"
	}
	repo, err := h.Store.ConnectRepo(r.Context(), orgID, projectID, req.InstallationID, req.FullName, req.CloneURL, req.DefaultBranch, secret)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "repo already connected")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to connect repo")
		return
	}
	h.emit(r, events.TopicDeploy, events.TypeRepoConnected, uid, orgID, map[string]any{
		"repo_id": repo.ID, "project_id": projectID, "full_name": repo.FullName,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"repo": repo})
}

func (h *Handler) ListRepos(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.ListRepos(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list repos")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repos": list})
}

func (h *Handler) DisconnectRepo(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	repoID := r.PathValue("repoId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if err := h.Store.DeleteRepo(r.Context(), orgID, projectID, repoID); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to disconnect repo")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalCommitStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName       string `json:"full_name"`
		SHA            string `json:"sha"`
		State          string `json:"state"`
		Description    string `json:"description"`
		TargetURL      string `json:"target_url"`
		InstallationID string `json:"installation_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.FullName = strings.TrimSpace(req.FullName)
	req.SHA = strings.TrimSpace(req.SHA)
	if req.FullName == "" || req.SHA == "" || req.SHA == "HEAD" {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "missing sha or repo"})
		return
	}
	if !h.appConfigured() {
		if h.Log != nil {
			h.Log.Info("commit status skipped (app not configured)", "repo", req.FullName, "sha", req.SHA, "state", req.State)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "app_not_configured"})
		return
	}
	instID := req.InstallationID
	if instID == "" {
		if id, err := h.Store.ResolveInstallationIDForRepo(r.Context(), req.FullName, ""); err == nil {
			instID = id
		}
	}
	if instID == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "no_installation"})
		return
	}
	desc := req.Description
	if desc == "" {
		desc = "jp deployment " + req.State
	}
	if err := h.GitHub.CreateCommitStatus(r.Context(), instID, req.FullName, req.SHA, req.State, desc, req.TargetURL); err != nil {
		if h.Log != nil {
			h.Log.Warn("commit status failed", "err", err, "repo", req.FullName, "sha", req.SHA)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "failed to read body")
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	secret := h.WebhookSecret
	if secret == "" {
		secret = "dev-webhook-secret"
	}
	if !verifyGitHubSignature(secret, body, sig) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid webhook signature")
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	switch event {
	case "ping":
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "pong": true})
		return
	case "push":
		h.handlePushWebhook(w, r, body)
		return
	case "pull_request":
		h.handlePullRequestWebhook(w, r, body)
		return
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": event})
	}
}

func (h *Handler) handlePushWebhook(w http.ResponseWriter, r *http.Request, body []byte) {
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName      string `json:"full_name"`
			CloneURL      string `json:"clone_url"`
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
		HeadCommit struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if payload.Deleted || strings.HasPrefix(payload.Ref, "refs/tags/") {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "deleted_or_tag"})
		return
	}
	repos, err := h.Store.FindReposByFullName(r.Context(), payload.Repository.FullName)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	sha := payload.After
	if sha == "" || sha == "0000000000000000000000000000000000000000" {
		sha = payload.HeadCommit.ID
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	created := 0
	for _, repo := range repos {
		defaultBranch := repo.DefaultBranch
		if defaultBranch == "" {
			defaultBranch = payload.Repository.DefaultBranch
		}
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		isPreview := !strings.EqualFold(branch, defaultBranch)
		gitBranch := branch
		message := payload.HeadCommit.Message
		source := "webhook"
		if isPreview {
			gitBranch = "preview/" + branch
			if !strings.Contains(strings.ToLower(message), "[preview]") {
				message = "[preview] " + message
			}
			source = "webhook-preview"
		}
		if h.createDeployFromGit(r.Context(), repo, sha, gitBranch,
			firstNonEmpty(payload.Repository.CloneURL, repo.CloneURL), message, source) {
			created++
		}
	}
	h.emit(r, events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{
		"full_name": payload.Repository.FullName, "sha": sha, "branch": branch,
		"repos_matched": len(repos), "deployments_created": created,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "repos_matched": len(repos), "deployments_created": created, "received_at": time.Now().UTC(),
	})
}

func (h *Handler) handlePullRequestWebhook(w http.ResponseWriter, r *http.Request, body []byte) {
	var payload struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Title string `json:"title"`
			Head  struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
			HTMLURL string `json:"html_url"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	switch payload.Action {
	case "opened", "synchronize", "reopened":
		// create preview deploy
	case "closed":
		h.teardownPreviewPR(w, r, payload.Repository.FullName, payload.Number)
		return
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": payload.Action})
		return
	}
	repos, err := h.Store.FindReposByFullName(r.Context(), payload.Repository.FullName)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	sha := payload.PullRequest.Head.SHA
	gitBranch := fmt.Sprintf("preview/pr-%d", payload.Number)
	message := fmt.Sprintf("[preview] PR #%d: %s", payload.Number, payload.PullRequest.Title)
	created := 0
	for _, repo := range repos {
		if h.createDeployFromGit(r.Context(), repo, sha, gitBranch,
			firstNonEmpty(payload.Repository.CloneURL, repo.CloneURL), message, "webhook-preview") {
			created++
		}
	}
	h.emit(r, events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{
		"full_name": payload.Repository.FullName, "sha": sha, "branch": gitBranch,
		"pr": payload.Number, "repos_matched": len(repos), "deployments_created": created, "preview": true,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "preview": true, "pr": payload.Number,
		"repos_matched": len(repos), "deployments_created": created,
	})
}

func (h *Handler) teardownPreviewPR(w http.ResponseWriter, r *http.Request, fullName string, prNumber int) {
	gitBranch := fmt.Sprintf("preview/pr-%d", prNumber)
	repos, err := h.Store.FindReposByFullName(r.Context(), fullName)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	torn := 0
	if h.DeploymentURL != "" && h.HTTP != nil {
		for _, repo := range repos {
			body, _ := json.Marshal(map[string]any{
				"org_id": repo.OrgID, "project_id": repo.ProjectID,
				"full_name": fullName, "git_branch": gitBranch,
			})
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
				strings.TrimRight(h.DeploymentURL, "/")+"/internal/preview/teardown",
				bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.HTTP.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				torn++
			}
		}
	}
	h.emit(r, events.TopicDeploy, events.TypeGitPush, "", "", map[string]any{
		"full_name": fullName, "branch": gitBranch, "pr": prNumber,
		"teardown": true, "repos_matched": len(repos), "teardown_calls": torn,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "teardown": true, "pr": prNumber, "git_branch": gitBranch,
		"repos_matched": len(repos), "teardown_calls": torn,
	})
}

func (h *Handler) createDeployFromGit(ctx context.Context, repo store.Repo, sha, branch, cloneURL, message, source string) bool {
	if h.DeploymentURL == "" || h.HTTP == nil {
		return false
	}
	reqBody, _ := json.Marshal(map[string]any{
		"org_id":     repo.OrgID,
		"project_id": repo.ProjectID,
		"repo_id":    repo.ID,
		"git_sha":    sha,
		"git_branch": branch,
		"clone_url":  cloneURL,
		"full_name":  repo.FullName,
		"message":    message,
		"source":     source,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(h.DeploymentURL, "/")+"/internal/deployments/from-git",
		strings.NewReader(string(reqBody)))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 300
}

func (h *Handler) emit(r *http.Request, topic, typ, actorID, orgID string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(topic, typ, actorID, orgID, payload)
	_, _ = events.PublishJSON(r.Context(), h.Redis, topic, env)
}

func verifyGitHubSignature(secret string, body []byte, header string) bool {
	if header == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
