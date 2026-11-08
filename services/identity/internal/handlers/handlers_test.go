package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/ratelimit"
	"github.com/jp-cloud/go-common/revocation"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/identity/internal/store"
	"github.com/jp-cloud/identity/internal/totp"
)

const testSecret = "0123456789abcdef0123456789abcdef0123"

type env struct {
	t    *testing.T
	h    *Handler
	mux  *http.ServeMux
	repo *fakeRepo
	mr   *miniredis.Miniredis
	jm   *jwtutil.Manager
}

func newEnv(t *testing.T, mode string) *env {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	repo := newFakeRepo()
	jm := jwtutil.NewManager(testSecret, 15*time.Minute, 24*time.Hour)
	revoker := &revocation.Store{Redis: rdb}
	h := &Handler{
		Store: repo, JWT: jm, Redis: rdb, Revoker: revoker,
		Limiter:          &ratelimit.Limiter{Redis: rdb, Prefix: "t:"},
		RegistrationMode: mode,
		EncKey:           secretbox.DeriveKey("master", "enc"),
		BackupKey:        secretbox.DeriveKey("master", "backup"),
		HTTP:             http.DefaultClient,
	}
	middleware.DenyCheckClaims = func(c context.Context, cl *jwtutil.Claims) bool {
		d, err := revoker.Denied(c, cl)
		return err != nil || d
	}
	t.Cleanup(func() { middleware.DenyCheckClaims = nil })
	mux := http.NewServeMux()
	h.Routes(mux)
	return &env{t: t, h: h, mux: mux, repo: repo, mr: mr, jm: jm}
}

func (e *env) do(method, path string, body any, token string, hdr ...string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "198.51.100.10:4000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil && rec.Body.Len() > 0 {
		t.Fatalf("bad json %q: %v", rec.Body.String(), err)
	}
	return m
}

func (e *env) bootstrap(email, pw string) (access, refresh string) {
	e.t.Helper()
	rec := e.do("POST", "/auth/bootstrap", map[string]string{"email": email, "name": "Admin", "password": pw}, "")
	if rec.Code != 201 {
		e.t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body)
	}
	m := decode(e.t, rec)
	return m["access_token"].(string), m["refresh_token"].(string)
}

func (e *env) login(email, pw string) *httptest.ResponseRecorder {
	return e.do("POST", "/auth/login", map[string]string{"email": email, "password": pw}, "")
}

