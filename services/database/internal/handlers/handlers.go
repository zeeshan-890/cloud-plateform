package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/database/internal/addonclient"
	"github.com/jp-cloud/database/internal/diagnostics"
	"github.com/jp-cloud/database/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

// Authz is implemented by *membership.Client.
type Authz interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store  store.Store
	Addons addonclient.Client
	JWT    *jwtutil.Manager
	Authz  Authz
	Redis  *redis.Client
	Log    *slog.Logger
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,24}[a-z0-9]$`)

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	p := "/orgs/{orgId}/projects/{projectId}/databases"
	mux.Handle("GET "+p, auth(http.HandlerFunc(h.List)))
	mux.Handle("POST "+p, auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET "+p+"/{dbId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("DELETE "+p+"/{dbId}", auth(http.HandlerFunc(h.Delete)))
	mux.Handle("GET "+p+"/{dbId}/connection", auth(http.HandlerFunc(h.Connection)))
	mux.Handle("GET "+p+"/{dbId}/slow-queries", auth(http.HandlerFunc(h.SlowQueries)))
	mux.Handle("GET "+p+"/{dbId}/diagnostics/slow-queries", auth(http.HandlerFunc(h.SlowQueries)))
	mux.Handle("GET "+p+"/{dbId}/backups", auth(http.HandlerFunc(h.ListBackups)))
	mux.Handle("POST "+p+"/{dbId}/backups", auth(http.HandlerFunc(h.CreateBackup)))
	mux.Handle("POST "+p+"/{dbId}/backups/{backupId}/restore", auth(http.HandlerFunc(h.Restore)))
	mux.Handle("DELETE /internal/projects/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteProject)))
}

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, need membership.Level) (orgID, projectID, uid string, acc *membership.Access, ok bool) {
	uid, orgID, projectID = middleware.UserIDFrom(r.Context()), r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	a, good := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDatabases, need)
	if !good {
		return
	}
	return orgID, projectID, uid, a, true
}

// writeAddonErr maps add-on service failures to API errors (real message preserved).
func writeAddonErr(w http.ResponseWriter, err error) {
	var ae *addonclient.Error
	if errors.As(err, &ae) {
		status := ae.Status
		if status < 400 {
			status = http.StatusBadGateway
		}
		httpx.Error(w, status, ae.Code, ae.Message)
		return
	}
	httpx.Error(w, http.StatusBadGateway, "bad_gateway", err.Error())
}

type view struct {
	store.Database
	Live *addonclient.Live `json:"live,omitempty"`
	// Databases are private to the project network: there is no public endpoint.
	PublicURL  *string         `json:"public_url"`
	Connection json.RawMessage `json:"connection,omitempty"`
	Endpoint   string          `json:"endpoint,omitempty"`
}

func (h *Handler) enrich(ctx context.Context, d store.Database, withConn bool) view {
	v := view{Database: d}
	if d.AddonID == nil || d.Status == store.StatusFailed {
		return v
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if a, err := h.Addons.Get(cctx, *d.AddonID); err == nil {
		v.Live, v.Endpoint = a.Live, a.Endpoint
	}
	if withConn {
		if c, err := h.Addons.Connection(cctx, *d.AddonID, false); err == nil {
			v.Connection = c
		}
	}
	return v
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]view, 0, len(list))
	for _, d := range list {
		out = append(out, h.enrich(r.Context(), d, false))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"databases": out})
}

// Create provisions a dedicated Postgres container. Failure is failure: nothing is simulated.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		Name       string   `json:"name"`
		Env        string   `json:"env,omitempty"`
		Envs       []string `json:"envs,omitempty"`
		SecretName string   `json:"secret_name,omitempty"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid request body")
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !nameRe.MatchString(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name must be 3-26 chars: lowercase letters, digits and '-', starting with a letter")
		return
	}
	envs := req.Envs
	if len(envs) == 0 && req.Env != "" {
		envs = []string{req.Env, "production"}
	}
	d := &store.Database{OrgID: orgID, ProjectID: projectID, Name: name, Status: store.StatusProvisioning, CreatedBy: &uid}
	if err := h.Store.Create(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "a database with this name already exists in the project")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Minute)
	defer cancel()
	rollback := func(addonID string) {
		rctx, c := context.WithTimeout(context.Background(), 2*time.Minute)
		defer c()
		if addonID != "" {
			if err := h.Addons.Delete(rctx, addonID, true); err != nil {
				h.log().Error("rollback addon", "addon", addonID, "err", err)
			}
		}
		_ = h.Store.Delete(rctx, d.ID)
	}
	a, err := h.Addons.Provision(work, addonclient.ProvisionReq{OrgID: orgID, ProjectID: projectID, Name: "db-" + name, Engine: "postgres",
		SecretName: req.SecretName, Envs: envs, CreatedBy: uid, Kind: "managed_db", PGStatStatements: true})
	if err != nil {
		rollback("")
		writeAddonErr(w, err)
		return
	}
	// pg_stat_statements must really be usable for diagnostics: failure here fails the whole create.
	if _, err := h.Addons.SQL(work, a.ID, diagnostics.EnableExtensionSQL); err != nil {
		rollback(a.ID)
		writeAddonErr(w, err)
		return
	}
	d.AddonID = &a.ID
	d.Status, d.SecretRef, d.ConnectionHint, d.Error = store.StatusReady, a.SecretRef, a.Hint, ""
	if err := h.Store.Update(work, d); err != nil {
		rollback(a.ID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not record database")
		return
	}
	h.publish(work, events.TypeDatabaseCreated, uid, orgID, map[string]any{"project_id": projectID, "database_id": d.ID, "name": d.Name, "addon_id": a.ID})
	httpx.JSON(w, http.StatusCreated, map[string]any{"database": h.enrich(work, *d, true)})
}

