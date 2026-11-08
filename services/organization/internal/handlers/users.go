package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/organization/internal/store"
)

// Auditor persists audit events (nil-safe).
type Auditor interface {
	Write(ctx context.Context, e audit.Event) error
}

func (h *Handler) audit(r *http.Request, orgID, actor, action, resourceID string, meta map[string]any) {
	if h.Audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := h.Audit.Write(ctx, audit.Event{ActorID: actor, Action: action, Resource: "user", ResourceID: resourceID, OrgID: orgID, Metadata: meta, IP: r.Header.Get("X-Forwarded-For"), UserAgent: r.UserAgent()}); err != nil {
		h.log().Warn("audit write failed", "action", action, "err", err)
	}
}

// grantReq is one project grant in a request.
type grantReq struct {
	ProjectID     string                               `json:"project_id"`
	Preset        string                               `json:"preset"`
	Areas         map[membership.Area]membership.Level `json:"areas"`
	SecretsReveal *bool                                `json:"secrets_reveal"`
}

// resolveGrants validates requested grants and expands presets. `actor` is what the granting user
// holds on every project; nobody can grant more than they hold.
func (h *Handler) resolveGrants(ctx context.Context, orgID string, actor *membership.Access, in []grantReq) ([]store.Grant, error) {
	seen := map[string]bool{}
	out := make([]store.Grant, 0, len(in))
	for _, g := range in {
		if g.ProjectID == "" || seen[g.ProjectID] {
			return nil, errors.New("each grant needs a distinct project_id")
		}
		seen[g.ProjectID] = true
		var areas map[membership.Area]membership.Level
		preset := g.Preset
		reveal := false
		if g.SecretsReveal != nil {
			reveal = *g.SecretsReveal
		}
		switch preset {
		case "", membership.PresetCustom:
			preset = membership.PresetCustom
			areas = g.Areas
		default:
			p, ok := membership.PresetAreas(preset)
			if !ok {
				return nil, fmt.Errorf("unknown preset %q", preset)
			}
			areas = p
			if g.SecretsReveal == nil {
				reveal = membership.PresetSecretsReveal(preset)
			}
		}
		for a, l := range areas {
			if !membership.ValidArea(a) || !membership.ValidLevel(l) {
				return nil, fmt.Errorf("invalid area/level %q=%q", a, l)
			}
		}
		areas = membership.NormalizeAreas(areas)
		if len(areas) == 0 {
			return nil, fmt.Errorf("grant for project %s has no area with access", g.ProjectID)
		}
		if reveal && !areas[membership.AreaSecrets].AtLeast(membership.LevelView) {
			return nil, errors.New("secrets_reveal requires at least view access on the secrets area")
		}
		if !actor.Within(areas, reveal) {
			return nil, errors.New("you cannot grant more access than you hold")
		}
		if ok, err := h.projectInOrg(ctx, orgID, g.ProjectID); err != nil {
			return nil, fmt.Errorf("project check unavailable: %w", err)
		} else if !ok {
			return nil, fmt.Errorf("project %s does not exist in this organization", g.ProjectID)
		}
		out = append(out, store.Grant{ProjectID: g.ProjectID, Preset: preset, Areas: areas, SecretsReveal: reveal})
	}
	return out, nil
}

