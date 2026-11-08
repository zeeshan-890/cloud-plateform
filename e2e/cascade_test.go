package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestProjectDelete_CascadesEverywhere(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)
	admin.Timeout = 10 * time.Minute
	defer func() { admin.Timeout = 60 * time.Second }()

	// a populated project: app, add-on (container + volume + secret), secrets, custom (unverified) domain, storage object, other project untouched
	other := NewProject(t, admin, org)
	admin.Must(t, "POST", other.Path("/environments/production/secrets"), map[string]string{"name": "KEEP_ME", "value": "x"}, 201)

	admin.Must(t, "POST", p.Path("/environments/production/secrets"), map[string]string{"name": "E2E_CASCADE", "value": "v"}, 201)
	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)
	cat := admin.Must(t, "GET", p.Path("/addons/catalog"), nil, 200).Map(t)
	redisOK := false
	for _, it := range Arr(cat, "catalog") {
		if Str(it, "id") == "redis" && Bool(it, "provisionable") {
			redisOK = true
		}
	}
	if redisOK {
		admin.Must(t, "POST", p.Path("/addons"), map[string]any{"engine": "redis", "name": "e2e-cascade-redis"}, 201)
	}
	custom := RandName("casc") + ".e2e.test"
	admin.Must(t, "POST", p.Path("/domains"), map[string]string{"hostname": custom}, 201)
	admin.Must(t, "POST", p.Path("/storage/objects"), map[string]string{"key": "a.txt", "content_base64": "aGk="}, 201)

	before := e.ProjectContainers(t, p.ID)
	if len(before) == 0 || (redisOK && len(before) < 2) {
		t.Fatalf("expected app (+addon) containers before delete: %+v", before)
	}
	if !strings.Contains(e.Run(t, "docker network ls --format '{{.Name}}'"), p.Network()) {
		t.Fatalf("project network missing before delete")
	}
	if g := e.TraefikDynamicGrep(t, host); g == "" {
		t.Fatalf("no router before delete")
	}

	admin.Must(t, "DELETE", p.Path(""), nil, 204)

	// --- docker: containers (all states), network, volumes
	if left := e.ProjectContainers(t, p.ID); len(left) != 0 {
		t.Errorf("containers left after project delete: %+v", left)
	}
	if nets := e.Run(t, "docker network ls --format '{{.Name}}'"); strings.Contains(nets, p.Network()) {
		t.Errorf("project network %s still exists", p.Network())
	}
	if vols := e.Run(t, "docker volume ls -q --filter label=jp.project="+p.ID); vols != "" {
		t.Errorf("volumes left after project delete: %s", vols)
	}
	// traefik no longer attached anywhere to a deleted net (covered by the network being gone); no router either
	Eventually(t, 30*time.Second, time.Second, "routers removed", func() (bool, string) {
		return e.TraefikDynamicGrep(t, host) == "" && e.TraefikDynamicGrep(t, custom) == "", "router remains"
	})
	if code, _, _ := e.EdgeGet(host, "/health"); code == 200 {
		t.Errorf("deleted project's hostname still answers 200")
	}
	// --- API: project gone, sub-resources gone
	admin.Must(t, "GET", p.Path(""), nil, 404)
	for _, s := range []string{"/domains", "/environments/production/secrets", "/addons", "/runtime/instances", "/storage/objects"} {
		sub := s
		// access/project lookups are cached for a few seconds by the services, so allow them to expire
		Eventually(t, 20*time.Second, time.Second, "GET "+sub+" after delete is 404", func() (bool, string) {
			r := admin.Do("GET", p.Path(sub), nil)
			return r.Status == 404, fmt.Sprintf("status %d", r.Status)
		})
	}
	// the sibling project is untouched
	g := admin.Must(t, "GET", other.Path("/environments/production/secrets"), nil, 200).Map(t)
	if len(Arr(g, "secrets")) != 1 {
		t.Errorf("sibling project's secrets were affected: %v", g)
	}
	// slug and system hostname can be reused (nothing dangling)
	r := admin.Must(t, "POST", "/orgs/"+org.ID+"/projects", map[string]string{"name": "again", "slug": p.Slug}, 201).Map(t)
	np := &Project{Org: org, ID: Str(r, "project", "id"), Slug: p.Slug}
	if SystemHost(t, admin, np) != host {
		t.Errorf("recreated project did not get the same system hostname")
	}
	admin.Must(t, "DELETE", np.Path(""), nil, 204)
}

func TestOrgDelete_RemovesProjectsAndResources(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	admin.Timeout = 10 * time.Minute
	defer func() { admin.Timeout = 60 * time.Second }()
	DeployApp(t, admin, p, img, nil)
	if len(e.ProjectContainers(t, p.ID)) == 0 {
		t.Fatalf("no container")
	}
	// only the owner may delete the org
	m := CreateUser(t, admin, org, "admin", false)
	if r := m.C.Do("DELETE", "/orgs/"+org.ID, nil); r.Status != 403 {
		t.Errorf("org admin deleting the org: want 403, got %d", r.Status)
	}
	admin.Must(t, "DELETE", "/orgs/"+org.ID, nil, 204, 200)
	if left := e.ProjectContainers(t, p.ID); len(left) != 0 {
		t.Errorf("containers left after org delete: %+v", left)
	}
	if strings.Contains(e.Run(t, "docker network ls --format '{{.Name}}'"), p.Network()) {
		t.Errorf("project network left after org delete")
	}
	admin.Must(t, "GET", "/orgs/"+org.ID, nil, 403, 404)
	if r := m.C.Do("GET", "/orgs/"+org.ID+"/projects", nil); r.Status == 200 {
		t.Errorf("former org admin still lists projects")
	}
}
