package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Domain struct {
	ID               string `json:"id"`
	Hostname         string `json:"hostname"`
	Status           string `json:"status"`
	VerificationType string `json:"verification_type"`
	ForceVerified    bool   `json:"force_verified"`
}

type RuntimeInstance struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	DesiredState string `json:"desired_state"`
	Kind         string `json:"kind"`
	ImageRef     string `json:"image_ref"`
	Mode         string `json:"mode"`
	HealthStatus string `json:"health_status"`
	ContainerID  string `json:"container_id"`
}

func (c *Client) ListDomains(orgID, projectID string) ([]Domain, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/domains", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Domains []Domain `json:"domains"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode domains: %w", err)
	}
	if wrapped.Domains == nil {
		return []Domain{}, nil
	}
	return wrapped.Domains, nil
}

func (c *Client) AddDomain(orgID, projectID, hostname string) (*Domain, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/domains", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{"hostname": hostname}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Domain Domain `json:"domain"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode domain: %w", err)
	}
	return &wrapped.Domain, nil
}

func (c *Client) VerifyDomain(orgID, projectID, domainID string, force bool) (*Domain, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/domains/%s/verify", orgID, projectID, domainID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{"force": force}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Domain Domain `json:"domain"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode domain: %w", err)
	}
	return &wrapped.Domain, nil
}

func (c *Client) DeleteDomain(orgID, projectID, domainID string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/domains/%s", orgID, projectID, domainID)
	_, err := c.doRaw(http.MethodDelete, path, nil, true)
	return err
}

func (c *Client) ListRuntime(orgID, projectID string) ([]RuntimeInstance, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/runtime/instances", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Instances []RuntimeInstance `json:"instances"`
		Mode      string            `json:"mode"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", fmt.Errorf("decode runtime: %w", err)
	}
	if wrapped.Instances == nil {
		wrapped.Instances = []RuntimeInstance{}
	}
	return wrapped.Instances, wrapped.Mode, nil
}
