// Package clients holds the add-on service's outbound service-to-service clients. All of them use
// internalauth.NewClient (signed requests), bounded timeouts and LimitReader on small bodies.
package clients

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jp-cloud/addon/internal/driver"
	"github.com/jp-cloud/go-common/internalauth"
)

// ErrExecUnsupported is returned when the runtime has no exec endpoint.
var ErrExecUnsupported = errors.New("runtime does not support exec")

// ErrNotFound is returned for 404s.
var ErrNotFound = errors.New("not found")

// CreateReq is the body of POST /internal/runtime/addon (CONTRACT.md).
type CreateReq struct {
	OrgID     string            `json:"org_id"`
	ProjectID string            `json:"project_id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Port      int               `json:"port"`
	Env       map[string]string `json:"env"`
	Volume    string            `json:"volume,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Health    *driver.Health    `json:"health,omitempty"`
}

// CreateResp is the runtime answer.
type CreateResp struct {
	ContainerID string `json:"container_id"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
}

// RuntimeStatus is the live state of an add-on container.
type RuntimeStatus struct {
	ContainerID  string `json:"container_id"`
	State        string `json:"state"`
	Running      bool   `json:"running"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	ExitCode     int    `json:"exit_code"`
	Error        string `json:"error"`
}

// Runtime abstracts the runtime service.
type Runtime interface {
	Create(ctx context.Context, req CreateReq) (CreateResp, error)
	Status(ctx context.Context, id string) (RuntimeStatus, error)
	Action(ctx context.Context, id, action string) error
	Delete(ctx context.Context, id string, purge bool) error
	Exec(ctx context.Context, id string, spec driver.ExecSpec, stdin io.Reader, stdout io.Writer, timeout time.Duration) (exit int, stderr string, err error)
}

// RuntimeHTTP implements Runtime.
type RuntimeHTTP struct {
	BaseURL string
	HTTP    *http.Client // short calls
	Long    *http.Client // exec (no overall timeout; per-call context)
}

func NewRuntime(base string) *RuntimeHTTP {
	return &RuntimeHTTP{BaseURL: strings.TrimRight(base, "/"), HTTP: internalauth.NewClient(30 * time.Second), Long: internalauth.NewClient(0)}
}

func do(c *http.Client, req *http.Request, out any) (int, []byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil && resp.StatusCode < 300 && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, raw, nil
}

func (r *RuntimeHTTP) call(ctx context.Context, method, path string, body any, out any) error {
	return r.callWith(r.HTTP, ctx, method, path, body, out)
}

// callWith performs a call with an explicit client (long operations such as create wait for the image pull and the
// readiness probe, so they use the no-overall-timeout client bounded only by the context deadline).
func (r *RuntimeHTTP) callWith(hc *http.Client, ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	code, raw, err := do(hc, req, out)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return ErrNotFound
	}
	if code >= 300 {
		return fmt.Errorf("runtime %s %s: status %d: %s", method, path, code, truncate(string(raw), 300))
	}
	return nil
}

func (r *RuntimeHTTP) Create(ctx context.Context, req CreateReq) (CreateResp, error) {
	var out CreateResp
	// image pull + readiness can take minutes: bounded by the context, not the short-call client timeout
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err := r.callWith(r.Long, cctx, http.MethodPost, "/internal/runtime/addon", req, &out)
	if err == nil && out.ContainerID == "" {
		err = errors.New("runtime returned no container_id")
	}
	return out, err
}

