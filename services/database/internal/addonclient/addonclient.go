// Package addonclient talks to the add-on service's internal API. Managed databases are
// dedicated Postgres add-ons, so provisioning, backups and SQL all run there (no duplicated driver logic).
package addonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/internalauth"
)

// Error is a failure reported by the add-on service.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("addon service: %s (%s, status %d)", e.Message, e.Code, e.Status)
}

// ProvisionReq creates a dedicated Postgres add-on.
type ProvisionReq struct {
	OrgID            string   `json:"org_id"`
	ProjectID        string   `json:"project_id"`
	Name             string   `json:"name"`
	Engine           string   `json:"engine"`
	SecretName       string   `json:"secret_name,omitempty"`
	Envs             []string `json:"envs,omitempty"`
	CreatedBy        string   `json:"created_by,omitempty"`
	Kind             string   `json:"kind"`
	PGStatStatements bool     `json:"pg_stat_statements"`
}

// Addon is the subset of the add-on record the database service needs.
type Addon struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	Endpoint   string   `json:"endpoint"`
	SecretRef  string   `json:"secret_ref"`
	SecretKeys []string `json:"secret_keys"`
	Hint       string   `json:"connection_hint"`
	Image      string   `json:"image"`
	VolumeName string   `json:"volume_name"`
	Error      string   `json:"error"`
	Live       *Live    `json:"live,omitempty"`
}

type Live struct {
	Status string `json:"status"`
	State  string `json:"state"`
	Health string `json:"health"`
}

// Client abstracts the add-on service.
type Client interface {
	Provision(ctx context.Context, req ProvisionReq) (*Addon, error)
	Get(ctx context.Context, id string) (*Addon, error)
	Connection(ctx context.Context, id string, reveal bool) (json.RawMessage, error)
	Delete(ctx context.Context, id string, purge bool) error
	SQL(ctx context.Context, id, sql string) (string, error)
	Backup(ctx context.Context, id, actor string) (json.RawMessage, error)
	ListBackups(ctx context.Context, id string) (json.RawMessage, error)
	Restore(ctx context.Context, id, backupID, actor string) (json.RawMessage, error)
}

type HTTP struct {
	BaseURL string
	Short   *http.Client
	Long    *http.Client
	Huge    *http.Client
}

func New(base string) *HTTP {
	return &HTTP{BaseURL: strings.TrimRight(base, "/"), Short: internalauth.NewClient(30 * time.Second),
		Long: internalauth.NewClient(5 * time.Minute), Huge: internalauth.NewClient(45 * time.Minute)}
}

func (c *HTTP) call(ctx context.Context, hc *http.Client, method, path string, body any) ([]byte, error) {
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("addon service unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
			if len(msg) > 300 {
				msg = msg[:300]
			}
		}
		return nil, &Error{Status: resp.StatusCode, Code: e.Error.Code, Message: msg}
	}
	return raw, nil
}

func (c *HTTP) Provision(ctx context.Context, req ProvisionReq) (*Addon, error) {
	raw, err := c.call(ctx, c.Long, http.MethodPost, "/internal/addons/provision", req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Addon Addon `json:"addon"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Addon.ID == "" {
		return nil, errors.New("addon service returned an invalid response")
	}
	return &out.Addon, nil
}

func (c *HTTP) Get(ctx context.Context, id string) (*Addon, error) {
	raw, err := c.call(ctx, c.Short, http.MethodGet, "/internal/addons/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Addon Addon `json:"addon"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out.Addon, nil
}

func (c *HTTP) Connection(ctx context.Context, id string, reveal bool) (json.RawMessage, error) {
	raw, err := c.call(ctx, c.Short, http.MethodGet, fmt.Sprintf("/internal/addons/%s/connection?reveal=%t", url.PathEscape(id), reveal), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Connection json.RawMessage `json:"connection"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Connection, nil
}

func (c *HTTP) Delete(ctx context.Context, id string, purge bool) error {
	_, err := c.call(ctx, c.Short, http.MethodDelete, fmt.Sprintf("/internal/addons/%s?purge=%t", url.PathEscape(id), purge), nil)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *HTTP) SQL(ctx context.Context, id, sql string) (string, error) {
	raw, err := c.call(ctx, c.Short, http.MethodPost, "/internal/addons/"+url.PathEscape(id)+"/sql", map[string]string{"sql": sql})
	if err != nil {
		return "", err
	}
	var out struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Output, nil
}

func (c *HTTP) Backup(ctx context.Context, id, actor string) (json.RawMessage, error) {
	return c.call(ctx, c.Huge, http.MethodPost, fmt.Sprintf("/internal/addons/%s/backups?actor=%s", url.PathEscape(id), url.QueryEscape(actor)), nil)
}

func (c *HTTP) ListBackups(ctx context.Context, id string) (json.RawMessage, error) {
	return c.call(ctx, c.Short, http.MethodGet, "/internal/addons/"+url.PathEscape(id)+"/backups", nil)
}

func (c *HTTP) Restore(ctx context.Context, id, backupID, actor string) (json.RawMessage, error) {
	return c.call(ctx, c.Huge, http.MethodPost,
		fmt.Sprintf("/internal/addons/%s/backups/%s/restore?actor=%s", url.PathEscape(id), url.PathEscape(backupID), url.QueryEscape(actor)), nil)
}
