package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/addon/internal/catalog"
	"github.com/jp-cloud/addon/internal/driver"
	"github.com/jp-cloud/addon/internal/service"
	"github.com/jp-cloud/addon/internal/store"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
)

// Authz is implemented by *membership.Client.
type Authz interface {
	Require(w http.ResponseWriter, r *http.Request, orgID, userID string, min membership.Role) (membership.Role, bool)
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
	Access(r *http.Request, orgID, projectID, userID string) (*membership.Access, error)
}

type Handler struct {
	Svc   *service.Service
	JWT   *jwtutil.Manager
	Authz Authz
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	p := "/orgs/{orgId}/projects/{projectId}/addons"
	mux.Handle("GET /orgs/{orgId}/addons", auth(http.HandlerFunc(h.ListOrg)))
	mux.Handle("GET /orgs/{orgId}/addons/catalog", auth(http.HandlerFunc(h.Catalog)))
	mux.Handle("GET "+p+"/catalog", auth(http.HandlerFunc(h.Catalog)))
	mux.Handle("GET "+p, auth(http.HandlerFunc(h.List)))
	mux.Handle("POST "+p, auth(http.HandlerFunc(h.Create)))
	mux.Handle("POST "+p+"/custom", auth(http.HandlerFunc(h.CreateCustom)))
	mux.Handle("GET "+p+"/{addonId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("DELETE "+p+"/{addonId}", auth(http.HandlerFunc(h.Delete)))
	for _, act := range []string{"stop", "start", "restart"} {
		act := act
		mux.Handle("POST "+p+"/{addonId}/"+act, auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.Action(w, r, act) })))
	}
	mux.Handle("GET "+p+"/{addonId}/connection", auth(http.HandlerFunc(h.Connection)))
	mux.Handle("GET "+p+"/{addonId}/backups", auth(http.HandlerFunc(h.ListBackups)))
	mux.Handle("POST "+p+"/{addonId}/backups", auth(http.HandlerFunc(h.CreateBackup)))
	mux.Handle("POST "+p+"/{addonId}/backups/{backupId}/restore", auth(http.HandlerFunc(h.Restore)))

	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/addons/provision", in(h.InternalProvision))
	mux.Handle("GET /internal/addons/{addonId}", in(h.InternalGet))
	mux.Handle("GET /internal/addons/{addonId}/connection", in(h.InternalConnection))
	mux.Handle("DELETE /internal/addons/{addonId}", in(h.InternalDelete))
	mux.Handle("POST /internal/addons/{addonId}/sql", in(h.InternalSQL))
	mux.Handle("GET /internal/addons/{addonId}/backups", in(h.InternalListBackups))
	mux.Handle("POST /internal/addons/{addonId}/backups", in(h.InternalBackup))
	mux.Handle("POST /internal/addons/{addonId}/backups/{backupId}/restore", in(h.InternalRestore))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

func writeErr(w http.ResponseWriter, e *service.Error) {
	httpx.Error(w, e.Status, e.Code, e.Message)
}

// view is the API shape of an add-on. Add-ons are never exposed publicly: public_url is always null.
type view struct {
	store.Addon
	Live       service.Live        `json:"live"`
	PublicURL  *string             `json:"public_url"`
	Connection *service.Connection `json:"connection,omitempty"`
	Backup     bool                `json:"backup_supported"`
	Restore    bool                `json:"restore_supported"`
}

func (h *Handler) view(ctx context.Context, a store.Addon, withConn bool) view {
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	v := view{Addon: a, Live: h.Svc.LiveStatus(cctx, &a)}
	if k, ok := h.Svc.BackupSupported(&a); ok {
		v.Backup, v.Restore = true, k.Restore != nil
	}
	if withConn && a.Status != store.StatusFailed {
		if c, e := h.Svc.Connection(cctx, &a, false); e == nil {
			v.Connection = c
		}
	}
	return v
}

type catalogView struct {
	catalog.Item
	ReasonText string `json:"reason_text,omitempty"`
}

func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	uid, orgID := middleware.UserIDFrom(r.Context()), r.PathValue("orgId")
	if !validID(orgID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.Require(w, r, orgID, uid, membership.Viewer); !ok {
		return
	}
	all := catalog.All()
	out := make([]catalogView, 0, len(all))
	for _, it := range all {
		out = append(out, catalogView{Item: it, ReasonText: catalog.ReasonText[it.Reason]})
	}
	total, prov := catalog.Counts()
	httpx.JSON(w, http.StatusOK, map[string]any{"catalog": out, "total": total, "provisionable": prov})
}

