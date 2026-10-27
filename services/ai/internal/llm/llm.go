package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Client handles Gemini and OpenAI-compatible completions with multi-key pool and smart fallback.
type Client struct {
	mu            sync.RWMutex
	GeminiKeys    []string
	OpenAIKey     string
	OpenRouterKey string
	BaseURL       string
	Model         string
	HTTP          *http.Client
}

func parseKeys(val string) []string {
	if val == "" {
		return nil
	}
	raw := strings.Split(val, ",")
	var out []string
	for _, k := range raw {
		trimmed := strings.TrimSpace(k)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func NewFromEnv() *Client {
	var geminiKeys []string
	for _, envVar := range []string{"GEMINI_API_KEYS", "GEMINI_API_KEY", "GOOGLE_API_KEY", "AI_API_KEY"} {
		if k := os.Getenv(envVar); k != "" {
			geminiKeys = append(geminiKeys, parseKeys(k)...)
		}
	}

	model := os.Getenv("AI_MODEL")
	if model == "" {
		model = os.Getenv("GEMINI_MODEL")
	}
	if model == "" {
		model = os.Getenv("GEMINI_DEFAULT_MODEL")
	}
	if model == "" {
		// Default to Google's fast, free tier Gemini 3.5 Flash Lite
		model = "gemini-3.5-flash-lite"
	}

	base := strings.TrimRight(os.Getenv("AI_BASE_URL"), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	return &Client{
		GeminiKeys:    dedupe(geminiKeys),
		OpenAIKey:     strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		OpenRouterKey: strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		BaseURL:       base,
		Model:         model,
		HTTP:          &http.Client{Timeout: 45 * time.Second},
	}
}

func dedupe(list []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, item := range list {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func (c *Client) SetConfig(geminiKey, openAIKey, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if geminiKey != "" {
		c.GeminiKeys = dedupe(append(parseKeys(geminiKey), c.GeminiKeys...))
	}
	if openAIKey != "" {
		c.OpenAIKey = openAIKey
	}
	if model != "" {
		c.Model = model
	}
}

func (c *Client) Mode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.GeminiKeys) > 0 {
		return "gemini (" + c.Model + ")"
	}
	if c.OpenRouterKey != "" {
		return "openrouter (" + c.Model + ")"
	}
	if c.OpenAIKey != "" {
		return "openai (" + c.Model + ")"
	}
	return "heuristic"
}

func (c *Client) HasKey() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.GeminiKeys) > 0 || c.OpenRouterKey != "" || c.OpenAIKey != ""
}

// Chat calls Gemini first, then other providers, and falls back to heuristic if needed.
func (c *Client) Chat(ctx context.Context, system, user string) (string, string, error) {
	c.mu.RLock()
	geminiKeys := append([]string{}, c.GeminiKeys...)
	model := c.Model
	openAIKey := c.OpenAIKey
	openRouterKey := c.OpenRouterKey
	baseURL := c.BaseURL
	c.mu.RUnlock()

	// 1. Try Google Gemini
	if len(geminiKeys) > 0 {
		for _, key := range geminiKeys {
			resp, err := c.callGemini(ctx, key, model, system, user)
			if err == nil && resp != "" {
				return resp, "gemini (" + model + ")", nil
			}
		}
	}

	// 2. Try OpenRouter if configured
	if openRouterKey != "" {
		resp, err := c.callOpenAICompatible(ctx, "https://openrouter.ai/api/v1", openRouterKey, "google/gemini-2.0-flash-lite:free", system, user)
		if err == nil && resp != "" {
			return resp, "openrouter", nil
		}
	}

	// 3. Try OpenAI if configured
	if openAIKey != "" {
		resp, err := c.callOpenAICompatible(ctx, baseURL, openAIKey, model, system, user)
		if err == nil && resp != "" {
			return resp, "openai (" + model + ")", nil
		}
	}

	// 4. Fallback to smart heuristic
	return HeuristicExplain(user), "heuristic", nil
}

// callGemini uses Google Generative Language v1beta generateContent API.
func (c *Client) callGemini(ctx context.Context, apiKey, model, system, user string) (string, error) {
	cleanModel := strings.TrimPrefix(model, "models/")
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", cleanModel, apiKey)

	payload := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{"text": user},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     0.2,
			"maxOutputTokens": 2048,
		},
	}
	if system != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []any{
				map[string]any{"text": system},
			},
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(bodyBytes, &res); err != nil || len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("unexpected gemini response format: %s", string(bodyBytes))
	}

	return strings.TrimSpace(res.Candidates[0].Content.Parts[0].Text), nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *Client) callOpenAICompatible(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"temperature": 0.2,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai error %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// HeuristicExplain produces a useful summary from log/context text without an LLM.
func HeuristicExplain(contextText string) string {
	lower := strings.ToLower(contextText)
	var findings []string
	checks := []struct {
		needle string
		msg    string
	}{
		{"oom", "Possible out-of-memory (OOM) kill — reduce memory use or raise limits."},
		{"out of memory", "Possible out-of-memory — reduce memory use or raise limits."},
		{"npm err", "npm install/build failed — check package.json and lockfile."},
		{"cannot find module", "Missing Node dependency — run install or fix imports."},
		{"module not found", "Missing dependency or wrong module path."},
		{"error: failed to", "Generic build/runtime failure — see log lines above."},
		{"exit code 1", "Process exited with code 1 — inspect the last error line."},
		{"permission denied", "Permission error — check filesystem or container user."},
		{"connection refused", "Dependency unreachable (ECONNREFUSED) — database or redis container is offline or unreachable."},
		{"econnrefused", "Connection refused — verify database connection string, host, and port in Secrets & Env."},
		{"eauth", "Authentication error (EAUTH) — verify SMTP or database credentials."},
		{"timeout", "Operation timed out — network or slow dependency."},
		{"no space", "Disk space exhausted on builder/runtime host."},
		{"dockerfile", "Docker build issue — verify Dockerfile and build context."},
		{"failed to enqueue build", "Build queue enqueue failed — check build service health."},
		{"status\": \"failed\"", "A deployment or build recorded status=failed."},
	}
	for _, c := range checks {
		if strings.Contains(lower, c.needle) {
			findings = append(findings, "- "+c.msg)
		}
	}
	if len(findings) == 0 {
		findings = append(findings, "- No critical failure signature matched. Review logs for the first ERROR or WARN event.")
		findings = append(findings, "- Ensure listening port matches container EXPOSE port and runtime bridge network is attached.")
	}
	var b strings.Builder
	b.WriteString("### Automated Operational Diagnosis\n\n")
	b.WriteString("**Key Findings:**\n")
	b.WriteString(strings.Join(findings, "\n"))
	b.WriteString("\n\n**Actionable Recommendations:**\n")
	b.WriteString("1. Inspect the latest container and build logs in the Builds & Logs tab.\n")
	b.WriteString("2. Check Environment Variables & Secrets to ensure all required database/service credentials are configured.\n")
	b.WriteString("3. If an Add-on (MongoDB/PostgreSQL/Redis) is required, attach it via Add-ons & Databases to automatically link connection envs.\n")
	if len(contextText) > 0 {
		snippet := contextText
		if len(snippet) > 1200 {
			snippet = snippet[len(snippet)-1200:]
		}
		b.WriteString("\n**Analyzed Log Excerpt:**\n```\n")
		b.WriteString(snippet)
		b.WriteString("\n```\n")
	}
	return b.String()
}
