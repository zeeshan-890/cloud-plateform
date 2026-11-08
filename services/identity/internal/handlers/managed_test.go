package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/internalauth"
)

type captureAudit struct {
	mu sync.Mutex
	ev []audit.Event
}

func (c *captureAudit) Write(_ context.Context, e audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ev = append(c.ev, e)
	return nil
}

func (c *captureAudit) has(action string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		if e.Action == action {
			return true
		}
	}
	return false
}

const itok = "internal-token-for-tests"

func (e *env) internal(method, path string, body any) map[string]any {
	e.t.Helper()
	rec := e.do(method, path, body, "", internalauth.Header, itok)
	m := decode(e.t, rec)
	if m == nil {
		m = map[string]any{}
	}
	m["_status"] = float64(rec.Code)
	return m
}

var orgM = uuid.NewString()

func createManaged(t *testing.T, e *env, username string) (id, password string) {
	t.Helper()
	m := e.internal("POST", "/internal/users", map[string]any{"org_id": orgM, "name": "Dev One", "username": username})
	if m["_status"] != float64(201) {
		t.Fatalf("create: %v", m)
	}
	return m["user"].(map[string]any)["id"].(string), m["generated_password"].(string)
}

func TestManagedUserLifecycleAndForcedPasswordChange(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", itok)
	e := newEnv(t, "invite")
	e.bootstrap("admin@example.com", "correct-horse-1")
	id, pw := createManaged(t, e, "dev.one")
	if len(pw) < 16 {
		t.Fatalf("generated password too short: %q", pw)
	}
	// the password is never retrievable afterwards
	got := e.internal("GET", "/internal/managed-users/"+id+"?org_id="+orgM, nil)
	if strings.Contains(toJSON(got), pw) || got["generated_password"] != nil {
		t.Fatal("password must be shown once only")
	}
	if u := e.repo.users[id]; u.PasswordHash == pw || !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Fatal("password must be stored as a bcrypt hash")
	}

	// login by username (case-insensitive) => limited token, no refresh token, no session
	rec := e.do("POST", "/auth/login", map[string]string{"login": "Dev.One", "password": pw}, "")
	lm := decode(t, rec)
	if rec.Code != 200 || lm["must_change_password"] != true || lm["refresh_token"] != nil {
		t.Fatalf("login: %d %v", rec.Code, lm)
	}
	limited := lm["access_token"].(string)
	c, _ := e.jm.Parse(limited)
	if c.Scope != "pwchange" || !c.Managed {
		t.Fatalf("limited claims: %+v", c)
	}
	nSess := 0
	for _, sess := range e.repo.sessions {
		if sess.UserID == id {
			nSess++
		}
	}
	if nSess != 0 {
		t.Fatal("no session before the password is changed")
	}
	if rec := e.do("GET", "/auth/me", nil, limited); rec.Code != 200 {
		t.Fatalf("me with limited token: %d", rec.Code)
	}
	for _, p := range []string{"/auth/pats", "/auth/sessions"} {
		if rec := e.do("GET", p, nil, limited); rec.Code != 403 {
			t.Fatalf("limited token on %s: %d", p, rec.Code)
		}
	}
	if rec := e.do("POST", "/auth/mfa/setup", nil, limited); rec.Code != 403 {
		t.Fatalf("limited token must not set up MFA: %d", rec.Code)
	}
	// wrong current password refused; right one changes it
	if rec := e.do("POST", "/auth/password", map[string]string{"current_password": "nope", "new_password": "a-brand-new-pass-1"}, limited); rec.Code != 401 {
		t.Fatalf("wrong current: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/password", map[string]string{"current_password": pw, "new_password": "a-brand-new-pass-1"}, limited); rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/auth/me", nil, limited); rec.Code != 401 {
		t.Fatalf("limited token must die after the change: %d", rec.Code)
	}
	rec = e.do("POST", "/auth/login", map[string]string{"login": "dev.one", "password": "a-brand-new-pass-1"}, "")
	lm = decode(t, rec)
	if rec.Code != 200 || lm["must_change_password"] == true || lm["refresh_token"] == nil {
		t.Fatalf("normal login after change: %d %v", rec.Code, lm)
	}
	if e.repo.users[id].LastLoginAt == nil {
		t.Fatal("last login must be recorded")
	}
}