// enableMFA enrols the user and returns the secret and the plaintext backup codes.
func (e *env) enableMFA(access string) (string, []string) {
	e.t.Helper()
	rec := e.do("POST", "/auth/mfa/setup", nil, access)
	if rec.Code != 200 {
		e.t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	secret := decode(e.t, rec)["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now().UTC())
	rec = e.do("POST", "/auth/mfa/enable", map[string]string{"code": code}, access)
	if rec.Code != 200 {
		e.t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	var codes []string
	for _, c := range decode(e.t, rec)["backup_codes"].([]any) {
		codes = append(codes, c.(string))
	}
	return secret, codes
}

// ---------------------------------------------------------------- legacy unit tests

func TestHashToken(t *testing.T) {
	token := "jp_pat_test1234567890abcdef"
	hash1 := hashToken(token)
	hash2 := hashToken(token)
	if hash1 == "" || hash1 != hash2 || hash1 == token {
		t.Fatal("hash must be deterministic and differ from the token")
	}
}

func TestRandomToken(t *testing.T) {
	tok1, _ := randomToken(16)
	tok2, _ := randomToken(16)
	if len(tok1) != 32 || tok1 == tok2 {
		t.Fatal("random tokens must be 32 hex chars and unique")
	}
}

func TestPublicUserZeroPasswordLeak(t *testing.T) {
	pub := publicUser(&store.User{ID: "u-123", Email: "alice@example.com", Name: "Alice",
		PasswordHash: "$2a$10$secret", CreatedAt: time.Now(), IsAdmin: true})
	if _, ok := pub["password_hash"]; ok {
		t.Fatal("password_hash leaked")
	}
	if pub["is_admin"] != true || pub["email"] != "alice@example.com" {
		t.Fatalf("unexpected public user: %v", pub)
	}
}

func TestClientIPNotSpoofable(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	if ip := clientIP(req); ip != "192.168.1.50" {
		t.Fatalf("got %s", ip)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.195")
	req.Header.Set("X-JP-Client-IP", "9.9.9.9")
	if ip := clientIP(req); ip != "192.168.1.50" {
		t.Fatalf("client-supplied headers must be ignored without the internal token, got %s", ip)
	}
	req.Header.Set(internalauth.Header, "internal-token-for-tests")
	if ip := clientIP(req); ip != "9.9.9.9" {
		t.Fatalf("trusted gateway header must be honoured for internal callers, got %s", ip)
	}
}

// ---------------------------------------------------------------- security regression tests

func TestLogoutRevokesAccessTokenAndSession(t *testing.T) {
	e := newEnv(t, "invite")
	access, refresh := e.bootstrap("admin@example.com", "correct-horse-1")
	if rec := e.do("GET", "/auth/me", nil, access); rec.Code != 200 {
		t.Fatalf("me before logout: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/logout", nil, access); rec.Code != 204 {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/auth/me", nil, access); rec.Code != 401 {
		t.Fatalf("access token must be rejected after logout, got %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); rec.Code != 401 {
		t.Fatalf("refresh token must be dead after logout, got %d", rec.Code)
	}
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	e := newEnv(t, "invite")
	a1, r1 := e.bootstrap("admin@example.com", "correct-horse-1")
	rec := e.login("admin@example.com", "correct-horse-1")
	m := decode(t, rec)
	a2, r2 := m["access_token"].(string), m["refresh_token"].(string)
	if rec := e.do("POST", "/auth/logout-all", nil, a1); rec.Code != 204 {
		t.Fatalf("logout-all: %d %s", rec.Code, rec.Body)
	}
	for _, tok := range []string{a1, a2} {
		if rec := e.do("GET", "/auth/me", nil, tok); rec.Code != 401 {
			t.Fatalf("every access token must be revoked, got %d", rec.Code)
		}
	}
	for _, rt := range []string{r1, r2} {
		if rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": rt}, ""); rec.Code != 401 {
			t.Fatalf("every refresh token must be revoked, got %d", rec.Code)
		}
	}
	time.Sleep(3 * time.Millisecond)
	if rec := e.login("admin@example.com", "correct-horse-1"); rec.Code != 200 {
		t.Fatalf("login after logout-all must work: %d", rec.Code)
	}
}

func TestRefreshRotationSingleUse(t *testing.T) {
	e := newEnv(t, "invite")
	_, refresh := e.bootstrap("admin@example.com", "correct-horse-1")
	rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	if rec.Code != 200 {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	if rec2 := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); rec2.Code != 401 {
		t.Fatalf("replayed refresh token must be rejected, got %d", rec2.Code)
	}
	newRefresh := decode(t, rec)["refresh_token"].(string)
	if rec3 := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": newRefresh}, ""); rec3.Code != 200 {
		t.Fatalf("rotated token must work: %d", rec3.Code)
	}
}

func TestBootstrapAdminClaimAndOnce(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	c, err := e.jm.Parse(access)
	if err != nil || !c.Admin || c.SessionID == "" {
		t.Fatalf("bootstrap token must carry adm and sid: %+v %v", c, err)
	}
	rec := e.do("POST", "/auth/bootstrap", map[string]string{"email": "evil@example.com", "name": "x", "password": "correct-horse-1"}, "")
	if rec.Code != 403 {
		t.Fatalf("second bootstrap must be refused, got %d", rec.Code)
	}
	st := decode(t, e.do("GET", "/auth/bootstrap/status", nil, ""))
	if st["initialized"] != true || st["registration"] != "invite" {
		t.Fatalf("status: %v", st)
	}
}

func TestBootstrapConcurrentOnlyOneWins(t *testing.T) {
	e := newEnv(t, "invite")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := e.do("POST", "/auth/bootstrap", map[string]string{"email": fmt.Sprintf("a%d@example.com", i), "name": "x", "password": "correct-horse-1"}, "")
			if rec.Code == 201 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("exactly one bootstrap must succeed, got %d", ok)
	}
}

func TestRegistrationModes(t *testing.T) {
	org := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Token, Email string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path == "/internal/invites/check" && b.Token == "good-token" && b.Email == "invitee@example.com" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer org.Close()
	reg := func(e *env, email, invite string) int {
		return e.do("POST", "/auth/register", map[string]string{"email": email, "name": "N", "password": "correct-horse-1", "invite_token": invite}, "").Code
	}

	// not initialised: nothing but bootstrap
	pre := newEnv(t, "open")
	if c := reg(pre, "x@example.com", ""); c != 403 {
		t.Fatalf("register before bootstrap must be refused, got %d", c)
	}

	closed := newEnv(t, "closed")
	closed.bootstrap("admin@example.com", "correct-horse-1")
	if c := reg(closed, "x@example.com", "good-token"); c != 403 {
		t.Fatalf("closed mode must refuse, got %d", c)
	}

	inv := newEnv(t, "invite")
	inv.h.OrgURL = org.URL
	inv.bootstrap("admin@example.com", "correct-horse-1")
	if c := reg(inv, "invitee@example.com", ""); c != 403 {
		t.Fatalf("invite mode without token must refuse, got %d", c)
	}
	if c := reg(inv, "someone-else@example.com", "good-token"); c != 403 {
		t.Fatalf("invite bound to another email must refuse, got %d", c)
	}
	if c := reg(inv, "invitee@example.com", "bad-token"); c != 403 {
		t.Fatalf("bad token must refuse, got %d", c)
	}
	if c := reg(inv, "invitee@example.com", "good-token"); c != 201 {
		t.Fatalf("valid invite must register, got %d", c)
	}

	open := newEnv(t, "open")
	open.bootstrap("admin@example.com", "correct-horse-1")
	if c := reg(open, "free@example.com", ""); c != 201 {
		t.Fatalf("open mode must register, got %d", c)
	}
	rec := open.do("POST", "/auth/register", map[string]string{"email": "free@example.com", "name": "N", "password": "correct-horse-1"}, "")
	if rec.Code != 409 || strings.Contains(strings.ToLower(rec.Body.String()), "already registered") {
		t.Fatalf("duplicate must be a generic conflict: %d %s", rec.Code, rec.Body)
	}
}

func TestLoginGenericErrorsAndRateLimit(t *testing.T) {
	e := newEnv(t, "invite")
	e.bootstrap("admin@example.com", "correct-horse-1")
	unknown := e.login("nobody@example.com", "whatever-pass")
	wrong := e.login("admin@example.com", "wrong-password")
	if unknown.Code != 401 || wrong.Code != 401 || unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("unknown user and wrong password must be indistinguishable: %d %q vs %d %q",
			unknown.Code, unknown.Body, wrong.Code, wrong.Body)
	}
	for i := 0; i < 10; i++ {
		e.login("admin@example.com", "wrong-password")
	}
	rec := e.login("admin@example.com", "correct-horse-1")
	if rec.Code != 429 {
		t.Fatalf("after repeated failures even the right password must be throttled, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 should carry Retry-After")
	}
}

func TestMFASetupCannotBeOverwritten(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	secret, codes := e.enableMFA(access)

	u := e.repo.byEmail("admin@example.com")
	if u.TOTPSecret == nil || !secretbox.IsSealed(*u.TOTPSecret) || strings.Contains(*u.TOTPSecret, secret) {
		t.Fatalf("TOTP secret must be stored encrypted, got %v", u.TOTPSecret)
	}
	for h := range e.repo.backup[u.ID] {
		for _, c := range codes {
			if h == c || strings.Contains(h, c) {
				t.Fatal("backup codes must be stored hashed")
			}
		}
	}

	if rec := e.do("POST", "/auth/mfa/setup", nil, access); rec.Code != 409 {
		t.Fatalf("setup with MFA enabled and no code must be refused, got %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/mfa/setup", map[string]string{"code": "000000"}, access); rec.Code != 403 {
		t.Fatalf("setup with wrong code must be refused, got %d", rec.Code)
	}
	if u2 := e.repo.byEmail("admin@example.com"); u2.TOTPPendingSecret != nil || *u2.TOTPSecret != *u.TOTPSecret {
		t.Fatal("refused setup must not touch stored secrets")
	}
	// A valid current code re-enrols but the live secret only changes after /enable.
	cur, _ := totp.GenerateCode(secret, time.Now().UTC().Add(-30*time.Second))
	rec := e.do("POST", "/auth/mfa/setup", map[string]string{"code": cur}, access)
	if rec.Code != 200 {
		t.Fatalf("setup with valid code: %d %s", rec.Code, rec.Body)
	}
	if u3 := e.repo.byEmail("admin@example.com"); *u3.TOTPSecret != *u.TOTPSecret || u3.TOTPPendingSecret == nil {
		t.Fatal("re-enrolment must stay pending until confirmed")
	}
}

func TestTwoStepMFALogin(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	secret, _ := e.enableMFA(access)

	rec := e.login("admin@example.com", "correct-horse-1")
	m := decode(t, rec)
	if rec.Code != 200 || m["mfa_required"] != true || m["access_token"] != nil {
		t.Fatalf("password step must only return mfa_token: %d %v", rec.Code, m)
	}
	tok := m["mfa_token"].(string)

	// second step needs no password
	code, _ := totp.GenerateCode(secret, time.Now().UTC().Add(30*time.Second))
	if rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": "123456"}, ""); rec.Code != 401 {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	ok := e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": code}, "")
	if ok.Code != 200 || decode(t, ok)["access_token"] == nil {
		t.Fatalf("second step: %d %s", ok.Code, ok.Body)
	}
	// single use
	code2, _ := totp.GenerateCode(secret, time.Now().UTC().Add(-30*time.Second))
	if rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": code2}, ""); rec.Code != 401 {
		t.Fatalf("mfa_token must be single use, got %d", rec.Code)
	}
	// replayed TOTP code must not work with a fresh token
	m2 := decode(t, e.login("admin@example.com", "correct-horse-1"))
	if rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": m2["mfa_token"].(string), "totp_code": code}, ""); rec.Code != 401 {
		t.Fatalf("a TOTP code must not be accepted twice, got %d", rec.Code)
	}
}

