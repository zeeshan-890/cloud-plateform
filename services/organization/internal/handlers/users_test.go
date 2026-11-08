package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/organization/internal/store"
)

const tok = "internal-token-for-tests"

func (hs *harness) projectInOrg(name string) string {
	id := uuid.NewString()
	hs.projects[id] = name
	return id
}

type storeGrant = store.Grant

func mustPreset(n string) map[membership.Area]membership.Level {
	a, _ := membership.PresetAreas(n)
	return a
}

func TestAdminCreatesRestrictedUserWithGrants(t *testing.T) {
	hs := newHarness(t)
	owner, admin, member := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("m@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", member: "member"})
	p1, p2 := hs.projectInOrg("web"), hs.projectInOrg("api")

	rec := hs.do("POST", "/orgs/"+org+"/users", map[string]any{
		"name": "Dev", "username": "dev.user", "role": "member",
		"grants": []map[string]any{{"project_id": p1, "preset": "developer"}},
	}, admin.token)
	out := body(rec)
	if rec.Code != 201 || out["generated_password"] != "Gen-Pass-123456789012" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	newID := out["user"].(map[string]any)["user_id"].(string)
	if hs.repo.members[org][newID] != "member" || !hs.repo.managed[org+"|"+newID] {
		t.Fatal("member row must be created as managed")
	}
	g, err := hs.repo.GetGrant(context.Background(), org, p1, newID)
	if err != nil || g.Areas[membership.AreaDeployments] != membership.LevelManage || g.Areas[membership.AreaSecrets] != membership.LevelView || g.SecretsReveal {
		t.Fatalf("developer preset not applied: %+v %v", g, err)
	}
	if _, err := hs.repo.GetGrant(context.Background(), org, p2, newID); err == nil {
		t.Fatal("no grant must exist on the other project")
	}
	if !hs.aud.has("user.created") {
		t.Fatal("creation must be audited")
	}
	// the listing never contains the password
	list := hs.do("GET", "/orgs/"+org+"/users", nil, admin.token)
	if list.Code != 200 || strings.Contains(list.Body.String(), "Gen-Pass") || !strings.Contains(list.Body.String(), "dev.user") {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	// duplicate username (global) -> 409
	if rec := hs.do("POST", "/orgs/"+org+"/users", map[string]any{"name": "X", "username": "dev.user", "role": "member"}, owner.token); rec.Code != 409 {
		t.Fatalf("duplicate username: %d", rec.Code)
	}
}

func TestOnlyAdminsCanManageUsersAndOnlyOwnersAdmins(t *testing.T) {
	hs := newHarness(t)
	owner, admin, member, viewer, outsider := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("m@e.com"), hs.user("v@e.com"), hs.user("x@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", member: "member", viewer: "viewer"})
	body := map[string]any{"name": "N", "username": "some.user", "role": "member"}
	for u, want := range map[user]int{member: 403, viewer: 403, outsider: 404} {
		if rec := hs.do("POST", "/orgs/"+org+"/users", body, u.token); rec.Code != want {
			t.Errorf("non-admin create = %d want %d", rec.Code, want)
		}
		if rec := hs.do("GET", "/orgs/"+org+"/users", nil, u.token); rec.Code != want {
			t.Errorf("non-admin list = %d want %d", rec.Code, want)
		}
	}
	if hs.identity.called("POST /internal/users") != 0 {
		t.Fatal("unauthorised callers must not reach identity")
	}
	// admin cannot create an administrator, owner can
	if rec := hs.do("POST", "/orgs/"+org+"/users", map[string]any{"name": "N", "username": "new.admin", "role": "admin"}, admin.token); rec.Code != 403 {
		t.Fatalf("admin creating admin: %d", rec.Code)
	}
	if rec := hs.do("POST", "/orgs/"+org+"/users", map[string]any{"name": "N", "username": "new.admin", "role": "admin"}, owner.token); rec.Code != 201 {
		t.Fatalf("owner creating admin: %d %s", rec.Code, rec.Body)
	}
	// admin cannot touch another admin / owner
	for _, target := range []user{owner} {
		if rec := hs.do("PATCH", "/orgs/"+org+"/users/"+target.id, map[string]any{"role": "member"}, admin.token); rec.Code != 403 {
			t.Errorf("admin modifying owner: %d", rec.Code)
		}
		if rec := hs.do("DELETE", "/orgs/"+org+"/users/"+target.id, nil, admin.token); rec.Code != 403 {
			t.Errorf("admin deleting owner: %d", rec.Code)
		}
	}
	// admin cannot promote to admin
	if rec := hs.do("PATCH", "/orgs/"+org+"/users/"+member.id, map[string]any{"role": "admin"}, admin.token); rec.Code != 403 {
		t.Errorf("admin promoting to admin: %d", rec.Code)
	}
}

