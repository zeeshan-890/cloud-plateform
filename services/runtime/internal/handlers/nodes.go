package handlers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/manager"
	"github.com/jp-cloud/runtime/internal/store"
)

// Multi-server API. All of it is internal: the gateway exposes it to platform administrators only.

var nodeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// allowLoopbackAgents lets tests register a fake agent on 127.0.0.1; it is never set in production.
var allowLoopbackAgents = false

type nodeView struct {
	store.Node
	Reachable   bool   `json:"reachable"`
	Error       string `json:"error,omitempty"`
	DockerVer   string `json:"docker_version,omitempty"`
	Arch        string `json:"architecture,omitempty"`
	ControlArch string `json:"control_architecture,omitempty"`
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes int64  `json:"memory_total_bytes,omitempty"`
	Running     int    `json:"containers_running"`
	Projects    int    `json:"projects"`
	Instances   int    `json:"instances"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`
}

func (h *Handler) nodesOff(w http.ResponseWriter) bool {
	if h.Nodes == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_configured", "multi-server support is not configured")
		return true
	}
	return false
}

// validPrivateAddr accepts host:port with a literal IP (the agent is reached on the private network) that is not
// loopback or link-local (never the cloud metadata address).
func validAgentAddr(addr string) (string, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", false
	}
	ip := net.ParseIP(host)
	if ip == nil || (ip.IsLoopback() && !allowLoopbackAgents) || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return "", false
	}
	return ip.String(), true
}

// view probes a node and returns its live state.
func (h *Handler) view(ctx context.Context, n store.Node) nodeView {
	v := nodeView{Node: n}
	v.Instances, _ = h.Nodes.CountInstancesOnNode(ctx, n.ID)
	v.Projects, _ = h.Nodes.CountPlacementsOnNode(ctx, n.ID)
	eng, err := h.Manager.NodeEngine(ctx, n.ID)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st := time.Now()
	info, err := eng.Info(c)
	if err != nil {
		v.Error = friendlyNodeError(err, n)
		return v
	}
	v.Reachable, v.LatencyMs = true, time.Since(st).Milliseconds()
	v.DockerVer, v.CPUs, v.MemoryBytes, v.Running, v.Arch = info.ServerVersion, info.NCPU, info.MemTotal, info.ContainersRunning, info.Architecture
	if ci, cerr := h.Manager.Engine.Info(c); cerr == nil {
		v.ControlArch = ci.Architecture
	}
	return v
}

func friendlyNodeError(err error, n store.Node) string {
	var ae *dockerx.APIError
	switch {
	case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
		return "the node agent rejected the token"
	case errors.Is(err, dockerx.ErrUnavailable):
		return "cannot reach the node agent at " + n.Addr + ": the agent may be stopped, or the network (cloud firewall / security group) blocks TCP " + portOf(n.Addr) + " from the control plane"
	}
	return err.Error()
}

func portOf(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

func (h *Handler) InternalListNodes(w http.ResponseWriter, r *http.Request) {
	if h.nodesOff(w) {
		return
	}
	nodes, err := h.Nodes.ListNodes(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not list nodes")
		return
	}
	out := make([]nodeView, len(nodes))
	done := make(chan struct{}, len(nodes))
	for i := range nodes {
		go func(i int) { out[i] = h.view(r.Context(), nodes[i]); done <- struct{}{} }(i)
	}
	for range nodes {
		<-done
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"nodes": out})
}

type putNodeReq struct {
	Addr      string `json:"addr"`
	PrivateIP string `json:"private_ip"`
	Token     string `json:"token"`
	Enabled   *bool  `json:"enabled"`
}

