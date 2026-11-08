// Package certclient is the domain service's client for the certificate service.
package certclient

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
)

// Info is the subset of a certificate the domain API shows.
type Info struct {
	ID        string     `json:"id"`
	DomainID  *string    `json:"domain_id"`
	Hostname  string     `json:"hostname"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	Error     string     `json:"error"`
}

// Client abstracts the certificate service.
type Client interface {
	Register(ctx context.Context, orgID, projectID, domainID, hostname string) (string, error)
	DeleteByDomain(ctx context.Context, domainID string) error
	DeleteByProject(ctx context.Context, projectID string) error
	ListByProject(ctx context.Context, projectID string) (map[string]Info, error)
}

// HTTP implements Client over the internal API.
type HTTP struct {
	BaseURL string
	Client  *http.Client
}

// New returns a signing client with a bounded timeout.
func New(baseURL string) *HTTP {
	return &HTTP{BaseURL: strings.TrimRight(baseURL, "/"), Client: internalauth.NewClient(10 * time.Second)}
}

func (c *HTTP) do(ctx context.Context, method, path string, body any, out any) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("certificate service not configured")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("certificate service %s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *HTTP) Register(ctx context.Context, orgID, projectID, domainID, hostname string) (string, error) {
	var out struct {
		Certificate struct {
			ID string `json:"id"`
		} `json:"certificate"`
	}
	err := c.do(ctx, http.MethodPost, "/internal/certificates", map[string]string{
		"org_id": orgID, "project_id": projectID, "domain_id": domainID, "hostname": hostname,
	}, &out)
	return out.Certificate.ID, err
}

func (c *HTTP) DeleteByDomain(ctx context.Context, domainID string) error {
	return c.do(ctx, http.MethodDelete, "/internal/certificates/by-domain/"+url.PathEscape(domainID), nil, nil)
}

func (c *HTTP) DeleteByProject(ctx context.Context, projectID string) error {
	return c.do(ctx, http.MethodDelete, "/internal/projects/"+url.PathEscape(projectID), nil, nil)
}

func (c *HTTP) ListByProject(ctx context.Context, projectID string) (map[string]Info, error) {
	var out struct {
		Certificates []Info `json:"certificates"`
	}
	if err := c.do(ctx, http.MethodGet, "/internal/projects/"+url.PathEscape(projectID)+"/certificates", nil, &out); err != nil {
		return nil, err
	}
	m := map[string]Info{}
	for _, ci := range out.Certificates {
		if ci.DomainID != nil {
			m[*ci.DomainID] = ci
		}
	}
	return m, nil
}