func (h *Handler) listFor(w http.ResponseWriter, r *http.Request, projectID string) {
	uid, orgID := middleware.UserIDFrom(r.Context()), r.PathValue("orgId")
	if !validID(orgID) || (projectID != "" && !validID(projectID)) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var role membership.Role
	if projectID == "" {
		var ok bool
		if role, ok = h.Authz.Require(w, r, orgID, uid, membership.Viewer); !ok {
			return
		}
	} else if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaAddons, membership.LevelView); !ok {
		return
	}
	list, err := h.Svc.Store.List(r.Context(), orgID, projectID, strings.TrimSpace(r.URL.Query().Get("engine")), false)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	if projectID == "" && !role.AtLeast(membership.Admin) {
		// org-wide listing: only projects where the user has addons:view
		allowed := map[string]bool{}
		filtered := list[:0:0]
		for _, a := range list {
			ok, seen := allowed[a.ProjectID]
			if !seen {
				acc, err := h.Authz.Access(r, orgID, a.ProjectID, uid)
				if err != nil {
					httpx.Error(w, http.StatusBadGateway, "bad_gateway", "access check unavailable")
					return
				}
				ok = acc != nil && acc.Level(membership.AreaAddons).AtLeast(membership.LevelView)
				allowed[a.ProjectID] = ok
			}
			if ok {
				filtered = append(filtered, a)
			}
		}
		list = filtered
	}
	views := make([]view, len(list))
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i] = h.view(r.Context(), list[i], false) }(i)
	}
	wg.Wait()
	httpx.JSON(w, http.StatusOK, map[string]any{"addons": views})
}

func (h *Handler) ListOrg(w http.ResponseWriter, r *http.Request) { h.listFor(w, r, "") }
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	h.listFor(w, r, r.PathValue("projectId"))
}

type createReq struct {
	Engine     string   `json:"engine"`
	Name       string   `json:"name"`
	SecretName string   `json:"secret_name,omitempty"`
	Env        string   `json:"env,omitempty"`
	Envs       []string `json:"envs,omitempty"`
	// legacy fields sent by older dashboards: rejected when they try to bypass the catalog
	DockerImage string `json:"docker_image,omitempty"`
	Port        int    `json:"port,omitempty"`
	Description string `json:"description,omitempty"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, orgID, projectID := middleware.UserIDFrom(r.Context()), r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaAddons, membership.LevelManage); !ok {
		return
	}
	var req createReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid request body")
		return
	}
	if req.DockerImage != "" {
		httpx.Error(w, http.StatusBadRequest, "use_custom", "custom images are created with POST .../addons/custom; the catalog cannot be extended")
		return
	}
	envs := req.Envs
	if len(envs) == 0 && req.Env != "" {
		envs = []string{req.Env, "production"}
	}
	a, e := h.Svc.Provision(r.Context(), service.ProvisionReq{OrgID: orgID, ProjectID: projectID, Name: req.Name, Engine: req.Engine,
		SecretName: req.SecretName, Envs: envs, CreatedBy: uid})
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"addon": h.view(r.Context(), *a, true)})
}

func (h *Handler) CreateCustom(w http.ResponseWriter, r *http.Request) {
	uid, orgID, projectID := middleware.UserIDFrom(r.Context()), r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaAddons, membership.LevelManage); !ok {
		return
	}
	var req struct {
		Name       string            `json:"name"`
		Image      string            `json:"image"`
		Port       int               `json:"port"`
		Env        map[string]string `json:"env"`
		Volume     string            `json:"volume"`
		SecretName string            `json:"secret_name,omitempty"`
		Envs       []string          `json:"envs,omitempty"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid request body")
		return
	}
	a, e := h.Svc.Provision(r.Context(), service.ProvisionReq{OrgID: orgID, ProjectID: projectID, Name: req.Name, SecretName: req.SecretName,
		Envs: req.Envs, CreatedBy: uid, Custom: &service.CustomSpec{Image: req.Image, Port: req.Port, Env: req.Env, Volume: req.Volume}})
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"addon": h.view(r.Context(), *a, true)})
}

// load authenticates and fetches an add-on of the project (area addons).
func (h *Handler) load(w http.ResponseWriter, r *http.Request, need membership.Level) (*store.Addon, string, *membership.Access, bool) {
	uid, orgID, projectID, id := middleware.UserIDFrom(r.Context()), r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("addonId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return nil, "", nil, false
	}
	acc, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaAddons, need)
	if !ok {
		return nil, "", nil, false
	}
	a, err := h.Svc.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "addon not found")
		return nil, "", nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return nil, "", nil, false
	}
	return a, uid, acc, true
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	a, _, _, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"addon": h.view(r.Context(), *a, true)})
}