func toJSON(m map[string]any) string { b, _ := jsonMarshal(m); return string(b) }

func TestUsernameValidationAndUniqueness(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", itok)
	e := newEnv(t, "invite")
	for name, want := range map[string]int{
		"ab": 400, "a b c": 400, "bad/name": 400, "UPPER-ok": 201, "has@at": 400, strings.Repeat("a", 33): 400,
		"dot.dash-und_1": 201, "": 400, "dup.name": 201,
	} {
		m := e.internal("POST", "/internal/users", map[string]any{"org_id": orgM, "name": "N", "username": name})
		if m["_status"] != float64(want) {
			t.Errorf("username %q => %v want %d", name, m["_status"], want)
		}
	}
	if m := e.internal("POST", "/internal/users", map[string]any{"org_id": uuid.NewString(), "name": "N", "username": "DUP.name"}); m["_status"] != float64(409) {
		t.Errorf("usernames are globally unique (case-insensitive): %v", m["_status"])
	}
	if m := e.internal("POST", "/internal/users", map[string]any{"org_id": orgM, "name": "N", "username": "mail.user", "email": "not-an-email"}); m["_status"] != float64(400) {
		t.Errorf("bad optional email: %v", m["_status"])
	}
	if m := e.internal("POST", "/internal/users", map[string]any{"org_id": orgM, "name": "N", "username": "mail.user", "email": "ok@example.com", "password": "short"}); m["_status"] != float64(400) {
		t.Errorf("weak provided password: %v", m["_status"])
	}
	m := e.internal("POST", "/internal/users", map[string]any{"org_id": orgM, "name": "N", "username": "chosen.pw", "password": "chosen-password-1", "must_change_password": false})
	if m["_status"] != float64(201) || m["generated_password"] != nil {
		t.Errorf("a supplied password is not echoed: %v", m)
	}
}

