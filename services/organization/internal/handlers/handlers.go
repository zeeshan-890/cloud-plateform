package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/organization/internal/store"
)

// Handler serves the organization API.
type Handler struct {
	Store           store.Repo
	JWT             *jwtutil.Manager
	NotificationURL string
	IdentityURL     string
	ProjectURL      string
	Audit           Auditor
	HTTP            *http.Client // internal client signed with INTERNAL_TOKEN
	Log             *slog.Logger
	InviteTTL       time.Duration
}

// slugRe is the organization slug format.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)

const (
	maxPendingInvites = 200
	maxBody           = 1 << 20
)

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return internalauth.NewClient(10 * time.Second)
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs", auth(http.HandlerFunc(h.CreateOrg)))
	mux.Handle("GET /orgs", auth(http.HandlerFunc(h.ListOrgs)))
	mux.Handle("POST /orgs/invites/accept", auth(http.HandlerFunc(h.AcceptInvite)))
	mux.Handle("GET /orgs/{orgId}", auth(http.HandlerFunc(h.GetOrg)))
	mux.Handle("DELETE /orgs/{orgId}", auth(http.HandlerFunc(h.DeleteOrg)))

	mux.Handle("POST /orgs/{orgId}/invites", auth(http.HandlerFunc(h.CreateInvite)))
	mux.Handle("GET /orgs/{orgId}/invites", auth(http.HandlerFunc(h.ListInvites)))
	mux.Handle("DELETE /orgs/{orgId}/invites/{inviteId}", auth(http.HandlerFunc(h.RevokeInvite)))

	mux.Handle("POST /orgs/{orgId}/users", auth(http.HandlerFunc(h.CreateUser)))
	mux.Handle("GET /orgs/{orgId}/users", auth(http.HandlerFunc(h.ListUsers)))
	mux.Handle("PATCH /orgs/{orgId}/users/{userId}", auth(http.HandlerFunc(h.UpdateUser)))
	mux.Handle("PUT /orgs/{orgId}/users/{userId}/grants", auth(http.HandlerFunc(h.PutGrants)))
	mux.Handle("POST /orgs/{orgId}/users/{userId}/reset-password", auth(http.HandlerFunc(h.ResetPassword)))
	mux.Handle("DELETE /orgs/{orgId}/users/{userId}", auth(http.HandlerFunc(h.DeleteUser)))
	mux.Handle("GET /orgs/{orgId}/users/{userId}/sessions", auth(http.HandlerFunc(h.UserSessions)))
	mux.Handle("DELETE /orgs/{orgId}/users/{userId}/sessions/{sessionId}", auth(http.HandlerFunc(h.RevokeUserSession)))
	mux.Handle("GET /orgs/{orgId}/access-presets", auth(http.HandlerFunc(h.AccessPresets)))
	mux.Handle("GET /orgs/{orgId}/members", auth(http.HandlerFunc(h.ListMembers)))
	mux.Handle("PATCH /orgs/{orgId}/members/{userId}", auth(http.HandlerFunc(h.UpdateMember)))
	mux.Handle("DELETE /orgs/{orgId}/members/{userId}", auth(http.HandlerFunc(h.RemoveMember)))

	// Internal (INTERNAL_TOKEN required): membership, org lookup, invite validation.
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("GET /internal/orgs/{orgId}", in(h.InternalOrg))
	mux.Handle("GET /internal/orgs/{orgId}/members/{userId}", in(h.InternalMember))
	mux.Handle("GET /internal/orgs/{orgId}/projects/{projectId}/access/{userId}", in(h.InternalAccess))
	mux.Handle("GET /internal/orgs/{orgId}/users/{userId}/projects", in(h.InternalUserProjects))
	mux.Handle("GET /internal/users/{userId}/memberships", in(h.InternalMemberships))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
	mux.Handle("POST /internal/invites/check", in(h.InternalInviteCheck))
}

var validRoles = map[string]bool{"owner": true, "admin": true, "member": true, "viewer": true}

func cleanName(n string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(n) {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if r := []rune(out); len(r) > 100 {
		out = string(r[:100])
	}
	return out
}

// requireRole resolves the caller's role in the org and enforces a minimum. Non-members get 404
// (do not reveal which org ids exist).
func (h *Handler) requireRole(w http.ResponseWriter, r *http.Request, min membership.Role) (orgID, uid string, role membership.Role, ok bool) {
	uid = middleware.UserIDFrom(r.Context())
	orgID = r.PathValue("orgId")
	raw, err := h.Store.MemberRole(r.Context(), orgID, uid)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "organization not found")
		return "", "", "", false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "membership lookup failed")
		return "", "", "", false
	}
	role = membership.Role(raw)
	if !role.AtLeast(min) {
		httpx.Error(w, http.StatusForbidden, "forbidden", fmt.Sprintf("%s role or higher required", min))
		return "", "", "", false
	}
	return orgID, uid, role, true
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
	if c, ok := middleware.ClaimsFrom(r.Context()); ok && c.Managed {
		httpx.Error(w, http.StatusForbidden, "forbidden", "accounts created by an administrator cannot create organizations")
		return
	}
	req.Name = cleanName(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name and slug required")
		return
	}
	if !slugRe.MatchString(req.Slug) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "slug must be 2-39 characters: lowercase letters, digits and hyphens, starting with a letter or digit")
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