func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	reveal := r.URL.Query().Get("reveal") == "true"
	a, _, acc, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	if reveal && !acc.CanRevealSecrets() {
		httpx.Error(w, http.StatusForbidden, "reveal_forbidden", "you are not allowed to reveal add-on passwords")
		return
	}
	c, e := h.Svc.Connection(r.Context(), a, reveal)
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"connection": c})
}

func (h *Handler) Action(w http.ResponseWriter, r *http.Request, action string) {
	a, _, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	if e := h.Svc.Action(r.Context(), a, action); e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"addon": h.view(r.Context(), *a, false)})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	a, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	purge := r.URL.Query().Get("purge") == "true"
	if e := h.Svc.Delete(r.Context(), a, purge, uid); e != nil {
		writeErr(w, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request) {
	a, _, _, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Svc.Store.ListBackups(r.Context(), a.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	_, supported := h.Svc.BackupSupported(a)
	httpx.JSON(w, http.StatusOK, map[string]any{"backups": list, "supported": supported})
}

func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	a, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	b, e := h.Svc.Backup(r.Context(), a, uid)
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"backup": b})
}

func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) {
	a, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	b, e := h.Svc.Restore(r.Context(), a, r.PathValue("backupId"), uid)
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backup": b, "restored": true, "verified": b.LastRestoreStatus == "verified"})
}

// ---------------------------------------------------------------- internal API

func (h *Handler) internalAddon(w http.ResponseWriter, r *http.Request) (*store.Addon, bool) {
	id := r.PathValue("addonId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return nil, false
	}
	a, err := h.Svc.Store.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "addon not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return nil, false
	}
	return a, true
}

// InternalProvision provisions an add-on on behalf of another service (managed databases).
func (h *Handler) InternalProvision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID            string   `json:"org_id"`
		ProjectID        string   `json:"project_id"`
		Name             string   `json:"name"`
		Engine           string   `json:"engine"`
		SecretName       string   `json:"secret_name"`
		Envs             []string `json:"envs"`
		CreatedBy        string   `json:"created_by"`
		Kind             string   `json:"kind"`
		PGStatStatements bool     `json:"pg_stat_statements"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validID(req.OrgID) || !validID(req.ProjectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id (uuid), name, engine required")
		return
	}
	if req.Kind != "" && req.Kind != store.KindManagedDB {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "kind must be managed_db")
		return
	}
	a, e := h.Svc.Provision(r.Context(), service.ProvisionReq{OrgID: req.OrgID, ProjectID: req.ProjectID, Name: req.Name, Engine: req.Engine,
		SecretName: req.SecretName, Envs: req.Envs, CreatedBy: req.CreatedBy, Kind: req.Kind, Options: driver.Options{PGStatStatements: req.PGStatStatements}})
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"addon": a})
}

func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"addon": h.view(r.Context(), *a, false)})
}

func (h *Handler) InternalConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	c, e := h.Svc.Connection(r.Context(), a, r.URL.Query().Get("reveal") == "true")
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"connection": c})
}

func (h *Handler) InternalDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	if e := h.Svc.Delete(r.Context(), a, r.URL.Query().Get("purge") == "true", ""); e != nil {
		writeErr(w, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalSQL(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	var req struct {
		SQL string `json:"sql"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.SQL) == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "sql required")
		return
	}
	out, e := h.Svc.SQL(r.Context(), a, req.SQL)
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"output": out})
}

func (h *Handler) InternalListBackups(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	list, err := h.Svc.Store.ListBackups(r.Context(), a.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backups": list})
}

func (h *Handler) InternalBackup(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	b, e := h.Svc.Backup(r.Context(), a, r.URL.Query().Get("actor"))
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"backup": b})
}

func (h *Handler) InternalRestore(w http.ResponseWriter, r *http.Request) {
	a, ok := h.internalAddon(w, r)
	if !ok {
		return
	}
	b, e := h.Svc.Restore(r.Context(), a, r.PathValue("backupId"), r.URL.Query().Get("actor"))
	if e != nil {
		writeErr(w, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backup": b, "restored": true, "verified": b.LastRestoreStatus == "verified"})
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	removed, failures := h.Svc.DeleteProject(r.Context(), id)
	if len(failures) > 0 {
		httpx.Error(w, http.StatusBadGateway, "partial_failure", strings.Join(failures, "; "))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed, "project_id": id})
}
