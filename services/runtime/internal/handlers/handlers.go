package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/manager"
	"github.com/jp-cloud/runtime/internal/store"
)

// Store is the persistence the handlers use (implemented by *store.Store).
type Store interface {
	manager.Repo
	List(ctx context.Context, orgID, projectID string) ([]store.Instance, error)
	Get(ctx context.Context, orgID, projectID, id string) (*store.Instance, error)
	ListJobs(ctx context.Context, orgID, projectID string, limit int) ([]store.JobRun, error)
	GetJob(ctx context.Context, orgID, projectID, id string) (*store.JobRun, error)
}

// NodeStore is the node and placement persistence (implemented by *store.Store); nil disables multi-server.
type NodeStore interface {
	manager.NodeRepo
	UpsertNode(ctx context.Context, n *store.Node) (*store.Node, error)
	GetNodeByName(ctx context.Context, name string) (*store.Node, error)
	ListNodes(ctx context.Context) ([]store.Node, error)
	DeleteNode(ctx context.Context, id string) error
	CountInstancesOnNode(ctx context.Context, nodeID string) (int, error)
	CountPlacementsOnNode(ctx context.Context, nodeID string) (int, error)
	SetPlacement(ctx context.Context, projectID, nodeID string) error
	ListPlacements(ctx context.Context) (map[string]string, error)
}

// Authorizer enforces org membership + project binding (implemented by *membership.Client).
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

// Handler serves the runtime HTTP API.
type Handler struct {
	Store   Store
	JWT     *jwtutil.Manager
	Authz   Authorizer
	Manager *manager.Manager
	Nodes   NodeStore
	NodeKey []byte
}

func (h *Handler) engine() *dockerx.Engine { return h.Manager.Engine }

// Routes registers all routes. Every /internal/* route is wrapped in internalauth.Require.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	user := func(f http.HandlerFunc) http.Handler { return auth(f) }
	internal := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }

	const p = "/orgs/{orgId}/projects/{projectId}/runtime"
	mux.Handle("GET "+p+"/instances", user(h.List))
	mux.Handle("POST "+p+"/instances", user(h.Create))
	mux.Handle("GET "+p+"/instances/{instanceId}", user(h.Get))
	mux.Handle("DELETE "+p+"/instances/{instanceId}", user(h.Delete))
	mux.Handle("GET "+p+"/instances/{instanceId}/logs", user(h.InstanceLogs))
	mux.Handle("POST "+p+"/instances/{instanceId}/start", user(h.Start))
	mux.Handle("POST "+p+"/instances/{instanceId}/stop", user(h.Stop))
	mux.Handle("POST "+p+"/instances/{instanceId}/restart", user(h.Restart))
	mux.Handle("GET "+p+"/containers", user(h.ListContainers))
	mux.Handle("GET "+p+"/logs", user(h.ProjectLogs))
	mux.Handle("GET "+p+"/jobs", user(h.ListJobs))
	mux.Handle("GET "+p+"/jobs/{jobId}", user(h.GetJob))
	mux.Handle("POST "+p+"/jobs", user(h.RunJobUser))

	mux.Handle("POST /internal/runtime/start", internal(h.InternalStart))
	mux.Handle("POST /internal/runtime/stop-by-deployment", internal(h.InternalStopByDeployment))
	mux.Handle("GET /internal/runtime/instances/{instanceId}", internal(h.InternalGet))
	mux.Handle("POST /internal/runtime/instances/{instanceId}/probe", internal(h.InternalProbe))
	mux.Handle("POST /internal/runtime/instances/{instanceId}/restart", internal(h.InternalRestart))
	mux.Handle("GET /internal/runtime/desired", internal(h.InternalDesired))
	mux.Handle("POST /internal/runtime/jobs", internal(h.InternalJob))
	mux.Handle("POST /internal/runtime/addon", internal(h.InternalAddonStart))
	mux.Handle("DELETE /internal/runtime/addon/{containerId}", internal(h.InternalAddonDelete))
	mux.Handle("GET /internal/runtime/addon/{containerId}", internal(h.InternalAddonStatus))
	mux.Handle("POST /internal/runtime/addon/{containerId}/{action}", internal(h.InternalAddonAction))
	mux.Handle("GET /internal/runtime/stats", internal(h.InternalStats))
	mux.Handle("POST /internal/runtime/prune", internal(h.InternalPrune))
	mux.Handle("POST /internal/runtime/reconcile", internal(h.InternalReconcile))
	mux.Handle("DELETE /internal/projects/{projectId}", internal(h.InternalDeleteProject))

	// multi-server: nodes and where each project runs
	mux.Handle("GET /internal/runtime/nodes", internal(h.InternalListNodes))
	mux.Handle("PUT /internal/runtime/nodes/{name}", internal(h.InternalPutNode))
	mux.Handle("DELETE /internal/runtime/nodes/{name}", internal(h.InternalDeleteNode))
	mux.Handle("GET /internal/runtime/placements/{projectId}", internal(h.InternalGetPlacement))
	mux.Handle("PUT /internal/runtime/placements/{projectId}", internal(h.InternalSetPlacement))
}

