package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/ai/internal/crypt"
	"github.com/jp-cloud/ai/internal/llm"
	"github.com/jp-cloud/ai/internal/redact"
	"github.com/jp-cloud/ai/internal/store"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
)

// Authz is implemented by *membership.Client.
type Authz interface {
	Require(w http.ResponseWriter, r *http.Request, orgID, userID string, min membership.Role) (membership.Role, bool)
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

// Chatter is implemented by *llm.Client.
type Chatter interface {
	Chat(ctx context.Context, cfg llm.Config, system, user string) (string, error)
}

// Limiter rate limits LLM calls per organisation.
type Limiter interface {
	Allow(ctx context.Context, orgID string) (bool, error)
}

// Tools are the platform services the assistant reads project data from.
type Tools struct {
	HTTP          *http.Client
	BuildURL      string
	DeploymentURL string
	RuntimeURL    string
	LoggingURL    string
}

type Handler struct {
	Store        store.Store
	Box          *crypt.Box // nil: per-org keys cannot be stored
	LLM          Chatter
	Limit        Limiter
	Authz        Authz
	JWT          *jwtutil.Manager
	Platform     llm.Config // fallback when an org has no configuration (APIKey may be empty)
	AllowPrivate bool       // AI_ALLOW_PRIVATE_BASE_URL
	Tools        Tools
	Log          *slog.Logger
}

const (
	maxPrompt  = 4000
	maxLogs    = 8000
	maxContext = 20000
)

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	p := "/orgs/{orgId}/projects/{projectId}/ai"
	mux.Handle("GET /orgs/{orgId}/ai/status", auth(http.HandlerFunc(h.Status)))
	mux.Handle("GET /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.GetConfig)))
	mux.Handle("POST /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.SetConfig)))
	mux.Handle("PUT /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.SetConfig)))
	mux.Handle("DELETE /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.DeleteConfig)))
	mux.Handle("POST /orgs/{orgId}/ai/ask", auth(http.HandlerFunc(h.Ask)))
	mux.Handle("POST "+p+"/explain", auth(http.HandlerFunc(h.Explain)))
	mux.Handle("POST "+p+"/analyze-logs", auth(http.HandlerFunc(h.AnalyzeLogs)))
	mux.Handle("POST "+p+"/overview-suggestions", auth(http.HandlerFunc(h.OverviewSuggestions)))
}

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

// ------------------------------------------------------------------ configuration

type resolved struct {
	cfg    llm.Config
	source string // org | platform | none
}

func (h *Handler) resolve(ctx context.Context, orgID string) (resolved, error) {
	c, err := h.Store.Get(ctx, orgID)
	if err == nil && len(c.EncryptedKey) > 0 {
		if h.Box == nil {
			return resolved{}, crypt.ErrNoKey
		}
		key, err := h.Box.Open(orgID, c.EncryptedKey)
		if err != nil {
			return resolved{}, err
		}
		return resolved{cfg: llm.Config{Provider: c.Provider, Model: c.Model, APIKey: key, BaseURL: c.BaseURL}, source: "org"}, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return resolved{}, err
	}
	if h.Platform.APIKey != "" {
		return resolved{cfg: h.Platform, source: "platform"}, nil
	}
	return resolved{source: "none"}, nil
}

func mask4(k string) string {
	if len(k) < 8 {
		return ""
	}
	return k[len(k)-4:]
}

func (h *Handler) configView(ctx context.Context, orgID string) map[string]any {
	out := map[string]any{"provider": "", "model": "", "has_key": false, "key_last4": "", "source": "none",
		"platform_fallback": h.Platform.APIKey != "", "can_store_keys": h.Box != nil,
		"providers": []string{llm.ProviderGemini, llm.ProviderOpenAI}}
	if c, err := h.Store.Get(ctx, orgID); err == nil {
		out["provider"], out["model"], out["base_url"] = c.Provider, c.Model, c.BaseURL
		out["has_key"] = len(c.EncryptedKey) > 0
		out["key_last4"] = c.KeyLast4
		if len(c.EncryptedKey) > 0 {
			out["source"] = "org"
		}
	}
	if out["source"] == "none" && h.Platform.APIKey != "" {
		out["source"], out["provider"], out["model"] = "platform", h.Platform.Provider, h.Platform.Model
	}
	if out["source"] == "none" {
		out["mode"] = "heuristic"
	} else {
		out["mode"] = "llm"
	}
	return out
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, middleware.UserIDFrom(r.Context()), membership.Viewer); !ok {
		return
	}
	v := h.configView(r.Context(), orgID)
	v["has_key"] = v["source"] != "none"
	v["tools"] = []string{"build_logs", "deployments", "runtime_logs"}
	httpx.JSON(w, http.StatusOK, v)
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, middleware.UserIDFrom(r.Context()), membership.Viewer); !ok {
		return
	}
	v := h.configView(r.Context(), orgID)
	if v["key_last4"] != "" {
		v["masked_key"] = "****" + v["key_last4"].(string)
	}
	httpx.JSON(w, http.StatusOK, v)
}

// SetConfig stores the org's provider/model/key. Admin+ only; the key is encrypted and never returned.
func (h *Handler) SetConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	uid := middleware.UserIDFrom(r.Context())
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, uid, membership.Admin); !ok {
		return
	}
	var req struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		APIKey    string `json:"api_key"`
		BaseURL   string `json:"base_url"`
		GeminiKey string `json:"gemini_key"` // legacy field
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "invalid JSON")
		return
	}
	existing, getErr := h.Store.Get(r.Context(), orgID)
	have := getErr == nil
	if req.APIKey == "" && req.GeminiKey != "" {
		req.APIKey = req.GeminiKey
		if req.Provider == "" {
			req.Provider = llm.ProviderGemini
		}
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" && have {
		provider = existing.Provider
	}
	if provider == "" {
		provider = llm.ProviderGemini
	}
	if !llm.ValidProvider(provider) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "provider must be gemini or openai")
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		if have && existing.Provider == provider && existing.Model != "" {
			model = existing.Model
		} else {
			model = llm.DefaultModel(provider)
		}
	}
	if !llm.ValidModel(model) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid model name")
		return
	}
	baseURL := ""
	if provider == llm.ProviderOpenAI {
		b, err := llm.ValidateBaseURL(firstNonEmpty(req.BaseURL, func() string {
			if have && existing.Provider == provider {
				return existing.BaseURL
			}
			return ""
		}()), h.AllowPrivate)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		baseURL = b
	}
	cfg := &store.Config{OrgID: orgID, Provider: provider, Model: model, BaseURL: baseURL, UpdatedBy: uid}
	key := strings.TrimSpace(req.APIKey)
	switch {
	case key != "":
		if len(key) < 8 || len(key) > 512 || strings.ContainsAny(key, " \t\r\n\x00") {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "api_key looks invalid")
			return
		}
		if h.Box == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "encryption_unconfigured", "the platform has no AI_ENC_KEY/SECRETS_MASTER_KEY; organisation keys cannot be stored")
			return
		}
		sealed, err := h.Box.Seal(orgID, key)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "encryption failed")
			return
		}
		cfg.EncryptedKey, cfg.KeyLast4 = sealed, mask4(key)
	case have && existing.Provider == provider:
		cfg.EncryptedKey, cfg.KeyLast4 = existing.EncryptedKey, existing.KeyLast4 // keep the key, change model only
	case have:
		httpx.Error(w, http.StatusBadRequest, "validation_error", "changing the provider requires a new api_key")
		return
	}
	if err := h.Store.Put(r.Context(), cfg); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save configuration")
		return
	}
	v := h.configView(r.Context(), orgID)
	v["status"] = "updated"
	httpx.JSON(w, http.StatusOK, v)
}

