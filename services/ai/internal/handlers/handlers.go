package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jp-cloud/ai/internal/llm"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	JWT             *jwtutil.Manager
	OrganizationURL string
	BuildURL        string
	LoggingURL      string
	DeploymentURL   string
	RuntimeURL      string
	LLM             *llm.Client
	Redis           *redis.Client
	HTTP            *http.Client
	Log             *slog.Logger
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": h.LLM.Mode()})
	})
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/ai/explain", auth(http.HandlerFunc(h.Explain)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/ai/analyze-logs", auth(http.HandlerFunc(h.AnalyzeLogs)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/ai/overview-suggestions", auth(http.HandlerFunc(h.OverviewSuggestions)))
	mux.Handle("POST /orgs/{orgId}/ai/ask", auth(http.HandlerFunc(h.Ask)))
	mux.Handle("GET /orgs/{orgId}/ai/status", auth(http.HandlerFunc(h.Status)))
	mux.Handle("GET /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.GetConfig)))
	mux.Handle("POST /orgs/{orgId}/ai/config", auth(http.HandlerFunc(h.SetConfig)))
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
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member")
		return false
	}
	return resp.StatusCode == http.StatusOK
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"mode":     h.LLM.Mode(),
		"model":    h.LLM.Model,
		"has_key":  h.LLM.HasKey(),
		"tools":    []string{"build_logs", "runtime_logs", "deploy_events", "container_logs", "docker_inspect"},
	})
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	masked := ""
	if len(h.LLM.GeminiKeys) > 0 {
		k := h.LLM.GeminiKeys[0]
		if len(k) > 10 {
			masked = k[:6] + "..." + k[len(k)-4:]
		} else {
			masked = "***"
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"mode":       h.LLM.Mode(),
		"model":      h.LLM.Model,
		"has_key":    h.LLM.HasKey(),
		"masked_key": masked,
		"provider":   "gemini",
	})
}