func TestGrantValidation(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	p1 := hs.projectInOrg("web")
	mk := func(grants []map[string]any) int {
		return hs.do("POST", "/orgs/"+org+"/users", map[string]any{"name": "N", "username": "u" + uuid.NewString()[:8], "role": "member", "grants": grants}, owner.token).Code
	}
	cases := map[string][]map[string]any{
		"foreign project":   {{"project_id": uuid.NewString(), "preset": "viewer"}},
		"unknown preset":    {{"project_id": p1, "preset": "root"}},
		"bad area":          {{"project_id": p1, "areas": map[string]string{"nope": "view"}}},
		"bad level":         {{"project_id": p1, "areas": map[string]string{"logs": "owner"}}},
		"empty custom":      {{"project_id": p1, "areas": map[string]string{}}},
		"duplicate project": {{"project_id": p1, "preset": "viewer"}, {"project_id": p1, "preset": "developer"}},
		"reveal w/o secrets": {{"project_id": p1, "areas": map[string]string{"logs": "view"}, "secrets_reveal": true}},
	}
	for name, g := range cases {
		if c := mk(g); c != 400 {
			t.Errorf("%s => %d, want 400", name, c)
		}
	}
	if c := mk([]map[string]any{{"project_id": p1, "areas": map[string]string{"logs": "view", "secrets": "view"}, "secrets_reveal": true}}); c != 201 {
		t.Errorf("valid custom grant with reveal => %d", c)
	}
	if c := mk([]map[string]any{{"project_id": p1, "preset": "project_admin"}}); c != 201 {
		t.Errorf("project_admin preset => %d", c)
	}
}