// DeleteOrg (owner only) deletes every project through the project service's cascade first, then
// the organization. If any project cannot be fully cleaned up the org is KEPT and the failures are
// reported so the owner can retry.
func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	orgID, uid, _, ok := h.requireRole(w, r, membership.Owner)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	if h.ProjectURL == "" {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "project service not configured; cannot clean up resources")
		return
	}
	ids, err := h.listProjectIDs(ctx, orgID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "could not list projects for cleanup: "+err.Error())
		return
	}
	type failure struct {
		ProjectID string `json:"project_id"`
		Error     string `json:"error"`
	}
	var (
		mu       sync.Mutex
		failures []failure
		wg       sync.WaitGroup
		sem      = make(chan struct{}, 4)
	)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := h.deleteProject(ctx, orgID, id); err != nil {
				mu.Lock()
				failures = append(failures, failure{ProjectID: id, Error: err.Error()})
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if len(failures) > 0 {
		h.log().Error("org delete aborted: project cleanup failed", "org", orgID, "failed", len(failures))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    map[string]string{"code": "partial_failure", "message": "some projects could not be cleaned up; the organization was not deleted"},
			"failures": failures,
		})
		return
	}
	if err := h.Store.DeleteOrg(r.Context(), orgID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete organization")
		return
	}
	h.log().Info("organization deleted", "org", orgID, "by", uid, "projects", len(ids))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listProjectIDs(ctx context.Context, orgID string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/internal/orgs/%s/projects", strings.TrimRight(h.ProjectURL, "/"), orgID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("project service returned %d", resp.StatusCode)
	}
	var out struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Projects))
	for _, p := range out.Projects {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

func (h *Handler) deleteProject(ctx context.Context, orgID, projectID string) error {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodDelete,
		fmt.Sprintf("%s/internal/orgs/%s/projects/%s", strings.TrimRight(h.ProjectURL, "/"), orgID, projectID), nil)
	if err != nil {
		return err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("project delete returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// ---------------------------------------------------------------- invites

func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
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
	if a, err := mail.ParseAddress(req.Email); err != nil || a.Address != req.Email || len(req.Email) > 254 || !validRoles[req.Role] {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "valid email and role (owner, admin, member, viewer) required")
		return
	}
	// Only an owner may mint owner/admin invitations: admins cannot escalate.
	if (req.Role == "owner" || req.Role == "admin") && callerRole != membership.Owner {
		httpx.Error(w, http.StatusForbidden, "forbidden", "only an owner can invite administrators or owners")
		return
	}
	ctx := r.Context()

	pending, err := h.Store.CountPendingInvites(ctx, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create invite")
		return
	}
	if pending >= maxPendingInvites {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many pending invitations; revoke some first")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create invite")
		return
	}
	token := hex.EncodeToString(raw)
	ttl := h.InviteTTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	inv, err := h.Store.CreateInvite(ctx, orgID, req.Email, req.Role, store.HashToken(token), uid, time.Now().UTC().Add(ttl))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create invite")
		return
	}

	delivery := h.notifyInvite(ctx, orgID, uid, inv.Email, token, inv.Role)
	// The plaintext token is returned exactly once (here); only its hash is stored.
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": inv.ID, "email": inv.Email, "role": inv.Role, "token": token, "expires_at": inv.ExpiresAt,
		"email_delivery": delivery,
	})
}

func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	list, err := h.Store.ListInvites(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list invites")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"invites": list})
}

func (h *Handler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	if err := h.Store.RevokeInvite(r.Context(), orgID, r.PathValue("inviteId")); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "invite not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to revoke invite")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Token) == "" || len(req.Token) > 200 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "token required")
		return
	}
	org, role, err := h.Store.AcceptInvite(r.Context(), store.HashToken(strings.TrimSpace(req.Token)), claims.UserID, strings.ToLower(claims.Email))
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "invite not found or expired")
	case errors.Is(err, store.ErrForbidden):
		// Same body as not-found: do not confirm that the token exists for someone else.
		httpx.Error(w, http.StatusNotFound, "not_found", "invite not found or expired")
	case errors.Is(err, store.ErrAlreadyExists):
		httpx.Error(w, http.StatusConflict, "conflict", "invite already accepted")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to accept invite")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"org": org, "role": role})
	}
}

// InternalInviteCheck lets the identity service validate an invite during registration.
func (h *Handler) InternalInviteCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.Token == "" || req.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "token and email required")
		return
	}
	inv, err := h.Store.CheckInvite(r.Context(), store.HashToken(strings.TrimSpace(req.Token)), strings.ToLower(strings.TrimSpace(req.Email)))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "invite invalid")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"valid": true, "org_id": inv.OrgID, "role": inv.Role})
}

