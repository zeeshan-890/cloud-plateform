package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/repository/internal/cd"
	"github.com/jp-cloud/repository/internal/store"
)

// cdRequest carries the optional continuous-deployment settings of a connect / update request.
// A nil field keeps the current value, so the same shape serves POST (defaults) and PATCH.
type cdRequest struct {
	RootDir        *string   `json:"root_dir"`
	AutoDeploy     *bool     `json:"auto_deploy"`
	DeployBranch   *string   `json:"deploy_branch"`
	BranchPreviews *bool     `json:"branch_previews"`
	PRPreviews     *bool     `json:"pr_previews"`
	WatchPaths     *[]string `json:"watch_paths"`
	IgnorePaths    *[]string `json:"ignore_paths"`
	RequireCI      *bool     `json:"require_ci"`
}

// apply merges the request onto base and validates the result.
func (q cdRequest) apply(base cd.Settings) (cd.Settings, error) {
	s := base
	var err error
	if q.RootDir != nil {
		if s.RootDir, err = cd.CleanRootDir(*q.RootDir); err != nil {
			return s, err
		}
	}
	if q.DeployBranch != nil {
		b := strings.TrimSpace(*q.DeployBranch)
		if b != "" && (!branchRe.MatchString(b) || strings.HasPrefix(b, "-") || strings.Contains(b, "..")) {
			return s, errors.New("invalid deploy_branch")
		}
		s.DeployBranch = b
	}
	if q.WatchPaths != nil {
		if s.WatchPaths, err = cd.CleanPatterns(*q.WatchPaths); err != nil {
			return s, err
		}
	}
	if q.IgnorePaths != nil {
		if s.IgnorePaths, err = cd.CleanPatterns(*q.IgnorePaths); err != nil {
			return s, err
		}
	}
	if q.AutoDeploy != nil {
		s.AutoDeploy = *q.AutoDeploy
	}
	if q.BranchPreviews != nil {
		s.BranchPreviews = *q.BranchPreviews
	}
	if q.PRPreviews != nil {
		s.PRPreviews = *q.PRPreviews
	}
	if q.RequireCI != nil {
		s.RequireCI = *q.RequireCI
	}
	if s.WatchPaths == nil {
		s.WatchPaths = []string{}
	}
	if s.IgnorePaths == nil {
		s.IgnorePaths = []string{}
	}
	return s, nil
}

func (h *Handler) webhookURL() string { return strings.TrimRight(h.PublicBaseURL, "/") + "/api/v1/webhooks/github" }

// webhookURLFor is the hook URL of ONE connection. The connection id selects whose secret verifies the delivery, so
// several projects of one repository (a monorepo) can each have their own hook with their own secret on GitHub.
func (h *Handler) webhookURLFor(connectionID string) string { return h.webhookURL() + "?c=" + connectionID }

// UpdateRepoSettings (admin+) changes the continuous-deployment settings of one connection.
func (h *Handler) UpdateRepoSettings(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	var req cdRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	cur, err := h.Store.GetRepo(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load repo")
		return
	}
	next, err := req.apply(cur.CD())
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	repo, err := h.Store.UpdateCD(r.Context(), orgID, projectID, id, next)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update settings")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repo": repo})
}

// RotateWebhookSecret (admin+) issues a new webhook secret, shown exactly once. A webhook configured with the old one stops working.
func (h *Handler) RotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	repo, err := h.Store.GetRepo(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load repo")
		return
	}
	secret := randHex(32)
	if err := h.Store.SetWebhookSecret(r.Context(), orgID, projectID, id, secret); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to rotate the webhook secret")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"repo": repo, "webhook_secret": secret, "webhook_url": h.webhookURLFor(id),
		"webhook_note": "Update the secret of the GitHub webhook (or press Install webhook again). It cannot be shown again.",
	})
}

// gh performs one GitHub API call (token optional) and returns status and body.
func (h *Handler) gh(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.api()+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := h.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, nil
}

// ciEvents are the GitHub events JP Cloud needs: pushes and PRs deploy, check suites / workflow runs release "wait for CI".
var ciEvents = []string{"push", "pull_request", "check_suite", "workflow_run"}

