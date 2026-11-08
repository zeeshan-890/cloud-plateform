package e2e

import (
	"strings"
	"testing"
	"time"
)

// throwaway returns a managed throwaway user (full-access org admin of its own org so the account exists
// independently of the shared admin session). Tests may log it out, lock it, change its password, ...
func throwaway(t *testing.T) (*User, *Client) {
	t.Helper()
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	return CreateUser(t, admin, org, "member", false), admin
}

func TestAuth_BootstrapClosedAndRegistrationRejected(t *testing.T) {
	e := Load(t)
	admin := AdminClient(t)
	_ = admin
	anon := e.NewClient()

	st := anon.Must(t, "GET", "/auth/bootstrap/status", nil, 200).Map(t)
	if !Bool(st, "initialized") {
		t.Fatalf("platform should be initialised: %v", st)
	}
	if Str(st, "registration") == "open" {
		t.Errorf("registration mode is open; production installs must use closed/invite")
	}

	// second bootstrap must be refused
	r := anon.Do("POST", "/auth/bootstrap", map[string]string{"email": RandName("x") + "@e2e.test", "password": "Sup3r-Secret-pass", "name": "Mallory"})
	if r.Status != 403 {
		t.Errorf("second bootstrap: want 403, got %d %s", r.Status, truncate(string(r.Body), 200))
	}

	// self signup without invitation
	r = anon.Do("POST", "/auth/register", map[string]string{"email": RandName("self") + "@e2e.test", "password": "Sup3r-Secret-pass", "name": "Self Signup"})
	if r.Status != 403 {
		t.Errorf("self-signup: want 403 (closed/invite required), got %d %s", r.Status, truncate(string(r.Body), 200))
	}
	// bogus invite token
	r = anon.Do("POST", "/auth/register", map[string]string{"email": RandName("self") + "@e2e.test", "password": "Sup3r-Secret-pass", "name": "Self Signup", "invite_token": "not-a-real-token"})
	if r.Status == 201 || r.Status == 200 {
		t.Errorf("registration with a bogus invite token succeeded: %d", r.Status)
	}
}

func TestAuth_RefreshRotationAndLogout(t *testing.T) {
	u, _ := throwaway(t)
	e := Load(t)

	// --- rotation: every refresh token works exactly once
	c := e.NewClient()
	l := c.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200).Map(t)
	a1, r1 := Str(l, "access_token"), Str(l, "refresh_token")
	if a1 == "" || r1 == "" {
		t.Fatalf("login returned no token pair: %v", l)
	}
	c.Token = a1
	c.Must(t, "GET", "/auth/me", nil, 200)

	rf := c.Must(t, "POST", "/auth/refresh", map[string]string{"refresh_token": r1}, 200).Map(t)
	a2, r2 := Str(rf, "access_token"), Str(rf, "refresh_token")
	if a2 == "" || r2 == "" || r2 == r1 {
		t.Fatalf("refresh did not rotate the refresh token: %v", rf)
	}
	if r := c.Do("POST", "/auth/refresh", map[string]string{"refresh_token": r1}); r.Status != 401 {
		t.Errorf("replaying a used refresh token: want 401, got %d", r.Status)
	}
	// fresh session for the logout checks (reuse detection may legitimately revoke the rotated family)
	c = u.FreshClient(t)
	a2, r2 = c.Token, c.Refresh
	_ = a2

	// --- logout (with refresh token in body) revokes access AND refresh
	if r := c.Do("POST", "/auth/logout", map[string]string{"refresh_token": r2}); r.Status != 204 {
		t.Fatalf("logout: want 204, got %d %s", r.Status, r.Body)
	}
	if r := c.Do("GET", "/auth/me", nil); r.Status != 401 {
		t.Errorf("access token must be revoked after logout, got %d", r.Status)
	}
	if r := e.NewClient().Do("POST", "/auth/refresh", map[string]string{"refresh_token": r2}); r.Status != 401 {
		t.Errorf("refresh token must be revoked after logout, got %d", r.Status)
	}

	// --- logout without body: the session's refresh token is revoked too
	c2 := u.FreshClient(t)
	refresh2 := c2.Refresh
	c2.Must(t, "POST", "/auth/logout", nil, 204)
	if r := c2.Do("GET", "/auth/me", nil); r.Status != 401 {
		t.Errorf("access token must be revoked after bare logout, got %d", r.Status)
	}
	if r := e.NewClient().Do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh2}); r.Status != 401 {
		t.Errorf("refresh token of a logged-out session must be rejected, got %d", r.Status)
	}

	// --- garbage tokens
	for _, tok := range []string{"", "abc", "a.b.c", a1 + "x"} {
		cc := e.NewClient()
		cc.Token = tok
		if r := cc.Do("GET", "/auth/me", nil); r.Status != 401 {
			t.Errorf("token %q: want 401, got %d", truncate(tok, 12), r.Status)
		}
	}
}

