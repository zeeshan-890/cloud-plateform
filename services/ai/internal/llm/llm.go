// Package llm calls Gemini and OpenAI-compatible chat APIs. Keys are passed per call (they belong to
// an organisation), are sent in headers (never in URLs) and never appear in returned errors.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Providers.
const (
	ProviderGemini = "gemini"
	ProviderOpenAI = "openai" // any OpenAI-compatible endpoint
)

// Defaults.
const (
	DefaultGeminiModel = "gemini-3.5-flash-lite"
	DefaultOpenAIModel = "gpt-4o-mini"
	DefaultOpenAIBase  = "https://api.openai.com/v1"
	maxResponseBytes   = 2 << 20
)

var modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-]{0,79}$`)

// Config selects provider, model and credentials for one call.
type Config struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string // openai-compatible only
}

// ValidProvider reports whether p is supported.
func ValidProvider(p string) bool { return p == ProviderGemini || p == ProviderOpenAI }

// ValidModel guards the model name (it ends up in a URL path for Gemini).
func ValidModel(m string) bool { return modelRe.MatchString(m) }

// DefaultModel returns the default model of a provider.
func DefaultModel(provider string) string {
	if provider == ProviderOpenAI {
		return DefaultOpenAIModel
	}
	return DefaultGeminiModel
}

// ValidateBaseURL accepts only https URLs without credentials. Private destinations are blocked at
// dial time (see NewHTTPClient) unless allowPrivate is set by the operator.
func ValidateBaseURL(raw string, allowPrivate bool) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return DefaultOpenAIBase, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("base_url must be a plain URL without credentials or query")
	}
	if u.Scheme != "https" && !(allowPrivate && u.Scheme == "http") {
		return "", errors.New("base_url must use https")
	}
	return raw, nil
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64) // 100.64/10 CGNAT
}

// NewHTTPClient returns a client with timeouts that refuses to connect to private/loopback/link-local
// addresses (SSRF protection for org-configured base URLs) unless allowPrivate.
func NewHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && isPrivateIP(ip) {
				return fmt.Errorf("blocked connection to non-public address %s", host)
			}
			return nil
		}
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: timeout,
		MaxIdleConns: 10, IdleConnTimeout: 60 * time.Second, Proxy: http.ProxyFromEnvironment}
	return &http.Client{Timeout: timeout, Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

// Client performs chat completions.
type Client struct {
	HTTP *http.Client
	// GeminiBase overrides https://generativelanguage.googleapis.com (tests).
	GeminiBase string
}

// New builds a Client with SSRF protection (AI_ALLOW_PRIVATE_BASE_URL=true disables it).
func New() *Client {
	allow := strings.EqualFold(os.Getenv("AI_ALLOW_PRIVATE_BASE_URL"), "true")
	return &Client{HTTP: NewHTTPClient(45*time.Second, allow)}
}

// Chat sends one system+user exchange.
func (c *Client) Chat(ctx context.Context, cfg Config, system, user string) (string, error) {
	if cfg.APIKey == "" {
		return "", errors.New("no API key")
	}
	if !ValidModel(cfg.Model) {
		return "", errors.New("invalid model name")
	}
	switch cfg.Provider {
	case ProviderGemini:
		return c.gemini(ctx, cfg, system, user)
	case ProviderOpenAI:
		return c.openai(ctx, cfg, system, user)
	}
	return "", fmt.Errorf("unsupported provider %q", cfg.Provider)
}

func (c *Client) do(req *http.Request, key string) ([]byte, int, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, scrub(err.Error(), key)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return b, resp.StatusCode, nil
}

// scrub makes sure the key can never leak through an error string.
func scrub(msg, key string) error {
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[key]")
	}
	return errors.New(msg)
}

func (c *Client) gemini(ctx context.Context, cfg Config, system, user string) (string, error) {
	base := c.GeminiBase
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	payload := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": user}}}},
		"generationConfig": map[string]any{"temperature": 0.2, "maxOutputTokens": 2048},
	}
	if system != "" {
		payload["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": system}}}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/v1beta/models/%s:generateContent", base, cfg.Model), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", cfg.APIKey)
	body, code, err := c.do(req, cfg.APIKey)
	if err != nil {
		return "", err
	}
	if code >= 300 {
		return "", fmt.Errorf("gemini API returned status %d: %s", code, short(body))
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
	if err := json.Unmarshal(body, &res); err != nil || len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("unexpected gemini response format")
	}
	return strings.TrimSpace(res.Candidates[0].Content.Parts[0].Text), nil
}

func (c *Client) openai(ctx context.Context, cfg Config, system, user string) (string, error) {
	base := cfg.BaseURL
	if base == "" {
		base = DefaultOpenAIBase
	}
	msgs := []map[string]string{}
	if system != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": system})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": user})
	raw, _ := json.Marshal(map[string]any{"model": cfg.Model, "messages": msgs, "temperature": 0.2, "max_tokens": 2048})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	body, code, err := c.do(req, cfg.APIKey)
	if err != nil {
		return "", err
	}
	if code >= 300 {
		return "", fmt.Errorf("provider returned status %d: %s", code, short(body))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Choices) == 0 {
		return "", errors.New("no choices in provider response")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func short(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// HeuristicExplain produces a keyword based summary without an LLM. Callers label it mode=heuristic.
func HeuristicExplain(contextText string) string {
	lower := strings.ToLower(contextText)
	var findings []string
	checks := []struct{ needle, msg string }{
		{"oom", "Possible out-of-memory (OOM) kill - reduce memory use or raise limits."},
		{"out of memory", "Possible out-of-memory - reduce memory use or raise limits."},
		{"npm err", "npm install/build failed - check package.json and lockfile."},
		{"cannot find module", "Missing Node dependency - run install or fix imports."},
		{"module not found", "Missing dependency or wrong module path."},
		{"exit code 1", "Process exited with code 1 - inspect the last error line."},
		{"permission denied", "Permission error - check filesystem or container user."},
		{"econnrefused", "Connection refused - verify the connection string, host and port in Secrets & Env."},
		{"connection refused", "Dependency unreachable - the database/cache container may be offline."},
		{"eauth", "Authentication error - verify SMTP or database credentials."},
		{"timeout", "Operation timed out - network or slow dependency."},
		{"no space", "Disk space exhausted on the builder/runtime host."},
		{"dockerfile", "Docker build issue - verify Dockerfile and build context."},
		{"status\": \"failed\"", "A deployment or build recorded status=failed."},
	}
	for _, c := range checks {
		if strings.Contains(lower, c.needle) {
			findings = append(findings, "- "+c.msg)
		}
	}
	if len(findings) == 0 {
		findings = append(findings, "- No known failure signature matched. Look for the first ERROR or WARN line.")
		findings = append(findings, "- Make sure the app listens on the port configured for the project.")
	}
	var b strings.Builder
	b.WriteString("### Automated keyword diagnosis (no AI model configured)\n\n**Findings:**\n")
	b.WriteString(strings.Join(findings, "\n"))
	b.WriteString("\n\n**Next steps:**\n1. Inspect the latest build and container logs.\n2. Check Environment Variables & Secrets for required credentials.\n3. Configure an AI provider (Settings > AI) for deeper analysis.\n")
	if snippet := strings.TrimSpace(contextText); snippet != "" {
		if len(snippet) > 1200 {
			snippet = snippet[len(snippet)-1200:]
		}
		b.WriteString("\n**Log excerpt:**\n```\n" + snippet + "\n```\n")
	}
	return b.String()
}
