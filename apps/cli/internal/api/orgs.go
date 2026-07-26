package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	OrgID       string `json:"org_id"`
	Description string `json:"description"`
}

func (c *Client) ListOrgs() ([]Org, error) {
	raw, err := c.doRaw(http.MethodGet, "/orgs", nil, true)
	if err != nil {
		return nil, err
	}
	return decodeOrgList(raw)
}

func (c *Client) ListProjects(orgID string) ([]Project, error) {
	if orgID == "" {
		return nil, fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
	}
	path := fmt.Sprintf("/orgs/%s/projects", orgID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	return decodeProjectList(raw)
}

func FindOrg(orgs []Org, slugOrID string) (*Org, error) {
	for i := range orgs {
		o := &orgs[i]
		if o.ID == slugOrID || o.Slug == slugOrID {
			return o, nil
		}
	}
	return nil, fmt.Errorf("organization %q not found", slugOrID)
}

func decodeOrgList(raw []byte) ([]Org, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Org{}, nil
	}
	var bare []Org
	if err := json.Unmarshal(raw, &bare); err == nil {
		return bare, nil
	}
	var wrapped struct {
		Orgs  []Org `json:"orgs"`
		Data  []Org `json:"data"`
		Items []Org `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode orgs: %w", err)
	}
	switch {
	case len(wrapped.Orgs) > 0:
		return wrapped.Orgs, nil
	case len(wrapped.Data) > 0:
		return wrapped.Data, nil
	case len(wrapped.Items) > 0:
		return wrapped.Items, nil
	default:
		return []Org{}, nil
	}
}

func decodeProjectList(raw []byte) ([]Project, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Project{}, nil
	}
	var bare []Project
	if err := json.Unmarshal(raw, &bare); err == nil {
		return bare, nil
	}
	var wrapped struct {
		Projects []Project `json:"projects"`
		Data     []Project `json:"data"`
		Items    []Project `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode projects: %w", err)
	}
	switch {
	case len(wrapped.Projects) > 0:
		return wrapped.Projects, nil
	case len(wrapped.Data) > 0:
		return wrapped.Data, nil
	case len(wrapped.Items) > 0:
		return wrapped.Items, nil
	default:
		return []Project{}, nil
	}
}