func TestCannotGrantMoreThanHeld(t *testing.T) {
	restricted := &membership.Access{Role: membership.Member, Areas: map[membership.Area]membership.Level{
		membership.AreaLogs: membership.LevelView, membership.AreaDeployments: membership.LevelManage, membership.AreaSecrets: membership.LevelView}}
	hs := newHarness(t)
	p1 := hs.projectInOrg("web")
	org := uuid.NewString()
	h := hs.h
	cases := []struct {
		name string
		g    grantReq
		ok   bool
	}{
		{"within", grantReq{ProjectID: p1, Areas: map[membership.Area]membership.Level{membership.AreaLogs: membership.LevelView, membership.AreaDeployments: membership.LevelView}}, true},
		{"higher level", grantReq{ProjectID: p1, Areas: map[membership.Area]membership.Level{membership.AreaLogs: membership.LevelManage}}, false},
		{"area not held", grantReq{ProjectID: p1, Areas: map[membership.Area]membership.Level{membership.AreaDomains: membership.LevelView}}, false},
		{"reveal not held", grantReq{ProjectID: p1, Areas: map[membership.Area]membership.Level{membership.AreaSecrets: membership.LevelView}, SecretsReveal: boolp(true)}, false},
		{"preset above", grantReq{ProjectID: p1, Preset: "operator"}, false},
	}
	for _, c := range cases {
		_, err := h.resolveGrants(context.Background(), org, restricted, []grantReq{c.g})
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
	// owner/admin (full) may grant anything
	if _, err := h.resolveGrants(context.Background(), org, fullAccess, []grantReq{{ProjectID: p1, Preset: "project_admin"}}); err != nil {
		t.Errorf("full access grants everything: %v", err)
	}
}

func boolp(b bool) *bool { return &b }

func TestInternalAccessEndpoint(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", tok)
	hs := newHarness(t)
	owner, admin, granted, nogrant, viewer, outsider := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("g@e.com"), hs.user("n@e.com"), hs.user("v@e.com"), hs.user("x@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", granted: "member", nogrant: "member", viewer: "viewer"})
	p1, p2 := hs.projectInOrg("web"), hs.projectInOrg("api")
	_ = hs.repo.ReplaceGrants(context.Background(), org, granted.id, owner.id, []storeGrant{{ProjectID: p1, Preset: "viewer", Areas: mustPreset("viewer")}})
	_ = hs.repo.ReplaceGrants(context.Background(), org, viewer.id, owner.id, []storeGrant{{ProjectID: p1, Preset: "viewer", Areas: mustPreset("viewer")}})
	get := func(u user, p string) (int, map[string]any) {
		rec := hs.do("GET", "/internal/orgs/"+org+"/projects/"+p+"/access/"+u.id, nil, "", internalauth.Header, tok)
		return rec.Code, body(rec)
	}
	if code, m := get(owner, p2); code != 200 || m["full"] != true || m["role"] != "owner" {
		t.Errorf("owner: %d %v", code, m)
	}
	if code, m := get(admin, p1); code != 200 || m["full"] != true {
		t.Errorf("admin: %d %v", code, m)
	}
	code, m := get(granted, p1)
	areas, _ := m["areas"].(map[string]any)
	if code != 200 || m["full"] != false || areas["logs"] != "view" || areas["deployments"] != "view" || areas["secrets"] != nil || m["secrets_reveal"] != false {
		t.Errorf("granted member: %d %v", code, m)
	}
	if code, m := get(granted, p2); code != 200 || len(m["areas"].(map[string]any)) != 0 {
		t.Errorf("member without a grant on p2 gets empty areas: %d %v", code, m)
	}
	if code, m := get(nogrant, p1); code != 200 || len(m["areas"].(map[string]any)) != 0 {
		t.Errorf("member without any grant: %d %v", code, m)
	}
	if code, _ := get(viewer, p1); code != 200 {
		t.Errorf("viewer alias with grant: %d", code)
	}
	if code, _ := get(outsider, p1); code != 404 {
		t.Errorf("non-member: %d", code)
	}
	if rec := hs.do("GET", "/internal/orgs/"+org+"/projects/"+p1+"/access/"+granted.id, nil, granted.token); rec.Code != 403 {
		t.Errorf("a user JWT must not read the access oracle: %d", rec.Code)
	}
	// visible projects
	vis := func(u user) map[string]any {
		return body(hs.do("GET", "/internal/orgs/"+org+"/users/"+u.id+"/projects", nil, "", internalauth.Header, tok))
	}
	if v := vis(admin); v["full"] != true {
		t.Errorf("admin sees all: %v", v)
	}
	if v := vis(granted); v["full"] != false || len(v["project_ids"].([]any)) != 1 || v["project_ids"].([]any)[0] != p1 {
		t.Errorf("restricted sees only the granted project: %v", v)
	}
	if v := vis(nogrant); len(v["project_ids"].([]any)) != 0 {
		t.Errorf("no grants => no projects: %v", v)
	}
	// memberships feed for /auth/me
	mm := body(hs.do("GET", "/internal/users/"+granted.id+"/memberships", nil, "", internalauth.Header, tok))
	ms := mm["memberships"].([]any)
	projs := ms[0].(map[string]any)["projects"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["full"] != false || len(projs) != 1 || projs[0].(map[string]any)["name"] != "web" {
		t.Errorf("memberships: %v", mm)
	}
	am := body(hs.do("GET", "/internal/users/"+admin.id+"/memberships", nil, "", internalauth.Header, tok))
	if len(am["memberships"].([]any)[0].(map[string]any)["projects"].([]any)) != 2 {
		t.Errorf("admin sees every project in memberships: %v", am)
	}
}

func TestPutGrantsReplacesAndRevealFlag(t *testing.T) {
	hs := newHarness(t)
	owner, admin, member := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("m@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", member: "member"})
	p1, p2 := hs.projectInOrg("web"), hs.projectInOrg("api")
	put := func(actor user, target user, g []map[string]any) int {
		return hs.do("PUT", "/orgs/"+org+"/users/"+target.id+"/grants", map[string]any{"grants": g}, actor.token).Code
	}
	if c := put(admin, member, []map[string]any{{"project_id": p1, "preset": "operator", "secrets_reveal": true}, {"project_id": p2, "preset": "viewer"}}); c != 200 {
		t.Fatalf("put: %d", c)
	}
	g, _ := hs.repo.GetGrant(context.Background(), org, p1, member.id)
	if !g.SecretsReveal || g.Areas[membership.AreaSecrets] != membership.LevelManage {
		t.Fatalf("operator + reveal: %+v", g)
	}
	// replace: p2 only, p1 revoked
	if c := put(owner, member, []map[string]any{{"project_id": p2, "preset": "viewer"}}); c != 200 {
		t.Fatal(c)
	}
	if _, err := hs.repo.GetGrant(context.Background(), org, p1, member.id); err == nil {
		t.Fatal("replaced grants must remove the old ones")
	}
	if c := put(member, member, nil); c != 403 {
		t.Errorf("members cannot grant themselves anything: %d", c)
	}
	if c := put(owner, admin, []map[string]any{{"project_id": p1, "preset": "viewer"}}); c != 400 {
		t.Errorf("grants do not apply to admins: %d", c)
	}
	if !hs.aud.has("user.grants_replaced") {
		t.Fatal("grant changes must be audited")
	}
	// a project preset keeps reveal off unless asked
	if c := put(owner, member, []map[string]any{{"project_id": p1, "preset": "developer"}}); c != 200 {
		t.Fatal(c)
	}
	g, _ = hs.repo.GetGrant(context.Background(), org, p1, member.id)
	if g.SecretsReveal {
		t.Fatal("developer preset must not reveal secrets")
	}
}