func (h *Handler) SetConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		GeminiKey string `json:"gemini_key"`
		Model     string `json:"model"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "invalid JSON")
		return
	}
	if req.Model == "" {
		req.Model = "gemini-3.5-flash-lite"
	}
	h.LLM.SetConfig(req.GeminiKey, "", req.Model)
	if h.Redis != nil {
		_ = h.Redis.HSet(r.Context(), "jp:ai:config", "gemini_key", req.GeminiKey, "model", req.Model).Err()
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status": "updated",
		"mode":   h.LLM.Mode(),
		"model":  h.LLM.Model,
	})
}

func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
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
	ctxBundle := h.gatherTools(r, orgID, req.ProjectID, "", "")
	userMsg := req.Prompt + "\n\n--- gathered system context ---\n" + ctxBundle
	system := "You are an intelligent Cloud Platform Assistant powered by Google Gemini 3.5 Flash Lite. Explain architecture, diagnose issues, and suggest concrete fixes based on real gathered platform state. Be concise and friendly."
	answer, mode, err := h.LLM.Chat(r.Context(), system, userMsg)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "ai_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"answer":  answer,
		"mode":    mode,
		"model":   h.LLM.Model,
		"context": truncate(ctxBundle, 4000),
	})
}

func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
		BuildID      string `json:"build_id"`
		Prompt       string `json:"prompt"`
	}
	_ = httpx.Decode(r, &req)

	ctxBundle := h.gatherTools(r, orgID, projectID, req.DeploymentID, req.BuildID)
	prompt := req.Prompt
	if prompt == "" {
		prompt = "Explain why this deployment or build failed and how to resolve it."
	}
	userMsg := prompt + "\n\n--- platform context ---\n" + ctxBundle
	system := "You are a DevOps and Deployment Diagnostic AI powered by Google Gemini 3.5 Flash Lite. Analyze build steps, error logs, and container state to identify root cause and precise resolution steps."
	answer, mode, err := h.LLM.Chat(r.Context(), system, userMsg)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "ai_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"explanation": answer,
		"mode":        mode,
		"model":       h.LLM.Model,
		"tools_used":  []string{"build_logs", "runtime_logs", "deploy_events"},
		"context":     truncate(ctxBundle, 4000),
	})
}

func (h *Handler) AnalyzeLogs(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Logs    string `json:"logs"`
		Source  string `json:"source"`
		BuildID string `json:"build_id"`
	}
	_ = httpx.Decode(r, &req)

	logs := req.Logs
	auth := r.Header.Get("Authorization")
	if strings.TrimSpace(logs) == "" {
		if req.Source == "build" && req.BuildID != "" {
			logs = h.fetchText(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs",
				strings.TrimRight(h.BuildURL, "/"), orgID, projectID, req.BuildID))
		} else if h.RuntimeURL != "" {
			logs = h.fetchJSON(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/runtime/logs?tail=150",
				strings.TrimRight(h.RuntimeURL, "/"), orgID, projectID))
		}
	}

	system := "You are an expert Cloud SRE and DevOps Engineer powered by Google Gemini 3.5 Flash Lite. " +
		"Analyze the provided live container stdout/stderr or build stream. " +
		"Identify any errors or warnings (e.g., ECONNREFUSED for databases, SMTP authentication errors, missing ENV vars, OOM, port conflicts). " +
		"Break down: 1. Root Cause Analysis 2. Exact Error Signatures 3. Step-by-Step Fix Instructions (code or env var changes). Be clear and structured."

	userMsg := fmt.Sprintf("Analyze these application logs for project %s:\n\n```\n%s\n```\nProvide a comprehensive diagnosis and fix recommendations.", projectID, truncate(logs, 8000))

	answer, mode, err := h.LLM.Chat(r.Context(), system, userMsg)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "ai_error", err.Error())
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"analysis": answer,
		"mode":     mode,
		"model":    h.LLM.Model,
	})
}

func (h *Handler) OverviewSuggestions(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}

	ctxBundle := h.gatherTools(r, orgID, projectID, "", "")

	system := "You are a Senior Cloud Architect & Site Reliability Engineer powered by Google Gemini 3.5 Flash Lite. " +
		"Review the project state (deployments, running container, listening port, domain SSL, and logs). " +
		"Produce a clean, crisp health audit: 1. Current Health & Potential Bottlenecks (highlight any warnings in logs) 2. Reliability & Secrets Configuration (suggest attached add-ons or envs if missing) 3. Next Actionable Improvements. Keep it encouraging and structured."

	userMsg := fmt.Sprintf("Review the state of project %s and provide operational suggestions:\n\n%s", projectID, truncate(ctxBundle, 8000))

	answer, mode, err := h.LLM.Chat(r.Context(), system, userMsg)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "ai_error", err.Error())
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"suggestions": answer,
		"mode":        mode,
		"model":       h.LLM.Model,
	})
}

func (h *Handler) gatherTools(r *http.Request, orgID, projectID, deploymentID, buildID string) string {
	var parts []string
	auth := r.Header.Get("Authorization")

	if projectID != "" {
		deploys := h.fetchJSON(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/deployments",
			strings.TrimRight(h.DeploymentURL, "/"), orgID, projectID))
		parts = append(parts, "### Recent deployments\n"+deploys)

		if deploymentID == "" {
			var wrap struct {
				Deployments []struct {
					ID      string  `json:"id"`
					Status  string  `json:"status"`
					BuildID *string `json:"build_id"`
					Error   string  `json:"error"`
				} `json:"deployments"`
			}
			_ = json.Unmarshal([]byte(deploys), &wrap)
			for _, d := range wrap.Deployments {
				if d.Status == "failed" {
					deploymentID = d.ID
					if buildID == "" && d.BuildID != nil {
						buildID = *d.BuildID
					}
					parts = append(parts, fmt.Sprintf("Selected failed deployment %s error=%s", d.ID, d.Error))
					break
				}
			}
		}

		if buildID != "" {
			logs := h.fetchText(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs",
				strings.TrimRight(h.BuildURL, "/"), orgID, projectID, buildID))
			parts = append(parts, "### Build logs\n"+truncate(logs, 6000))
		} else {
			builds := h.fetchJSON(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/builds",
				strings.TrimRight(h.BuildURL, "/"), orgID, projectID))
			parts = append(parts, "### Recent builds\n"+truncate(builds, 3000))
			var wrap struct {
				Builds []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"builds"`
			}
			_ = json.Unmarshal([]byte(builds), &wrap)
			for _, b := range wrap.Builds {
				if b.Status == "failed" {
					logs := h.fetchText(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs",
						strings.TrimRight(h.BuildURL, "/"), orgID, projectID, b.ID))
					parts = append(parts, "### Failed build logs\n"+truncate(logs, 6000))
					break
				}
			}
		}

		if h.RuntimeURL != "" {
			rtLogs := h.fetchJSON(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/runtime/logs?tail=60",
				strings.TrimRight(h.RuntimeURL, "/"), orgID, projectID))
			parts = append(parts, "### Live Container Logs\n"+truncate(rtLogs, 5000))
		} else {
			rtLogs := h.fetchJSON(r, auth, fmt.Sprintf("%s/orgs/%s/projects/%s/logs?source=runtime&limit=50",
				strings.TrimRight(h.LoggingURL, "/"), orgID, projectID))
			parts = append(parts, "### Runtime logs\n"+truncate(rtLogs, 4000))
		}
	}

	if h.Redis != nil {
		msgs, err := h.Redis.XRevRangeN(r.Context(), "jp.deploy", "+", "-", 20).Result()
		if err == nil && len(msgs) > 0 {
			var buf strings.Builder
			buf.WriteString("### Recent deploy stream events\n")
			for _, m := range msgs {
				buf.WriteString(fmt.Sprintf("- %s %v\n", m.ID, m.Values))
			}
			parts = append(parts, buf.String())
		}
	}

	if len(parts) == 0 {
		return "(no project context; answer generally)"
	}
	return strings.Join(parts, "\n\n")
}

func (h *Handler) fetchJSON(r *http.Request, auth, url string) string {
	if url == "" || h.HTTP == nil {
		return "(unavailable)"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return "(error)"
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "(unreachable: " + err.Error() + ")"
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Sprintf("(status %d)", resp.StatusCode)
	}
	return string(b)
}

func (h *Handler) fetchText(r *http.Request, auth, url string) string {
	raw := h.fetchJSON(r, auth, url)
	var wrap struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err == nil && wrap.Logs != "" {
		return wrap.Logs
	}
	return raw
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
