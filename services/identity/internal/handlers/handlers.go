package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/clientip"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/ratelimit"
	"github.com/jp-cloud/go-common/revocation"
	"github.com/jp-cloud/identity/internal/store"
)

// Auditor persists audit events (nil-safe).
type Auditor interface {
	Write(ctx context.Context, e audit.Event) error
}

// Handler serves the identity API.
type Handler struct {
	Store   store.Repo
	JWT     *jwtutil.Manager
	Redis   redis.UniversalClient
	Audit   Auditor
	Log     *slog.Logger
	HTTP    *http.Client // internal client (signs with INTERNAL_TOKEN)
	OrgURL  string
	Revoker *revocation.Store
	Limiter *ratelimit.Limiter

	// RegistrationMode is closed | invite | open.
	RegistrationMode string
	// BootstrapToken, when set, must accompany POST /auth/bootstrap.
	BootstrapToken string
	// EncKey seals TOTP seeds and secret settings; BackupKey keys backup-code hashes.
	EncKey    []byte
	BackupKey []byte
	// Env is the JP_ENV value ("production" tightens a few things).
	Env string
}

const (
	maxLoginFailEmail = 8
	maxLoginFailIP    = 40
	failWindow        = 15 * time.Minute
	mfaTokenTTL       = 5 * time.Minute
	mfaMaxAttempts    = 5
	maxPasswordBytes  = 72 // bcrypt limit
)

// dummyHash is compared against when a login names an unknown user so the response time does
// not reveal whether the account exists.
var dummyHash = func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("jp-dummy-password-for-timing"), bcrypt.DefaultCost)
	return h
}()

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) limiter() *ratelimit.Limiter {
	if h.Limiter != nil {
		return h.Limiter
	}
	return &ratelimit.Limiter{Redis: h.Redis, Prefix: "jp:rl:id:"}
}

func (h *Handler) revoker() *revocation.Store {
	if h.Revoker != nil {
		return h.Revoker
	}
	return &revocation.Store{Redis: h.Redis}
}

func (h *Handler) audit(r *http.Request, e audit.Event) {
	if h.Audit == nil {
		return
	}
	if e.IP == "" {
		e.IP = clientIP(r)
	}
	if e.UserAgent == "" {
		e.UserAgent = r.UserAgent()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := h.Audit.Write(ctx, e); err != nil {
		h.log().Warn("audit write failed", "action", e.Action, "err", err)
	}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)

	mux.HandleFunc("GET /auth/bootstrap/status", h.BootstrapStatus)
	mux.HandleFunc("POST /auth/bootstrap", h.Bootstrap)

	mux.HandleFunc("POST /auth/register", h.Register)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("POST /auth/refresh", h.Refresh)

	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("POST /auth/logout", auth(http.HandlerFunc(h.Logout)))
	mux.Handle("POST /auth/logout-all", auth(http.HandlerFunc(h.LogoutAll)))
	mux.Handle("GET /auth/me", auth(http.HandlerFunc(h.Me)))
	mux.Handle("POST /auth/password", auth(http.HandlerFunc(h.ChangePassword)))
	mux.Handle("GET /auth/sessions", auth(http.HandlerFunc(h.ListSessions)))
	mux.Handle("DELETE /auth/sessions/{id}", auth(http.HandlerFunc(h.RevokeSession)))
	mux.Handle("POST /auth/pats", auth(http.HandlerFunc(h.CreatePAT)))
	mux.Handle("GET /auth/pats", auth(http.HandlerFunc(h.ListPATs)))
	mux.Handle("DELETE /auth/pats/{id}", auth(http.HandlerFunc(h.RevokePAT)))

	// Multi-Factor Authentication (MFA / TOTP)
	mux.Handle("POST /auth/mfa/setup", auth(http.HandlerFunc(h.SetupMFA)))
	mux.Handle("POST /auth/mfa/enable", auth(http.HandlerFunc(h.EnableMFA)))
	mux.Handle("POST /auth/mfa/disable", auth(http.HandlerFunc(h.DisableMFA)))

	// Platform settings (platform admin only; the gateway also enforces `adm`).
	mux.Handle("GET /system/settings", auth(http.HandlerFunc(h.GetSettings)))
	mux.Handle("PATCH /system/settings", auth(http.HandlerFunc(h.PatchSettings)))

	// Internal (INTERNAL_TOKEN required).
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/users", in(h.CreateManagedInternal))
	mux.Handle("GET /internal/managed-users/{id}", in(h.GetManagedInternal))
	mux.Handle("PATCH /internal/managed-users/{id}", in(h.UpdateManagedInternal))
	mux.Handle("POST /internal/managed-users/{id}/reset-password", in(h.ResetManagedPasswordInternal))
	mux.Handle("DELETE /internal/managed-users/{id}", in(h.DeleteManagedInternal))
	mux.Handle("GET /internal/managed-users/{id}/sessions", in(h.ManagedSessionsInternal))
	mux.Handle("DELETE /internal/managed-users/{id}/sessions/{sessionId}", in(h.RevokeManagedSessionInternal))
	mux.Handle("GET /internal/users/{id}", in(h.GetUserInternal))
	mux.Handle("POST /internal/users/lookup", in(h.LookupUsersInternal))
	mux.Handle("POST /internal/pats/verify", in(h.VerifyPATInternal))
	mux.Handle("GET /internal/settings", in(h.InternalSettings))
	mux.Handle("GET /internal/security/stats", in(h.InternalSecurityStats))
}

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	Name        string `json:"name"`
	InviteToken string `json:"invite_token,omitempty"`
	// BootstrapToken is only read by POST /auth/bootstrap.
	BootstrapToken string `json:"bootstrap_token,omitempty"`
}

