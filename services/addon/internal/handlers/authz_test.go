package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jp-cloud/go-common/membership"
)

// testOrg is a fake organization + project service that answers the same internal endpoints the real
// ones do, so the REAL membership.Client (RequireArea / Require / project binding) is what the tests exercise.
type testOrg struct {
	mu       sync.Mutex
	Projects map[string]string // projectID -> orgID (project service binding)
	Users    map[string]membership.Access
	Client   *membership.Client
}

func allAreas(l membership.Level) map[membership.Area]membership.Level {
	m := map[membership.Area]membership.Level{}
	for _, a := range membership.AllAreas {
		m[a] = l
	}
	return m
}

// standard users: owner/admin (full), member (manage everywhere, no reveal), viewer (view everywhere),
// revealer (manage + secrets_reveal), nogrant (org member without any project grant); anyone else is not a member.
func stdUsers() map[string]membership.Access {
	return map[string]membership.Access{
		"owner":    {Role: membership.Owner, Full: true, SecretsReveal: true},
		"admin":    {Role: membership.Admin, Full: true, SecretsReveal: true},
		"member":   {Role: membership.Member, Areas: allAreas(membership.LevelManage)},
		"viewer":   {Role: membership.Viewer, Areas: allAreas(membership.LevelView)},
		"revealer": {Role: membership.Member, Areas: allAreas(membership.LevelManage), SecretsReveal: true},
		"nogrant":  {Role: membership.Member, Areas: map[membership.Area]membership.Level{}},
	}
}

func newTestOrg(t *testing.T, projects map[string]string) *testOrg {
	o := &testOrg{Projects: projects, Users: stdUsers()}
	if o.Projects == nil {
		o.Projects = map[string]string{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // internal orgs {o} ...
		switch {
		case len(p) == 5 && p[3] == "members": // /internal/orgs/{o}/members/{u}
			a, ok := o.Users[p[4]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"role": string(a.Role)})
		case len(p) == 5 && p[3] == "projects": // /internal/orgs/{o}/projects/{p}
			if org, ok := o.Projects[p[4]]; ok && org == p[2] {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		case len(p) == 7 && p[5] == "access": // /internal/orgs/{o}/projects/{p}/access/{u}
			a, ok := o.Users[p[6]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(a)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	o.Client = membership.New(srv.URL, srv.URL)
	return o
}