func (r *RuntimeHTTP) Status(ctx context.Context, id string) (RuntimeStatus, error) {
	var out RuntimeStatus
	err := r.call(ctx, http.MethodGet, "/internal/runtime/addon/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (r *RuntimeHTTP) Action(ctx context.Context, id, action string) error {
	return r.call(ctx, http.MethodPost, "/internal/runtime/addon/"+url.PathEscape(id)+"/"+action, nil, nil)
}

func (r *RuntimeHTTP) Delete(ctx context.Context, id string, purge bool) error {
	err := r.call(ctx, http.MethodDelete, fmt.Sprintf("/internal/runtime/addon/%s?purge=%t", url.PathEscape(id), purge), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Exec streams: stdin -> container process -> stdout. Exit code and the stderr tail arrive as trailers.
func (r *RuntimeHTTP) Exec(ctx context.Context, id string, spec driver.ExecSpec, stdin io.Reader, stdout io.Writer, timeout time.Duration) (int, string, error) {
	if stdin == nil {
		stdin = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+"/internal/runtime/addon/"+url.PathEscape(id)+"/exec", stdin)
	if err != nil {
		return -1, "", err
	}
	cmd, _ := json.Marshal(spec.Cmd)
	req.Header.Set("X-Exec-Cmd", base64.StdEncoding.EncodeToString(cmd))
	if len(spec.Env) > 0 {
		env, _ := json.Marshal(spec.Env)
		req.Header.Set("X-Exec-Env", base64.StdEncoding.EncodeToString(env))
	}
	if timeout > 0 {
		req.Header.Set("X-Exec-Timeout", fmt.Sprint(int(timeout.Seconds())))
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := r.Long.Do(req)
	if err != nil {
		return -1, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotImplemented, http.StatusNotFound, http.StatusMethodNotAllowed:
		if resp.StatusCode == http.StatusNotFound {
			return -1, "", ErrNotFound
		}
		return -1, "", ErrExecUnsupported
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return -1, "", fmt.Errorf("runtime exec: status %d: %s", resp.StatusCode, truncate(string(b), 300))
	}
	if _, err := io.Copy(stdout, resp.Body); err != nil {
		return -1, "", err
	}
	codeStr := resp.Trailer.Get("X-Exec-Exit-Code")
	var stderr string
	if b, err := base64.StdEncoding.DecodeString(resp.Trailer.Get("X-Exec-Stderr")); err == nil {
		stderr = string(b)
	}
	if codeStr == "" {
		return -1, stderr, errors.New("runtime exec ended without an exit code (stream truncated)")
	}
	var code int
	fmt.Sscanf(codeStr, "%d", &code)
	return code, stderr, nil
}

// ------------------------------------------------------------------ secrets

// Secrets abstracts the secret service internal API.
type Secrets interface {
	Upsert(ctx context.Context, org, project, env, name, value string) error
	Delete(ctx context.Context, org, project, env, name string) error
	Env(ctx context.Context, org, project, env string) (map[string]string, error)
}

type SecretsHTTP struct {
	BaseURL string
	HTTP    *http.Client
}

func NewSecrets(base string) *SecretsHTTP {
	return &SecretsHTTP{BaseURL: strings.TrimRight(base, "/"), HTTP: internalauth.NewClient(15 * time.Second)}
}

func (s *SecretsHTTP) path(org, project, env, name string) string {
	return fmt.Sprintf("%s/internal/orgs/%s/projects/%s/environments/%s/secrets/%s", s.BaseURL,
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(env), url.PathEscape(name))
}

func (s *SecretsHTTP) Upsert(ctx context.Context, org, project, env, name, value string) error {
	b, _ := json.Marshal(map[string]string{"value": value, "actor": "addon"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.path(org, project, env, name), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	code, raw, err := do(s.HTTP, req, nil)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("secret service: upsert %s/%s: status %d: %s", env, name, code, truncate(string(raw), 200))
	}
	return nil
}

func (s *SecretsHTTP) Delete(ctx context.Context, org, project, env, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.path(org, project, env, name), nil)
	if err != nil {
		return err
	}
	code, raw, err := do(s.HTTP, req, nil)
	if err != nil {
		return err
	}
	if code >= 300 && code != http.StatusNotFound {
		return fmt.Errorf("secret service: delete %s/%s: status %d: %s", env, name, code, truncate(string(raw), 200))
	}
	return nil
}

func (s *SecretsHTTP) Env(ctx context.Context, org, project, env string) (map[string]string, error) {
	u := fmt.Sprintf("%s/internal/orgs/%s/projects/%s/env?environment=%s", s.BaseURL, url.PathEscape(org), url.PathEscape(project), url.QueryEscape(env))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Env map[string]string `json:"env"`
	}
	code, raw, err := do(s.HTTP, req, &out)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("secret service: env %s: status %d: %s", env, code, truncate(string(raw), 200))
	}
	if out.Env == nil {
		out.Env = map[string]string{}
	}
	return out.Env, nil
}

// ------------------------------------------------------------------ storage

// Storage abstracts the storage service internal object API (used for backups).
type Storage interface {
	Put(ctx context.Context, orgID, projectID, key string, r io.Reader) error
	Get(ctx context.Context, projectID, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, projectID, key string) error
}

type StorageHTTP struct {
	BaseURL string
	HTTP    *http.Client
}

func NewStorage(base string) *StorageHTTP {
	return &StorageHTTP{BaseURL: strings.TrimRight(base, "/"), HTTP: internalauth.NewClient(0)}
}

func (s *StorageHTTP) u(projectID, key string) string {
	return fmt.Sprintf("%s/internal/projects/%s/objects?key=%s", s.BaseURL, url.PathEscape(projectID), url.QueryEscape(key))
}

func (s *StorageHTTP) Put(ctx context.Context, orgID, projectID, key string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.u(projectID, key)+"&org_id="+url.QueryEscape(orgID), r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	code, raw, err := do(s.HTTP, req, nil)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("storage service: put: status %d: %s", code, truncate(string(raw), 200))
	}
	return nil
}

func (s *StorageHTTP) Get(ctx context.Context, projectID, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.u(projectID, key), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("storage service: get: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *StorageHTTP) Delete(ctx context.Context, projectID, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.u(projectID, key), nil)
	if err != nil {
		return err
	}
	code, _, err := do(s.HTTP, req, nil)
	if err != nil {
		return err
	}
	if code >= 300 && code != http.StatusNotFound {
		return fmt.Errorf("storage service: delete: status %d", code)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
