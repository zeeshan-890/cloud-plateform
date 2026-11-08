// Package clients holds the runtime's outbound calls to other platform services.
// Every call is signed with the internal token, has a timeout and bounded reads, and
// builds URLs with path escaping (never string concatenation of caller data).
package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/runtime/internal/manager"
)

// Secrets fetches the merged environment of a project from the secret service.
type Secrets struct {
	BaseURL string
	HTTP    *http.Client
}

// NewSecrets builds a Secrets client.
func NewSecrets(baseURL string) *Secrets {
	return &Secrets{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: internalauth.NewClient(10 * time.Second)}
}

// Env returns the environment for (org, project, environment). Preview deployments are
// served preview(+all) secrets only by the secret service.
func (s *Secrets) Env(ctx context.Context, orgID, projectID, environment string) (map[string]string, error) {
	if s == nil || s.BaseURL == "" {
		return nil, fmt.Errorf("secret service not configured")
	}
	u := fmt.Sprintf("%s/internal/orgs/%s/projects/%s/env?environment=%s",
		s.BaseURL, url.PathEscape(orgID), url.PathEscape(projectID), url.QueryEscape(environment))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("secret service: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("secret service status %d", resp.StatusCode)
	}
	var out struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("secret service: bad response")
	}
	if out.Env == nil {
		out.Env = map[string]string{}
	}
	return out.Env, nil
}

// Routes switches the production routers of a project to a Traefik service (blue/green).
type Routes struct {
	BaseURL string
	HTTP    *http.Client
}

// NewRoutes builds a Routes client.
func NewRoutes(baseURL string) *Routes {
	return &Routes{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: internalauth.NewClient(15 * time.Second)}
}

// SetService calls the domain service: PUT /internal/routes/{projectId} {"service":...}.
func (r *Routes) SetService(ctx context.Context, projectID, service string) error {
	return r.put(ctx, projectID, map[string]any{"service": service})
}

// SetUpstreams points the production routers at apps on a remote server (http://<private ip>:<port>); several
// upstreams are load-balanced. The service name is still sent (the domain service requires it) but unused.
func (r *Routes) SetUpstreams(ctx context.Context, projectID, service string, upstreams []string) error {
	if len(upstreams) == 0 {
		return fmt.Errorf("no upstream addresses for the remote app")
	}
	return r.put(ctx, projectID, map[string]any{"service": service, "upstreams": upstreams})
}

func (r *Routes) put(ctx context.Context, projectID string, body map[string]any) error {
	if r == nil || r.BaseURL == "" {
		return fmt.Errorf("domain service not configured")
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, r.BaseURL+"/internal/routes/"+url.PathEscape(projectID), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("domain service: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("domain service status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Metrics pushes samples to the metrics service in batches of at most 500.
type Metrics struct {
	BaseURL string
	HTTP    *http.Client
}

// NewMetrics builds a Metrics client.
func NewMetrics(baseURL string) *Metrics {
	return &Metrics{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: internalauth.NewClient(10 * time.Second)}
}

// Push implements manager.MetricsSink.
func (m *Metrics) Push(ctx context.Context, samples []manager.MetricSample) error {
	for len(samples) > 0 {
		n := len(samples)
		if n > 500 {
			n = 500
		}
		raw, _ := json.Marshal(map[string]any{"samples": samples[:n]})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/internal/metrics", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := m.HTTP.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("metrics service status %d", resp.StatusCode)
		}
		samples = samples[n:]
	}
	return nil
}