func TestMFATokenAttemptLimited(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	secret, _ := e.enableMFA(access)
	tok := decode(t, e.login("admin@example.com", "correct-horse-1"))["mfa_token"].(string)
	for i := 0; i < 5; i++ {
		e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": "111111"}, "")
	}
	good, _ := totp.GenerateCode(secret, time.Now().UTC().Add(30*time.Second))
	if rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": good}, ""); rec.Code != 401 {
		t.Fatalf("token must be burned after repeated wrong codes, got %d", rec.Code)
	}
}

func TestMFATokenBoundToUser(t *testing.T) {
	e := newEnv(t, "open")
	a1, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	_, _ = e.enableMFA(a1)
	rec := e.do("POST", "/auth/register", map[string]string{"email": "bob@example.com", "name": "Bob", "password": "bobs-password-1"}, "")
	bobAccess := decode(t, rec)["access_token"].(string)
	bobSecret, _ := e.enableMFA(bobAccess)

	adminTok := decode(t, e.login("admin@example.com", "correct-horse-1"))["mfa_token"].(string)
	bobCode, _ := totp.GenerateCode(bobSecret, time.Now().UTC())
	if rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": adminTok, "totp_code": bobCode}, ""); rec.Code != 401 {
		t.Fatalf("another user's valid code must not complete this token, got %d", rec.Code)
	}
}

