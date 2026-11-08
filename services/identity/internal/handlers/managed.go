package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/identity/internal/store"
)

// usernameRe: lowercase letters, digits, dot, underscore, hyphen; 3-32 chars.
var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// ValidUsername reports whether a (lower-cased) username is acceptable.
func ValidUsername(u string) bool { return usernameRe.MatchString(u) }

const pwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!#"

// GeneratePassword returns a random 20 character password (no ambiguous characters).
func GeneratePassword() (string, error) {
	out := make([]byte, 20)
	max := big.NewInt(int64(len(pwAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = pwAlphabet[n.Int64()]
	}
	return string(out), nil
}

// internalUser is the account view exposed to the organization service (never the hash).
func internalUser(u *store.User) map[string]any {
	m := publicUser(u)
	m["disabled"] = u.DisabledAt != nil
	m["last_login_at"] = u.LastLoginAt
	if u.ManagedOrgID != nil {
		m["managed_org_id"] = *u.ManagedOrgID
	}
	return m
}

// managedTarget loads the managed account named in the path, scoped to the org (404 otherwise).
func (h *Handler) managedTarget(w http.ResponseWriter, r *http.Request, orgID string) (*store.User, bool) {
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "org_id required")
		return nil, false
	}
	u, err := h.Store.GetManaged(r.Context(), orgID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "managed user not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return nil, false
	}
	return u, true
}

// CreateManagedInternal creates an admin-managed account. A password is generated when omitted and
// returned exactly once.
func (h *Handler) CreateManagedInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID              string `json:"org_id"`
		Name               string `json:"name"`
		Username           string `json:"username"`
		Email              string `json:"email"`
		Password           string `json:"password"`
		MustChangePassword *bool  `json:"must_change_password"`
		ActorID            string `json:"actor_id"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and a JSON body required")
		return
	}
	req.Name = cleanName(req.Name)
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required")
		return
	}
	if !ValidUsername(req.Username) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "username must be 3-32 characters: lowercase letters, digits, '.', '_' or '-'")
		return
	}
	if req.Email != "" && !validEmail(req.Email) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid email")
		return
	}
	generated := false
	if req.Password == "" {
		pw, err := GeneratePassword()
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "password generation failed")
			return
		}
		req.Password, generated = pw, true
	} else if msg := validatePassword(req.Password); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	must := true
	if req.MustChangePassword != nil {
		must = *req.MustChangePassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	u, err := h.Store.CreateManagedUser(r.Context(), req.OrgID, req.Name, req.Username, req.Email, string(hash), must)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "username or email is already in use")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create user")
		return
	}
	h.audit(r, audit.Event{ActorID: req.ActorID, Action: "managed_user.created", Resource: "user", ResourceID: u.ID, OrgID: req.OrgID,
		Metadata: map[string]any{"username": u.Username, "must_change_password": must}})
	out := map[string]any{"user": internalUser(u)}
	if generated {
		out["generated_password"] = req.Password // returned once, never stored in clear
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (h *Handler) GetManagedInternal(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedTarget(w, r, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": internalUser(u)})
}

// UpdateManagedInternal renames and/or disables/enables. Disabling revokes every session and token at once.
func (h *Handler) UpdateManagedInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID    string  `json:"org_id"`
		Name     *string `json:"name"`
		Disabled *bool   `json:"disabled"`
		ActorID  string  `json:"actor_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	u, ok := h.managedTarget(w, r, req.OrgID)
	if !ok {
		return
	}
	if req.Name != nil {
		n := cleanName(*req.Name)
		if n == "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "name cannot be empty")
			return
		}
		req.Name = &n
	}
	if req.Disabled != nil && *req.Disabled {
		// Cut access first so nothing survives even if the update below fails midway.
		if err := h.revokeAllForUser(r.Context(), u.ID); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not revoke sessions; retry")
			return
		}
	}
	updated, err := h.Store.UpdateManaged(r.Context(), req.OrgID, u.ID, req.Name, req.Disabled)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update user")
		return
	}
	if req.Disabled != nil {
		act := "managed_user.enabled"
		if *req.Disabled {
			act = "managed_user.disabled"
		}
		h.audit(r, audit.Event{ActorID: req.ActorID, Action: act, Resource: "user", ResourceID: u.ID, OrgID: req.OrgID})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": internalUser(updated)})
}

// ResetManagedPasswordInternal sets a new password (generated when omitted, returned once),
// forces a change on next login and revokes all sessions.
func (h *Handler) ResetManagedPasswordInternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID              string `json:"org_id"`
		Password           string `json:"password"`
		MustChangePassword *bool  `json:"must_change_password"`
		ActorID            string `json:"actor_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	u, ok := h.managedTarget(w, r, req.OrgID)
	if !ok {
		return
	}
	generated := false
	if req.Password == "" {
		pw, err := GeneratePassword()
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "password generation failed")
			return
		}
		req.Password, generated = pw, true
	} else if msg := validatePassword(req.Password); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	must := true
	if req.MustChangePassword != nil {
		must = *req.MustChangePassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to hash password")
		return
	}
	if err := h.Store.ResetManagedPassword(r.Context(), req.OrgID, u.ID, string(hash), must); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to reset password")
		return
	}
	if err := h.revokeAllForUser(r.Context(), u.ID); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "password reset but sessions could not be revoked; retry")
		return
	}
	h.audit(r, audit.Event{ActorID: req.ActorID, Action: "managed_user.password_reset", Resource: "user", ResourceID: u.ID, OrgID: req.OrgID})
	out := map[string]any{"must_change_password": must}
	if generated {
		out["generated_password"] = req.Password
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) DeleteManagedInternal(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	u, ok := h.managedTarget(w, r, orgID)
	if !ok {
		return
	}
	if err := h.revokeAllForUser(r.Context(), u.ID); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not revoke sessions; retry")
		return
	}
	if err := h.Store.DeleteManaged(r.Context(), orgID, u.ID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete user")
		return
	}
	h.audit(r, audit.Event{ActorID: r.URL.Query().Get("actor_id"), Action: "managed_user.deleted", Resource: "user", ResourceID: u.ID, OrgID: orgID,
		Metadata: map[string]any{"username": u.Username}})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ManagedSessionsInternal(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedTarget(w, r, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	sessions, err := h.Store.ListSessions(r.Context(), u.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]any{"id": s.ID, "user_agent": s.UserAgent, "ip": s.IP, "created_at": s.CreatedAt, "expires_at": s.ExpiresAt})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handler) RevokeManagedSessionInternal(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedTarget(w, r, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	sid := r.PathValue("sessionId")
	if _, err := h.Store.RevokeSession(r.Context(), u.ID, sid); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "session not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	h.revokeSessionByID(r.Context(), u.ID, sid)
	w.WriteHeader(http.StatusNoContent)
}

// memberships asks the organization service for the user's organizations and per-project grants.
func (h *Handler) memberships(ctx context.Context, userID string) (json.RawMessage, error) {
	if h.OrgURL == "" || h.HTTP == nil {
		return nil, errors.New("organization service not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, fmt.Sprintf("%s/internal/users/%s/memberships", strings.TrimRight(h.OrgURL, "/"), userID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("organization service status %d", resp.StatusCode)
	}
	var body struct {
		Memberships json.RawMessage `json:"memberships"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Memberships) == 0 {
		return json.RawMessage("[]"), nil
	}
	return body.Memberships, nil
}
