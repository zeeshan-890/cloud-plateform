package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type AIExplainResult struct {
	Explanation string `json:"explanation"`
	Mode        string `json:"mode"`
}

type AIAskResult struct {
	Answer string `json:"answer"`
	Mode   string `json:"mode"`
}

func (c *Client) AIExplain(orgID, projectID, prompt, deploymentID, buildID string) (*AIExplainResult, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/ai/explain", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{
		"prompt":        prompt,
		"deployment_id": deploymentID,
		"build_id":      buildID,
	}, true)
	if err != nil {
		return nil, err
	}
	var out AIExplainResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ai explain: %w", err)
	}
	return &out, nil
}

func (c *Client) AIAsk(orgID, prompt, projectID string) (*AIAskResult, error) {
	path := fmt.Sprintf("/orgs/%s/ai/ask", orgID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{
		"prompt":     prompt,
		"project_id": projectID,
	}, true)
	if err != nil {
		return nil, err
	}
	var out AIAskResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ai ask: %w", err)
	}
	return &out, nil
}

func (c *Client) ApplyProjectConfig(orgID, projectID string, body map[string]any) (map[string]any, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/config", orgID, projectID)
	raw, err := c.doRaw(http.MethodPut, path, body, true)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode config apply: %w", err)
	}
	return out, nil
}

func (c *Client) GetProjectConfig(orgID, projectID string) (map[string]any, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/config", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return out, nil
}

func (c *Client) ConfigDrift(orgID, projectID string) (map[string]any, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/config/drift", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode drift: %w", err)
	}
	return out, nil
}

type BillingUsage struct {
	Usage  []map[string]any `json:"usage"`
	PlanID string           `json:"plan_id"`
}

func (c *Client) BillingUsage(orgID string) (*BillingUsage, error) {
	path := fmt.Sprintf("/orgs/%s/billing/usage", orgID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var out BillingUsage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode billing: %w", err)
	}
	return &out, nil
}
