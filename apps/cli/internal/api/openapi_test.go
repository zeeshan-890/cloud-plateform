package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The published OpenAPI document must be valid YAML, describe only public routes, and have
// resolvable $refs.
func TestOpenAPIDocument(t *testing.T) {
	raw, err := os.ReadFile("../../../../packages/openapi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) < 60 {
		t.Fatalf("suspiciously few paths: %d", len(paths))
	}
	for p, item := range paths {
		if strings.Contains(p, "internal") {
			t.Errorf("internal route documented: %s", p)
		}
		ops, _ := item.(map[string]any)
		for m, op := range ops {
			if m == "parameters" {
				continue
			}
			o, _ := op.(map[string]any)
			if _, ok := o["responses"]; !ok {
				t.Errorf("%s %s has no responses", m, p)
			}
		}
	}
	for _, must := range []string{"/auth/mfa/setup", "/auth/mfa/enable", "/auth/mfa/disable", "/auth/logout-all", "/auth/password",
		"/system/settings", "/system/security", "/system/cluster", "/system/maintenance/cleanup", "/orgs/{orgId}/members/{userId}", "/orgs/{orgId}/invites/{inviteId}", "/orgs/{orgId}/users", "/orgs/{orgId}/users/{userId}/grants", "/orgs/{orgId}/access-presets"} {
		if _, ok := paths[must]; !ok {
			t.Errorf("missing path %s", must)
		}
	}
	for p := range paths {
		if strings.Contains(p, "billing") {
			t.Errorf("billing was removed: %s", p)
		}
	}
	if _, ok := paths["/orgs/{orgId}/projects/{projectId}/metrics/targets"]; ok {
		t.Error("removed route still documented")
	}
	refRe := regexp.MustCompile(`#/components/(\w+)/([A-Za-z0-9_]+)`)
	comps, _ := doc["components"].(map[string]any)
	for _, m := range refRe.FindAllStringSubmatch(string(raw), -1) {
		group, _ := comps[m[1]].(map[string]any)
		if _, ok := group[m[2]]; !ok {
			t.Errorf("dangling $ref %s", m[0])
		}
	}
}
