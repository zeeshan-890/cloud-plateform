package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/repository/internal/store"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	DeploymentURL   string
	WebhookSecret   string
	PublicBaseURL   string
	HTTP            *http.Client
	Redis           *redis.Client
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

func (h *Handler) InstallStart(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	state := uuid.NewString()
	// Stub GitHub App install URL (no real OAuth app required in Phase 2 MVP).
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
	}
	_ = httpx.Decode(r, &req)
	if req.InstallationID == "" {
		req.InstallationID = "stub-" + uuid.NewString()[:8]
	}
	if req.AccountLogin == "" {
		req.AccountLogin = "stub-github-org"
	}
	inst, err := h.Store.CreateInstallation(r.Context(), orgID, req.InstallationID, req.AccountLogin)
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

// ListAvailableRepos returns mock GitHub repos, or live list when GITHUB_TOKEN is set.
func (h *Handler) ListAvailableRepos(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
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

func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
	if event != "push" && event != "ping" {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": event})
		return
	}
	if event == "ping" {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "pong": true})
		return
	}

	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
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
	repos, err := h.Store.FindReposByFullName(r.Context(), payload.Repository.FullName)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	sha := payload.After
	if sha == "" {
		sha = payload.HeadCommit.ID
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	created := 0
	for _, repo := range repos {
		if h.DeploymentURL == "" {
			continue
		}
		reqBody, _ := json.Marshal(map[string]any{
			"org_id":     repo.OrgID,
			"project_id": repo.ProjectID,
			"repo_id":    repo.ID,
			"git_sha":    sha,
			"git_branch": branch,
			"clone_url":  firstNonEmpty(payload.Repository.CloneURL, repo.CloneURL),
			"full_name":  repo.FullName,
			"message":    payload.HeadCommit.Message,
			"source":     "webhook",
		})
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			strings.TrimRight(h.DeploymentURL, "/")+"/internal/deployments/from-git",
			strings.NewReader(string(reqBody)))
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
