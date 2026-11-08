package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

var addonNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// AddonHealth optionally makes the runtime wait for a TCP listener before returning.
type AddonHealth struct {
	Type           string   `json:"type"` // "", tcp, cmd (http is accepted; the add-on service probes it itself)
	TCP            bool     `json:"tcp"`
	Port           int      `json:"port"`
	Path           string   `json:"path"`
	Cmd            []string `json:"cmd"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// AddonRequest starts a stateful add-on container on the project network.
type AddonRequest struct {
	OrgID     string            `json:"org_id"`
	ProjectID string            `json:"project_id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Port      int               `json:"port"`
	Env       map[string]string `json:"env"`
	Volume    string            `json:"volume"`
	Command   []string          `json:"command"`
	Health    *AddonHealth      `json:"health"`
	MemoryMB  int               `json:"memory_mb"`
}

// AddonResult is returned to the add-on service.
type AddonResult struct {
	ContainerID string `json:"container_id"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Network     string `json:"network"`
}

// ErrConflict marks an already existing resource.
var ErrConflict = errors.New("already exists")

// addonCaps are the extra capabilities official database images need to chown their
// data directory and drop to their service user. Everything else stays dropped.
var addonCaps = []string{"CHOWN", "SETUID", "SETGID", "FOWNER", "DAC_OVERRIDE"}

func addonContainerName(projectID, name string) string {
	return "jp-addon-" + dockerx.Short8(projectID) + "-" + name
}

func addonVolumeName(projectID, name string) string {
	return "jp-addon-" + dockerx.Short8(projectID) + "-" + name
}

// StartAddon creates and starts an add-on container (never on the platform network).
func (m *Manager) StartAddon(ctx context.Context, req AddonRequest) (*AddonResult, error) {
	if !dockerx.ValidUUID(req.OrgID) || !dockerx.ValidUUID(req.ProjectID) {
		return nil, bad("org_id and project_id must be UUIDs")
	}
	if !addonNameRe.MatchString(req.Name) {
		return nil, bad("addon name must match ^[a-z0-9][a-z0-9-]{0,40}$")
	}
	if err := dockerx.ValidImageRef(req.Image); err != nil {
		return nil, bad("%v", err)
	}
	if req.Port <= 0 || req.Port > 65535 {
		return nil, bad("port out of range")
	}
	for k := range req.Env {
		if !dockerx.ValidEnvKey(k) {
			return nil, bad("invalid env name %q", k)
		}
	}
	if req.Volume != "" && (!strings.HasPrefix(req.Volume, "/") || strings.Contains(req.Volume, "..") || strings.ContainsAny(req.Volume, ":,\x00")) {
		return nil, bad("volume must be an absolute path inside the container")
	}
	ctx, cancel := detach(ctx, 5*time.Minute)
	defer cancel()
	unlock := m.locks.lock(req.ProjectID)
	defer unlock()

	netName := dockerx.ProjectNetwork(req.ProjectID)
	cname := addonContainerName(req.ProjectID, req.Name)
	// add-ons live on the same server as the project's app, so the app reaches them on the project network
	eng, node, perr := m.placement(ctx, req.ProjectID)
	if perr != nil {
		return nil, perr
	}
	if st, err := eng.Inspect(ctx, cname); err != nil {
		return nil, nodeDown(node, err)
	} else if st.Exists {
		return nil, fmt.Errorf("%w: addon container %s", ErrConflict, cname)
	}
	if err := m.ensureNetwork(ctx, eng, node, req.OrgID, req.ProjectID); err != nil {
		return nil, err
	}
	labels := map[string]string{
		"jp.managed": "true", "jp.org": req.OrgID, "jp.project": req.ProjectID, "jp.deployment": "", "jp.kind": "addon",
	}
	spec := dockerx.ContainerSpec{
		Name: cname, Image: req.Image, Env: envList(req.Env), Labels: labels, Network: netName,
		Aliases: []string{"addon-" + req.Name}, Port: req.Port, Memory: m.clampMemory(req.MemoryMB),
		RestartPolicy: "unless-stopped", Cmd: req.Command, ExtraCaps: addonCaps,
	}
	if req.Volume != "" {
		spec.Volume = &dockerx.Volume{Name: addonVolumeName(req.ProjectID, req.Name), Target: req.Volume,
			Labels: map[string]string{"jp.managed": "true", "jp.org": req.OrgID, "jp.project": req.ProjectID, "jp.addon": req.Name}}
	}
	in := &store.Instance{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Kind: "addon", Role: "addon", ImageRef: req.Image,
		Status: "starting", DesiredState: "running", Port: req.Port, RestartPolicy: "always", Mode: eng.Mode(),
		Network: netName, ContainerName: cname, Service: "addon-" + req.Name, HealthPath: "/",
	}
	if node != nil {
		in.NodeID = node.ID
	}
	if err := m.Repo.Create(ctx, in); err != nil {
		return nil, err
	}
	fail := func(stage string, err error) (*AddonResult, error) {
		if in.ContainerID != "" {
			_ = m.engOf(ctx, in).Remove(ctx, in.ContainerID, false)
		}
		in.Status, in.DesiredState, in.Error, in.HealthStatus = "failed", "stopped", truncateStr(err.Error(), 500), "unhealthy"
		_ = m.Repo.Update(ctx, in)
		return nil, fmt.Errorf("%s: %w", stage, err)
	}
	id, err := eng.Create(ctx, spec)
	if err != nil {
		return fail("create", err)
	}
	in.ContainerID = id
	_ = m.Repo.Update(ctx, in)
	if err := eng.Start(ctx, id); err != nil {
		return fail("start", err)
	}
	if !eng.Simulated() {
		timeout := 30 * time.Second
		if req.Health != nil && req.Health.TimeoutSeconds > 0 {
			timeout = time.Duration(req.Health.TimeoutSeconds) * time.Second
			if timeout > 5*time.Minute {
				timeout = 5 * time.Minute
			}
		}
		if err := m.addonGate(ctx, in, req.Health, timeout); err != nil {
			return fail("readiness", err)
		}
	}
	in.Status, in.HealthStatus, in.Error = "running", "healthy", ""
	in.LastHealthAt = nowPtr()
	_ = m.Repo.Update(ctx, in)
	return &AddonResult{ContainerID: id, Host: "addon-" + req.Name, Port: req.Port, Network: netName}, nil
}

func (m *Manager) addonGate(ctx context.Context, in *store.Instance, h *AddonHealth, timeout time.Duration) error {
	tcp := h != nil && (h.TCP || h.Type == "tcp")
	port := in.Port
	if h != nil && h.Port > 0 {
		port = h.Port
	}
	var cmd []string
	if h != nil && h.Type == "cmd" {
		cmd = h.Cmd
	}
	deadline := time.Now().Add(timeout)
	for {
		st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
		if err != nil {
			return err
		}
		if !st.Exists {
			return errors.New("container disappeared")
		}
		if !st.Running && !st.Restarting {
			return fmt.Errorf("container exited with code %d", st.ExitCode)
		}
		if st.Running && len(cmd) > 0 {
			if _, _, code, xerr := m.engOf(ctx, in).Exec(ctx, in.ContainerID, cmd); xerr == nil && code == 0 {
				return nil
			}
		} else if st.Running && (!tcp || in.NodeID != "") { // remote add-ons cannot be dialled by IP from here
			return nil // running; the add-on service performs its own protocol-level readiness probe
		}
		if ip := st.IPs[in.Network]; ip != "" && tcp && in.NodeID == "" {
			if m.tcpProbe(ctx, in, ip, port) == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("add-on not ready after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.Cfg.HealthInterval):
		}
	}
}

// RemoveAddon stops and removes an add-on container; purge also deletes its data volume.
func (m *Manager) RemoveAddon(ctx context.Context, containerID string, purge bool) error {
	if !dockerx.ValidName(containerID) {
		return bad("invalid container id")
	}
	ctx, cancel := detach(ctx, 3*time.Minute)
	defer cancel()
	in, err := m.Repo.GetByContainerID(ctx, containerID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	eng := m.Engine
	if in != nil {
		eng = m.engOf(ctx, in)
	}
	st, serr := eng.Inspect(ctx, containerID)
	if serr != nil {
		return serr
	}
	if in != nil && in.Role != "addon" {
		return bad("not an add-on container")
	}
	if in == nil && st.Exists && st.Labels["jp.kind"] != "addon" {
		return bad("not an add-on container")
	}
	projectID, name := "", ""
	if in != nil {
		projectID = in.ProjectID
		name = strings.TrimPrefix(in.Service, "addon-")
	} else if st.Exists {
		projectID = st.Labels["jp.project"]
	}
	if projectID != "" {
		unlock := m.locks.lock(projectID)
		defer unlock()
	}
	if st.Exists {
		if err := eng.Stop(ctx, containerID, m.Cfg.StopTimeout); err != nil {
			return err
		}
		if err := eng.Remove(ctx, containerID, false); err != nil {
			return err
		}
	}
	if purge && projectID != "" && name != "" {
		if err := eng.RemoveVolume(ctx, addonVolumeName(projectID, name)); err != nil {
			return err
		}
	}
	if in != nil {
		return m.Repo.Delete(ctx, in.ID)
	}
	return nil
}

func (m *Manager) tcpProbe(ctx context.Context, in *store.Instance, ip string, port int) error {
	if m.TCP != nil {
		return m.TCP(ctx, ip, port)
	}
	return TCPProbe(ctx, ip, port)
}

// addonInstance loads an add-on instance by container id; app containers are refused.
func (m *Manager) addonInstance(ctx context.Context, containerID string) (*store.Instance, error) {
	if !dockerx.ValidName(containerID) {
		return nil, bad("invalid container id")
	}
	in, err := m.Repo.GetByContainerID(ctx, containerID)
	if err != nil {
		return nil, err
	}
	if in.Role != "addon" {
		return nil, bad("not an add-on container")
	}
	return in, nil
}

// AddonStatus is the live state of an add-on container.
type AddonStatus struct {
	ContainerID  string `json:"container_id"`
	State        string `json:"state"`
	Running      bool   `json:"running"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	ExitCode     int    `json:"exit_code"`
	Error        string `json:"error"`
}