func TestBackupCodeSingleUseUnderConcurrency(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	_, codes := e.enableMFA(access)

	const n = 8
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = decode(t, e.login("admin@example.com", "correct-horse-1"))["mfa_token"].(string)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			rec := e.do("POST", "/auth/login", map[string]string{"mfa_token": tok, "totp_code": codes[0]}, "")
			if rec.Code == 200 {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(tokens[i])
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("a backup code must work exactly once under concurrency, got %d successes", success)
	}
}

func TestDisableMFARequiresCode(t *testing.T) {
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	secret, _ := e.enableMFA(access)
	if rec := e.do("POST", "/auth/mfa/disable", map[string]string{"code": "000000"}, access); rec.Code != 400 {
		t.Fatalf("wrong code must not disable MFA: %d", rec.Code)
	}
	code, _ := totp.GenerateCode(secret, time.Now().UTC().Add(30*time.Second))
	if rec := e.do("POST", "/auth/mfa/disable", map[string]string{"code": code}, access); rec.Code != 200 {
		t.Fatalf("valid code disables: %d %s", rec.Code, rec.Body)
	}
	if e.repo.byEmail("admin@example.com").TOTPEnabled {
		t.Fatal("MFA should be off")
	}
}

func TestPATScopesExpiryAndVerify(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")

	if rec := e.do("POST", "/auth/pats", map[string]any{"name": "x", "scopes": []string{"superuser"}}, access); rec.Code != 400 {
		t.Fatalf("unknown scope must be rejected: %d", rec.Code)
	}
	rec := e.do("POST", "/auth/pats", map[string]any{"name": "ci", "scopes": []string{"read"}, "expires_in_days": 30}, access)
	if rec.Code != 201 {
		t.Fatalf("create pat: %d %s", rec.Code, rec.Body)
	}
	body := decode(t, rec)
	tok := body["token"].(string)
	if body["expires_at"] == nil {
		t.Fatal("expiry should be set")
	}
	verify := func(token string) *httptest.ResponseRecorder {
		return e.do("POST", "/internal/pats/verify", map[string]string{"token": token}, "", internalauth.Header, "internal-token-for-tests")
	}
	v := verify(tok)
	vm := decode(t, v)
	if v.Code != 200 || vm["scope"] != "read" || vm["pat_id"] == nil {
		t.Fatalf("verify: %d %v", v.Code, vm)
	}
	if verify("jp_pat_doesnotexist").Code != 401 {
		t.Fatal("unknown PAT must be 401")
	}
	// expire it
	for _, p := range e.repo.pats {
		past := time.Now().Add(-time.Hour)
		p.ExpiresAt = &past
	}
	if verify(tok).Code != 401 {
		t.Fatal("expired PAT must be 401")
	}
	// unauthenticated internal call is refused
	if rec := e.do("POST", "/internal/pats/verify", map[string]string{"token": tok}, ""); rec.Code != 403 {
		t.Fatalf("internal endpoint must require the internal token, got %d", rec.Code)
	}
	// default scope and highest-scope-wins
	if EffectiveScope(nil) != "write" || EffectiveScope([]string{"read", "write"}) != "write" || EffectiveScope([]string{"read", "admin"}) != "admin" {
		t.Fatal("EffectiveScope")
	}
}