// ---------------------------------------------------------------- helpers

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID, uid string, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	if !dockerx.ValidUUID(orgID) || !dockerx.ValidUUID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid organization or project id")
		return
	}
	if min != membership.LevelView {
		if c, has := middleware.ClaimsFrom(r.Context()); has && c.ReadOnly() {
			httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
			return
		}
	}
	uid = middleware.UserIDFrom(r.Context())
	if _, ok = h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaRuntime, min); !ok {
		return
	}
	return orgID, projectID, uid, true
}

// instance loads an instance scoped to the authorised org+project.
func (h *Handler) instance(w http.ResponseWriter, r *http.Request, orgID, projectID string) (*store.Instance, bool) {
	id := r.PathValue("instanceId")
	if !dockerx.ValidUUID(id) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return nil, false
	}
	in, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load instance")
		return nil, false
	}
	return in, true
}

// writeErr maps manager errors to the platform error shape.
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var br *manager.ErrBadRequest
	var de *manager.DeployError
	switch {
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, "bad_request", br.Msg)
	case errors.Is(err, manager.ErrConflict):
		httpx.Error(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, manager.ErrBusy):
		httpx.Error(w, http.StatusTooManyRequests, "busy", err.Error())
	case errors.Is(err, manager.ErrNodeUnavailable):
		httpx.Error(w, http.StatusServiceUnavailable, "node_unavailable", err.Error())
	case errors.Is(err, dockerx.ErrUnavailable):
		httpx.Error(w, http.StatusServiceUnavailable, "docker_unavailable", "container engine unavailable")
	case errors.As(err, &de):
		httpx.JSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":     httpx.ErrorDetail{Code: "deploy_failed", Message: de.Error()},
			"stage":     de.Stage,
			"instances": de.Instances,
			"mode":      h.engine().Mode(),
		})
	default:
		httpx.Error(w, http.StatusBadGateway, "runtime_error", err.Error())
	}
}

func (h *Handler) mode() string { return h.engine().Mode() }

func tailParam(r *http.Request) int {
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			tail = n
		}
	}
	if tail > dockerx.MaxTail {
		tail = dockerx.MaxTail
	}
	return tail
}

// ---------------------------------------------------------------- user API

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list instances")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instances": list, "mode": h.mode()})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in, "mode": h.mode()})
}

