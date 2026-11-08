package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/identity/internal/store"
)

type patReq struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
}

var validScopes = map[string]bool{jwtutil.ScopeRead: true, jwtutil.ScopeWrite: true, jwtutil.ScopeAdmin: true}

// EffectiveScope collapses a scope list to the single highest scope (admin > write > read).
// An empty list means "write" (backwards compatible default for tokens created without scopes).
func EffectiveScope(scopes []string) string {
	best := ""
	for _, s := range scopes {
		switch s {
		case jwtutil.ScopeAdmin:
			return jwtutil.ScopeAdmin
		case jwtutil.ScopeWrite:
			best = jwtutil.ScopeWrite
		case jwtutil.ScopeRead:
			if best == "" {
				best = jwtutil.ScopeRead
			}
		}
	}
	if best == "" {
		return jwtutil.ScopeWrite
	}
	return best
}

func (h *Handler) CreatePAT(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	uid := claims.UserID
	var req patReq
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 100 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required (max 100 chars)")
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{jwtutil.ScopeWrite}
	}
	seen := map[string]bool{}
	scopes := make([]string, 0, len(req.Scopes))
	for _, s := range req.Scopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if !validScopes[s] {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "scopes must be any of: read, write, admin")
			return
		}
		if !seen[s] {
			seen[s] = true
			scopes = append(scopes, s)
		}
	}
	var expiresAt *time.Time
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "expires_in_days must be between 1 and 3650 (omit for no expiry)")
		return
	}
	if req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	raw, err := randomToken(32)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "token generation failed")
		return
	}
	token := "jp_pat_" + raw
	p, err := h.Store.CreatePAT(r.Context(), uid, strings.TrimSpace(req.Name), hashToken(token), token[:12], scopes, expiresAt)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create PAT")
		return
	}
	h.audit(r, audit.Event{ActorID: uid, Action: "pat.created", Resource: "pat", ResourceID: p.ID,
		Metadata: map[string]any{"scopes": scopes}})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":         p.ID,
		"name":       p.Name,
		"scopes":     p.Scopes,
		"created_at": p.CreatedAt,
		"expires_at": p.ExpiresAt,
		"token":      token, // shown once; only the SHA-256 is stored
	})
}

func (h *Handler) ListPATs(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	pats, err := h.Store.ListPATs(r.Context(), claims.UserID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list PATs")
		return
	}
	out := make([]map[string]any, 0, len(pats))
	now := time.Now()
	for _, p := range pats {
		out = append(out, map[string]any{
			"id":           p.ID,
			"name":         p.Name,
			"scopes":       p.Scopes,
			"token_prefix": p.TokenPrefix,
			"created_at":   p.CreatedAt,
			"last_used_at": p.LastUsedAt,
			"expires_at":   p.ExpiresAt,
			"expired":      p.ExpiresAt != nil && now.After(*p.ExpiresAt),
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pats": out})
}

func (h *Handler) RevokePAT(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	id := r.PathValue("id")
	if err := h.Store.RevokePAT(r.Context(), claims.UserID, id); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "PAT not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to revoke PAT")
		return
	}
	h.audit(r, audit.Event{ActorID: claims.UserID, Action: "pat.revoked", Resource: "pat", ResourceID: id})
	w.WriteHeader(http.StatusNoContent)
}

// VerifyPATInternal is called by the gateway to authenticate a jp_pat_* token.
func (h *Handler) VerifyPATInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !strings.HasPrefix(token, "jp_pat_") || len(token) > 200 {
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
	if pat.ExpiresAt != nil && time.Now().After(*pat.ExpiresAt) {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "token expired")
		return
	}
	if user.DisabledAt != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	_ = h.Store.TouchPAT(r.Context(), pat.ID)
	scope := EffectiveScope(pat.Scopes)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":       publicUser(user),
		"pat_id":     pat.ID,
		"scopes":     pat.Scopes,
		"scope":      scope,
		"expires_at": pat.ExpiresAt,
	})
}