func TestPATTokenCannotManageAccountSecurity(t *testing.T) {
	e := newEnv(t, "invite")
	e.bootstrap("admin@example.com", "correct-horse-1")
	u := e.repo.byEmail("admin@example.com")
	patJWT, _, _ := e.jm.IssueAccessWith(u.ID, u.Email, u.Name, jwtutil.IssueOptions{Scope: jwtutil.ScopeAdmin})
	for _, tc := range []struct{ method, path string }{
		{"POST", "/auth/pats"}, {"GET", "/auth/pats"}, {"POST", "/auth/mfa/setup"},
		{"POST", "/auth/password"}, {"GET", "/auth/sessions"},
	} {
		if rec := e.do(tc.method, tc.path, map[string]string{"name": "x"}, patJWT); rec.Code != 403 {
			t.Errorf("%s %s with PAT token should be 403, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestPasswordChangeRevokesSessions(t *testing.T) {
	e := newEnv(t, "invite")
	access, refresh := e.bootstrap("admin@example.com", "correct-horse-1")
	if rec := e.do("POST", "/auth/password", map[string]string{"current_password": "nope-nope-1", "new_password": "brand-new-pass-2"}, access); rec.Code != 401 {
		t.Fatalf("wrong current password: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/password", map[string]string{"current_password": "correct-horse-1", "new_password": "short"}, access); rec.Code != 400 {
		t.Fatalf("weak new password: %d", rec.Code)
	}
	rec := e.do("POST", "/auth/password", map[string]string{"current_password": "correct-horse-1", "new_password": "brand-new-pass-2"}, access)
	if rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/auth/me", nil, access); rec.Code != 401 {
		t.Fatalf("old access token must die after password change: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); rec.Code != 401 {
		t.Fatalf("old refresh token must die after password change: %d", rec.Code)
	}
	time.Sleep(3 * time.Millisecond)
	if rec := e.login("admin@example.com", "correct-horse-1"); rec.Code != 401 {
		t.Fatalf("old password must stop working: %d", rec.Code)
	}
	if rec := e.login("admin@example.com", "brand-new-pass-2"); rec.Code != 200 {
		t.Fatalf("new password works: %d", rec.Code)
	}
}

func TestInternalRoutesRequireInternalToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	e := newEnv(t, "invite")
	access, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	u := e.repo.byEmail("admin@example.com")
	for _, tc := range []struct{ method, path string }{
		{"GET", "/internal/users/" + u.ID}, {"POST", "/internal/users/lookup"}, {"POST", "/internal/pats/verify"},
		{"GET", "/internal/settings"}, {"GET", "/internal/security/stats"},
	} {
		if rec := e.do(tc.method, tc.path, map[string]any{}, access); rec.Code != 403 {
			t.Errorf("%s %s without internal token = %d, want 403 (user JWT must not suffice)", tc.method, tc.path, rec.Code)
		}
	}
	rec := e.do("GET", "/internal/users/"+u.ID, nil, "", internalauth.Header, "internal-token-for-tests")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("internal user lookup: %d %s", rec.Code, rec.Body)
	}
}

func TestSystemSettingsAdminOnlyAndEncrypted(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	e := newEnv(t, "open")
	admin, _ := e.bootstrap("admin@example.com", "correct-horse-1")
	rec := e.do("POST", "/auth/register", map[string]string{"email": "bob@example.com", "name": "Bob", "password": "bobs-password-1"}, "")
	bob := decode(t, rec)["access_token"].(string)

	if rec := e.do("GET", "/system/settings", nil, bob); rec.Code != 403 {
		t.Fatalf("non-admin read: %d", rec.Code)
	}
	if rec := e.do("PATCH", "/system/settings", map[string]string{"smtp_host": "evil"}, bob); rec.Code != 403 {
		t.Fatalf("non-admin write: %d", rec.Code)
	}
	if rec := e.do("PATCH", "/system/settings", map[string]string{"jwt_secret": "x"}, admin); rec.Code != 400 {
		t.Fatalf("unknown setting must be refused: %d", rec.Code)
	}
	if rec := e.do("PATCH", "/system/settings", map[string]string{"smtp_port": "99999"}, admin); rec.Code != 400 {
		t.Fatalf("invalid port must be refused: %d", rec.Code)
	}
	rec = e.do("PATCH", "/system/settings", map[string]string{"smtp_host": "smtp.example.com", "smtp_port": "587", "smtp_password": "s3cr3t-pass", "smtp_from": "noreply@example.com"}, admin)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t-pass") {
		t.Fatal("secret must never be returned by the public API")
	}
	if raw := e.repo.settings["smtp_password"]; !secretbox.IsSealed(raw.Value) || strings.Contains(raw.Value, "s3cr3t-pass") {
		t.Fatalf("secret must be sealed at rest: %q", raw.Value)
	}
	in := e.do("GET", "/internal/settings", nil, "", internalauth.Header, "internal-token-for-tests")
	settings := decode(t, in)["settings"].(map[string]any)
	if settings["smtp_password"] != "s3cr3t-pass" || settings["smtp_host"] != "smtp.example.com" {
		t.Fatalf("internal settings must be decrypted for consumers: %v", settings)
	}
	// removing a key
	if rec := e.do("PATCH", "/system/settings", map[string]string{"smtp_password": ""}, admin); rec.Code != 200 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if _, still := e.repo.settings["smtp_password"]; still {
		t.Fatal("empty value should delete the key")
	}
}

func TestAdminClaimAloneIsNotEnough(t *testing.T) {
	e := newEnv(t, "open")
	e.bootstrap("admin@example.com", "correct-horse-1")
	rec := e.do("POST", "/auth/register", map[string]string{"email": "bob@example.com", "name": "Bob", "password": "bobs-password-1"}, "")
	bobID := decode(t, rec)["user"].(map[string]any)["id"].(string)
	forged, _, _ := e.jm.IssueAccessWith(bobID, "bob@example.com", "Bob", jwtutil.IssueOptions{Admin: true})
	if rec := e.do("GET", "/system/settings", nil, forged); rec.Code != 403 {
		t.Fatalf("a token claiming adm for a non-admin DB user must be refused, got %d", rec.Code)
	}
}

func TestPasswordLengthLimits(t *testing.T) {
	e := newEnv(t, "invite")
	long := strings.Repeat("a", 73)
	rec := e.do("POST", "/auth/bootstrap", map[string]string{"email": "admin@example.com", "name": "A", "password": long}, "")
	if rec.Code != 400 {
		t.Fatalf("passwords above bcrypt's 72 byte limit must be refused, got %d", rec.Code)
	}
}