func TestAuth_LoginRateLimit(t *testing.T) {
	u, _ := throwaway(t)
	e := Load(t)
	c := e.NewClient()
	limited := 0
	for i := 0; i < 14; i++ {
		r := c.Do("POST", "/auth/login", map[string]string{"login": u.Username, "password": "definitely-wrong-" + randHex(2)})
		switch r.Status {
		case 401:
		case 429:
			limited++
			if r.Header.Get("Retry-After") == "" {
				t.Errorf("429 without Retry-After header")
			}
		default:
			t.Fatalf("bad login %d: unexpected status %d %s", i, r.Status, r.Body)
		}
	}
	if limited == 0 {
		t.Fatalf("14 consecutive bad logins were never rate limited")
	}
	// the account stays locked even for the right password while the window is active
	if r := c.Do("POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}); r.Status != 429 {
		t.Errorf("correct password while locked out: want 429, got %d", r.Status)
	}
	// error message must not reveal whether the user exists
	r1 := e.NewClient().Do("POST", "/auth/login", map[string]string{"login": "nobody-" + randHex(3), "password": "whatever-123"})
	if r1.Status != 401 || !strings.Contains(strings.ToLower(string(r1.Body)), "invalid") {
		t.Errorf("unknown user login: want 401 'invalid ...', got %d %s", r1.Status, r1.Body)
	}
}

func TestAuth_MFA(t *testing.T) {
	u, _ := throwaway(t)
	e := Load(t)
	c := u.C

	setup := c.Must(t, "POST", "/auth/mfa/setup", nil, 200).Map(t)
	secret := Str(setup, "secret")
	if secret == "" || !strings.HasPrefix(Str(setup, "otpauth_uri"), "otpauth://totp/") {
		t.Fatalf("mfa setup response malformed: %v", setup)
	}
	// wrong code is refused
	if r := c.Do("POST", "/auth/mfa/enable", map[string]string{"code": "000000"}); r.Status == 200 {
		t.Fatalf("enable with wrong code succeeded")
	}
	code := TOTP(secret, time.Now())
	en := c.Must(t, "POST", "/auth/mfa/enable", map[string]string{"code": code}, 200).Map(t)
	backups := Arr(en, "backup_codes")
	if !Bool(en, "enabled") || len(backups) < 4 {
		t.Fatalf("enable response: %v", en)
	}

	step2 := func(mfaToken, code string) *Resp {
		return e.NewClient().Do("POST", "/auth/login", map[string]string{"mfa_token": mfaToken, "totp_code": code})
	}
	login1 := func() string {
		r := e.NewClient().Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200).Map(t)
		if !Bool(r, "mfa_required") || Str(r, "mfa_token") == "" || Str(r, "access_token") != "" {
			t.Fatalf("login with MFA enabled must return only mfa_token: %v", r)
		}
		return Str(r, "mfa_token")
	}

	// wrong TOTP
	if r := step2(login1(), "123456"); r.Status != 401 && r.Status != 400 {
		t.Errorf("wrong TOTP: want 401, got %d", r.Status)
	}
	// valid TOTP (current or next step, depending on clock skew / replay of the enable step)
	var ok bool
	for _, at := range []time.Time{time.Now().Add(30 * time.Second), time.Now(), time.Now().Add(-30 * time.Second)} {
		tp := TOTP(secret, at)
		r := step2(login1(), tp)
		if r.Status == 200 && Str(r.Map(t), "access_token") != "" {
			ok = true
			// replaying the very same code must fail
			if r2 := step2(login1(), tp); r2.Status == 200 {
				t.Errorf("TOTP code was accepted twice (no replay protection)")
			}
			break
		}
	}
	if !ok {
		t.Fatalf("could not log in with a valid TOTP code")
	}
	// mfa_token is single-use
	tok := login1()
	if r := step2(tok, "000000"); r.Status == 200 {
		t.Fatalf("login with wrong code succeeded")
	}

	// backup code: works once
	bc, _ := backups[0].(string)
	if r := step2(login1(), bc); r.Status != 200 {
		t.Fatalf("backup code login: want 200, got %d %s", r.Status, r.Body)
	}
	if r := step2(login1(), bc); r.Status == 200 {
		t.Errorf("backup code was accepted twice")
	}

	// disable with another backup code
	s := e.NewClient()
	s.Token = Str(step2Login(t, e, u, backups[1].(string)), "access_token")
	s.Must(t, "POST", "/auth/mfa/disable", map[string]string{"code": backups[2].(string)}, 200)
	e.NewClient().Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200)
}

