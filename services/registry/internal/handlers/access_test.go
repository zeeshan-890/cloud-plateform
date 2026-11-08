package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// testAccess is the fake organization service's answer for
// GET /internal/orgs/{o}/projects/{p}/access/{userId}: (nil, false) means "not a member".
func testAccess(user string) (map[string]any, bool) {
	user = strings.TrimPrefix(user, "u-")
	view := map[string]string{}
	for _, a := range []string{"overview", "deployments", "builds", "runtime", "domains", "secrets", "addons", "databases", "storage", "logs", "metrics", "repository", "settings"} {
		view[a] = "view"
	}
	with := func(base map[string]string, kv ...string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	developer := with(view, "deployments", "manage", "builds", "manage", "runtime", "manage", "storage", "manage")
	operator := with(developer, "domains", "manage", "addons", "manage", "databases", "manage", "secrets", "manage", "repository", "manage")
	switch user {
	case "admin", "owner":
		return map[string]any{"role": "admin", "full": true}, true
	case "viewer":
		return map[string]any{"role": "member", "areas": view}, true
	case "member":
		return map[string]any{"role": "member", "areas": developer}, true
	case "operator":
		return map[string]any{"role": "member", "areas": operator}, true
	case "revealer":
		return map[string]any{"role": "member", "areas": operator, "secrets_reveal": true}, true
	case "secretsview":
		return map[string]any{"role": "member", "areas": map[string]string{"overview": "view", "secrets": "view"}}, true
	case "restricted":
		return map[string]any{"role": "member", "areas": map[string]string{"overview": "view"}}, true
	}
	return nil, false
}

// serveAccess answers the access endpoint; it reports whether the path was one.
func serveAccess(w http.ResponseWriter, p []string) bool {
	if len(p) == 7 && p[0] == "internal" && p[1] == "orgs" && p[3] == "projects" && p[5] == "access" {
		if a, ok := testAccess(p[6]); ok {
			_ = json.NewEncoder(w).Encode(a)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return true
	}
	return false
}
