package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	Engine          *dockerx.Engine
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/runtime/instances", auth(http.HandlerFunc(h.List)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/runtime/instances", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/start", auth(http.HandlerFunc(h.Start)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/stop", auth(http.HandlerFunc(h.Stop)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/runtime/containers", auth(http.HandlerFunc(h.ListContainers)))

	mux.HandleFunc("POST /internal/runtime/start", h.InternalStart)
	mux.HandleFunc("POST /internal/runtime/instances/{instanceId}/health", h.InternalHealth)
	mux.HandleFunc("GET /internal/runtime/instances/{instanceId}", h.InternalGet)
	mux.HandleFunc("GET /internal/runtime/desired", h.InternalDesired)
}

func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request, orgID, userID string) bool {
	if h.OrganizationURL == "" {
		return true
	}
	url := fmt.Sprintf("%s/internal/orgs/%s/members/%s", strings.TrimRight(h.OrganizationURL, "/"), orgID, userID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "membership check failed")
		return false
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "organization service unavailable")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member")
		return false
	}
	return resp.StatusCode == http.StatusOK
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list instances")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"instances": list,
		"mode":      h.Engine.EffectiveMode(r.Context()),
	})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Kind          string `json:"kind"`
		ImageRef      string `json:"image_ref"`
		DeploymentID  string `json:"deployment_id"`
		RestartPolicy string `json:"restart_policy"`
		Port          int    `json:"port"`
		Start         *bool  `json:"start"`
	}
	_ = httpx.Decode(r, &req)
	if req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "image_ref required")
		return
	}
	if req.Kind == "" {
		req.Kind = inferKind(req.ImageRef)
	}
	in := &store.Instance{
		OrgID: orgID, ProjectID: projectID, Kind: req.Kind, ImageRef: req.ImageRef,
		Status: "desired", DesiredState: "running", RestartPolicy: req.RestartPolicy, Port: req.Port,
		Mode: h.Engine.EffectiveMode(r.Context()),
	}
	if req.DeploymentID != "" {
		in.DeploymentID = &req.DeploymentID
	}
	if err := h.Store.Create(r.Context(), in); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create instance")
		return
	}
	start := true
	if req.Start != nil {
		start = *req.Start
	}
	if start {
		if err := h.startInstance(r, in); err != nil {
			in.Status = "failed"
			in.Error = err.Error()
			_ = h.Store.Update(r.Context(), in)
		}
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"instance": in})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("instanceId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	in, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("instanceId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	in, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	in.DesiredState = "running"
	if err := h.startInstance(r, in); err != nil {
		httpx.Error(w, http.StatusBadGateway, "runtime_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("instanceId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	in, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	in.DesiredState = "stopped"
	mode, err := h.Engine.Stop(r.Context(), in.ContainerID)
	in.Mode = mode
	if err != nil {
		in.Error = err.Error()
		in.Status = "failed"
		_ = h.Store.Update(r.Context(), in)
		httpx.Error(w, http.StatusBadGateway, "runtime_error", err.Error())
		return
	}
	in.Status = "stopped"
	in.Error = ""
	in.HealthStatus = "stopped"
	_ = h.Store.Update(r.Context(), in)
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

func (h *Handler) ListContainers(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, mode, err := h.Engine.List(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "runtime_error", err.Error())
		return
	}
	instances, _ := h.Store.List(r.Context(), orgID, projectID)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"mode":       mode,
		"containers": list,
		"instances":  instances,
	})
}