func (h *Handler) DeleteConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, middleware.UserIDFrom(r.Context()), membership.Admin); !ok {
		return
	}
	if err := h.Store.Delete(r.Context(), orgID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ------------------------------------------------------------------ completions

type answer struct {
	Text    string
	Mode    string // llm | heuristic
	Source  string
	Model   string
	Prov    string
	Warning string
}

func (a answer) meta(m map[string]any) map[string]any {
	m["mode"], m["source"], m["model"], m["provider"] = a.Mode, a.Source, a.Model, a.Prov
	if a.Warning != "" {
		m["warning"] = a.Warning
	}
	if a.Mode == "heuristic" {
		m["notice"] = "Keyword based analysis - no AI model answered. Configure a provider in Settings > AI."
	}
	return m
}

// complete runs the LLM (when configured) with redacted input and falls back to a clearly labelled heuristic.
func (h *Handler) complete(ctx context.Context, orgID, system, user, heuristicInput string) (answer, error) {
	res, err := h.resolve(ctx, orgID)
	if err != nil {
		h.log().Error("ai config", "org", orgID, "err", err)
		return answer{}, err
	}
	if res.source == "none" {
		return answer{Text: llm.HeuristicExplain(heuristicInput), Mode: "heuristic", Source: "none"}, nil
	}
	if h.Limit != nil {
		ok, err := h.Limit.Allow(ctx, orgID)
		if err != nil {
			return answer{}, fmt.Errorf("rate limiter unavailable")
		}
		if !ok {
			return answer{}, errRateLimited
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	text, err := h.LLM.Chat(cctx, res.cfg, system, user)
	if err != nil || strings.TrimSpace(text) == "" {
		msg := "empty response"
		if err != nil {
			msg = err.Error()
		}
		h.log().Warn("llm call failed", "org", orgID, "provider", res.cfg.Provider, "err", msg)
		return answer{Text: llm.HeuristicExplain(heuristicInput), Mode: "heuristic", Source: res.source,
			Model: res.cfg.Model, Prov: res.cfg.Provider, Warning: "the AI provider failed: " + redact.Text(msg)}, nil
	}
	return answer{Text: text, Mode: "llm", Source: res.source, Model: res.cfg.Model, Prov: res.cfg.Provider}, nil
}

var errRateLimited = errors.New("rate limited")

func (h *Handler) writeCompleteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRateLimited):
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "AI request limit reached for this organisation; try again in a minute")
	case errors.Is(err, crypt.ErrNoKey):
		httpx.Error(w, http.StatusServiceUnavailable, "encryption_unconfigured", err.Error())
	default:
		httpx.Error(w, http.StatusBadGateway, "ai_error", err.Error())
	}
}

