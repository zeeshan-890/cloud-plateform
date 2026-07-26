package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Deployment struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Source     string `json:"source"`
	GitSHA     string `json:"git_sha"`
	GitBranch  string `json:"git_branch"`
	ImageRef   string `json:"image_ref"`
	BuildID    string `json:"build_id"`
	RollbackOf string `json:"rollback_of"`
	Error      string `json:"error"`
}

type Build struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Framework string `json:"framework"`
	GitSHA    string `json:"git_sha"`
	ImageRef  string `json:"image_ref"`
}

func (c *Client) CreateDeployment(orgID, projectID string, body map[string]any) (*Deployment, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/deployments", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, body, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Deployment Deployment `json:"deployment"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode deployment: %w", err)
	}
	return &wrapped.Deployment, nil
}

func (c *Client) ListDeployments(orgID, projectID string) ([]Deployment, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/deployments", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode deployments: %w", err)
	}
	if wrapped.Deployments == nil {
		return []Deployment{}, nil
	}
	return wrapped.Deployments, nil
}

func (c *Client) RollbackDeployment(orgID, projectID, deploymentID string) (*Deployment, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/deployments/rollback", orgID, projectID)
	if deploymentID != "" {
		path = fmt.Sprintf("/orgs/%s/projects/%s/deployments/%s/rollback", orgID, projectID, deploymentID)
	}
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Deployment Deployment `json:"deployment"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode deployment: %w", err)
	}
	return &wrapped.Deployment, nil
}

func (c *Client) ListBuilds(orgID, projectID string) ([]Build, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/builds", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Builds []Build `json:"builds"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode builds: %w", err)
	}
	if wrapped.Builds == nil {
		return []Build{}, nil
	}
	return wrapped.Builds, nil
}