func (h *Handler) projectInOrg(ctx context.Context, orgID, projectID string) (bool, error) {
	if h.ProjectURL == "" {
		return false, errors.New("project service not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, fmt.Sprintf("%s/internal/orgs/%s/projects/%s", strings.TrimRight(h.ProjectURL, "/"), orgID, url.PathEscape(projectID)), nil)
	if err != nil {
		return false, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("project service status %d", resp.StatusCode)
}

type projectInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (h *Handler) listProjects(ctx context.Context, orgID string) ([]projectInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, fmt.Sprintf("%s/internal/orgs/%s/projects", strings.TrimRight(h.ProjectURL, "/"), orgID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("project service status %d", resp.StatusCode)
	}
	var out struct {
		Projects []projectInfo `json:"projects"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// ---------------------------------------------------------------- identity calls

func (h *Handler) identityCall(ctx context.Context, method, path string, body any) (int, map[string]any, error) {
	if h.IdentityURL == "" {
		return 0, nil, errors.New("identity service not configured")
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, strings.TrimRight(h.IdentityURL, "/")+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, nil
}

// identityMsg extracts the error message of an identity response.
func identityMsg(m map[string]any, fallback string) string {
	if e, ok := m["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok && msg != "" {
			return msg
		}
	}
	return fallback
}

// ---------------------------------------------------------------- views

type userView struct {
	UserID             string        `json:"user_id"`
	Name               string        `json:"name"`
	Username           string        `json:"username,omitempty"`
	Email              string        `json:"email,omitempty"`
	Role               string        `json:"role"`
	Managed            bool          `json:"managed"`
	Disabled           bool          `json:"disabled"`
	Status             string        `json:"status"`
	MustChangePassword bool          `json:"must_change_password"`
	LastLoginAt        any           `json:"last_login_at"`
	Full               bool          `json:"full"`
	Grants             []store.Grant `json:"grants"`
}

func fillFromIdentity(v *userView, u userInfo) {
	v.Name, v.Username, v.Email = u.Name, u.Username, u.Email
	v.Disabled, v.MustChangePassword, v.LastLoginAt = u.Disabled, u.MustChangePassword, u.LastLoginAt
	v.Status = "active"
	if v.Disabled {
		v.Status = "disabled"
	}
}

// ---------------------------------------------------------------- handlers

// actorAccess is what an org owner/admin holds on every project.
var fullAccess = &membership.Access{Full: true}

type createUserReq struct {
	Name               string     `json:"name"`
	Username           string     `json:"username"`
	Email              string     `json:"email"`
	Password           string     `json:"password"`
	Role               string     `json:"role"`
	MustChangePassword *bool      `json:"must_change_password"`
	Grants             []grantReq `json:"grants"`
}

// CreateUser (org owner/admin) creates a managed account. The generated password is returned once.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	var req createUserReq
	if err := httpx.DecodeLimit(r, &req, 1<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role != "admin" && req.Role != "member" && req.Role != "viewer" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "role must be admin, member or viewer")
		return
	}
	if req.Role == "admin" && callerRole != membership.Owner {
		httpx.Error(w, http.StatusForbidden, "forbidden", "only an owner can create administrators")
		return
	}
	var grants []store.Grant
	if req.Role != "admin" {
		var err error
		if grants, err = h.resolveGrants(r.Context(), orgID, fullAccess, req.Grants); err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
	} else if len(req.Grants) > 0 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "administrators have full access; grants are not applicable")
		return
	}
	status, resp, err := h.identityCall(r.Context(), http.MethodPost, "/internal/users", map[string]any{
		"org_id": orgID, "name": req.Name, "username": req.Username, "email": req.Email, "password": req.Password,
		"must_change_password": req.MustChangePassword, "actor_id": uid,
	})
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "identity service unavailable")
		return
	}
	if status != http.StatusCreated {
		switch status {
		case http.StatusBadRequest:
			httpx.Error(w, status, "validation_error", identityMsg(resp, "invalid user"))
		case http.StatusConflict:
			httpx.Error(w, status, "conflict", identityMsg(resp, "username or email already in use"))
		default:
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "user could not be created")
		}
		return
	}
	userMap, _ := resp["user"].(map[string]any)
	newID, _ := userMap["id"].(string)
	// Membership + grants; undo the account if either fails so no orphan login remains.
	if err := h.Store.AddMember(r.Context(), orgID, newID, req.Role, true); err == nil && len(grants) > 0 {
		err = h.Store.ReplaceGrants(r.Context(), orgID, newID, uid, grants)
		if err != nil {
			_ = h.Store.RemoveMember(r.Context(), orgID, newID)
			h.undoAccount(r.Context(), orgID, newID)
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to store grants")
			return
		}
	} else if err != nil {
		h.undoAccount(r.Context(), orgID, newID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to add member")
		return
	}
	h.audit(r, orgID, uid, "user.created", newID, map[string]any{"username": userMap["username"], "role": req.Role, "grants": grantSummary(grants)})
	v := userView{UserID: newID, Role: req.Role, Managed: true, Full: req.Role == "admin", Grants: nonNilGrants(grants)}
	fillFromIdentity(&v, userInfoFrom(userMap))
	out := map[string]any{"user": v}
	if pw, ok := resp["generated_password"].(string); ok {
		out["generated_password"] = pw // shown once; never retrievable later
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (h *Handler) undoAccount(ctx context.Context, orgID, userID string) {
	_, _, _ = h.identityCall(context.WithoutCancel(ctx), http.MethodDelete, fmt.Sprintf("/internal/managed-users/%s?org_id=%s", userID, orgID), nil)
}

func grantSummary(gs []store.Grant) []map[string]any {
	out := make([]map[string]any, 0, len(gs))
	for _, g := range gs {
		out = append(out, map[string]any{"project_id": g.ProjectID, "preset": g.Preset, "secrets_reveal": g.SecretsReveal})
	}
	return out
}

func nonNilGrants(g []store.Grant) []store.Grant {
	if g == nil {
		return []store.Grant{}
	}
	return g
}

func userInfoFrom(m map[string]any) userInfo {
	b, _ := json.Marshal(m)
	var u userInfo
	_ = json.Unmarshal(b, &u)
	return u
}

// ListUsers (admin+) lists every member with status, last login and grants.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	members, err := h.Store.ListMembers(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list users")
		return
	}
	grants, err := h.Store.ListGrantsForOrg(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list users")
		return
	}
	byUser := map[string][]store.Grant{}
	for _, g := range grants {
		byUser[g.UserID] = append(byUser[g.UserID], g)
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	infos := h.lookupUsers(r.Context(), ids)
	out := make([]userView, 0, len(members))
	for _, m := range members {
		v := userView{UserID: m.UserID, Role: m.Role, Managed: m.Managed, Full: membership.Role(m.Role).AtLeast(membership.Admin), Grants: nonNilGrants(byUser[m.UserID])}
		fillFromIdentity(&v, infos[m.UserID])
		out = append(out, v)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
}

// target loads the addressed member and applies the "only an owner may touch admins/owners" rule.
func (h *Handler) target(w http.ResponseWriter, r *http.Request, orgID string, callerRole membership.Role) (userID, role string, managed bool, ok bool) {
	userID = r.PathValue("userId")
	role, err := h.Store.MemberRole(r.Context(), orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found")
		return "", "", false, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return "", "", false, false
	}
	managed, _ = h.Store.IsManagedMember(r.Context(), orgID, userID)
	if membership.Role(role).AtLeast(membership.Admin) && callerRole != membership.Owner {
		httpx.Error(w, http.StatusForbidden, "forbidden", "only an owner can manage administrators and owners")
		return "", "", false, false
	}
	return userID, role, managed, true
}

// UpdateUser changes name / disabled (managed accounts) and role.
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, curRole, managed, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Disabled *bool   `json:"disabled"`
		Role     *string `json:"role"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if req.Role != nil {
		nr := strings.ToLower(strings.TrimSpace(*req.Role))
		if !validRoles[nr] {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "role must be one of owner, admin, member, viewer")
			return
		}
		if (nr == "admin" || nr == "owner") && callerRole != membership.Owner {
			httpx.Error(w, http.StatusForbidden, "forbidden", "only an owner can create administrators or owners")
			return
		}
		if nr != curRole {
			switch err := h.Store.SetMemberRole(r.Context(), orgID, targetID, nr); {
			case errors.Is(err, store.ErrLastOwner):
				httpx.Error(w, http.StatusConflict, "last_owner", "an organization must keep at least one owner")
				return
			case err != nil:
				httpx.Error(w, http.StatusInternalServerError, "internal", "failed to change role")
				return
			}
			h.audit(r, orgID, uid, "user.role_changed", targetID, map[string]any{"from": curRole, "to": nr})
		}
	}
	if req.Name != nil || req.Disabled != nil {
		if !managed {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "only admin-created accounts can be renamed or disabled here")
			return
		}
		if req.Disabled != nil && *req.Disabled && targetID == uid {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "you cannot disable your own account")
			return
		}
		status, resp, err := h.identityCall(r.Context(), http.MethodPatch, "/internal/managed-users/"+targetID,
			map[string]any{"org_id": orgID, "name": req.Name, "disabled": req.Disabled, "actor_id": uid})
		if err != nil || status != http.StatusOK {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", identityMsg(resp, "identity service unavailable"))
			return
		}
		if req.Disabled != nil {
			act := "user.enabled"
			if *req.Disabled {
				act = "user.disabled"
			}
			h.audit(r, orgID, uid, act, targetID, nil)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user_id": targetID})
}

// PutGrants replaces all project grants of a restricted user.
func (h *Handler) PutGrants(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, role, _, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	if membership.Role(role).AtLeast(membership.Admin) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "owners and administrators have full access; grants do not apply")
		return
	}
	var req struct {
		Grants []grantReq `json:"grants"`
	}
	if err := httpx.DecodeLimit(r, &req, 1<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	grants, err := h.resolveGrants(r.Context(), orgID, fullAccess, req.Grants)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.Store.ReplaceGrants(r.Context(), orgID, targetID, uid, grants); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save grants")
		return
	}
	h.audit(r, orgID, uid, "user.grants_replaced", targetID, map[string]any{"grants": grantSummary(grants)})
	httpx.JSON(w, http.StatusOK, map[string]any{"user_id": targetID, "grants": nonNilGrants(grants)})
}

// ResetPassword (managed accounts) sets a new password, returned once, and forces a change.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, _, managed, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	if !managed {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "only admin-created accounts can be reset here; other users use their own password reset")
		return
	}
	var req struct {
		Password           string `json:"password"`
		MustChangePassword *bool  `json:"must_change_password"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
			return
		}
	}
	status, resp, err := h.identityCall(r.Context(), http.MethodPost, "/internal/managed-users/"+targetID+"/reset-password",
		map[string]any{"org_id": orgID, "password": req.Password, "must_change_password": req.MustChangePassword, "actor_id": uid})
	if err != nil || status != http.StatusOK {
		code := http.StatusBadGateway
		if status == http.StatusBadRequest {
			code = status
		}
		httpx.Error(w, code, "bad_gateway", identityMsg(resp, "identity service unavailable"))
		return
	}
	h.audit(r, orgID, uid, "user.password_reset", targetID, nil)
	out := map[string]any{"user_id": targetID, "must_change_password": resp["must_change_password"]}
	if pw, ok := resp["generated_password"].(string); ok {
		out["generated_password"] = pw
	}
	httpx.JSON(w, http.StatusOK, out)
}

// DeleteUser removes a member; admin-created accounts are deleted and all their sessions revoked.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, _, managed, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	if targetID == uid {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "you cannot delete your own account here")
		return
	}
	if status, code, msg := h.removeMember(r.Context(), orgID, targetID, managed, uid); status != 0 {
		httpx.Error(w, status, code, msg)
		return
	}
	h.audit(r, orgID, uid, "user.deleted", targetID, map[string]any{"managed": managed})
	w.WriteHeader(http.StatusNoContent)
}

// removeMember removes the membership (last-owner protected) and, for managed accounts, the account.
func (h *Handler) removeMember(ctx context.Context, orgID, targetID string, managed bool, actor string) (int, string, string) {
	switch err := h.Store.RemoveMember(ctx, orgID, targetID); {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not_found", "user not found"
	case errors.Is(err, store.ErrLastOwner):
		return http.StatusConflict, "last_owner", "cannot remove the last owner"
	case err != nil:
		return http.StatusInternalServerError, "internal", "failed to remove user"
	}
	if managed {
		status, _, err := h.identityCall(ctx, http.MethodDelete, fmt.Sprintf("/internal/managed-users/%s?org_id=%s&actor_id=%s", targetID, orgID, actor), nil)
		if err != nil || (status != http.StatusNoContent && status != http.StatusNotFound) {
			return http.StatusBadGateway, "bad_gateway", "membership removed but the account could not be deleted; retry or disable it"
		}
	}
	return 0, "", ""
}

// AccessPresets lists areas, levels and presets for the admin UI.
func (h *Handler) AccessPresets(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := h.requireRole(w, r, membership.Admin); !ok {
		return
	}
	type preset struct {
		Name          string                               `json:"name"`
		Areas         map[membership.Area]membership.Level `json:"areas"`
		SecretsReveal bool                                 `json:"secrets_reveal"`
	}
	presets := []preset{}
	for _, n := range membership.PresetNames {
		if a, ok := membership.PresetAreas(n); ok {
			presets = append(presets, preset{Name: n, Areas: a, SecretsReveal: membership.PresetSecretsReveal(n)})
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"areas": membership.AllAreas, "levels": []membership.Level{membership.LevelNone, membership.LevelView, membership.LevelManage},
		"presets": presets, "custom": membership.PresetCustom,
	})
}

func (h *Handler) UserSessions(w http.ResponseWriter, r *http.Request) {
	orgID, _, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, _, managed, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	if !managed {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "sessions are only managed for admin-created accounts")
		return
	}
	status, resp, err := h.identityCall(r.Context(), http.MethodGet, fmt.Sprintf("/internal/managed-users/%s/sessions?org_id=%s", targetID, orgID), nil)
	if err != nil || status != http.StatusOK {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "identity service unavailable")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) RevokeUserSession(w http.ResponseWriter, r *http.Request) {
	orgID, uid, callerRole, ok := h.requireRole(w, r, membership.Admin)
	if !ok {
		return
	}
	targetID, _, managed, ok := h.target(w, r, orgID, callerRole)
	if !ok {
		return
	}
	if !managed {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "sessions are only managed for admin-created accounts")
		return
	}
	sid := r.PathValue("sessionId")
	status, _, err := h.identityCall(r.Context(), http.MethodDelete, fmt.Sprintf("/internal/managed-users/%s/sessions/%s?org_id=%s", targetID, url.PathEscape(sid), orgID), nil)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "identity service unavailable")
		return
	}
	if status == http.StatusNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if status != http.StatusNoContent {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "session could not be revoked")
		return
	}
	h.audit(r, orgID, uid, "user.session_revoked", targetID, map[string]any{"session_id": sid})
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- internal

// InternalAccess is served to every service: the user's effective access on one project.
func (h *Handler) InternalAccess(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, userID := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("userId")
	role, err := h.Store.MemberRole(r.Context(), orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "not a member")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	if membership.Role(role).AtLeast(membership.Admin) {
		httpx.JSON(w, http.StatusOK, membership.Access{Role: membership.Role(role), Full: true})
		return
	}
	g, err := h.Store.GetGrant(r.Context(), orgID, projectID, userID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.JSON(w, http.StatusOK, membership.Access{Role: membership.Role(role), Areas: map[membership.Area]membership.Level{}})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, g.Access(role))
}

// InternalUserProjects lists which projects a user may see (full = all).
func (h *Handler) InternalUserProjects(w http.ResponseWriter, r *http.Request) {
	orgID, userID := r.PathValue("orgId"), r.PathValue("userId")
	role, err := h.Store.MemberRole(r.Context(), orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "not a member")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	if membership.Role(role).AtLeast(membership.Admin) {
		httpx.JSON(w, http.StatusOK, map[string]any{"full": true, "project_ids": []string{}})
		return
	}
	grants, err := h.Store.ListGrantsForUser(r.Context(), orgID, userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	ids := []string{}
	for _, g := range grants {
		if (&membership.Access{Areas: g.Areas}).Any() {
			ids = append(ids, g.ProjectID)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"full": false, "project_ids": ids})
}

// InternalMemberships feeds GET /auth/me: every org of the user with role, project grants and names.
func (h *Handler) InternalMemberships(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	orgs, err := h.Store.ListOrgsForUser(r.Context(), userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	type projOut struct {
		ProjectID     string                               `json:"project_id"`
		Name          string                               `json:"name"`
		Areas         map[membership.Area]membership.Level `json:"areas"`
		SecretsReveal bool                                 `json:"secrets_reveal"`
	}
	out := make([]map[string]any, 0, len(orgs))
	for _, o := range orgs {
		full := membership.Role(o.Role).AtLeast(membership.Admin)
		projects := []projOut{}
		if h.ProjectURL != "" {
			list, err := h.listProjects(r.Context(), o.ID)
			if err != nil {
				h.log().Warn("project list for memberships failed", "org", o.ID, "err", err)
			}
			grants := map[string]store.Grant{}
			if !full {
				gs, _ := h.Store.ListGrantsForUser(r.Context(), o.ID, userID)
				for _, g := range gs {
					grants[g.ProjectID] = g
				}
			}
			for _, p := range list {
				if p.Status == "deleting" {
					continue
				}
				if full {
					all := map[membership.Area]membership.Level{}
					for _, a := range membership.AllAreas {
						all[a] = membership.LevelManage
					}
					projects = append(projects, projOut{ProjectID: p.ID, Name: p.Name, Areas: all, SecretsReveal: true})
				} else if g, ok := grants[p.ID]; ok && (&membership.Access{Areas: g.Areas}).Any() {
					projects = append(projects, projOut{ProjectID: p.ID, Name: p.Name, Areas: g.Areas, SecretsReveal: g.SecretsReveal})
				}
			}
		}
		out = append(out, map[string]any{"org_id": o.ID, "org_name": o.Name, "org_slug": o.Slug, "role": o.Role, "full": full, "projects": projects})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"memberships": out})
}

// InternalDeleteProject drops grants of a deleted project (project delete cascade).
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteGrantsForProject(r.Context(), r.PathValue("projectId")); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