// project authorizes a project-scoped AI endpoint on the given area (logs:view for log analysis).
func (h *Handler) project(w http.ResponseWriter, r *http.Request, area membership.Area) (orgID, projectID string, acc *membership.Access, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	acc, ok = h.Authz.RequireArea(w, r, orgID, projectID, middleware.UserIDFrom(r.Context()), area, membership.LevelView)
	return
}

func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, middleware.UserIDFrom(r.Context()), membership.Viewer); !ok {
		return
	}
	var req struct {
		Prompt    string `json:"prompt"`
		ProjectID string `json:"project_id"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "prompt required")
		return
	}
	ctxBundle := "(no project selected)"
	if req.ProjectID != "" {
		if !validID(req.ProjectID) {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project_id")
			return
		}
		// the project must belong to this org and the caller must be allowed to see it
		acc, ok := h.Authz.RequireArea(w, r, orgID, req.ProjectID, middleware.UserIDFrom(r.Context()), membership.AreaLogs, membership.LevelView)
		if !ok {
			return
		}
		ctxBundle = h.gather(r, orgID, req.ProjectID, acc, "", "")
	}
	system := "You are the assistant of a self-hosted cloud platform. Diagnose issues and suggest concrete fixes based only on the supplied context. Be concise."
	user := redact.Prepare(req.Prompt, maxPrompt) + "\n\n--- project context ---\n" + ctxBundle
	a, err := h.complete(r.Context(), orgID, system, user, ctxBundle)
	if err != nil {
		h.writeCompleteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.meta(map[string]any{"answer": a.Text, "context": redact.Truncate(ctxBundle, 4000)}))
}

func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, acc, ok := h.project(w, r, membership.AreaLogs)
	if !ok {
		return
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
		BuildID      string `json:"build_id"`
		Prompt       string `json:"prompt"`
	}
	_ = httpx.Decode(r, &req)
	if (req.DeploymentID != "" && !validID(req.DeploymentID)) || (req.BuildID != "" && !validID(req.BuildID)) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid deployment_id or build_id")
		return
	}
	ctxBundle := h.gather(r, orgID, projectID, acc, req.DeploymentID, req.BuildID)
	prompt := firstNonEmpty(req.Prompt, "Explain why this deployment or build failed and how to resolve it.")
	system := "You are a DevOps diagnostic assistant. Analyse build steps, error logs and container state to find the root cause and give precise resolution steps."
	a, err := h.complete(r.Context(), orgID, system, redact.Prepare(prompt, maxPrompt)+"\n\n--- platform context ---\n"+ctxBundle, ctxBundle)
	if err != nil {
		h.writeCompleteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.meta(map[string]any{"explanation": a.Text, "tools_used": []string{"build_logs", "deployments", "runtime_logs"}, "context": redact.Truncate(ctxBundle, 4000)}))
}

func (h *Handler) AnalyzeLogs(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, acc, ok := h.project(w, r, membership.AreaLogs)
	if !ok {
		return
	}
	var req struct {
		Logs    string `json:"logs"`
		Source  string `json:"source"`
		BuildID string `json:"build_id"`
	}
	_ = httpx.Decode(r, &req)
	if req.BuildID != "" && !validID(req.BuildID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid build_id")
		return
	}
	logs := req.Logs
	if strings.TrimSpace(logs) == "" {
		if req.Source == "build" && req.BuildID != "" && acc.Level(membership.AreaBuilds).AtLeast(membership.LevelView) {
			logs = h.fetchText(r, fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs", base(h.Tools.BuildURL), orgID, projectID, req.BuildID))
		} else if h.Tools.RuntimeURL != "" {
			logs = h.fetchText(r, fmt.Sprintf("%s/orgs/%s/projects/%s/runtime/logs?tail=150", base(h.Tools.RuntimeURL), orgID, projectID))
		}
	}
	logs = redact.Prepare(logs, maxLogs)
	system := "You are a cloud SRE. Analyse the application or build logs. Give: 1. Root cause 2. Exact error signatures 3. Step-by-step fix (code or env var changes). Be structured and brief."
	user := fmt.Sprintf("Analyse these logs for project %s:\n\n```\n%s\n```", projectID, logs)
	a, err := h.complete(r.Context(), orgID, system, user, logs)
	if err != nil {
		h.writeCompleteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.meta(map[string]any{"analysis": a.Text}))
}

func (h *Handler) OverviewSuggestions(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, acc, ok := h.project(w, r, membership.AreaOverview)
	if !ok {
		return
	}
	ctxBundle := h.gather(r, orgID, projectID, acc, "", "")
	system := "You are a senior SRE. Review the project state and give: 1. current health and bottlenecks 2. reliability and secrets/config suggestions 3. next improvements. Be structured and brief."
	a, err := h.complete(r.Context(), orgID, system, "Review project "+projectID+":\n\n"+redact.Truncate(ctxBundle, maxContext), ctxBundle)
	if err != nil {
		h.writeCompleteErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.meta(map[string]any{"suggestions": a.Text}))
}

// ------------------------------------------------------------------ tools (read only, ONE project)

func base(s string) string { return strings.TrimRight(s, "/") }

// gather reads deployments, build logs and runtime logs of exactly this project. Every id in the URLs is a
// validated UUID, requests are signed with the internal token and carry the caller's own Authorization so
// the target service re-checks membership. All text is redacted before it can reach an LLM.
func (h *Handler) gather(r *http.Request, orgID, projectID string, acc *membership.Access, deploymentID, buildID string) string {
	var parts []string
	pre := fmt.Sprintf("/orgs/%s/projects/%s", orgID, projectID)
	if h.Tools.DeploymentURL != "" && acc.Level(membership.AreaDeployments).AtLeast(membership.LevelView) {
		deploys := h.fetch(r, base(h.Tools.DeploymentURL)+pre+"/deployments")
		parts = append(parts, "### Recent deployments\n"+redact.Prepare(deploys, 3000))
		var wrap struct {
			Deployments []struct {
				ID      string  `json:"id"`
				Status  string  `json:"status"`
				BuildID *string `json:"build_id"`
				Error   string  `json:"error"`
			} `json:"deployments"`
		}
		if deploymentID == "" && json.Unmarshal([]byte(deploys), &wrap) == nil {
			for _, d := range wrap.Deployments {
				if d.Status == "failed" && validID(d.ID) {
					deploymentID = d.ID
					if buildID == "" && d.BuildID != nil && validID(*d.BuildID) {
						buildID = *d.BuildID
					}
					parts = append(parts, fmt.Sprintf("Selected failed deployment %s error=%s", d.ID, redact.Text(d.Error)))
					break
				}
			}
		}
	}
	if h.Tools.BuildURL != "" && acc.Level(membership.AreaBuilds).AtLeast(membership.LevelView) {
		if buildID != "" {
			logs := h.fetchText(r, base(h.Tools.BuildURL)+pre+"/builds/"+url.PathEscape(buildID)+"/logs")
			parts = append(parts, "### Build logs\n"+redact.Prepare(logs, 6000))
		} else {
			builds := h.fetch(r, base(h.Tools.BuildURL)+pre+"/builds")
			parts = append(parts, "### Recent builds\n"+redact.Prepare(builds, 3000))
		}
	}
	canLogs := acc.Level(membership.AreaLogs).AtLeast(membership.LevelView)
	if h.Tools.RuntimeURL != "" && canLogs {
		rt := h.fetch(r, base(h.Tools.RuntimeURL)+pre+"/runtime/logs?tail=60")
		parts = append(parts, "### Live container logs\n"+redact.Prepare(rt, 5000))
	} else if h.Tools.LoggingURL != "" && canLogs {
		lg := h.fetch(r, base(h.Tools.LoggingURL)+pre+"/logs?source=runtime&limit=50")
		parts = append(parts, "### Runtime logs\n"+redact.Prepare(lg, 4000))
	}
	if len(parts) == 0 {
		return "(no project data available)"
	}
	return redact.Truncate(strings.Join(parts, "\n\n"), maxContext)
}

func (h *Handler) fetch(r *http.Request, target string) string {
	hc := h.Tools.HTTP
	if hc == nil {
		hc = internalauth.NewClient(15 * time.Second)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "(error)"
	}
	if a := r.Header.Get("Authorization"); a != "" {
		req.Header.Set("Authorization", a)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "(unreachable)"
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Sprintf("(status %d)", resp.StatusCode)
	}
	return string(b)
}

func (h *Handler) fetchText(r *http.Request, target string) string {
	raw := h.fetch(r, target)
	var wrap struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err == nil && wrap.Logs != "" {
		return wrap.Logs
	}
	return raw
}