func TestDisabledUserLockedOutImmediately(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", itok)
	e := newEnv(t, "invite")
	cap := &captureAudit{}
	e.h.Audit = cap
	id, pw := createManaged(t, e, "to.disable")
	e.internal("POST", "/internal/managed-users/"+id+"/reset-password", map[string]any{"org_id": orgM, "password": "temp-pass-for-test-1", "must_change_password": false})
	login := func(p string) map[string]any {
		rec := e.do("POST", "/auth/login", map[string]string{"login": "to.disable", "password": p}, "")
		m := decode(t, rec)
		m["_status"] = float64(rec.Code)
		return m
	}
	_ = pw
	lm := login("temp-pass-for-test-1")
	access, refresh := lm["access_token"].(string), lm["refresh_token"].(string)
	if rec := e.do("GET", "/auth/me", nil, access); rec.Code != 200 {
		t.Fatalf("pre-check: %d", rec.Code)
	}
	if m := e.internal("PATCH", "/internal/managed-users/"+id, map[string]any{"org_id": orgM, "disabled": true}); m["_status"] != float64(200) {
		t.Fatalf("disable: %v", m)
	}
	if rec := e.do("GET", "/auth/me", nil, access); rec.Code != 401 {
		t.Fatalf("access token must be rejected immediately after disable: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); rec.Code != 401 {
		t.Fatalf("refresh must be dead: %d", rec.Code)
	}
	good, bad := login("temp-pass-for-test-1"), login("wrong-password-xyz")
	if good["_status"] != float64(401) || good["error"].(map[string]any)["message"] != bad["error"].(map[string]any)["message"] {
		t.Fatalf("a disabled account must fail like a wrong password: %v vs %v", good, bad)
	}
	if !cap.has("managed_user.disabled") || !cap.has("user.login_failed") {
		t.Fatal("disable and login failure must be audited")
	}
	// enabling restores access
	e.internal("PATCH", "/internal/managed-users/"+id, map[string]any{"org_id": orgM, "disabled": false})
	if login("temp-pass-for-test-1")["_status"] != float64(200) {
		t.Fatal("re-enabled user must be able to log in")
	}
}

func TestResetAndDeleteRevokeSessionsAndAreOrgScoped(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", itok)
	e := newEnv(t, "invite")
	cap := &captureAudit{}
	e.h.Audit = cap
	id, _ := createManaged(t, e, "reset.me")
	e.internal("POST", "/internal/managed-users/"+id+"/reset-password", map[string]any{"org_id": orgM, "password": "temp-pass-for-test-1", "must_change_password": false})
	rec := e.do("POST", "/auth/login", map[string]string{"login": "reset.me", "password": "temp-pass-for-test-1"}, "")
	tok, refresh := decode(t, rec)["access_token"].(string), decode(t, rec)["refresh_token"].(string)

	// another org's admin cannot touch this account
	other := uuid.NewString()
	for _, call := range []func() map[string]any{
		func() map[string]any { return e.internal("PATCH", "/internal/managed-users/"+id, map[string]any{"org_id": other, "disabled": true}) },
		func() map[string]any { return e.internal("POST", "/internal/managed-users/"+id+"/reset-password", map[string]any{"org_id": other}) },
		func() map[string]any { return e.internal("DELETE", "/internal/managed-users/"+id+"?org_id="+other, nil) },
	} {
		if m := call(); m["_status"] != float64(404) {
			t.Fatalf("cross-org access must be 404, got %v", m["_status"])
		}
	}
	if rec := e.do("GET", "/auth/me", nil, tok); rec.Code != 200 {
		t.Fatal("failed cross-org attempts must not revoke anything")
	}
	// admin accounts (not managed) are never reachable through these endpoints
	admin, _ := e.bootstrap("boss@example.com", "correct-horse-1")
	_ = admin
	var adminID string
	for uid, u := range e.repo.users {
		if u.Email == "boss@example.com" {
			adminID = uid
		}
	}
	if m := e.internal("POST", "/internal/managed-users/"+adminID+"/reset-password", map[string]any{"org_id": orgM}); m["_status"] != float64(404) {
		t.Fatalf("non-managed accounts must be untouchable: %v", m["_status"])
	}

	m := e.internal("POST", "/internal/managed-users/"+id+"/reset-password", map[string]any{"org_id": orgM})
	if m["_status"] != float64(200) || m["must_change_password"] != true || len(m["generated_password"].(string)) < 16 {
		t.Fatalf("reset: %v", m)
	}
	if rec := e.do("GET", "/auth/me", nil, tok); rec.Code != 401 {
		t.Fatalf("reset must revoke access tokens: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); rec.Code != 401 {
		t.Fatalf("reset must revoke refresh tokens: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/login", map[string]string{"login": "reset.me", "password": "temp-pass-for-test-1"}, ""); rec.Code != 401 {
		t.Fatalf("old password must stop working: %d", rec.Code)
	}
	newPw := m["generated_password"].(string)
	lm := decode(t, e.do("POST", "/auth/login", map[string]string{"login": "reset.me", "password": newPw}, ""))
	if lm["must_change_password"] != true {
		t.Fatalf("a reset forces a change: %v", lm)
	}
	if !cap.has("managed_user.password_reset") {
		t.Fatal("reset must be audited")
	}
	if m := e.internal("DELETE", "/internal/managed-users/"+id+"?org_id="+orgM, nil); m["_status"] != float64(204) && m["_status"] != float64(0) {
		t.Fatalf("delete: %v", m)
	}
	if rec := e.do("POST", "/auth/login", map[string]string{"login": "reset.me", "password": newPw}, ""); rec.Code != 401 {
		t.Fatalf("deleted user cannot log in: %d", rec.Code)
	}
}

func TestMeIncludesMemberships(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", itok)
	e := newEnv(t, "invite")
	org := newOrgStub(t)
	e.h.OrgURL = org
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	rec := e.do("GET", "/auth/me", nil, access)
	m := decode(t, rec)
	ms, ok := m["memberships"].([]any)
	if rec.Code != 200 || !ok || len(ms) != 1 || ms[0].(map[string]any)["org_name"] != "Acme" {
		t.Fatalf("me: %d %v", rec.Code, m)
	}
}

func newOrgStub(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"memberships":[{"org_id":"o1","org_name":"Acme","role":"owner","full":true,"projects":[]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
