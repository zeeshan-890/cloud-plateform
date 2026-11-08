// Package regclient deletes image manifests from a Docker Distribution (registry:2)
// instance through its HTTP API. The registry must run with REGISTRY_STORAGE_DELETE_ENABLED=true;
// freed blobs are only reclaimed by `registry garbage-collect`.
package regclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	repoRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

	ErrDeleteDisabled = errors.New("registry has deletes disabled (REGISTRY_STORAGE_DELETE_ENABLED)")
	ErrForeign        = errors.New("image does not belong to the configured registry")
)

const manifestAccept = "application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json"

// Client talks to one registry.
type Client struct {
	BaseURL  string // http://registry:5000
	Host     string // host:port that appears in image refs (e.g. registry:5000 or 127.0.0.1:5000)
	Hosts    []string
	User     string
	Password string
	HTTP     *http.Client
}

// New builds a client; hosts are the image-ref hosts that map to this registry.
func New(baseURL string, hosts ...string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Hosts: hosts, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Parse splits "host/repo:tag" and validates every part.
func (c *Client) Parse(ref string) (repo, tag string, err error) {
	i := strings.Index(ref, "/")
	if i <= 0 {
		return "", "", fmt.Errorf("invalid image ref")
	}
	host, rest := ref[:i], ref[i+1:]
	known := false
	for _, h := range c.Hosts {
		if h != "" && h == host {
			known = true
		}
	}
	if !known {
		return "", "", ErrForeign
	}
	tag = "latest"
	if j := strings.LastIndex(rest, ":"); j >= 0 && !strings.Contains(rest[j:], "/") {
		rest, tag = rest[:j], rest[j+1:]
	}
	if !repoRe.MatchString(rest) || !tagRe.MatchString(tag) {
		return "", "", fmt.Errorf("invalid image ref")
	}
	return rest, tag, nil
}

func (c *Client) repoPath(repo string) string {
	parts := strings.Split(repo, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c *Client) do(ctx context.Context, method, path string, accept bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if accept {
		req.Header.Set("Accept", manifestAccept)
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	return c.HTTP.Do(req)
}

// Delete removes the manifest behind ref. A missing image counts as deleted.
func (c *Client) Delete(ctx context.Context, ref string) error {
	repo, tag, err := c.Parse(ref)
	if err != nil {
		return err
	}
	rp := c.repoPath(repo)
	resp, err := c.do(ctx, http.MethodHead, "/v2/"+rp+"/manifests/"+url.PathEscape(tag), true)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry HEAD manifest: status %d", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(digest) {
		return fmt.Errorf("registry returned no valid digest")
	}
	dresp, err := c.do(ctx, http.MethodDelete, "/v2/"+rp+"/manifests/"+digest, false)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(dresp.Body, 1<<16))
	dresp.Body.Close()
	switch dresp.StatusCode {
	case http.StatusAccepted, http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusMethodNotAllowed:
		return ErrDeleteDisabled
	}
	return fmt.Errorf("registry DELETE manifest: status %d", dresp.StatusCode)
}
