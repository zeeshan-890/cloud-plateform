package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type Environment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
}

type SecretMeta struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Environment    string `json:"environment"`
	CurrentVersion int    `json:"current_version"`
	ValueHint      string `json:"value_hint"`
}

type LogEntry struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	BuildID   string `json:"build_id,omitempty"`
	LoggedAt  string `json:"logged_at"`
}

type MetricSummary struct {
	Name   string  `json:"name"`
	Latest float64 `json:"latest"`
	Count  int     `json:"count"`
}

func (c *Client) ListEnvironments(orgID, projectID string) ([]Environment, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/environments", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Environments []Environment `json:"environments"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Environments == nil {
		return []Environment{}, nil
	}
	return wrapped.Environments, nil
}

func (c *Client) ListSecrets(orgID, projectID, env string) ([]SecretMeta, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/environments/%s/secrets", orgID, projectID, env)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Secrets []SecretMeta `json:"secrets"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Secrets == nil {
		return []SecretMeta{}, nil
	}
	return wrapped.Secrets, nil
}

func (c *Client) SetSecret(orgID, projectID, env, name, value string) (*SecretMeta, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/environments/%s/secrets/%s", orgID, projectID, env, name)
	raw, err := c.doRaw(http.MethodPut, path, map[string]any{"value": value}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Secret SecretMeta `json:"secret"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return &wrapped.Secret, nil
}

func (c *Client) CreateSecret(orgID, projectID, env, name, value string) (*SecretMeta, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/environments/%s/secrets", orgID, projectID, env)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{"name": name, "value": value}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Secret SecretMeta `json:"secret"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return &wrapped.Secret, nil
}

func (c *Client) DeleteSecret(orgID, projectID, env, name string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/environments/%s/secrets/%s", orgID, projectID, env, name)
	_, err := c.doRaw(http.MethodDelete, path, nil, true)
	return err
}

func (c *Client) QueryLogs(orgID, projectID string, q url.Values) ([]LogEntry, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/logs", orgID, projectID)
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Entries   []LogEntry `json:"entries"`
		BuildLogs string     `json:"build_logs"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", err
	}
	if wrapped.Entries == nil {
		wrapped.Entries = []LogEntry{}
	}
	return wrapped.Entries, wrapped.BuildLogs, nil
}

func (c *Client) IngestLog(orgID, projectID, source, level, message string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/logs", orgID, projectID)
	_, err := c.doRaw(http.MethodPost, path, map[string]any{
		"source": source, "level": level, "message": message,
	}, true)
	return err
}

func (c *Client) ProjectMetrics(orgID, projectID string) ([]MetricSummary, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/metrics", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Metrics []MetricSummary `json:"metrics"`
		Mode    string          `json:"mode"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", err
	}
	if wrapped.Metrics == nil {
		wrapped.Metrics = []MetricSummary{}
	}
	return wrapped.Metrics, wrapped.Mode, nil
}
