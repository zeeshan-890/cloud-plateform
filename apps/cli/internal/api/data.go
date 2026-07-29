package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type StorageBucket struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	OrgID     string `json:"org_id"`
	ProjectID string `json:"project_id"`
}

type StorageObject struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}

type ManagedDatabase struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	SecretRef      string `json:"secret_ref"`
	ConnectionHint string `json:"connection_hint"`
}

func (c *Client) GetStorageBucket(orgID, projectID string) (*StorageBucket, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/storage/bucket", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Bucket StorageBucket `json:"bucket"`
		Mode   string        `json:"mode"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", err
	}
	return &wrapped.Bucket, wrapped.Mode, nil
}

func (c *Client) ListStorageObjects(orgID, projectID, prefix string) ([]StorageObject, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/storage/objects", orgID, projectID)
	if prefix != "" {
		path += "?prefix=" + url.QueryEscape(prefix)
	}
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Objects []StorageObject `json:"objects"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Objects == nil {
		return []StorageObject{}, nil
	}
	return wrapped.Objects, nil
}

func (c *Client) UploadStorageObject(orgID, projectID, key, dataBase64, contentType string) (*StorageObject, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/storage/objects", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{
		"key": key, "data_base64": dataBase64, "content_type": contentType,
	}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Object StorageObject `json:"object"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return &wrapped.Object, nil
}

func (c *Client) SignedStorageURL(orgID, projectID, key, expires string) (string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/storage/signed-url", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{"key": key, "expires": expires}, true)
	if err != nil {
		return "", err
	}
	var wrapped struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return "", err
	}
	return wrapped.URL, nil
}

func (c *Client) DeleteStorageObject(orgID, projectID, key string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/storage/objects?key=%s", orgID, projectID, url.QueryEscape(key))
	_, err := c.doRaw(http.MethodDelete, path, nil, true)
	return err
}

func (c *Client) ListDatabases(orgID, projectID string) ([]ManagedDatabase, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/databases", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Databases []ManagedDatabase `json:"databases"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Databases == nil {
		return []ManagedDatabase{}, nil
	}
	return wrapped.Databases, nil
}

func (c *Client) CreateDatabase(orgID, projectID, name, env string) (*ManagedDatabase, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/databases", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{"name": name, "env": env}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Database ManagedDatabase `json:"database"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return &wrapped.Database, nil
}

func (c *Client) DeleteDatabase(orgID, projectID, dbID string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/databases/%s", orgID, projectID, dbID)
	_, err := c.doRaw(http.MethodDelete, path, nil, true)
	return err
}

type ManagedAddon struct {
	ID             string `json:"id"`
	Engine         string `json:"engine"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	Endpoint       string `json:"endpoint"`
	SecretRef      string `json:"secret_ref"`
	ConnectionHint string `json:"connection_hint"`
}

type AddonCatalogItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
	SecretKeys     []string `json:"secret_keys"`
	ModesSupported []string `json:"modes_supported"`
}

func (c *Client) AddonCatalog(orgID, projectID string) ([]AddonCatalogItem, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/addons/catalog", orgID, projectID)
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Catalog []AddonCatalogItem `json:"catalog"`
		Mode    string             `json:"mode"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", err
	}
	if wrapped.Catalog == nil {
		wrapped.Catalog = []AddonCatalogItem{}
	}
	return wrapped.Catalog, wrapped.Mode, nil
}

func (c *Client) ListAddons(orgID, projectID, engine string) ([]ManagedAddon, string, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/addons", orgID, projectID)
	if engine != "" {
		path += "?engine=" + url.QueryEscape(engine)
	}
	raw, err := c.doRaw(http.MethodGet, path, nil, true)
	if err != nil {
		return nil, "", err
	}
	var wrapped struct {
		Addons []ManagedAddon `json:"addons"`
		Mode   string         `json:"mode"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, "", err
	}
	if wrapped.Addons == nil {
		wrapped.Addons = []ManagedAddon{}
	}
	return wrapped.Addons, wrapped.Mode, nil
}

func (c *Client) CreateAddon(orgID, projectID, engine, name, env string) (*ManagedAddon, error) {
	path := fmt.Sprintf("/orgs/%s/projects/%s/addons", orgID, projectID)
	raw, err := c.doRaw(http.MethodPost, path, map[string]any{
		"engine": engine, "name": name, "env": env,
	}, true)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Addon ManagedAddon `json:"addon"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return &wrapped.Addon, nil
}

func (c *Client) DeleteAddon(orgID, projectID, addonID string) error {
	path := fmt.Sprintf("/orgs/%s/projects/%s/addons/%s", orgID, projectID, addonID)
	_, err := c.doRaw(http.MethodDelete, path, nil, true)
	return err
}