// InstallWebhook (admin+) creates (or updates) the repository webhook on GitHub with this connection's secret.
// Only connections that hold a personal token can do this; GitHub App connections receive events through the App.
func (h *Handler) InstallWebhook(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	repo, token, err := h.Store.Credentials(r.Context(), id)
	if err != nil || repo.OrgID != orgID || repo.ProjectID != projectID {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	switch repo.AuthType {
	case "app":
		httpx.Error(w, http.StatusBadRequest, "validation_error", "this repository is connected through the GitHub App: its events already arrive through the App, no webhook is needed")
		return
	case "token":
	default:
		httpx.Error(w, http.StatusBadRequest, "validation_error", "automatic webhook creation needs a connection with an access token; add the webhook on GitHub manually with the secret shown when connecting (or rotate it to see a new one)")
		return
	}
	secret, err := h.Store.WebhookSecretOf(r.Context(), orgID, projectID, id)
	if err != nil || secret == "" {
		httpx.Error(w, http.StatusInternalServerError, "internal", "webhook secret unavailable: rotate it first")
		return
	}
	hookURL := h.webhookURLFor(id)
	spec := map[string]any{"name": "web", "active": true, "events": ciEvents,
		"config": map[string]any{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"}}

	denied := func(code int) bool {
		if code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnauthorized {
			httpx.Error(w, http.StatusBadRequest, "validation_error",
				fmt.Sprintf("GitHub refused (%d): the token needs admin access to %s (classic token scope admin:repo_hook, or a fine-grained token with Webhooks: read and write)", code, repo.FullName))
			return true
		}
		return false
	}
	code, body, err := h.gh(r.Context(), http.MethodGet, "/repos/"+repo.FullName+"/hooks?per_page=100", token, nil)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
		return
	}
	if denied(code) {
		return
	}
	if code != http.StatusOK {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", fmt.Sprintf("GitHub answered %d while listing webhooks", code))
		return
	}
	var hooks []struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	_ = json.Unmarshal(body, &hooks)
	var existing int64
	for _, hk := range hooks {
		if strings.EqualFold(strings.TrimRight(hk.Config.URL, "/"), hookURL) {
			existing = hk.ID
			break
		}
	}
	created := existing == 0
	method, path := http.MethodPost, "/repos/"+repo.FullName+"/hooks"
	if !created {
		method, path = http.MethodPatch, fmt.Sprintf("/repos/%s/hooks/%d", repo.FullName, existing)
	}
	code, body, err = h.gh(r.Context(), method, path, token, spec)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
		return
	}
	if denied(code) {
		return
	}
	if code != http.StatusCreated && code != http.StatusOK {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", fmt.Sprintf("GitHub answered %d: %s", code, truncate(string(body), 200)))
		return
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &out)
	httpx.JSON(w, http.StatusOK, map[string]any{"installed": true, "created": created, "hook_id": out.ID, "events": ciEvents, "webhook_url": hookURL})
}

var folderMarkers = map[string]bool{"Dockerfile": true, "package.json": true, "go.mod": true, "requirements.txt": true, "pom.xml": true,
	"build.gradle": true, "Cargo.toml": true, "composer.json": true, "Gemfile": true, "jp.yaml": true, "jp.yml": true, "pyproject.toml": true}

// RepoFolders (admin+) lists the sub-folders of one directory of the repository so a root directory can be picked
// instead of typed. It uses the connection's own credentials (token, GitHub App) or none for public repositories.
func (h *Handler) RepoFolders(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	repo, token, err := h.Store.Credentials(r.Context(), id)
	if err != nil || repo.OrgID != orgID || repo.ProjectID != projectID {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	dir, err := cd.CleanRootDir(r.URL.Query().Get("path"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref != "" && (!branchRe.MatchString(ref) || strings.HasPrefix(ref, "-") || strings.Contains(ref, "..")) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid ref")
		return
	}
	if ref == "" {
		ref = repo.CD().Branch(repo.DefaultBranch)
	}
	switch repo.AuthType {
	case "app":
		if repo.InstallationID == nil || !h.appConfigured() {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub App unavailable")
			return
		}
		if token, err = h.GitHub.InstallationToken(r.Context(), *repo.InstallationID); err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "cannot mint installation token")
			return
		}
	case "deploy_key":
		httpx.Error(w, http.StatusBadRequest, "validation_error", "folder browsing needs an access token or the GitHub App (a deploy key cannot read the GitHub API); type the folder instead")
		return
	}
	path := "/repos/" + repo.FullName + "/contents"
	if dir != "" {
		segs := strings.Split(dir, "/")
		for i := range segs {
			segs[i] = url.PathEscape(segs[i])
		}
		path += "/" + strings.Join(segs, "/")
	}
	code, body, err := h.gh(r.Context(), http.MethodGet, path+"?ref="+url.QueryEscape(ref), token, nil)
	// the production branch may not exist (yet): browse the default branch instead of failing
	if err == nil && code == http.StatusNotFound && r.URL.Query().Get("ref") == "" && repo.DefaultBranch != "" && ref != repo.DefaultBranch {
		if c2, b2, e2 := h.gh(r.Context(), http.MethodGet, path+"?ref="+url.QueryEscape(repo.DefaultBranch), token, nil); e2 == nil && c2 == http.StatusOK {
			code, body, ref = c2, b2, repo.DefaultBranch
		}
	}
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
		return
	}
	switch {
	case code == http.StatusNotFound:
		httpx.Error(w, http.StatusNotFound, "not_found", fmt.Sprintf("%q does not exist on branch %s (or the connection cannot read it)", firstNonEmpty(dir, "the repository root"), ref))
		return
	case code != http.StatusOK:
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", fmt.Sprintf("GitHub answered %d", code))
		return
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", fmt.Sprintf("%q is a file, not a folder", dir))
		return
	}
	folders := []map[string]string{}
	markers := []string{}
	for _, e := range entries {
		switch {
		case e.Type == "dir" && e.Name != ".git" && len(folders) < 200:
			p := e.Name
			if dir != "" {
				p = dir + "/" + e.Name
			}
			folders = append(folders, map[string]string{"name": e.Name, "path": p})
		case e.Type == "file" && folderMarkers[e.Name]:
			markers = append(markers, e.Name)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": dir, "ref": ref, "folders": folders, "markers": markers})
}