func (h *Handler) load(w http.ResponseWriter, r *http.Request, need membership.Level) (*store.Database, string, *membership.Access, bool) {
	orgID, projectID, uid, acc, ok := h.authorize(w, r, need)
	if !ok {
		return nil, "", nil, false
	}
	id := r.PathValue("dbId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return nil, "", nil, false
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "database not found")
		return nil, "", nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return nil, "", nil, false
	}
	return d, uid, acc, true
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	d, _, _, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"database": h.enrich(r.Context(), *d, true)})
}

func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	reveal := r.URL.Query().Get("reveal") == "true"
	d, _, acc, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	if reveal && !acc.CanRevealSecrets() {
		httpx.Error(w, http.StatusForbidden, "reveal_forbidden", "you are not allowed to reveal database passwords")
		return
	}
	if d.AddonID == nil {
		httpx.Error(w, http.StatusConflict, "invalid_state", "database has no container")
		return
	}
	c, err := h.Addons.Connection(r.Context(), *d.AddonID, reveal)
	if err != nil {
		writeAddonErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"connection": c})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	d, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	if d.AddonID != nil {
		if err := h.Addons.Delete(r.Context(), *d.AddonID, r.URL.Query().Get("purge") == "true"); err != nil {
			writeAddonErr(w, err)
			return
		}
	}
	if err := h.Store.Delete(r.Context(), d.ID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.publish(r.Context(), events.TypeDatabaseDeleted, uid, d.OrgID, map[string]any{"project_id": d.ProjectID, "database_id": d.ID, "name": d.Name})
	w.WriteHeader(http.StatusNoContent)
}

// SlowQueries reads pg_stat_statements from the container (member+: query shapes are redacted but still sensitive).
func (h *Handler) SlowQueries(w http.ResponseWriter, r *http.Request) {
	d, _, _, ok := h.load(w, r, membership.LevelView)
	if !ok {
		return
	}
	if d.AddonID == nil || d.Status != store.StatusReady {
		httpx.Error(w, http.StatusConflict, "invalid_state", "database is "+d.Status)
		return
	}
	out, err := h.Addons.SQL(r.Context(), *d.AddonID, diagnostics.SlowQueriesSQL)
	if err != nil {
		writeAddonErr(w, err)
		return
	}
	slow, err := diagnostics.ParseSlowQueries(out)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	stats := diagnostics.DatabaseStats{DatabaseID: d.ID, SlowQueries: slow, CapturedAt: time.Now().UTC()}
	if cout, err := h.Addons.SQL(r.Context(), *d.AddonID, diagnostics.ConnectionsSQL); err == nil {
		stats.Connections, stats.MaxConnections, _ = diagnostics.ParseConnections(cout)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"stats": stats, "slow_queries": slow, "source": "pg_stat_statements"})
}

func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request) {
	d, _, _, ok := h.load(w, r, membership.LevelView)
	if !ok || d.AddonID == nil {
		return
	}
	raw, err := h.Addons.ListBackups(r.Context(), *d.AddonID)
	if err != nil {
		writeAddonErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	d, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	if d.AddonID == nil || d.Status != store.StatusReady {
		httpx.Error(w, http.StatusConflict, "invalid_state", "database is "+d.Status)
		return
	}
	raw, err := h.Addons.Backup(r.Context(), *d.AddonID, uid)
	if err != nil {
		writeAddonErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(raw)
}

func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) {
	d, uid, _, ok := h.load(w, r, membership.LevelManage)
	if !ok {
		return
	}
	if d.AddonID == nil || d.Status != store.StatusReady {
		httpx.Error(w, http.StatusConflict, "invalid_state", "database is "+d.Status)
		return
	}
	bid := r.PathValue("backupId")
	if !validID(bid) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid backup id")
		return
	}
	raw, err := h.Addons.Restore(r.Context(), *d.AddonID, bid, uid)
	if err != nil {
		writeAddonErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// InternalDeleteProject is the project-delete cascade: containers are purged, rows removed.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if !validID(pid) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	list, err := h.Store.ListByProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	var failures []string
	removed := 0
	for _, d := range list {
		if d.AddonID != nil {
			if err := h.Addons.Delete(r.Context(), *d.AddonID, true); err != nil {
				failures = append(failures, "database "+d.Name+": "+err.Error())
				continue
			}
		}
		if err := h.Store.Delete(r.Context(), d.ID); err != nil {
			failures = append(failures, "database "+d.Name+": "+err.Error())
			continue
		}
		removed++
	}
	if len(failures) > 0 {
		httpx.Error(w, http.StatusBadGateway, "partial_failure", strings.Join(failures, "; "))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed, "project_id": pid})
}

func (h *Handler) publish(ctx context.Context, typ, actor, org string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDatabase, typ, actor, org, payload)
	if _, err := events.PublishJSON(ctx, h.Redis, events.TopicDatabase, env); err != nil {
		h.log().Warn("publish event", "type", typ, "err", err)
	}
}