// InternalPutNode registers (or updates) a node and proves the agent is reachable before saving anything.
func (h *Handler) InternalPutNode(w http.ResponseWriter, r *http.Request) {
	if h.nodesOff(w) {
		return
	}
	name := r.PathValue("name")
	if !nodeNameRe.MatchString(name) {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "node name must be 1-32 characters: lowercase letters, digits, hyphens")
		return
	}
	var in putNodeReq
	if err := httpx.DecodeLimit(r, &in, 16<<10); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	ip, ok := validAgentAddr(in.Addr)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "addr must be host:port with a literal IP address (not loopback or link-local)")
		return
	}
	pip := net.ParseIP(strings.TrimSpace(in.PrivateIP))
	if pip == nil || pip.IsLoopback() || pip.IsLinkLocalUnicast() || pip.IsMulticast() || pip.IsUnspecified() {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "private_ip must be the server's address that apps are published on")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	existing, _ := h.Nodes.GetNodeByName(r.Context(), name)
	token := in.Token
	sealed := ""
	if token == "" {
		if existing == nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "token is required for a new node")
			return
		}
		sealed = existing.TokenSealed // keep the stored token when editing
	} else {
		if len(token) < 32 || len(token) > 256 {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "token must be 32-256 characters")
			return
		}
		var err error
		if sealed, err = manager.SealNodeToken(h.NodeKey, name, token); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "could not store the token")
			return
		}
	}
	n := &store.Node{Name: name, Addr: net.JoinHostPort(ip, portOf(in.Addr)), PrivateIP: pip.String(), TokenSealed: sealed, Enabled: enabled}
	// prove the agent answers with this token BEFORE saving (a node that cannot be reached is never registered)
	if enabled {
		probeTok := token
		if probeTok == "" && existing != nil {
			if t, err := h.openToken(existing); err == nil {
				probeTok = t
			}
		}
		c, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		if _, err := dockerx.NewRemote(n.Addr, probeTok).Info(c); err != nil {
			httpx.Error(w, http.StatusBadGateway, "node_unreachable", friendlyNodeError(err, *n))
			return
		}
	}
	saved, err := h.Nodes.UpsertNode(r.Context(), n)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save the node")
		return
	}
	h.Manager.InvalidateNode(saved.ID)
	httpx.JSON(w, http.StatusOK, h.view(r.Context(), *saved))
}

func (h *Handler) openToken(n *store.Node) (string, error) {
	return secretbox.Open(h.NodeKey, n.TokenSealed, "runtime-node:"+n.Name)
}

func (h *Handler) InternalDeleteNode(w http.ResponseWriter, r *http.Request) {
	if h.nodesOff(w) {
		return
	}
	n, err := h.Nodes.GetNodeByName(r.Context(), r.PathValue("name"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such node")
		return
	}
	if c, _ := h.Nodes.CountInstancesOnNode(r.Context(), n.ID); c > 0 {
		httpx.Error(w, http.StatusConflict, "conflict", "containers still run on this node ("+strconv.Itoa(c)+"); stop or remove them first")
		return
	}
	if c, _ := h.Nodes.CountPlacementsOnNode(r.Context(), n.ID); c > 0 {
		httpx.Error(w, http.StatusConflict, "conflict", strconv.Itoa(c)+" project(s) are placed on this node; move them to another server first")
		return
	}
	if err := h.Nodes.DeleteNode(r.Context(), n.ID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete the node")
		return
	}
	h.Manager.InvalidateNode(n.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalGetPlacement(w http.ResponseWriter, r *http.Request) {
	if h.nodesOff(w) {
		return
	}
	pid := r.PathValue("projectId")
	if !dockerx.ValidUUID(pid) {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid project id")
		return
	}
	id, err := h.Nodes.Placement(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not read the placement")
		return
	}
	name := ""
	if id != "" {
		if n, err := h.Nodes.GetNode(r.Context(), id); err == nil {
			name = n.Name
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project_id": pid, "node": name})
}

// InternalSetPlacement chooses the server NEW deployments of a project run on. Containers already running stay
// where they are until the next deploy replaces them (which is what makes a move zero-downtime).
func (h *Handler) InternalSetPlacement(w http.ResponseWriter, r *http.Request) {
	if h.nodesOff(w) {
		return
	}
	pid := r.PathValue("projectId")
	if !dockerx.ValidUUID(pid) {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid project id")
		return
	}
	var in struct {
		Node string `json:"node"`
	}
	if err := httpx.DecodeLimit(r, &in, 4<<10); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	target := ""
	if in.Node != "" {
		n, err := h.Nodes.GetNodeByName(r.Context(), in.Node)
		if err != nil {
			httpx.Error(w, http.StatusNotFound, "not_found", "no such node")
			return
		}
		if !n.Enabled {
			httpx.Error(w, http.StatusConflict, "conflict", "node "+n.Name+" is disabled")
			return
		}
		target = n.ID
	}
	// add-ons are reached over the project network, so they must live on the same server as the app
	if rows, err := h.Store.ListByProject(r.Context(), pid); err == nil {
		for _, in := range rows {
			if in.Role == "addon" && in.DesiredState == "running" && in.NodeID != target {
				httpx.Error(w, http.StatusConflict, "conflict", "this project has add-ons or databases on another server; remove them first (their data stays on the server they were created on), then move the project")
				return
			}
		}
	}
	if err := h.Nodes.SetPlacement(r.Context(), pid, target); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save the placement")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project_id": pid, "node": in.Node})
}