func TestLastOwnerAndSelfProtection(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	if rec := hs.do("PATCH", "/orgs/"+org+"/users/"+owner.id, map[string]any{"role": "member"}, owner.token); rec.Code != 409 {
		t.Errorf("demoting the last owner: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/users/"+owner.id, nil, owner.token); rec.Code != 400 {
		t.Errorf("deleting yourself via users API: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/members/"+owner.id, nil, owner.token); rec.Code != 409 {
		t.Errorf("removing the last owner via members API: %d", rec.Code)
	}
	if hs.repo.members[org][owner.id] != "owner" {
		t.Fatal("owner must remain")
	}
}

func TestDisableResetDeleteManagedUser(t *testing.T) {
	hs := newHarness(t)
	owner, admin := hs.user("o@e.com"), hs.user("a@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin"})
	p1 := hs.projectInOrg("web")
	rec := hs.do("POST", "/orgs/"+org+"/users", map[string]any{"name": "D", "username": "managed.one", "role": "member", "grants": []map[string]any{{"project_id": p1, "preset": "viewer"}}}, admin.token)
	id := body(rec)["user"].(map[string]any)["user_id"].(string)

	if rec := hs.do("PATCH", "/orgs/"+org+"/users/"+id, map[string]any{"disabled": true}, admin.token); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if hs.identity.users[id]["disabled"] != true || !hs.aud.has("user.disabled") {
		t.Fatal("disable must reach identity (which revokes sessions) and be audited")
	}
	if rec := hs.do("PATCH", "/orgs/"+org+"/users/"+admin.id, map[string]any{"disabled": true}, owner.token); rec.Code != 400 {
		// admin is not managed => rejected
		t.Errorf("non-managed accounts cannot be disabled here: %d", rec.Code)
	}
	rr := hs.do("POST", "/orgs/"+org+"/users/"+id+"/reset-password", nil, admin.token)
	if rr.Code != 200 || body(rr)["generated_password"] != "Reset-Pass-1234567890" || body(rr)["must_change_password"] != true {
		t.Fatalf("reset: %d %s", rr.Code, rr.Body)
	}
	if rec := hs.do("POST", "/orgs/"+org+"/users/"+admin.id+"/reset-password", nil, owner.token); rec.Code != 400 {
		t.Errorf("reset of a non-managed account: %d", rec.Code)
	}
	if rec := hs.do("GET", "/orgs/"+org+"/users/"+id+"/sessions", nil, admin.token); rec.Code != 200 {
		t.Errorf("sessions: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/users/"+id+"/sessions/"+uuid.NewString(), nil, admin.token); rec.Code != 204 {
		t.Errorf("revoke session: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/users/"+id, nil, admin.token); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, ok := hs.repo.members[org][id]; ok {
		t.Fatal("membership must be gone")
	}
	if g, _ := hs.repo.ListGrantsForUser(context.Background(), org, id); len(g) != 0 {
		t.Fatal("grants must be gone")
	}
	if _, still := hs.identity.users[id]; still {
		t.Fatal("the account must be deleted at identity (revoking all sessions)")
	}
	for _, a := range []string{"user.password_reset", "user.deleted"} {
		if !hs.aud.has(a) {
			t.Errorf("%s must be audited", a)
		}
	}
}

func TestManagedAccountsCannotCreateOrgs(t *testing.T) {
	hs := newHarness(t)
	id := uuid.NewString()
	managed, _, _ := hs.jm.IssueAccessWith(id, "", "M", jwtutil.IssueOptions{Managed: true})
	if rec := hs.do("POST", "/orgs", map[string]string{"name": "X", "slug": "xx-org"}, managed); rec.Code != 403 {
		t.Fatalf("managed account creating an org: %d", rec.Code)
	}
	normal, _, _ := hs.jm.IssueAccess(id, "u@e.com", "U")
	if rec := hs.do("POST", "/orgs", map[string]string{"name": "X", "slug": "xx-org"}, normal); rec.Code != 201 {
		t.Fatalf("normal account: %d", rec.Code)
	}
}

func TestAccessPresetsEndpoint(t *testing.T) {
	hs := newHarness(t)
	owner, member := hs.user("o@e.com"), hs.user("m@e.com")
	org := hs.orgWith(owner, map[user]string{member: "member"})
	if rec := hs.do("GET", "/orgs/"+org+"/access-presets", nil, member.token); rec.Code != 403 {
		t.Fatalf("members cannot read admin presets: %d", rec.Code)
	}
	rec := hs.do("GET", "/orgs/"+org+"/access-presets", nil, owner.token)
	m := body(rec)
	if rec.Code != 200 || len(m["areas"].([]any)) != 13 || len(m["presets"].([]any)) != 4 {
		t.Fatalf("presets: %d %s", rec.Code, rec.Body)
	}
}

func TestGrantsDroppedWhenProjectDeleted(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", tok)
	hs := newHarness(t)
	owner, member := hs.user("o@e.com"), hs.user("m@e.com")
	org := hs.orgWith(owner, map[user]string{member: "member"})
	p1 := hs.projectInOrg("web")
	_ = hs.repo.ReplaceGrants(context.Background(), org, member.id, owner.id, []storeGrant{{ProjectID: p1, Preset: "viewer", Areas: mustPreset("viewer")}})
	if rec := hs.do("DELETE", "/internal/projects/"+p1, nil, "", internalauth.Header, tok); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if g, _ := hs.repo.ListGrantsForUser(context.Background(), org, member.id); len(g) != 0 {
		t.Fatal("grants of a deleted project must be removed")
	}
	_ = time.Now
}
