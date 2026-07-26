package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/jp/internal/config"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	cfg        *config.Config
}

func New(cfg *config.Config) *Client {
	base := strings.TrimRight(cfg.APIURL, "/")
	return &Client{
		baseURL: base,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cfg: cfg,
	}
}

type APIError struct {
	Status  int
	Code    string
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		if e.Code != "" {
			return fmt.Sprintf("API error (%d/%s): %s", e.Status, e.Code, e.Message)
		}
		return fmt.Sprintf("API error (%d): %s", e.Status, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("API error (%d): %s", e.Status, e.Body)
	}
	return fmt.Sprintf("API error (%d)", e.Status)
}

// Matches packages/go-common/httpx.ErrorBody
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

func (c *Client) do(method, path string, body any, auth bool, out any) error {
	respBody, err := c.doRaw(method, path, body, auth)
	if err != nil {
		return err
	}
	if out == nil || len(respBody) == 0 || string(respBody) == "null" {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) doRaw(method, path string, body any, auth bool) ([]byte, error) {
	return c.doRawOnce(method, path, body, auth, true)
}

func (c *Client) doRawOnce(method, path string, body any, auth bool, allowRefresh bool) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	url := c.baseURL + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.cfg.AccessToken == "" {
			return nil, fmt.Errorf("not logged in; run `jp login`")
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && auth && allowRefresh && c.cfg.RefreshToken != "" {
		if err := c.refresh(); err == nil {
			return c.doRawOnce(method, path, body, auth, false)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ""
		code := ""
		var eb errorBody
		if json.Unmarshal(respBody, &eb) == nil {
			msg = firstNonEmpty(eb.Error.Message, eb.Message, eb.Detail)
			code = eb.Error.Code
		}
		return nil, &APIError{
			Status:  resp.StatusCode,
			Code:    code,
			Message: msg,
			Body:    strings.TrimSpace(string(respBody)),
		}
	}

	return respBody, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (c *Client) refresh() error {
	var res TokenResponse
	if err := c.do(http.MethodPost, "/auth/refresh", map[string]string{
		"refresh_token": c.cfg.RefreshToken,
	}, false, &res); err != nil {
		return err
	}
	applyTokens(c.cfg, &res)
	return config.Save(c.cfg)
}
