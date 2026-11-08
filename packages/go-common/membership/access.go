package membership

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/httpx"
)

// Area is a functional part of a project that access can be granted on separately.
type Area string

const (
	AreaOverview    Area = "overview"
	AreaDeployments Area = "deployments" // deploy, rollback, deployment history
	AreaBuilds      Area = "builds"
	AreaRuntime     Area = "runtime" // instances, start/stop/restart
	AreaDomains     Area = "domains"
	AreaSecrets     Area = "secrets" // environment variables & secrets
	AreaAddons      Area = "addons"
	AreaDatabases   Area = "databases"
	AreaStorage     Area = "storage"
	AreaLogs        Area = "logs"
	AreaMetrics     Area = "metrics"
	AreaRepository  Area = "repository" // git connection, deploy keys
	AreaSettings    Area = "settings"   // rename, jp.yaml config, delete project
)

// AllAreas lists every area (stable order, used by UIs and presets).
var AllAreas = []Area{AreaOverview, AreaDeployments, AreaBuilds, AreaRuntime, AreaDomains, AreaSecrets,
	AreaAddons, AreaDatabases, AreaStorage, AreaLogs, AreaMetrics, AreaRepository, AreaSettings}

// Level is the access level on an area.
type Level string

const (
	LevelNone   Level = "none"
	LevelView   Level = "view"   // read-only
	LevelManage Level = "manage" // create/update/delete within the area
)

var levelRank = map[Level]int{LevelNone: 0, LevelView: 1, LevelManage: 2}

// AtLeast reports whether l satisfies need.
func (l Level) AtLeast(need Level) bool { return levelRank[l] >= levelRank[need] }

// Access is what a user may do inside one project.
type Access struct {
	Role          Role           `json:"role"`
	Full          bool           `json:"full"`           // org owner/admin: everything, every project
	Areas         map[Area]Level `json:"areas"`          // ignored when Full
	SecretsReveal bool           `json:"secrets_reveal"` // may read plaintext secret values
}

// Level returns the access level for an area.
func (a *Access) Level(area Area) Level {
	if a == nil {
		return LevelNone
	}
	if a.Full {
		return LevelManage
	}
	if l, ok := a.Areas[area]; ok {
		return l
	}
	return LevelNone
}

// Any reports whether the user has at least view access on some area (i.e. may see the project at all).
func (a *Access) Any() bool {
	if a == nil {
		return false
	}
	if a.Full {
		return true
	}
	for _, l := range a.Areas {
		if l.AtLeast(LevelView) {
			return true
		}
	}
	return false
}

// CanRevealSecrets reports whether plaintext secret values may be returned.
func (a *Access) CanRevealSecrets() bool {
	return a != nil && (a.Full || (a.SecretsReveal && a.Level(AreaSecrets).AtLeast(LevelView)))
}

type accessEntry struct {
	a   *Access
	exp time.Time
}

var accessCache = struct {
	sync.Mutex
	m map[string]accessEntry
}{m: map[string]accessEntry{}}

const accessTTL = 5 * time.Second

// Access fetches the user's effective access on a project from the organization service
// (GET /internal/orgs/{orgId}/projects/{projectId}/access/{userId}). Returns (nil, nil) when
// the user is not a member of the org. Results are cached briefly (revocations take effect
// within accessTTL).
func (c *Client) Access(r *http.Request, orgID, projectID, userID string) (*Access, error) {
	if c == nil || c.OrgURL == "" {
		return nil, fmt.Errorf("organization service not configured")
	}
	key := c.OrgURL + "|" + orgID + "|" + projectID + "|" + userID
	accessCache.Lock()
	if e, ok := accessCache.m[key]; ok && time.Now().Before(e.exp) {
		accessCache.Unlock()
		return e.a, nil
	}
	accessCache.Unlock()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/internal/orgs/%s/projects/%s/access/%s", c.OrgURL, orgID, projectID, userID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out *Access
	switch resp.StatusCode {
	case http.StatusNotFound:
		out = nil
	case http.StatusOK:
		var a Access
		if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<16)).Decode(&a); err != nil {
			return nil, err
		}
		out = &a
	default:
		return nil, fmt.Errorf("organization service status %d", resp.StatusCode)
	}
	accessCache.Lock()
	if len(accessCache.m) > 8192 {
		accessCache.m = map[string]accessEntry{}
	}
	accessCache.m[key] = accessEntry{a: out, exp: time.Now().Add(accessTTL)}
	accessCache.Unlock()
	return out, nil
}

