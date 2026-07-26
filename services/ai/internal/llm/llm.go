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
	"time"
)

// Client calls a hosted OpenAI-compatible chat API, or simulates without a key.
type Client struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
}

func NewFromEnv() *Client {
	base := strings.TrimRight(os.Getenv("AI_BASE_URL"), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := os.Getenv("AI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &Client{
		APIKey:  strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		BaseURL: base,
		Model:   model,
		HTTP:    &http.Client{Timeout: 40 * time.Second},
	}
}

func (c *Client) Mode() string {
	if c.APIKey == "" {
		return "simulate"
	}
	return "openai"
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *Client) Chat(ctx context.Context, system, user string) (string, string, error) {
	if c.APIKey == "" {
		return HeuristicExplain(user), "simulate", nil
	}
	body := map[string]any{
		"model": c.Model,
		"messages": []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"temperature": 0.2,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", "openai", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return HeuristicExplain(user) + "\n\n(Note: LLM request failed; fell back to heuristic.)", "simulate", nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return HeuristicExplain(user) + "\n\n(Note: LLM returned " + fmt.Sprintf("%d", resp.StatusCode) + "; fell back to heuristic.)", "simulate", nil
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return HeuristicExplain(user), "simulate", nil
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), "openai", nil
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
		{"connection refused", "Dependency unreachable — check database/redis/network."},
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
		findings = append(findings, "- No strong pattern matched; review the last 20–50 log lines for the first ERROR.")
		findings = append(findings, "- Confirm git SHA, image_ref, and whether the worker ran in simulate vs docker mode.")
	}
	var b strings.Builder
	b.WriteString("## Heuristic failure analysis (simulate mode — no OPENAI_API_KEY)\n\n")
	b.WriteString("Likely causes:\n")
	b.WriteString(strings.Join(findings, "\n"))
	b.WriteString("\n\nSuggested next steps:\n")
	b.WriteString("1. Open build/runtime logs for the failed deployment.\n")
	b.WriteString("2. Re-run `jp deploy` after fixing the root cause.\n")
	b.WriteString("3. Use `jp rollback` if a previous ready deploy exists.\n")
	if len(contextText) > 0 {
		snippet := contextText
		if len(snippet) > 1200 {
			snippet = snippet[len(snippet)-1200:]
		}
		b.WriteString("\n### Context excerpt\n```\n")
		b.WriteString(snippet)
		b.WriteString("\n```\n")
	}
	return b.String()
}
