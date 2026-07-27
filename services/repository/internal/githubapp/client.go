package githubapp

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrNotConfigured = errors.New("github app not configured")
	ErrBadKey        = errors.New("invalid github app private key")
)

// Client authenticates as a GitHub App and mints installation tokens.
type Client struct {
	AppID  int64
	Slug   string
	HTTP   *http.Client
	key    *rsa.PrivateKey
	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// ConfigFromEnv loads App credentials from environment.
// Configured when GITHUB_APP_ID and a private key are present.
func ConfigFromEnv() (appID int64, slug, pemData string, ok bool) {
	idStr := strings.TrimSpace(os.Getenv("GITHUB_APP_ID"))
	if idStr == "" {
		return 0, "", "", false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", "", false
	}
	slug = strings.TrimSpace(os.Getenv("GITHUB_APP_SLUG"))
	pemData = strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY"))
	if pemData == "" {
		path := strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"))
		if path == "" {
			return 0, "", "", false
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, "", "", false
		}
		pemData = string(b)
	}
	// Support escaped newlines in .env
	pemData = strings.ReplaceAll(pemData, `\n`, "\n")
	return id, slug, pemData, true
}

func New(appID int64, slug, pemData string, httpClient *http.Client) (*Client, error) {
	key, err := parseRSAPrivateKey(pemData)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		AppID:  appID,
		Slug:   slug,
		HTTP:   httpClient,
		key:    key,
		tokens: map[string]cachedToken{},
	}, nil
}

func NewFromEnv(httpClient *http.Client) (*Client, error) {
	id, slug, pem, ok := ConfigFromEnv()
	if !ok {
		return nil, ErrNotConfigured
	}
	return New(id, slug, pem, httpClient)
}

func (c *Client) Configured() bool { return c != nil && c.key != nil && c.AppID > 0 }

func (c *Client) InstallURL(state string) string {
	slug := c.Slug
	if slug == "" {
		slug = "jp-cloud"
	}
	base := fmt.Sprintf("https://github.com/apps/%s/installations/new", slug)
	if state != "" {
		return base + "?state=" + state
	}
	return base
}

func (c *Client) appJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
		Issuer:    strconv.FormatInt(c.AppID, 10),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return t.SignedString(c.key)
}

func (c *Client) InstallationToken(ctx context.Context, installationID string) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return "", fmt.Errorf("installation_id required")
	}

	c.mu.Lock()
	if tok, ok := c.tokens[installationID]; ok && time.Until(tok.ExpiresAt) > 2*time.Minute {
		c.mu.Unlock()
		return tok.Token, nil
	}
	c.mu.Unlock()

	appJWT, err := c.appJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("installation token: %s: %s", resp.Status, truncate(string(body), 200))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[installationID] = cachedToken{Token: out.Token, ExpiresAt: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

type RepoInfo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

func (c *Client) ListInstallationRepos(ctx context.Context, installationID string) ([]RepoInfo, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/installation/repositories?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list repos: %s: %s", resp.Status, truncate(string(body), 200))
	}
	var raw struct {
		Repositories []struct {
			FullName      string `json:"full_name"`
			CloneURL      string `json:"clone_url"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]RepoInfo, 0, len(raw.Repositories))
	for _, r := range raw.Repositories {
		out = append(out, RepoInfo{
			FullName: r.FullName, CloneURL: r.CloneURL,
			DefaultBranch: r.DefaultBranch, Private: r.Private,
		})
	}
	return out, nil
}

func (c *Client) GetInstallationAccount(ctx context.Context, installationID string) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	appJWT, err := c.appJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://api.github.com/app/installations/%s", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get installation: %s: %s", resp.Status, truncate(string(body), 200))
	}
	var raw struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", err
	}
	return raw.Account.Login, nil
}

// CreateCommitStatus posts a commit status (pending|success|failure|error).
func (c *Client) CreateCommitStatus(ctx context.Context, installationID, fullName, sha, state, description, targetURL string) error {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid full_name %q", fullName)
	}
	owner, repo := parts[0], parts[1]
	payload := map[string]any{
		"state":       mapCommitState(state),
		"context":     "jp/deploy",
		"description": truncate(description, 140),
	}
	if targetURL != "" {
		payload["target_url"] = targetURL
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/statuses/%s", owner, repo, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("commit status: %s: %s", resp.Status, truncate(string(respBody), 200))
	}
	return nil
}

func mapCommitState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "success", "ready":
		return "success"
	case "failure", "failed", "error":
		return "failure"
	case "error_state":
		return "error"
	default:
		return "pending"
	}
}

func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, ErrBadKey
	}
	if key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemData)); err == nil {
		return key, nil
	}
	// PKCS#1 / PKCS#8 via jwt helper already covers common cases; retry raw
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem.EncodeToMemory(block)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadKey, err)
	}
	return key, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