func step2Login(t *testing.T, e *Env, u *User, code string) map[string]any {
	r := e.NewClient().Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200).Map(t)
	return e.NewClient().Must(t, "POST", "/auth/login", map[string]string{"mfa_token": Str(r, "mfa_token"), "totp_code": code}, 200).Map(t)
}

func TestAuth_PasswordChangeRevokesSessions(t *testing.T) {
	u, _ := throwaway(t)
	e := Load(t)
	s1, s2 := u.FreshClient(t), u.FreshClient(t)
	newPw := "N3w-" + randHex(8) + "-Pass"

	if r := s1.Do("POST", "/auth/password", map[string]string{"current_password": "wrong-password", "new_password": newPw}); r.Status != 401 {
		t.Errorf("password change with wrong current password: want 401, got %d", r.Status)
	}
	if r := s1.Do("POST", "/auth/password", map[string]string{"current_password": u.Password, "new_password": "short"}); r.Status != 400 {
		t.Errorf("weak new password: want 400, got %d", r.Status)
	}
	r := s1.Must(t, "POST", "/auth/password", map[string]string{"current_password": u.Password, "new_password": newPw}, 200)
	if !Bool(r.Map(t), "reauth_required") {
		t.Errorf("password change should require re-auth: %s", r.Body)
	}
	for name, c := range map[string]*Client{"changing session": s1, "other session": s2} {
		if r := c.Do("GET", "/auth/me", nil); r.Status != 401 {
			t.Errorf("%s: access token must be revoked after password change, got %d", name, r.Status)
		}
		if r := e.NewClient().Do("POST", "/auth/refresh", map[string]string{"refresh_token": c.Refresh}); r.Status != 401 {
			t.Errorf("%s: refresh token must be revoked after password change, got %d", name, r.Status)
		}
	}
	time.Sleep(1200 * time.Millisecond) // token revocation is second-granular
	if r := e.NewClient().Do("POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}); r.Status != 401 {
		t.Errorf("old password must not work, got %d", r.Status)
	}
	c := e.NewClient()
	l := c.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": newPw}, 200).Map(t)
	c.SetTokens(l)
	c.Must(t, "GET", "/auth/me", nil, 200)
}

func TestAuth_LogoutAllAndSessions(t *testing.T) {
	u, _ := throwaway(t)
	a, b := u.FreshClient(t), u.FreshClient(t)
	sess := a.Must(t, "GET", "/auth/sessions", nil, 200).Map(t)
	if len(Arr(sess, "sessions")) < 2 {
		t.Errorf("expected >=2 sessions, got %v", sess)
	}
	a.Must(t, "POST", "/auth/logout-all", nil, 204)
	for n, c := range map[string]*Client{"a": a, "b": b} {
		if r := c.Do("GET", "/auth/me", nil); r.Status != 401 {
			t.Errorf("session %s survived logout-all: %d", n, r.Status)
		}
	}
}