// ---------------------------------------------------------------- members

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, ok := h.requireRole(w, r, membership.Viewer)
	if !ok {
		return
	}
	members, err := h.Store.ListMembers(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list members")
		return
	}
	h.enrich(r.Context(), members)
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}

// UpdateMember changes a member's role. Role changes are owner-only; the last owner cannot be demoted.
func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, ok := h.requireRole(w, r, membership.Owner)
	if !ok {
		return
	}
	target := r.PathValue("userId")
	var req struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if !validRoles[req.Role] {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "role must be one of owner, admin, member, viewer")
		return
	}
	switch err := h.Store.SetMemberRole(r.Context(), orgID, target, req.Role); {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "member not found")
	case errors.Is(err, store.ErrLastOwner):
		httpx.Error(w, http.StatusConflict, "last_owner", "an organization must keep at least one owner")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update member")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"user_id": target, "role": req.Role})
	}
}

// RemoveMember removes a member. Members may remove themselves (leave); admins can remove members
// and viewers; owners can remove anyone except the last owner.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Viewer)
	if !ok {
		return
	}
	target := r.PathValue("userId")
	if target != uid {
		if !callerRole.AtLeast(membership.Admin) {
			httpx.Error(w, http.StatusForbidden, "forbidden", "admin role or higher required")
			return
		}
		raw, err := h.Store.MemberRole(r.Context(), orgID, target)
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to remove member")
			return
		}
		// Admins can only remove people below the owner/admin tier; owners can remove anyone.
		if callerRole != membership.Owner && membership.Role(raw).AtLeast(membership.Admin) {
			httpx.Error(w, http.StatusForbidden, "forbidden", "only an owner can remove an admin or owner")
			return
		}
	}
	managed, _ := h.Store.IsManagedMember(r.Context(), orgID, target)
	if managed && target == uid {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "an administrator-created account cannot delete itself")
		return
	}
	if status, code, msg := h.removeMember(r.Context(), orgID, target, managed, uid); status != 0 {
		httpx.Error(w, status, code, msg)
		return
	}
	h.audit(r, orgID, uid, "member.removed", target, map[string]any{"managed": managed})
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- internal

// InternalMember is the membership oracle used by every other service.
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

// InternalOrg returns slug/name for an org id (domain/project services need the slug).
func (h *Handler) InternalOrg(w http.ResponseWriter, r *http.Request) {
	org, err := h.Store.GetOrgByID(r.Context(), r.PathValue("orgId"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": org.ID, "slug": org.Slug, "name": org.Name})
}

// ---------------------------------------------------------------- outbound helpers

// notifyInvite asks the notification service to e-mail the invitation. It is bounded by a
// timeout and reports the outcome ("queued" | "failed" | "disabled") instead of hiding it.
func (h *Handler) notifyInvite(ctx context.Context, orgID, inviterID, email, token, role string) string {
	if h.NotificationURL == "" {
		return "disabled"
	}
	orgName := ""
	if org, err := h.Store.GetOrgByID(ctx, orgID); err == nil {
		orgName = org.Name
	}
	inviterName := ""
	if _, name, ok := h.lookupUser(ctx, inviterID); ok {
		inviterName = name
	}
	body, _ := json.Marshal(map[string]any{
		"type": "org.invite", "email": email, "token": token, "org_id": orgID,
		"org_name": orgName, "role": role, "inviter_name": inviterName,
	})
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(h.NotificationURL, "/")+"/notify", bytes.NewReader(body))
	if err != nil {
		return "failed"
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		h.log().Warn("invite notification failed", "err", err)
		return "failed"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		h.log().Warn("invite notification rejected", "status", resp.StatusCode)
		return "failed"
	}
	return "queued"
}

func (h *Handler) lookupUser(ctx context.Context, userID string) (email, name string, ok bool) {
	users := h.lookupUsers(ctx, []string{userID})
	if u, found := users[userID]; found {
		return u.Email, u.Name, true
	}
	return "", "", false
}

type userInfo struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	Name               string `json:"name"`
	Username           string `json:"username"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
	LastLoginAt        any    `json:"last_login_at"`
}

// lookupUsers resolves many users in a single call to the identity service.
func (h *Handler) lookupUsers(ctx context.Context, ids []string) map[string]userInfo {
	out := map[string]userInfo{}
	if h.IdentityURL == "" || len(ids) == 0 {
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"ids": ids})
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(h.IdentityURL, "/")+"/internal/users/lookup", bytes.NewReader(body))
	if err != nil {
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	var payload struct {
		Users []userInfo `json:"users"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return out
	}
	for _, u := range payload.Users {
		out[u.ID] = u
	}
	return out
}

func (h *Handler) enrich(ctx context.Context, members []store.Member) {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users := h.lookupUsers(ctx, ids)
	for i := range members {
		if u, ok := users[members[i].UserID]; ok {
			members[i].Email = u.Email
			members[i].Name = u.Name
		}
	}
}
