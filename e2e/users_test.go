package e2e

import (
	"strings"
	"testing"
	"time"
)

func in(code int, want ...int) bool {
	for _, w := range want {
		if code == w {
			return true
		}
	}
	return false
}

func TestUsers_RestrictedAccessAndIsolation(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	orgA := NewOrg(t, admin)
	p1 := NewProject(t, admin, orgA)
	p2 := NewProject(t, admin, orgA)

	// a secret in P1 to test reveal behaviour
	const secVal = "s3cr3t-value-for-e2e-xyz"
	admin.Must(t, "POST", p1.Path("/environments/production/secrets"), map[string]string{"name": "E2E_SECRET_ONE", "value": secVal}, 201)

	viewer := CreateUser(t, admin, orgA, "member", false, Grant{"project_id": p1.ID, "preset": "viewer"})
	operator := CreateUser(t, admin, orgA, "member", false, Grant{"project_id": p1.ID, "preset": "operator", "secrets_reveal": false})
	revealer := CreateUser(t, admin, orgA, "member", false, Grant{"project_id": p1.ID, "preset": "operator", "secrets_reveal": true})

	t.Run("generated password shown once", func(t *testing.T) {
		if len(viewer.Password) < 16 {
			t.Errorf("generated password too short: %d chars", len(viewer.Password))
		}
		r := admin.Must(t, "GET", "/orgs/"+orgA.ID+"/users", nil, 200)
		if strings.Contains(string(r.Body), viewer.Password) || strings.Contains(string(r.Body), "password_hash") {
			t.Errorf("user list leaks a password / hash")
		}
		found := false
		for _, u := range Arr(r.Map(t), "users") {
			if Str(u, "user_id") == viewer.ID {
				found = true
				if Str(u, "username") != viewer.Username {
					t.Errorf("username mismatch in list: %v", u)
				}
			}
		}
		if !found {
			t.Errorf("created user missing from list")
		}
		// duplicate username -> 409
		if r := admin.Do("POST", "/orgs/"+orgA.ID+"/users", map[string]any{"name": "dup", "username": viewer.Username, "role": "member"}); r.Status != 409 {
			t.Errorf("duplicate username: want 409, got %d", r.Status)
		}
		// invalid username
		if r := admin.Do("POST", "/orgs/"+orgA.ID+"/users", map[string]any{"name": "bad", "username": "Bad Name!", "role": "member"}); r.Status != 400 {
			t.Errorf("invalid username: want 400, got %d", r.Status)
		}
	})

	t.Run("me lists only granted project", func(t *testing.T) {
		me := viewer.C.Must(t, "GET", "/auth/me", nil, 200).Map(t)
		var mine map[string]any
		for _, m := range Arr(me, "memberships") {
			if Str(m, "org_id") == orgA.ID {
				mine, _ = m.(map[string]any)
			}
		}
		if mine == nil {
			t.Fatalf("no membership for org in /auth/me: %v", me)
		}
		if Bool(mine, "full") {
			t.Errorf("restricted member must not have full access")
		}
		ps := Arr(mine, "projects")
		if len(ps) != 1 || Str(ps[0], "project_id") != p1.ID {
			t.Fatalf("/auth/me projects = %v, want only %s", ps, p1.ID)
		}
		if Str(ps[0], "areas", "domains") != "view" || Str(ps[0], "areas", "secrets") == "manage" {
			t.Errorf("unexpected areas for viewer preset: %v", Dig(ps[0], "areas"))
		}
		if Bool(ps[0], "secrets_reveal") {
			t.Errorf("viewer must not have secrets_reveal")
		}
		// project list filtered
		pl := Arr(viewer.C.Must(t, "GET", "/orgs/"+orgA.ID+"/projects", nil, 200).Map(t), "projects")
		if len(pl) != 1 || Str(pl[0], "id") != p1.ID {
			t.Errorf("project list for restricted user = %v", pl)
		}
	})

	t.Run("other project is 404 and ungranted area is 403", func(t *testing.T) {
		for _, suffix := range []string{"", "/domains", "/runtime/instances", "/environments/production/secrets", "/addons", "/storage/bucket"} {
			if r := viewer.C.Do("GET", p2.Path(suffix), nil); r.Status != 404 {
				t.Errorf("GET other project %q: want 404, got %d", suffix, r.Status)
			}
		}
		for _, suffix := range []string{"/environments/production/secrets", "/addons", "/databases", "/storage/bucket"} {
			if r := viewer.C.Do("GET", p1.Path(suffix), nil); r.Status != 403 {
				t.Errorf("viewer GET granted project area %q: want 403, got %d", suffix, r.Status)
			}
		}
		if r := viewer.C.Do("GET", p1.Path("/domains"), nil); r.Status != 200 {
			t.Errorf("viewer GET domains (granted view): want 200, got %d", r.Status)
		}
		if r := viewer.C.Do("GET", "/orgs/"+orgA.ID+"/users", nil); r.Status != 403 {
			t.Errorf("restricted user listing org users: want 403, got %d", r.Status)
		}
		if r := viewer.C.Do("GET", "/orgs/"+orgA.ID+"/access-presets", nil); r.Status != 403 {
			t.Errorf("restricted user reading presets: want 403, got %d", r.Status)
		}
	})

	t.Run("view only cannot mutate", func(t *testing.T) {
		img := ImageBase() + ":1"
		tries := []struct {
			m, path string
			body    any
		}{
			{"POST", p1.Path("/domains"), map[string]string{"hostname": RandName("v") + ".e2e.test"}},
			{"POST", p1.Path("/environments/production/secrets"), map[string]string{"name": "X_Y", "value": "z"}},
			{"POST", p1.Path("/runtime/instances"), map[string]any{"image_ref": img}},
			{"PATCH", p1.Path(""), map[string]string{"name": "pwned"}},
			{"DELETE", p1.Path(""), nil},
			{"POST", p1.Path("/addons"), map[string]string{"engine": "redis", "name": "viewer-redis"}},
			{"POST", p1.Path("/deployments"), map[string]string{"git_sha": "HEAD"}},
			{"POST", p1.Path("/repos"), map[string]string{"full_name": "heroku/node-js-getting-started"}},
		}
		for _, tc := range tries {
			if r := viewer.C.Do(tc.m, tc.path, tc.body); r.Status != 403 {
				t.Errorf("viewer %s %s: want 403, got %d %s", tc.m, tc.path, r.Status, truncate(string(r.Body), 120))
			}
		}
		// the project is unchanged
		got := admin.Must(t, "GET", p1.Path(""), nil, 200).Map(t)
		if Str(got, "project", "name") == "pwned" {
			t.Errorf("viewer modified the project")
		}
		// restricted users cannot create projects or orgs
		if r := viewer.C.Do("POST", "/orgs/"+orgA.ID+"/projects", map[string]string{"name": "n", "slug": RandName("np")}); r.Status != 403 {
			t.Errorf("restricted user creating project: want 403, got %d", r.Status)
		}
		if r := viewer.C.Do("POST", "/orgs", map[string]string{"name": "n", "slug": RandName("no")}); r.Status != 403 {
			t.Errorf("managed user creating org: want 403, got %d", r.Status)
		}
	})

	t.Run("secret values require the reveal flag", func(t *testing.T) {
		path := p1.Path("/environments/production/secrets/E2E_SECRET_ONE")
		// list never contains plaintext, for anybody
		for n, c := range map[string]*Client{"admin": admin, "operator": operator.C, "revealer": revealer.C} {
			r := c.Must(t, "GET", p1.Path("/environments/production/secrets"), nil, 200)
			if strings.Contains(string(r.Body), secVal) {
				t.Errorf("%s: secret list contains the plaintext", n)
			}
			if !strings.Contains(string(r.Body), "E2E_SECRET_ONE") {
				t.Errorf("%s: secret list misses the secret name", n)
			}
		}
		// operator (manage secrets, no reveal)
		r := operator.C.Must(t, "GET", path, nil, 200)
		if strings.Contains(string(r.Body), secVal) || Bool(r.Map(t), "revealed") {
			t.Errorf("operator without reveal flag received the plaintext: %s", truncate(string(r.Body), 300))
		}
		operator.C.Must(t, "POST", p1.Path("/environments/production/secrets"), map[string]string{"name": "E2E_OP_SECRET", "value": "op-value"}, 201)
		// revealer + admin
		for n, c := range map[string]*Client{"revealer": revealer.C, "admin": admin} {
			r := c.Must(t, "GET", path, nil, 200)
			if Str(r.Map(t), "secret", "value") != secVal || !Bool(r.Map(t), "revealed") {
				t.Errorf("%s should reveal the value: %s", n, truncate(string(r.Body), 300))
			}
		}
		// cannot grant secrets_reveal without secrets access
		if r := admin.Do("PUT", "/orgs/"+orgA.ID+"/users/"+viewer.ID+"/grants", map[string]any{"grants": []Grant{{"project_id": p1.ID, "preset": "viewer", "secrets_reveal": true}}}); r.Status != 400 {
			t.Errorf("secrets_reveal without secrets access: want 400, got %d", r.Status)
		}
		// grants for a project of another org are refused
		other := NewOrg(t, admin)
		po := NewProject(t, admin, other)
		if r := admin.Do("PUT", "/orgs/"+orgA.ID+"/users/"+viewer.ID+"/grants", map[string]any{"grants": []Grant{{"project_id": po.ID, "preset": "viewer"}}}); r.Status == 200 {
			t.Errorf("grant for a foreign project accepted")
		}
	})

	t.Run("cross organisation isolation", func(t *testing.T) {
		orgB := NewOrg(t, admin)
		pb := NewProject(t, admin, orgB)
		adminB := CreateUser(t, admin, orgB, "admin", false)

		if r := adminB.C.Do("GET", "/orgs/"+orgB.ID+"/projects/"+pb.ID, nil); r.Status != 200 {
			t.Fatalf("positive control: org admin reading own project: %d", r.Status)
		}
		type try struct {
			m, path string
			body    any
		}
		foreign := []try{
			{"GET", "/orgs/" + orgA.ID, nil},
			{"GET", "/orgs/" + orgA.ID + "/projects", nil},
			{"GET", p1.Path(""), nil},
			{"PATCH", p1.Path(""), map[string]string{"name": "idor"}},
			{"DELETE", p1.Path(""), nil},
			{"GET", p1.Path("/environments/production/secrets/E2E_SECRET_ONE"), nil},
			{"POST", "/orgs/" + orgA.ID + "/users", map[string]any{"name": "x", "username": RandName("idor"), "role": "member"}},
			{"GET", "/orgs/" + orgA.ID + "/users", nil},
		}
		for _, f := range foreign {
			if r := adminB.C.Do(f.m, f.path, f.body); !in(r.Status, 403, 404) {
				t.Errorf("foreign org %s %s: want 403/404, got %d %s", f.m, f.path, r.Status, truncate(string(r.Body), 120))
			}
		}
		// IDOR: A's project id inside B's org path
		idor := "/orgs/" + orgB.ID + "/projects/" + p1.ID
		for _, f := range []try{
			{"GET", idor, nil}, {"PATCH", idor, map[string]string{"name": "idor"}}, {"DELETE", idor, nil},
			{"GET", idor + "/environments/production/secrets", nil},
			{"GET", idor + "/environments/production/secrets/E2E_SECRET_ONE", nil},
			{"GET", idor + "/domains", nil}, {"GET", idor + "/runtime/instances", nil},
			{"GET", idor + "/addons", nil}, {"GET", idor + "/storage/objects", nil}, {"GET", idor + "/deployments", nil},
		} {
			if r := adminB.C.Do(f.m, f.path, f.body); !in(r.Status, 403, 404) {
				t.Errorf("IDOR %s %s: want 403/404, got %d %s", f.m, f.path, r.Status, truncate(string(r.Body), 120))
			}
		}
		// the owner-level admin also cannot be tricked: wrong org in path with own project id
		if r := admin.Do("GET", "/orgs/"+orgB.ID+"/projects/"+p1.ID, nil); r.Status != 404 {
			t.Errorf("project id of org A under org B (even for the owner of both): want 404, got %d", r.Status)
		}
		// P1 untouched
		got := admin.Must(t, "GET", p1.Path(""), nil, 200).Map(t)
		if Str(got, "project", "name") == "idor" {
			t.Errorf("foreign admin modified the project")
		}
		// an org admin may not create administrators; grants for foreign projects are refused
		if r := adminB.C.Do("POST", "/orgs/"+orgB.ID+"/users", map[string]any{"name": "x", "username": RandName("a"), "role": "admin"}); r.Status != 403 {
			t.Errorf("org admin creating admin: want 403, got %d", r.Status)
		}
		if r := adminB.C.Do("POST", "/orgs/"+orgB.ID+"/users", map[string]any{"name": "x", "username": RandName("g"), "role": "member", "grants": []Grant{{"project_id": p1.ID, "preset": "viewer"}}}); r.Status == 201 {
			t.Errorf("org admin granted access to a project of another org")
		}
	})

	t.Run("spoofed identity headers are ignored", func(t *testing.T) {
		me := admin.Must(t, "GET", "/auth/me", nil, 200).Map(t)
		adminID := Str(me, "user", "id")
		hdr := map[string]string{"X-Org-Role": "owner", "X-User-ID": adminID, "X-Org-ID": orgA.ID, "X-User-Role": "admin", "X-Forwarded-User": adminID, "X-Is-Admin": "true"}
		if r := viewer.C.DoH("GET", p2.Path(""), nil, hdr); r.Status != 404 {
			t.Errorf("spoofed headers on other project: want 404, got %d", r.Status)
		}
		if r := viewer.C.DoH("GET", "/orgs/"+orgA.ID+"/users", nil, hdr); r.Status != 403 {
			t.Errorf("spoofed headers on admin API: want 403, got %d", r.Status)
		}
		if r := viewer.C.DoH("GET", "/system/security", nil, hdr); r.Status != 403 {
			t.Errorf("spoofed headers on system API: want 403, got %d", r.Status)
		}
		// unauthenticated + spoofed
		if r := e.NewClient().DoH("GET", "/orgs", nil, hdr); r.Status != 401 {
			t.Errorf("unauthenticated with spoofed headers: want 401, got %d", r.Status)
		}
	})

	t.Run("disabling a user kills sessions immediately", func(t *testing.T) {
		victim := CreateUser(t, admin, orgA, "member", false, Grant{"project_id": p1.ID, "preset": "viewer"})
		sess := victim.FreshClient(t)
		sess.Must(t, "GET", "/orgs/"+orgA.ID+"/projects", nil, 200)
		admin.Must(t, "PATCH", "/orgs/"+orgA.ID+"/users/"+victim.ID, map[string]any{"disabled": true}, 200)
		time.Sleep(200 * time.Millisecond)
		if r := sess.Do("GET", "/orgs/"+orgA.ID+"/projects", nil); r.Status != 401 {
			t.Errorf("access token of a disabled user: want 401, got %d", r.Status)
		}
		if r := e.NewClient().Do("POST", "/auth/refresh", map[string]string{"refresh_token": sess.Refresh}); r.Status != 401 {
			t.Errorf("refresh token of a disabled user: want 401, got %d", r.Status)
		}
		if r := e.NewClient().Do("POST", "/auth/login", map[string]string{"login": victim.Username, "password": victim.Password}); r.Status != 401 {
			t.Errorf("login of a disabled user: want 401, got %d", r.Status)
		}
		// re-enable restores login; reset-password returns a new password once and kills sessions
		admin.Must(t, "PATCH", "/orgs/"+orgA.ID+"/users/"+victim.ID, map[string]any{"disabled": false}, 200)
		time.Sleep(1200 * time.Millisecond)
		s2 := victim.FreshClient(t)
		rp := admin.Must(t, "POST", "/orgs/"+orgA.ID+"/users/"+victim.ID+"/reset-password", map[string]any{}, 200).Map(t)
		np := Str(rp, "generated_password")
		if np == "" || np == victim.Password {
			t.Errorf("reset-password did not return a fresh password: %v", rp)
		}
		if r := s2.Do("GET", "/auth/me", nil); r.Status != 401 {
			t.Errorf("session must die on reset-password, got %d", r.Status)
		}
		time.Sleep(1200 * time.Millisecond)
		r := e.NewClient().Must(t, "POST", "/auth/login", map[string]string{"login": victim.Username, "password": np}, 200).Map(t)
		if !Bool(r, "must_change_password") && Str(r, "refresh_token") != "" {
			t.Errorf("password reset must force a password change: %v", r)
		}
	})

	t.Run("forced password change flow", func(t *testing.T) {
		u := CreateUser(t, admin, orgA, "member", true, Grant{"project_id": p1.ID, "preset": "viewer"})
		c := u.C
		l := c.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200).Map(t)
		if !Bool(l, "must_change_password") {
			t.Fatalf("login must flag must_change_password: %v", l)
		}
		if Str(l, "refresh_token") != "" {
			t.Errorf("limited token must not come with a refresh token")
		}
		c.SetTokens(l)
		c.Must(t, "GET", "/auth/me", nil, 200)
		for _, p := range []string{"/orgs", "/orgs/" + orgA.ID + "/projects", p1.Path("")} {
			if r := c.Do("GET", p, nil); r.Status != 403 {
				t.Errorf("limited token GET %s: want 403, got %d", p, r.Status)
			}
		}
		newPw := "Chang3d-" + randHex(6)
		c.Must(t, "POST", "/auth/password", map[string]string{"current_password": u.Password, "new_password": newPw}, 200)
		time.Sleep(1200 * time.Millisecond)
		c2 := e.NewClient()
		l2 := c2.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": newPw}, 200).Map(t)
		if Bool(l2, "must_change_password") || Str(l2, "refresh_token") == "" {
			t.Errorf("after the change a normal session is expected: %v", l2)
		}
		c2.SetTokens(l2)
		c2.Must(t, "GET", "/orgs/"+orgA.ID+"/projects", nil, 200)
	})

	t.Run("PATs cannot manage account security", func(t *testing.T) {
		// personal access tokens cannot manage account security
		pat := viewer.FreshClient(t)
		r := pat.Do("POST", "/auth/pats", map[string]any{"name": "e2e", "scopes": []string{"read"}})
		if r.Status == 201 || r.Status == 200 {
			tok := Str(r.Map(t), "token")
			if tok != "" {
				pc := e.NewClient()
				pc.Token = tok
				if rr := pc.Do("POST", "/auth/mfa/setup", nil); rr.Status != 403 && rr.Status != 401 {
					t.Errorf("PAT must not reach MFA setup: %d", rr.Status)
				}
			}
		}
	})
}