// RequireArea is THE check for project-scoped endpoints: the user must be an org member, the project must
// belong to the org, and the user's access on `area` must be at least `need` (org owner/admin always pass).
// It writes the HTTP error and returns ok=false on failure; it FAILS CLOSED on lookup problems.
func (c *Client) RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area Area, need Level) (*Access, bool) {
	if ok := c.checkProject(w, r, orgID, projectID); !ok {
		return nil, false
	}
	a, err := c.Access(r, orgID, projectID, userID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "access check unavailable")
		return nil, false
	}
	if a == nil {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return nil, false
	}
	if !a.Any() {
		// do not reveal that the project exists to users without any grant
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return nil, false
	}
	if !a.Level(area).AtLeast(need) {
		httpx.Error(w, http.StatusForbidden, "forbidden",
			fmt.Sprintf("you do not have %s access to %s for this project", need, area))
		return a, false
	}
	return a, true
}

// RequireOrgAdmin requires org owner/admin (user management, org-wide settings).
func (c *Client) RequireOrgAdmin(w http.ResponseWriter, r *http.Request, orgID, userID string) bool {
	_, ok := c.Require(w, r, orgID, userID, Admin)
	return ok
}

// checkProject verifies projectID belongs to orgID (404 otherwise). Cached 30s on success.
func (c *Client) checkProject(w http.ResponseWriter, r *http.Request, orgID, projectID string) bool {
	if c.ProjectURL == "" || projectID == "" {
		return true
	}
	key := orgID + "|" + projectID
	c.mu.Lock()
	if e, hit := c.cache[key]; hit && time.Now().Before(e.exp) {
		c.mu.Unlock()
		if e.ok {
			return true
		}
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return false
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/internal/orgs/%s/projects/%s", c.ProjectURL, orgID, projectID), nil)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "project check unavailable")
		return false
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "project check unavailable")
		return false
	}
	defer resp.Body.Close()
	exists := resp.StatusCode == http.StatusOK
	if !exists && resp.StatusCode != http.StatusNotFound {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "project check unavailable")
		return false
	}
	c.mu.Lock()
	if c.cache == nil || len(c.cache) > 4096 {
		c.cache = map[string]cacheEntry{}
	}
	if exists {
		c.cache[key] = cacheEntry{ok: true, exp: time.Now().Add(5 * time.Second)}
	}
	c.mu.Unlock()
	if !exists {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return false
	}
	return true
}

// AccessibleProjects returns which projects of the org the user may see
// (GET /internal/orgs/{orgId}/users/{userId}/projects). Not a member -> ok=false.
func (c *Client) AccessibleProjects(r *http.Request, orgID, userID string) (ProjectScope, bool, error) {
	if c == nil || c.OrgURL == "" {
		return ProjectScope{}, false, fmt.Errorf("organization service not configured")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/internal/orgs/%s/users/%s/projects", c.OrgURL, orgID, userID), nil)
	if err != nil {
		return ProjectScope{}, false, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ProjectScope{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ProjectScope{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ProjectScope{}, false, fmt.Errorf("organization service status %d", resp.StatusCode)
	}
	var body struct {
		Full       bool     `json:"full"`
		ProjectIDs []string `json:"project_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(&body); err != nil {
		return ProjectScope{}, false, err
	}
	sc := ProjectScope{Full: body.Full, ProjectIDs: map[string]bool{}}
	for _, id := range body.ProjectIDs {
		sc.ProjectIDs[id] = true
	}
	return sc, true, nil
}
