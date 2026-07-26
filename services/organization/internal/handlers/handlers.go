package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/organization/internal/store"
)

type Handler struct {
	Store            *store.Store
	JWT              *jwtutil.Manager
	NotificationURL  string
	IdentityURL      string
	HTTP             *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs", auth(http.HandlerFunc(h.CreateOrg)))
	mux.Handle("GET /orgs", auth(http.HandlerFunc(h.ListOrgs)))
	mux.Handle("GET /orgs/{orgId}", auth(http.HandlerFunc(h.GetOrg)))
	mux.Handle("POST /orgs/{orgId}/invites", auth(http.HandlerFunc(h.CreateInvite)))
	mux.Handle("POST /orgs/invites/accept", auth(http.HandlerFunc(h.AcceptInvite)))
	mux.Handle("GET /orgs/{orgId}/members", auth(http.HandlerFunc(h.ListMembers)))

	// Internal membership check for project service / gateway
	mux.HandleFunc("GET /internal/orgs/{orgId}/members/{userId}", h.InternalMember)
}

var validRoles = map[string]bool{
	"owner": true, "admin": true, "member": true, "viewer": true,
}

func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name and slug required")
		return
	}
	org, err := h.Store.CreateOrg(r.Context(), req.Name, req.Slug, uid)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "slug already taken")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create org")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"org": org})
}

func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgs, err := h.Store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list orgs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	org, err := h.Store.GetOrg(r.Context(), orgID, uid)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to get org")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"org": org})
}

func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	role, err := h.Store.MemberRole(r.Context(), orgID, uid)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if role != "owner" && role != "admin" {
		httpx.Error(w, http.StatusForbidden, "forbidden", "owner or admin required")
		return
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Role = strings.TrimSpace(strings.ToLower(req.Role))
	if req.Email == "" || !validRoles[req.Role] {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "valid email and role required")
		return
	}
	tokenBytes := make([]byte, 24)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	expires := time.Now().UTC().Add(7 * 24 * time.Hour)
	inv, err := h.Store.CreateInvite(r.Context(), orgID, req.Email, req.Role, token, uid, expires)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create invite")
		return
	}
	// Fire-and-forget notification
	go h.notifyInvite(inv.Email, inv.Token, orgID, inv.Role)

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": inv.ID, "email": inv.Email, "role": inv.Role, "token": inv.Token, "expires_at": inv.ExpiresAt,
	})
}

func (h *Handler) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.ClaimsFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "token required")
		return
	}
	org, role, err := h.Store.AcceptInvite(r.Context(), strings.TrimSpace(req.Token), claims.UserID, claims.Email)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "invite not found or expired")
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "invite email mismatch")
		return
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "invite already accepted")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to accept invite")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"org": org, "role": role})
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if _, err := h.Store.GetOrg(r.Context(), orgID, uid); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	members, err := h.Store.ListMembers(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list members")
		return
	}
	// Enrich with identity service when available
	for i := range members {
		if email, name, ok := h.lookupUser(members[i].UserID); ok {
			members[i].Email = email
			members[i].Name = name
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}

func (h *Handler) InternalMember(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	userID := r.PathValue("userId")
	role, err := h.Store.MemberRole(r.Context(), orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "not a member")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"org_id": orgID, "user_id": userID, "role": role})
}

func (h *Handler) notifyInvite(email, token, orgID, role string) {
	if h.NotificationURL == "" || h.HTTP == nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"type":  "org.invite",
		"email": email,
		"token": token,
		"org_id": orgID,
		"role":  role,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.NotificationURL, "/")+"/notify", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (h *Handler) lookupUser(userID string) (email, name string, ok bool) {
	if h.IdentityURL == "" || h.HTTP == nil {
		return "", "", false
	}
	url := fmt.Sprintf("%s/internal/users/%s", strings.TrimRight(h.IdentityURL, "/"), userID)
	resp, err := h.HTTP.Get(url)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}
	var payload struct {
		User struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", false
	}
	return payload.User.Email, payload.User.Name, true
}
