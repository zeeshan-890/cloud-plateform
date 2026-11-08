// Package membership centralises org membership, role and project-binding checks so
// every service enforces RBAC identically.
package membership

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
)

// Role is an org role.
type Role string

const (
	Viewer Role = "viewer"
	Member Role = "member"
	Admin  Role = "admin"
	Owner  Role = "owner"
)

var rank = map[Role]int{Viewer: 1, Member: 2, Admin: 3, Owner: 4}

// AtLeast reports whether r satisfies min.
func (r Role) AtLeast(min Role) bool { return rank[r] >= rank[min] && rank[r] > 0 }

// Client verifies membership against the organization service and (optionally)
// that a project belongs to the org via the project service.
type Client struct {
	OrgURL     string
	ProjectURL string // optional; enables project<->org binding checks
	HTTP       *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	ok  bool
	exp time.Time
}

// New builds a Client with a signing HTTP client.
func New(orgURL, projectURL string) *Client {
	return &Client{OrgURL: strings.TrimRight(orgURL, "/"), ProjectURL: strings.TrimRight(projectURL, "/"),
		HTTP: internalauth.NewClient(5 * time.Second), cache: map[string]cacheEntry{}}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return internalauth.NewClient(5 * time.Second)
}

// Role looks up the user's role in the org. Returns ("", nil) when not a member.
func (c *Client) Role(r *http.Request, orgID, userID string) (Role, error) {
	if c == nil || c.OrgURL == "" {
		return "", fmt.Errorf("organization service not configured")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/internal/orgs/%s/members/%s", c.OrgURL, orgID, userID), nil)
	if err != nil {
		return "", err
	}
	hc := c.httpClient()
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("organization service status %d", resp.StatusCode)
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return Role(body.Role), nil
}

// Require enforces membership with at least min role. It writes the HTTP error and
// returns ok=false on failure. FAILS CLOSED on any lookup problem.
func (c *Client) Require(w http.ResponseWriter, r *http.Request, orgID, userID string, min Role) (Role, bool) {
	role, err := c.Role(r, orgID, userID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "membership check unavailable")
		return "", false
	}
	if role == "" {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return "", false
	}
	if !role.AtLeast(min) {
		httpx.Error(w, http.StatusForbidden, "forbidden", fmt.Sprintf("%s role or higher required", min))
		return role, false
	}
	return role, true
}

// RequireProject (legacy, role based) = org role check + project binding. Org owner/admin pass by role;
// restricted users (member/viewer) additionally need an explicit grant on the project. Prefer RequireArea.
func (c *Client) RequireProject(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, min Role) (Role, bool) {
	role, ok := c.Require(w, r, orgID, userID, min)
	if !ok {
		return role, false
	}
	if !c.checkProject(w, r, orgID, projectID) {
		return role, false
	}
	if role.AtLeast(Admin) {
		return role, true
	}
	a, err := c.Access(r, orgID, projectID, userID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "access check unavailable")
		return role, false
	}
	if a == nil || !a.Any() {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return role, false
	}
	return role, true
}