// Create runs a manual deployment of an image (rolling) for the project.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		ImageRef      string `json:"image_ref"`
		DeploymentID  string `json:"deployment_id"`
		RestartPolicy string `json:"restart_policy"`
		Port          int    `json:"port"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "image_ref required")
		return
	}
	if err := h.Manager.CheckUserImage(req.ImageRef); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.Manager.Deploy(r.Context(), manager.DeployRequest{
		OrgID: orgID, ProjectID: projectID, ImageRef: req.ImageRef, Strategy: "rolling",
		Spec: manager.Spec{Port: req.Port, RestartPolicy: req.RestartPolicy},
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"instance": res.Instances[0], "instances": res.Instances, "service": res.Service, "mode": h.mode()})
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	if err := h.Manager.Start(r.Context(), in); err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in, "mode": h.mode()})
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	if err := h.Manager.Stop(r.Context(), in); err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in, "mode": h.mode()})
}

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	if err := h.Manager.Restart(r.Context(), in); err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in, "mode": h.mode()})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	if err := h.Manager.Delete(r.Context(), in); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListContainers returns ONLY the containers labelled with this org and project.
func (h *Handler) ListContainers(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Manager.ProjectContainers(r.Context(), orgID, projectID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	instances, _ := h.Store.List(r.Context(), orgID, projectID)
	httpx.JSON(w, http.StatusOK, map[string]any{"mode": h.mode(), "containers": list, "instances": instances})
}

func (h *Handler) logsFor(w http.ResponseWriter, r *http.Request, in *store.Instance) {
	target := in.ContainerID
	if target == "" {
		target = in.ContainerName
	}
	opts := dockerx.LogOptions{Tail: tailParam(r)}
	eng, eerr := h.Manager.NodeEngine(r.Context(), in.NodeID)
	if eerr != nil {
		h.writeErr(w, fmt.Errorf("%w: %v", dockerx.ErrUnavailable, eerr))
		return
	}
	if r.URL.Query().Get("follow") == "true" || r.URL.Query().Get("follow") == "1" {
		h.followLogs(w, r, eng, target, opts)
		return
	}
	entries, err := eng.Logs(r.Context(), target, opts)
	if errors.Is(err, dockerx.ErrNotFound) {
		entries, err = []dockerx.LogEvent{}, nil
	}
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"instance": in, "container_id": in.ContainerID, "container_name": in.ContainerName,
		"status": in.Status, "entries": entries, "mode": h.mode(),
	})
}

// followLogs streams NDJSON log events, bounded in time and bytes.
func (h *Handler) followLogs(w http.ResponseWriter, r *http.Request, eng *dockerx.Engine, target string, opts dockerx.LogOptions) {
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	enc := json.NewEncoder(w)
	_ = eng.FollowLogs(ctx, target, opts, 16<<20, func(ev dockerx.LogEvent) bool {
		if err := enc.Encode(ev); err != nil {
			return false
		}
		if fl != nil {
			fl.Flush()
		}
		return true
	})
}

func (h *Handler) InstanceLogs(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	in, ok := h.instance(w, r, orgID, projectID)
	if !ok {
		return
	}
	h.logsFor(w, r, in)
}

// ProjectLogs returns logs of the active production instance (or the newest instance).
func (h *Handler) ProjectLogs(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	act, err := h.Store.ListActiveProduction(r.Context(), orgID, projectID)
	var in *store.Instance
	if err == nil && len(act) > 0 {
		in = &act[0]
	} else if list, lerr := h.Store.List(r.Context(), orgID, projectID); lerr == nil && len(list) > 0 {
		in = &list[0]
	}
	if in == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"instance": nil, "container_id": "", "container_name": "", "status": "stopped",
			"entries": []dockerx.LogEvent{}, "mode": h.mode()})
		return
	}
	h.logsFor(w, r, in)
}

func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Store.ListJobs(r.Context(), orgID, projectID, 50)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list jobs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"jobs": list, "mode": h.mode()})
}

func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	id := r.PathValue("jobId")
	if !dockerx.ValidUUID(id) {
		httpx.Error(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	j, err := h.Store.GetJob(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"job": j})
}

// RunJobUser lets a member run a one-off command (always in a separate container).
func (h *Handler) RunJobUser(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req manager.JobRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		return
	}
	req.OrgID, req.ProjectID, req.CronID = orgID, projectID, ""
	if req.ImageRef != "" {
		if err := h.Manager.CheckUserImage(req.ImageRef); err != nil {
			h.writeErr(w, err)
			return
		}
	}
	run, err := h.Manager.RunJob(r.Context(), req)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"job": run, "mode": h.mode()})
}

// ---------------------------------------------------------------- internal API

func (h *Handler) InternalStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID        string       `json:"org_id"`
		ProjectID    string       `json:"project_id"`
		DeploymentID string       `json:"deployment_id"`
		ImageRef     string       `json:"image_ref"`
		Strategy     string       `json:"strategy"`
		Preview      bool         `json:"preview"`
		NoSecrets    bool         `json:"no_secrets"`
		Spec         manager.Spec `json:"spec"`
	}
	if err := httpx.DecodeLimit(r, &req, 1<<20); err != nil {
		badPayload(w, err)
		return
	}
	res, err := h.Manager.Deploy(r.Context(), manager.DeployRequest{
		OrgID: req.OrgID, ProjectID: req.ProjectID, DeploymentID: req.DeploymentID, ImageRef: req.ImageRef,
		Strategy: req.Strategy, Preview: req.Preview, NoSecrets: req.NoSecrets, Spec: req.Spec,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"instance": res.Instances[0], "instances": res.Instances, "service": res.Service, "color": res.Color,
		"port": res.Port, "replaced": res.Replaced, "idempotent": res.Idempotent, "mode": h.mode(), "preview": req.Preview,
	})
}

func (h *Handler) InternalStopByDeployment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.DeploymentID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "deployment_id required")
		return
	}
	n, err := h.Manager.StopByDeployment(r.Context(), req.DeploymentID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"stopped": n, "deployment_id": req.DeploymentID})
}

func (h *Handler) internalInstance(w http.ResponseWriter, r *http.Request) (*store.Instance, bool) {
	id := r.PathValue("instanceId")
	if !dockerx.ValidUUID(id) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return nil, false
	}
	in, err := h.Store.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return nil, false
	}
	return in, true
}

func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	in, ok := h.internalInstance(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

// InternalProbe inspects docker + probes HTTP and returns/persists the truth.
func (h *Handler) InternalProbe(w http.ResponseWriter, r *http.Request) {
	in, ok := h.internalInstance(w, r)
	if !ok {
		return
	}
	res, err := h.Manager.Probe(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (h *Handler) InternalRestart(w http.ResponseWriter, r *http.Request) {
	in, ok := h.internalInstance(w, r)
	if !ok {
		return
	}
	if err := h.Manager.Restart(r.Context(), in); err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

func (h *Handler) InternalDesired(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListDesiredRunning(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instances": list, "mode": h.mode()})
}

func (h *Handler) InternalJob(w http.ResponseWriter, r *http.Request) {
	var req manager.JobRequest
	if err := httpx.Decode(r, &req); err != nil {
		badPayload(w, err)
		return
	}
	run, err := h.Manager.RunJob(r.Context(), req)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"job": run, "mode": h.mode()})
}

func (h *Handler) InternalAddonStart(w http.ResponseWriter, r *http.Request) {
	var req manager.AddonRequest
	if err := httpx.Decode(r, &req); err != nil {
		badPayload(w, err)
		return
	}
	res, err := h.Manager.StartAddon(r.Context(), req)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, res)
}

func (h *Handler) InternalAddonDelete(w http.ResponseWriter, r *http.Request) {
	purge := r.URL.Query().Get("purge") == "true"
	if err := h.Manager.RemoveAddon(r.Context(), r.PathValue("containerId"), purge); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalStats(w http.ResponseWriter, r *http.Request) {
	s, err := h.Manager.Stats(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

func (h *Handler) InternalPrune(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Containers *bool   `json:"containers"`
		Images     *bool   `json:"images"`
		Networks   *bool   `json:"networks"`
		UntilHours float64 `json:"until_hours"`
	}
	_ = httpx.Decode(r, &req)
	on := func(b *bool) bool { return b == nil || *b }
	res, err := h.engine().Prune(r.Context(), on(req.Containers), on(req.Images), on(req.Networks), time.Duration(req.UntilHours*float64(time.Hour)))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (h *Handler) InternalReconcile(w http.ResponseWriter, r *http.Request) {
	rep, err := h.Manager.Reconcile(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rep)
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	rep, err := h.Manager.DeleteProject(r.Context(), r.PathValue("projectId"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	status := http.StatusOK
	if len(rep.Errors) > 0 {
		status = http.StatusMultiStatus
	}
	httpx.JSON(w, status, rep)
}

// badPayload names the offending field/problem instead of a bare "invalid payload".
func badPayload(w http.ResponseWriter, err error) {
	msg := "invalid payload"
	if err != nil {
		msg += ": " + truncateMsg(err.Error(), 200)
	}
	httpx.Error(w, http.StatusBadRequest, "bad_request", msg)
}

func truncateMsg(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (h *Handler) InternalAddonStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.Manager.AddonStatus(r.Context(), r.PathValue("containerId"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "add-on container not found")
		return
	}
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// InternalAddonAction handles POST .../{id}/start|stop|restart and POST .../{id}/exec.
func (h *Handler) InternalAddonAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("containerId"), r.PathValue("action")
	if action == "exec" {
		h.addonExec(w, r, id)
		return
	}
	err := h.Manager.AddonAction(r.Context(), id, action)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "add-on container not found")
		return
	}
	if err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addonExec streams stdin -> process -> stdout. Headers: X-Exec-Cmd (base64 JSON array),
// X-Exec-Env (base64 JSON object, optional), X-Exec-Timeout (seconds). The exit code and the
// stderr tail (base64) arrive as HTTP trailers.
func (h *Handler) addonExec(w http.ResponseWriter, r *http.Request, id string) {
	var cmd []string
	raw, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Exec-Cmd"))
	if err != nil || json.Unmarshal(raw, &cmd) != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "X-Exec-Cmd must be base64 of a JSON string array")
		return
	}
	var env map[string]string
	if v := r.Header.Get("X-Exec-Env"); v != "" {
		eb, err := base64.StdEncoding.DecodeString(v)
		if err != nil || json.Unmarshal(eb, &env) != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "X-Exec-Env must be base64 of a JSON object")
			return
		}
	}
	timeout := 10 * time.Minute
	if n, err := strconv.Atoi(r.Header.Get("X-Exec-Timeout")); err == nil && n > 0 {
		timeout = time.Duration(n) * time.Second
	}
	if timeout > time.Hour {
		timeout = time.Hour
	}
	if _, err := h.Manager.AddonStatus(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "add-on container not found")
			return
		}
		h.writeErr(w, err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.EnableFullDuplex()
	w.Header().Set("Trailer", "X-Exec-Exit-Code, X-Exec-Stderr")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	fw := flushWriter{w: w, rc: rc}
	code, stderr, err := h.Manager.AddonExec(ctx, id, cmd, env, r.Body, fw)
	if err != nil {
		code = -1
		stderr = truncateMsg(err.Error(), 500)
	}
	w.Header().Set("X-Exec-Exit-Code", strconv.Itoa(code))
	w.Header().Set("X-Exec-Stderr", base64.StdEncoding.EncodeToString([]byte(stderr)))
}

type flushWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	_ = f.rc.Flush()
	return n, err
}