func (h *Handler) InternalStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID         string `json:"org_id"`
		ProjectID     string `json:"project_id"`
		DeploymentID  string `json:"deployment_id"`
		ImageRef      string `json:"image_ref"`
		Kind          string `json:"kind"`
		Slot          string `json:"slot"`
		RestartPolicy string `json:"restart_policy"`
		Port          int    `json:"port"`
		Rolling       bool   `json:"rolling"`
		Strategy      string `json:"strategy"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" || req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id, image_ref required")
		return
	}
	if req.Kind == "" {
		req.Kind = inferKind(req.ImageRef)
	}
	if req.Slot == "" {
		req.Slot = "node-1"
	}
	if req.RestartPolicy == "" {
		req.RestartPolicy = "on-failure"
	}
	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	if strategy == "" {
		if req.Rolling {
			strategy = "rolling"
		} else {
			strategy = "rolling"
		}
	}
	if strategy == "blue-green" || strategy == "bluegreen" {
		strategy = "blue_green"
	}

	prev, _ := h.Store.FindActiveByProject(r.Context(), req.OrgID, req.ProjectID)

	// Rolling (default): stop previous desired instance, then start new.
	// Blue/green keeps previous until flip stub drains it.
	if strategy != "blue_green" {
		if prev != nil {
			prev.DesiredState = "stopped"
			_, _ = h.Engine.Stop(r.Context(), prev.ContainerID)
			prev.Status = "stopped"
			prev.HealthStatus = "stopped"
			_ = h.Store.Update(r.Context(), prev)
		}
	}

	// Blue/green stub: start new color alongside previous; then flip Traefik weight (logged stub).
	color := "blue"
	if strategy == "blue_green" && prev != nil {
		if strings.Contains(strings.ToLower(prev.ContainerName), "green") {
			color = "blue"
		} else {
			color = "green"
		}
	}

	in := &store.Instance{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Kind: req.Kind, ImageRef: req.ImageRef,
		Status: "desired", DesiredState: "running", Slot: req.Slot, RestartPolicy: req.RestartPolicy,
		Port: req.Port, Mode: h.Engine.EffectiveMode(r.Context()),
	}
	if req.DeploymentID != "" {
		in.DeploymentID = &req.DeploymentID
	}
	if err := h.Store.Create(r.Context(), in); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create instance")
		return
	}
	if strategy == "blue_green" {
		suffix := req.ProjectID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		in.ContainerName = "jp-" + suffix + "-" + color
	}
	if err := h.startInstance(r, in); err != nil {
		in.Status = "failed"
		in.Error = err.Error()
		_ = h.Store.Update(r.Context(), in)
		httpx.JSON(w, http.StatusCreated, map[string]any{"instance": in, "warning": err.Error(), "strategy": strategy})
		return
	}

	flip := map[string]any{}
	if strategy == "blue_green" {
		flip = h.blueGreenFlipStub(r.Context(), prev, in, color)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"instance": in, "strategy": strategy, "blue_green": flip})
}

func (h *Handler) InternalHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("instanceId")
	var req struct {
		HealthStatus string `json:"health_status"`
		Status       string `json:"status"`
		Error        string `json:"error"`
	}
	_ = httpx.Decode(r, &req)
	in, err := h.Store.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	now := time.Now().UTC()
	in.LastHealthAt = &now
	if req.HealthStatus != "" {
		in.HealthStatus = req.HealthStatus
	}
	if req.Status != "" {
		in.Status = req.Status
	}
	if req.Error != "" {
		in.Error = req.Error
	}
	_ = h.Store.Update(r.Context(), in)
	httpx.JSON(w, http.StatusOK, map[string]any{"instance": in})
}

func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	in, err := h.Store.GetByID(r.Context(), r.PathValue("instanceId"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
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
	httpx.JSON(w, http.StatusOK, map[string]any{"instances": list})
}

func (h *Handler) startInstance(r *http.Request, in *store.Instance) error {
	name := in.ContainerName
	if name == "" {
		name = fmt.Sprintf("jp-%s-%s", short(in.ProjectID), short(in.ID))
	}
	in.ContainerName = name
	cid, mode, err := h.Engine.Start(r.Context(), name, in.ImageRef, in.Port)
	in.Mode = mode
	if err != nil {
		in.Status = "failed"
		in.Error = err.Error()
		_ = h.Store.Update(r.Context(), in)
		return err
	}
	in.ContainerID = cid
	in.Status = "running"
	in.DesiredState = "running"
	in.Error = ""
	in.HealthStatus = "healthy"
	now := time.Now().UTC()
	in.LastHealthAt = &now
	return h.Store.Update(r.Context(), in)
}

// blueGreenFlipStub simulates Traefik weighted routing flip: new color gets 100%, old is drained.
func (h *Handler) blueGreenFlipStub(ctx context.Context, prev, next *store.Instance, color string) map[string]any {
	_ = ctx
	out := map[string]any{
		"stub":           true,
		"active_color":   color,
		"traefik_action": "weighted_service_flip",
		"message":        "Blue/green flip stub: Traefik would point 100% traffic to the new color.",
	}
	if next != nil {
		out["active_instance_id"] = next.ID
		out["active_container"] = next.ContainerName
	}
	if prev != nil {
		out["previous_instance_id"] = prev.ID
		out["previous_container"] = prev.ContainerName
		// Keep previous running briefly (drain), then mark standby — stub stops it.
		prev.DesiredState = "stopped"
		_, _ = h.Engine.Stop(ctx, prev.ContainerID)
		prev.Status = "stopped"
		prev.HealthStatus = "drained"
		_ = h.Store.Update(ctx, prev)
		out["previous_status"] = "drained"
	}
	return out
}

func inferKind(imageRef string) string {
	l := strings.ToLower(imageRef)
	switch {
	case strings.Contains(l, "static"), strings.Contains(l, "nginx"), strings.Contains(l, "caddy"):
		return "static"
	case strings.Contains(l, "node"), strings.Contains(l, "nodejs"):
		return "node"
	default:
		return "container"
	}
}

func short(s string) string {
	s = strings.ReplaceAll(s, "-", "")
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
