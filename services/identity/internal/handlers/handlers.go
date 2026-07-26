package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/identity/internal/store"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	Store  *store.Store
	JWT    *jwtutil.Manager
	Redis  *redis.Client
	Audit  *audit.Writer
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)

	mux.HandleFunc("POST /auth/register", h.Register)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("POST /auth/refresh", h.Refresh)

	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("POST /auth/logout", auth(http.HandlerFunc(h.Logout)))
	mux.Handle("GET /auth/me", auth(http.HandlerFunc(h.Me)))
	mux.Handle("GET /auth/sessions", auth(http.HandlerFunc(h.ListSessions)))
	mux.Handle("DELETE /auth/sessions/{id}", auth(http.HandlerFunc(h.RevokeSession)))
	mux.Handle("POST /auth/pats", auth(http.HandlerFunc(h.CreatePAT)))
	mux.Handle("GET /auth/pats", auth(http.HandlerFunc(h.ListPATs)))
	mux.Handle("DELETE /auth/pats/{id}", auth(http.HandlerFunc(h.RevokePAT)))

	// Internal: validate user exists (for org service member enrichment later)
	mux.HandleFunc("GET /internal/users/{id}", h.GetUserInternal)
	// Internal: verify PAT for gateway auth
	mux.HandleFunc("POST /internal/pats/verify", h.VerifyPATInternal)
}

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

type patReq struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || req.Name == "" || len(req.Password) < 8 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "email, name, and password (min 8) required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	user, err := h.Store.CreateUser(r.Context(), req.Email, req.Name, string(hash))
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "email already registered")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create user")
		return
	}
	tokens, err := h.issueTokens(w, r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	_ = h.Audit.Write(r.Context(), audit.Event{
		ActorID: user.ID, Action: "user.registered", Resource: "user", ResourceID: user.ID,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"user":          publicUser(user),
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	user, err := h.Store.GetUserByEmail(r.Context(), req.Email)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}
	tokens, err := h.issueTokens(w, r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	_ = h.Audit.Write(r.Context(), audit.Event{
		ActorID: user.ID, Action: "user.logged_in", Resource: "user", ResourceID: user.ID,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":          publicUser(user),
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := httpx.Decode(r, &req); err != nil || req.RefreshToken == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "refresh_token required")
		return
	}
	claims, err := h.JWT.Parse(req.RefreshToken)
	if err != nil || claims.Type != "refresh" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid refresh token")
		return
	}
	// Check Redis blacklist / session
	key := refreshKey(claims.ID)
	exists, err := h.Redis.Exists(r.Context(), key).Result()
	if err != nil || exists == 0 {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "refresh token revoked or expired")
		return
	}
	sess, err := h.Store.GetSessionByRefreshJTI(r.Context(), claims.ID)
	if err != nil || sess.RevokedAt != nil || time.Now().After(sess.ExpiresAt) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "session invalid")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	// Rotate: revoke old refresh, issue new pair
	_ = h.Redis.Del(r.Context(), key).Err()
	_ = h.Store.RevokeSessionByJTI(r.Context(), claims.ID)

	tokens, err := h.issueTokens(w, r, user)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue tokens")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	// Best-effort: blacklist access jti for remaining TTL
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		_ = h.Redis.Set(r.Context(), accessDenyKey(claims.ID), "1", ttl).Err()
	}
	_ = h.Audit.Write(r.Context(), audit.Event{
		ActorID: claims.UserID, Action: "user.logged_out", Resource: "user", ResourceID: claims.UserID,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	w.WriteHeader(http.StatusNoContent)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	sessions, err := h.Store.ListSessions(r.Context(), uid)
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
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	id := r.PathValue("id")
	if err := h.Store.RevokeSession(r.Context(), uid, id); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "session not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to revoke session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreatePAT(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	var req patReq
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required")
		return
	}
	raw, err := randomToken(32)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "token generation failed")
		return
	}
	token := "jp_pat_" + raw
	hash := hashToken(token)
	prefix := token[:12]
	p, err := h.Store.CreatePAT(r.Context(), uid, strings.TrimSpace(req.Name), hash, prefix, req.Scopes)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create PAT")
		return
	}
	_ = h.Audit.Write(r.Context(), audit.Event{
		ActorID: uid, Action: "pat.created", Resource: "pat", ResourceID: p.ID,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":         p.ID,
		"name":       p.Name,
		"scopes":     p.Scopes,
		"created_at": p.CreatedAt,
		"token":      token,
	})
}

func (h *Handler) ListPATs(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	pats, err := h.Store.ListPATs(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list PATs")
		return
	}
	out := make([]map[string]any, 0, len(pats))
	for _, p := range pats {
		out = append(out, map[string]any{
			"id":           p.ID,
			"name":         p.Name,
			"scopes":       p.Scopes,
			"token_prefix": p.TokenPrefix,
			"created_at":   p.CreatedAt,
			"last_used_at": p.LastUsedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pats": out})
}

func (h *Handler) RevokePAT(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	id := r.PathValue("id")
	if err := h.Store.RevokePAT(r.Context(), uid, id); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "PAT not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to revoke PAT")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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

func (h *Handler) VerifyPATInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !strings.HasPrefix(token, "jp_pat_") {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	pat, user, err := h.Store.LookupPATByHash(r.Context(), hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "pat lookup failed")
		return
	}
	_ = h.Store.TouchPAT(r.Context(), pat.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":   publicUser(user),
		"pat_id": pat.ID,
		"scopes": pat.Scopes,
	})
}

type tokenPair struct {
	access  string
	refresh string
}

func (h *Handler) issueTokens(w http.ResponseWriter, r *http.Request, user *store.User) (*tokenPair, error) {
	access, _, err := h.JWT.IssueAccess(user.ID, user.Email, user.Name)
	if err != nil {
		return nil, err
	}
	refresh, exp, err := h.JWT.IssueRefresh(user.ID, user.Email, user.Name)
	if err != nil {
		return nil, err
	}
	claims, err := h.JWT.Parse(refresh)
	if err != nil {
		return nil, err
	}
	_, err = h.Store.CreateSession(r.Context(), user.ID, claims.ID, r.UserAgent(), clientIP(r), exp)
	if err != nil {
		return nil, err
	}
	ttl := time.Until(exp)
	if ttl < time.Minute {
		ttl = h.JWT.RefreshTTL()
	}
	if err := h.Redis.Set(r.Context(), refreshKey(claims.ID), user.ID, ttl).Err(); err != nil {
		return nil, err
	}
	return &tokenPair{access: access, refresh: refresh}, nil
}

func publicUser(u *store.User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"name":       u.Name,
		"created_at": u.CreatedAt,
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}
	return host
}

func refreshKey(jti string) string { return "jp:refresh:" + jti }
func accessDenyKey(jti string) string { return "jp:deny:" + jti }

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