// AddonStatus inspects the container (store.ErrNotFound when unknown).
func (m *Manager) AddonStatus(ctx context.Context, containerID string) (*AddonStatus, error) {
	in, err := m.addonInstance(ctx, containerID)
	if err != nil {
		return nil, err
	}
	st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
	if err != nil {
		return nil, err
	}
	out := &AddonStatus{ContainerID: in.ContainerID, RestartCount: in.RestartCount, ExitCode: st.ExitCode, Error: st.Error}
	switch {
	case !st.Exists:
		out.State, out.Health, out.Error = "missing", "unhealthy", "container missing"
	case st.Running:
		out.State, out.Running, out.Health = "running", true, "healthy"
	default:
		out.State, out.Health = st.Status, "unhealthy"
	}
	return out, nil
}

// AddonAction performs start, stop or restart and records the desired state.
func (m *Manager) AddonAction(ctx context.Context, containerID, action string) error {
	in, err := m.addonInstance(ctx, containerID)
	if err != nil {
		return err
	}
	ctx, cancel := detach(ctx, 3*time.Minute)
	defer cancel()
	unlock := m.locks.lock(in.ProjectID)
	defer unlock()
	switch action {
	case "start":
		if err := m.engOf(ctx, in).Start(ctx, in.ContainerID); err != nil {
			return err
		}
		in.DesiredState, in.Status = "running", "running"
	case "stop":
		if err := m.engOf(ctx, in).Stop(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
			return err
		}
		in.DesiredState, in.Status, in.HealthStatus = "stopped", "stopped", "stopped"
	case "restart":
		if err := m.engOf(ctx, in).Restart(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
			return err
		}
		in.DesiredState, in.Status = "running", "running"
		in.RestartCount++
	default:
		return bad("unknown action %q", action)
	}
	return m.Repo.Update(ctx, in)
}

// AddonExec runs a command inside the add-on container, streaming stdin/stdout.
func (m *Manager) AddonExec(ctx context.Context, containerID string, cmd []string, env map[string]string, stdin io.Reader, stdout io.Writer) (int, string, error) {
	in, err := m.addonInstance(ctx, containerID)
	if err != nil {
		return -1, "", err
	}
	if len(cmd) == 0 || len(cmd) > 64 {
		return -1, "", bad("cmd must have 1-64 arguments")
	}
	for _, a := range cmd {
		if len(a) > 8192 || strings.ContainsRune(a, 0) {
			return -1, "", bad("invalid cmd argument")
		}
	}
	return m.engOf(ctx, in).ExecStream(ctx, in.ContainerID, cmd, env, stdin, stdout)
}