type loginReq struct {
	// Login is a username or an e-mail; Email is the legacy spelling of the same field.
	Login    string `json:"login"`
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
	MFAToken string `json:"mfa_token,omitempty"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// ---------------------------------------------------------------- rate limiting

func (h *Handler) failCount(ctx context.Context, key string) int64 {
	n, _ := h.Redis.Get(ctx, h.limiter().Prefix+key).Int64()
	return n
}

// recordFail atomically counts a failure (INCR + PEXPIRE in one Lua script).
func (h *Handler) recordFail(ctx context.Context, key string) {
	if _, err := h.limiter().Allow(ctx, key, 1<<30, failWindow); err != nil {
		h.log().Warn("rate limit counter failed", "err", err)
	}
}

func (h *Handler) clearFail(ctx context.Context, key string) {
	_ = h.Redis.Del(ctx, h.limiter().Prefix+key).Err()
}

func emailKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return "login:email:" + hex.EncodeToString(sum[:8])
}

func (h *Handler) loginLimited(ctx context.Context, ip, email string) bool {
	if h.failCount(ctx, "login:ip:"+ip) >= maxLoginFailIP {
		return true
	}
	if email != "" && h.failCount(ctx, emailKey(email)) >= maxLoginFailEmail {
		return true
	}
	return false
}

// hitLimit counts one request against a per-IP bucket and reports whether it is over the limit.
func (h *Handler) hitLimit(ctx context.Context, key string, limit int64, window time.Duration) bool {
	res, err := h.limiter().Allow(ctx, key, limit, window)
	if err != nil {
		h.log().Warn("rate limiter unavailable", "err", err)
		return false
	}
	return !res.Allowed
}

// ---------------------------------------------------------------- registration / bootstrap

func validEmail(e string) bool {
	if e == "" || len(e) > 254 {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e
}

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	var b strings.Builder
	for _, r := range n {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len([]rune(out)) > 100 {
		out = string([]rune(out)[:100])
	}
	return out
}

func validatePassword(p string) string {
	if len(p) < 8 {
		return "password must be at least 8 characters"
	}
	if len(p) > maxPasswordBytes {
		return "password must be at most 72 bytes"
	}
	return ""
}

func (h *Handler) mode() string {
	switch h.RegistrationMode {
	case "closed", "open", "invite":
		return h.RegistrationMode
	}
	return "invite"
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if h.hitLimit(r.Context(), "register:ip:"+ip, 20, time.Hour) {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many registration attempts; try again later")
		return
	}
	var req registerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = cleanName(req.Name)
	if !validEmail(req.Email) || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "valid email, name and password (min 8) required")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	init, err := h.Store.IsBootstrapInitialized(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "registration unavailable")
		return
	}
	if !init {
		httpx.Error(w, http.StatusForbidden, "not_initialized", "platform is not initialised; create the first administrator via bootstrap")
		return
	}
	switch h.mode() {
	case "closed":
		httpx.Error(w, http.StatusForbidden, "registration_closed", "registration is closed")
		return
	case "invite":
		if strings.TrimSpace(req.InviteToken) == "" {
			httpx.Error(w, http.StatusForbidden, "invite_required", "registration requires an invitation")
			return
		}
		ok, err := h.inviteValid(r.Context(), req.InviteToken, req.Email)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "invitation check unavailable")
			return
		}
		if !ok {
			httpx.Error(w, http.StatusForbidden, "invalid_invite", "invitation is invalid, expired, or issued for a different email")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	user, err := h.Store.CreateUser(r.Context(), req.Email, req.Name, string(hash))
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "unable to register with these details")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create user")
		return
	}
	tokens, err := h.issueTokens(r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "user.registered", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"user":          publicUser(user),
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

// inviteValid asks the organization service whether token is a live invite for email.
func (h *Handler) inviteValid(ctx context.Context, token, email string) (bool, error) {
	if h.OrgURL == "" || h.HTTP == nil {
		return false, errors.New("organization service not configured")
	}
	body := strings.NewReader(`{"token":` + jsonString(token) + `,"email":` + jsonString(email) + `}`)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.OrgURL, "/")+"/internal/invites/check", body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusForbidden, http.StatusBadRequest:
		return false, nil
	}
	return false, errors.New("organization service error")
}

func (h *Handler) BootstrapStatus(w http.ResponseWriter, r *http.Request) {
	init, err := h.Store.IsBootstrapInitialized(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to check bootstrap status")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"initialized": init, "registration": h.mode()})
}

func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if h.hitLimit(r.Context(), "bootstrap:ip:"+ip, 10, time.Hour) {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many attempts")
		return
	}
	var req registerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if h.BootstrapToken != "" && !constEq(req.BootstrapToken, h.BootstrapToken) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "bootstrap token required")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = cleanName(req.Name)
	if !validEmail(req.Email) || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "valid email, name and password (min 8) required")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	user, err := h.Store.BootstrapAdmin(r.Context(), req.Email, req.Name, string(hash))
	if errors.Is(err, store.ErrAlreadyInitialized) {
		httpx.Error(w, http.StatusForbidden, "already_initialized", "platform bootstrap is already complete")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create administrator")
		return
	}
	tokens, err := h.issueTokens(r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "platform.bootstrap", Resource: "system", ResourceID: user.ID})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"user":          publicUser(user),
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

// ---------------------------------------------------------------- login

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	ip := clientIP(r)
	ctx := r.Context()

	// Step 2 of the MFA flow: mfa_token + totp_code (the password is NOT resent).
	if req.MFAToken != "" {
		h.loginMFAStep(w, r, req, ip)
		return
	}

	if req.Login != "" {
		req.Email = req.Login
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if h.loginLimited(ctx, ip, req.Email) {
		w.Header().Set("Retry-After", "900")
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed login attempts; please try again later")
		return
	}

	user, err := h.Store.GetUserByLogin(ctx, req.Email)
	if errors.Is(err, store.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password)) // equalise timing
		h.auditLoginFailure(r, nil, ip)
		h.failLogin(ctx, ip, req.Email)
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	if len(req.Password) > maxPasswordBytes || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil || user.DisabledAt != nil {
		// A disabled account fails exactly like a wrong password.
		h.auditLoginFailure(r, user, ip)
		h.failLogin(ctx, ip, req.Email)
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	if user.TOTPEnabled {
		mfaTok, err := randomToken(32)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "login failed")
			return
		}
		if err := h.Redis.Set(ctx, mfaKey(mfaTok), user.ID, mfaTokenTTL).Err(); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "login temporarily unavailable")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"mfa_required": true,
			"mfa_token":    mfaTok,
			"expires_in":   int(mfaTokenTTL.Seconds()),
		})
		return
	}
	h.clearFail(ctx, emailKey(req.Email))
	h.finishLogin(w, r, user)
}

func (h *Handler) failLogin(ctx context.Context, ip, email string) {
	h.recordFail(ctx, "login:ip:"+ip)
	if email != "" {
		h.recordFail(ctx, emailKey(email))
	}
}

func (h *Handler) loginMFAStep(w http.ResponseWriter, r *http.Request, req loginReq, ip string) {
	ctx := r.Context()
	if strings.TrimSpace(req.TOTPCode) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "totp_code required")
		return
	}
	if h.loginLimited(ctx, ip, "") {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed attempts; please try again later")
		return
	}
	key := mfaKey(req.MFAToken)
	uid, err := h.Redis.Get(ctx, key).Result()
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired mfa_token; sign in again")
		return
	}
	// Attempt-limited: the token is burned after mfaMaxAttempts wrong codes.
	res, rerr := h.limiter().Allow(ctx, "mfa:"+key, mfaMaxAttempts, mfaTokenTTL)
	if rerr == nil && !res.Allowed {
		_ = h.Redis.Del(ctx, key).Err()
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "too many attempts; sign in again")
		return
	}
	user, err := h.Store.GetUserByID(ctx, uid)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired mfa_token; sign in again")
		return
	}
	if user.DisabledAt != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired mfa_token; sign in again")
		return
	}
	if !h.verifySecondFactor(ctx, user, req.TOTPCode) {
		h.auditLoginFailure(r, user, ip)
		h.recordFail(ctx, "login:ip:"+ip)
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid two-factor authentication code")
		return
	}
	// Single use: only the request that deletes the key may continue.
	if _, err := h.Redis.GetDel(ctx, key).Result(); err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired mfa_token; sign in again")
		return
	}
	h.clearFail(ctx, emailKey(user.Email))
	h.finishLogin(w, r, user)
}

func (h *Handler) auditLoginFailure(r *http.Request, user *store.User, ip string) {
	ev := audit.Event{Action: "user.login_failed", Resource: "user", IP: ip, Metadata: map[string]any{}}
	if user != nil {
		ev.ActorID, ev.ResourceID = user.ID, user.ID
		ev.Metadata["disabled"] = user.DisabledAt != nil
		if user.ManagedOrgID != nil {
			ev.OrgID = *user.ManagedOrgID
		}
	}
	h.audit(r, ev)
}

func (h *Handler) finishLogin(w http.ResponseWriter, r *http.Request, user *store.User) {
	_ = h.Store.TouchLogin(r.Context(), user.ID)
	if user.MustChangePassword {
		// Limited token: only change-password, /auth/me and logout accept it. No refresh token, no session.
		access, _, err := h.JWT.IssueAccessWith(user.ID, user.Email, user.Name, jwtutil.IssueOptions{
			Scope: jwtutil.ScopePWChange, Managed: user.ManagedOrgID != nil, TTL: 15 * time.Minute})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
			return
		}
		h.audit(r, audit.Event{ActorID: user.ID, Action: "user.logged_in_must_change_password", Resource: "user", ResourceID: user.ID})
		httpx.JSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "access_token": access, "must_change_password": true})
		return
	}
	tokens, err := h.issueTokens(r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "user.logged_in", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":          publicUser(user),
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

// ---------------------------------------------------------------- refresh / logout

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := httpx.Decode(r, &req); err != nil || req.RefreshToken == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "refresh_token required")
		return
	}
	ctx := r.Context()
	claims, err := h.JWT.Parse(req.RefreshToken)
	if err != nil || claims.Type != "refresh" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid refresh token")
		return
	}
	// Atomic claim of the refresh key: a replayed refresh token finds nothing.
	if _, err := h.Redis.GetDel(ctx, refreshKey(claims.ID)).Result(); err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "refresh token revoked or expired")
		return
	}
	newJTI := uuid.NewString()
	// Look up the session first to learn the user, then rotate atomically.
	oldSess, err := h.Store.GetSessionByRefreshJTI(ctx, claims.ID)
	if err != nil || oldSess.RevokedAt != nil || time.Now().After(oldSess.ExpiresAt) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "session invalid")
		return
	}
	user, err := h.Store.GetUserByID(ctx, oldSess.UserID)
	if err != nil || user.DisabledAt != nil || user.MustChangePassword {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	refresh, exp, err := h.JWT.IssueRefreshWith(user.ID, user.Email, user.Name, jwtutil.IssueOptions{JTI: newJTI, SessionID: oldSess.ID})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	sess, err := h.Store.RotateSession(ctx, claims.ID, newJTI, exp)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "session invalid")
		return
	}
	ttl := time.Until(exp)
	if err := h.Redis.Set(ctx, refreshKey(newJTI), user.ID, ttl).Err(); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "refresh temporarily unavailable")
		return
	}
	access, _, err := h.JWT.IssueAccessWith(user.ID, user.Email, user.Name, jwtutil.IssueOptions{Admin: user.IsAdmin, Managed: user.ManagedOrgID != nil, SessionID: sess.ID})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
	})
}

// Logout revokes the presented access token AND its session (and refresh key). A refresh_token
// may be supplied in the body to additionally revoke a specific session; {"all":true} signs out
// every session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
		All          bool   `json:"all"`
	}
	if r.ContentLength != 0 {
		_ = httpx.Decode(r, &body) // body is optional; ignore malformed
	}
	ctx := r.Context()
	if body.All {
		h.logoutAll(w, r, claims)
		return
	}
	if ttl := time.Until(claims.ExpiresAt.Time); ttl > 0 {
		if err := h.revoker().DenyToken(ctx, claims.ID, ttl); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "logout could not be completed; retry")
			return
		}
	}
	if claims.SessionID != "" {
		h.revokeSessionByID(ctx, claims.UserID, claims.SessionID)
	}
	if body.RefreshToken != "" {
		if rc, err := h.JWT.Parse(body.RefreshToken); err == nil && rc.Type == "refresh" && rc.UserID == claims.UserID {
			if sess, err := h.Store.GetSessionByRefreshJTI(ctx, rc.ID); err == nil && sess.UserID == claims.UserID {
				h.revokeSessionByID(ctx, claims.UserID, sess.ID)
			}
			_ = h.Redis.Del(ctx, refreshKey(rc.ID)).Err()
		}
	}
	h.audit(r, audit.Event{ActorID: claims.UserID, Action: "user.logged_out", Resource: "user", ResourceID: claims.UserID})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	h.logoutAll(w, r, claims)
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request, claims *jwtutil.Claims) {
	if err := h.revokeAllForUser(r.Context(), claims.UserID); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "logout could not be completed; retry")
		return
	}
	h.audit(r, audit.Event{ActorID: claims.UserID, Action: "user.logged_out_all", Resource: "user", ResourceID: claims.UserID})
	w.WriteHeader(http.StatusNoContent)
}

// revokeAllForUser revokes every session, refresh key and previously issued access token.
func (h *Handler) revokeAllForUser(ctx context.Context, userID string) error {
	// Cut off access tokens first so nothing issued so far survives.
	if err := h.revoker().RevokeUserBefore(ctx, userID, 31*24*time.Hour); err != nil {
		return err
	}
	sessions, err := h.Store.RevokeAllSessions(ctx, userID)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		_ = h.Redis.Del(ctx, refreshKey(s.RefreshJTI)).Err()
	}
	return nil
}

func (h *Handler) revokeSessionByID(ctx context.Context, userID, sessionID string) {
	jti, err := h.Store.RevokeSession(ctx, userID, sessionID)
	if err == nil && jti != "" {
		_ = h.Redis.Del(ctx, refreshKey(jti)).Err()
	}
	// Always deny the session's access tokens, even if the row was already revoked.
	_ = h.revoker().DenySession(ctx, sessionID, h.JWT.AccessTTL()+time.Minute)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	user, err := h.Store.GetUserByID(r.Context(), uid)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load user")
		return
	}
	out := map[string]any{"user": publicUser(user)}
	if !(claims0(r) != nil && claims0(r).PasswordChangeOnly()) {
		if ms, merr := h.memberships(r.Context(), user.ID); merr != nil {
			out["memberships"] = []any{}
			out["memberships_error"] = "organization service unavailable"
		} else {
			out["memberships"] = ms
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func claims0(r *http.Request) *jwtutil.Claims { c, _ := middleware.ClaimsFrom(r.Context()); return c }

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	sessions, err := h.Store.ListSessions(r.Context(), claims.UserID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list sessions")
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]any{
			"id":         s.ID,
			"user_agent": s.UserAgent,
			"ip":         s.IP,
			"created_at": s.CreatedAt,
			"expires_at": s.ExpiresAt,
			"current":    s.ID == claims.SessionID,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	id := r.PathValue("id")
	jti, err := h.Store.RevokeSession(r.Context(), claims.UserID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "session not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to revoke session")
		return
	}
	_ = h.Redis.Del(r.Context(), refreshKey(jti)).Err()
	_ = h.revoker().DenySession(r.Context(), id, h.JWT.AccessTTL()+time.Minute)
	w.WriteHeader(http.StatusNoContent)
}

// ChangePassword verifies the current password, sets the new one and signs out everywhere.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if claims != nil && claims.PasswordChangeOnly() {
		// the limited token exists precisely for this call
	} else if !requireInteractive(w, claims) {
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if msg := validatePassword(req.New); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	ctx := r.Context()
	key := "pwchange:" + claims.UserID
	if h.failCount(ctx, key) >= 5 {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed attempts; try again later")
		return
	}
	user, err := h.Store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if len(req.Current) > maxPasswordBytes || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Current)) != nil {
		h.recordFail(ctx, key)
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "current password is incorrect")
		return
	}
	if req.Current == req.New {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "new password must differ from the current password")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	if err := h.Store.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update password")
		return
	}
	h.clearFail(ctx, key)
	if err := h.revokeAllForUser(ctx, user.ID); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "password changed but sessions could not be revoked; sign out everywhere manually")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "user.password_changed", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "password_changed", "reauth_required": true})
}

// requireInteractive rejects PAT-minted tokens (which carry a scope) for account-security actions.
func requireInteractive(w http.ResponseWriter, c *jwtutil.Claims) bool {
	if c == nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return false
	}
	if c.Scope != "" {
		httpx.Error(w, http.StatusForbidden, "forbidden", "personal access tokens cannot manage account security; sign in interactively")
		return false
	}
	return true
}

// ---------------------------------------------------------------- internal

func (h *Handler) GetUserInternal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.Store.GetUserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

// LookupUsersInternal resolves many user ids at once (org member enrichment).
func (h *Handler) LookupUsersInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.IDs) > 500 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "at most 500 ids")
		return
	}
	users, err := h.Store.GetUsersByIDs(r.Context(), req.IDs)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, internalUser(&users[i]))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
}

// ---------------------------------------------------------------- tokens & helpers

type tokenPair struct {
	access  string
	refresh string
}

func (h *Handler) issueTokens(r *http.Request, user *store.User) (*tokenPair, error) {
	ctx := r.Context()
	sid := uuid.NewString()
	refreshJTI := uuid.NewString()
	refresh, exp, err := h.JWT.IssueRefreshWith(user.ID, user.Email, user.Name, jwtutil.IssueOptions{JTI: refreshJTI, SessionID: sid})
	if err != nil {
		return nil, err
	}
	if _, err := h.Store.CreateSession(ctx, sid, user.ID, refreshJTI, r.UserAgent(), clientIP(r), exp); err != nil {
		return nil, err
	}
	ttl := time.Until(exp)
	if ttl < time.Minute {
		ttl = h.JWT.RefreshTTL()
	}
	if err := h.Redis.Set(ctx, refreshKey(refreshJTI), user.ID, ttl).Err(); err != nil {
		_, _ = h.Store.RevokeSession(ctx, user.ID, sid)
		return nil, err
	}
	access, _, err := h.JWT.IssueAccessWith(user.ID, user.Email, user.Name, jwtutil.IssueOptions{Admin: user.IsAdmin, Managed: user.ManagedOrgID != nil, SessionID: sid})
	if err != nil {
		return nil, err
	}
	return &tokenPair{access: access, refresh: refresh}, nil
}

func publicUser(u *store.User) map[string]any {
	return map[string]any{
		"id":           u.ID,
		"email":        u.Email,
		"name":         u.Name,
		"username":     u.Username,
		"managed":      u.ManagedOrgID != nil,
		"must_change_password": u.MustChangePassword,
		"is_admin":     u.IsAdmin,
		"totp_enabled": u.TOTPEnabled,
		"created_at":   u.CreatedAt,
	}
}

// clientIP returns the caller address. The gateway-resolved address (X-JP-Client-IP) is honoured
// ONLY on requests presenting the internal token; X-Forwarded-For is never trusted here.
func clientIP(r *http.Request) string {
	if internalauth.Valid(r) {
		if v := strings.TrimSpace(r.Header.Get(clientip.Header)); v != "" && net.ParseIP(v) != nil {
			return v
		}
	}
	return clientip.Peer(r)
}

func refreshKey(jti string) string { return "jp:refresh:" + jti }

func mfaKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "jp:mfa:" + hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func constEq(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtleEq(ha[:], hb[:])
}
