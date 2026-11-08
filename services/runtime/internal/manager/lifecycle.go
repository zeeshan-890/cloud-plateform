package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

// ProbeResult is the truthful state of an instance, as observed right now.
type ProbeResult struct {
	Instance   store.Instance `json:"instance"`
	Exists     bool           `json:"exists"`
	Running    bool           `json:"running"`
	Healthy    bool           `json:"healthy"`
	HTTPStatus int            `json:"http_status,omitempty"`
	ExitCode   int            `json:"exit_code,omitempty"`
	Detail     string         `json:"detail,omitempty"`
}

// Probe inspects the container with Docker and probes it over HTTP (apps) or TCP
// (add-ons), then persists the observed state. It never creates containers.
func (m *Manager) Probe(ctx context.Context, in *store.Instance) (*ProbeResult, error) {
	res := &ProbeResult{}
	finish := func() (*ProbeResult, error) {
		in.LastHealthAt = nowPtr()
		if err := m.Repo.Update(ctx, in); err != nil {
			return nil, err
		}
		res.Instance = *in
		return res, nil
	}
	if m.Engine.Simulated() {
		res.Exists, res.Running, res.Healthy = in.ContainerID != "", in.ContainerID != "", in.ContainerID != ""
		if res.Healthy {
			in.Status, in.HealthStatus = "running", "healthy"
		}
		return finish()
	}
	if in.ContainerID == "" {
		in.Status, in.HealthStatus, in.Error = "failed", "unhealthy", "no container"
		res.Detail = "no container"
		return finish()
	}
	st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
	if err != nil {
		return nil, err // docker unreachable: do not rewrite state with guesses
	}
	switch {
	case !st.Exists:
		in.Status, in.HealthStatus, in.Error = "failed", "unhealthy", "container missing"
		res.Detail = "container missing"
	case !st.Running:
		code := st.ExitCode
		in.ExitCode = &code
		res.ExitCode = code
		in.Status, in.HealthStatus = "exited", "unhealthy"
		in.Error = fmt.Sprintf("container exited with code %d", code)
		if st.OOMKilled {
			in.Error += " (out of memory)"
		}
		res.Exists, res.Detail = true, in.Error
	default:
		res.Exists, res.Running = true, true
		ip, port := st.IPs[in.Network], in.Port
		if in.NodeID != "" {
			// Remote: the container's own IP is on another server's Docker network and cannot be dialled from here.
			// Apps are probed on their published private-IP port (the path Traefik uses); add-ons are never
			// published, so a running container is the evidence available.
			if in.Role == "addon" {
				res.Healthy = true
				break
			}
			ip = ""
			if hp := st.HostPorts[in.Port]; hp > 0 {
				in.HostPort = hp
			}
			if _, node, nerr := m.nodeEngine(ctx, in.NodeID); nerr == nil && node != nil && in.HostPort > 0 {
				ip, port = node.PrivateIP, in.HostPort
			}
		}
		switch {
		case ip == "":
			in.Status, in.HealthStatus, in.Error = "unhealthy", "unhealthy", "no address to probe"
			res.Detail = in.Error
		case in.Role == "addon":
			if perr := TCPProbe(ctx, ip, in.Port); perr != nil {
				in.Status, in.HealthStatus, in.Error = "unhealthy", "unhealthy", "tcp probe failed"
				res.Detail = in.Error
			} else {
				res.Healthy = true
			}
		default:
			status, perr := m.Prober.Probe(ctx, ip, port, in.HealthPath)
			res.HTTPStatus = status
			switch {
			case perr != nil:
				in.Status, in.HealthStatus, in.Error = "unhealthy", "unhealthy", "probe failed: "+truncateStr(perr.Error(), 200)
				res.Detail = in.Error
			case status >= 500:
				in.Status, in.HealthStatus, in.Error = "unhealthy", "unhealthy", fmt.Sprintf("probe status %d", status)
				res.Detail = in.Error
			default:
				res.Healthy = true
			}
		}
	}
	if res.Healthy {
		in.Status, in.HealthStatus, in.Error = "running", "healthy", ""
	}
	return finish()
}

// Restart restarts the existing container in place (no new instance is created) and waits
// for it to become healthy again.
func (m *Manager) Restart(ctx context.Context, in *store.Instance) error {
	ctx, cancel := detach(ctx, 5*time.Minute)
	defer cancel()
	unlock := m.locks.lock(in.ProjectID)
	defer unlock()
	if in.ContainerID == "" {
		return bad("instance has no container; redeploy instead")
	}
	st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
	if err != nil {
		return err
	}
	if !st.Exists {
		in.Status, in.HealthStatus, in.Error = "failed", "unhealthy", "container missing; redeploy required"
		_ = m.Repo.Update(ctx, in)
		return bad("container no longer exists; redeploy instead")
	}
	in.DesiredState = "running"
	in.Status = "starting"
	if err := m.engOf(ctx, in).Restart(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
		in.Status, in.Error, in.HealthStatus = "failed", truncateStr(err.Error(), 500), "unhealthy"
		_ = m.Repo.Update(ctx, in)
		return err
	}
	in.RestartCount++
	in.LastRestartAt = nowPtr()
	if in.Role == "addon" {
		in.Status, in.HealthStatus, in.Error = "running", "healthy", ""
		return m.Repo.Update(ctx, in)
	}
	if gerr := m.healthGate(ctx, in, m.Cfg.HealthTimeout); gerr != nil {
		in.Status, in.Error, in.HealthStatus = "unhealthy", truncateStr(gerr.Error(), 500), "unhealthy"
		_ = m.Repo.Update(ctx, in)
		return gerr
	}
	in.Status, in.Error, in.HealthStatus = "running", "", "healthy"
	in.LastHealthAt = nowPtr()
	return m.Repo.Update(ctx, in)
}

// Start starts a stopped instance's existing container.
func (m *Manager) Start(ctx context.Context, in *store.Instance) error {
	ctx, cancel := detach(ctx, 5*time.Minute)
	defer cancel()
	unlock := m.locks.lock(in.ProjectID)
	defer unlock()
	if in.ContainerID == "" {
		return bad("instance has no container; redeploy instead")
	}
	st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
	if err != nil {
		return err
	}
	if !st.Exists {
		in.Status, in.Error = "failed", "container missing; redeploy required"
		_ = m.Repo.Update(ctx, in)
		return bad("container no longer exists; redeploy instead")
	}
	in.DesiredState = "running"
	if !st.Running {
		if err := m.engOf(ctx, in).Start(ctx, in.ContainerID); err != nil {
			in.Status, in.Error = "failed", truncateStr(err.Error(), 500)
			_ = m.Repo.Update(ctx, in)
			return err
		}
	}
	if in.Role != "addon" {
		if gerr := m.healthGate(ctx, in, m.Cfg.HealthTimeout); gerr != nil {
			in.Status, in.Error, in.HealthStatus = "unhealthy", truncateStr(gerr.Error(), 500), "unhealthy"
			_ = m.Repo.Update(ctx, in)
			return gerr
		}
	}
	in.Status, in.Error, in.HealthStatus = "running", "", "healthy"
	in.LastHealthAt = nowPtr()
	return m.Repo.Update(ctx, in)
}

// Stop gracefully stops an instance and records the desired state.
func (m *Manager) Stop(ctx context.Context, in *store.Instance) error {
	ctx, cancel := detach(ctx, 2*time.Minute)
	defer cancel()
	in.DesiredState = "stopped"
	if in.ContainerID != "" {
		if err := m.engOf(ctx, in).Stop(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
			in.Status, in.Error = "failed", truncateStr(err.Error(), 500)
			_ = m.Repo.Update(ctx, in)
			return err
		}
	}
	in.Status, in.HealthStatus, in.Error = "stopped", "stopped", ""
	return m.Repo.Update(ctx, in)
}

// Delete stops and removes the container and deletes the instance record.
func (m *Manager) Delete(ctx context.Context, in *store.Instance) error {
	ctx, cancel := detach(ctx, 2*time.Minute)
	defer cancel()
	if in.ContainerID != "" {
		if err := m.engOf(ctx, in).Stop(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
			return err
		}
		if err := m.engOf(ctx, in).Remove(ctx, in.ContainerID, false); err != nil {
			return err
		}
	}
	return m.Repo.Delete(ctx, in.ID)
}

// StopByDeployment stops and removes every container of a deployment (preview teardown,
// cancel). It also removes the instance records. Returns how many instances were handled.
func (m *Manager) StopByDeployment(ctx context.Context, deploymentID string) (int, error) {
	if !dockerx.ValidUUID(deploymentID) {
		return 0, bad("deployment_id must be a UUID")
	}
	ctx, cancel := detach(ctx, 3*time.Minute)
	defer cancel()
	list, err := m.Repo.FindByDeploymentID(ctx, deploymentID)
	if err != nil {
		return 0, err
	}
	n := 0
	var firstErr error
	for i := range list {
		in := &list[i]
		unlock := m.locks.lock(in.ProjectID)
		if in.ContainerID != "" {
			if err := m.engOf(ctx, in).Stop(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := m.engOf(ctx, in).Remove(ctx, in.ContainerID, false); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		in.DesiredState, in.Status, in.HealthStatus = "stopped", "stopped", "stopped"
		_ = m.Repo.Update(ctx, in)
		unlock()
		n++
	}
	return n, firstErr
}

// ProjectContainers lists docker containers of one project/org (never other tenants'), on every server the
// project runs on.
func (m *Manager) ProjectContainers(ctx context.Context, orgID, projectID string) ([]dockerx.ContainerSummary, error) {
	if m.Engine.Simulated() {
		return []dockerx.ContainerSummary{}, nil
	}
	out := []dockerx.ContainerSummary{}
	for i, ref := range m.projectEngines(ctx, projectID) {
		if ref.err != nil {
			continue
		}
		list, err := ref.eng.List(ctx, "jp.managed=true", "jp.org="+orgID, "jp.project="+projectID)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			continue // an unreachable remote server must not hide the local containers
		}
		for _, c := range list { // defence in depth: re-check labels on our side
			if c.Labels["jp.org"] == orgID && c.Labels["jp.project"] == projectID {
				c.Labels = map[string]string{"jp.kind": c.Labels["jp.kind"], "jp.deployment": c.Labels["jp.deployment"]}
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// DeleteProjectReport summarises a project cascade delete.
type DeleteProjectReport struct {
	ContainersRemoved int      `json:"containers_removed"`
	VolumesRemoved    int      `json:"volumes_removed"`
	NetworkRemoved    bool     `json:"network_removed"`
	Errors            []string `json:"errors,omitempty"`
}

// DeleteProject stops+removes every container, volume and the network of a project.
func (m *Manager) DeleteProject(ctx context.Context, projectID string) (*DeleteProjectReport, error) {
	if !dockerx.ValidUUID(projectID) {
		return nil, bad("project id must be a UUID")
	}
	ctx, cancel := detach(ctx, 5*time.Minute)
	defer cancel()
	unlock := m.locks.lock(projectID)
	defer unlock()
	rep := &DeleteProjectReport{}
	addErr := func(format string, a ...any) { rep.Errors = append(rep.Errors, fmt.Sprintf(format, a...)) }

	netName := dockerx.ProjectNetwork(projectID)
	for i, ref := range m.projectEngines(ctx, projectID) {
		where := "this server"
		if ref.node != nil {
			where = ref.node.Name
		}
		if ref.err != nil {
			addErr("%s: %v", where, ref.err)
			continue
		}
		eng := ref.eng
		list, err := eng.List(ctx, "jp.managed=true", "jp.project="+projectID)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			addErr("%s: list containers: %v", where, err)
			continue
		}
		for _, c := range list {
			if c.Labels["jp.project"] != projectID {
				continue
			}
			if err := eng.Stop(ctx, c.ID, m.Cfg.StopTimeout); err != nil {
				addErr("stop %s: %v", shortID(c.ID), err)
			}
			if err := eng.Remove(ctx, c.ID, false); err != nil {
				addErr("remove %s: %v", shortID(c.ID), err)
				continue
			}
			rep.ContainersRemoved++
		}
		vols, err := eng.ListVolumes(ctx, "jp.managed=true", "jp.project="+projectID)
		if err != nil {
			addErr("list volumes: %v", err)
		}
		for _, v := range vols {
			if err := eng.RemoveVolume(ctx, v); err != nil {
				addErr("remove volume %s: %v", v, err)
				continue
			}
			rep.VolumesRemoved++
		}
		if ref.node == nil {
			for _, probe := range []string{m.Cfg.ProberContainer, m.Cfg.SelfContainer} {
				if probe != "" && dockerx.ValidName(probe) {
					if err := eng.DisconnectNetwork(ctx, netName, probe); err != nil {
						addErr("disconnect prober: %v", err)
					}
				}
			}
			if !m.Cfg.SkipTraefikAttach {
				if err := eng.DisconnectNetwork(ctx, netName, m.Cfg.TraefikContainer); err != nil {
					addErr("disconnect traefik: %v", err)
				}
			}
		}
		if err := eng.RemoveNetwork(ctx, netName); err != nil {
			addErr("remove network on %s: %v", where, err)
		} else if ref.node == nil {
			rep.NetworkRemoved = true
		}
	}
	if m.Nodes != nil {
		if err := m.Nodes.DeletePlacement(ctx, projectID); err != nil {
			addErr("delete placement: %v", err)
		}
	}
	if err := m.Repo.DeleteByProject(ctx, projectID); err != nil {
		addErr("delete records: %v", err)
	}
	return rep, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ReconcileReport is the outcome of one reconcile pass.
type ReconcileReport struct {
	MarkedMissing  int `json:"marked_missing"`
	OrphansFound   int `json:"orphans_found"`
	OrphansRemoved int `json:"orphans_removed"`
	StuckFailed    int `json:"stuck_failed"`
	StaleJobs      int `json:"stale_jobs"`
}

const orphanGrace = 5 * time.Minute

// Reconcile aligns the database with Docker: rows whose container vanished are marked
// failed (never silently recreated), labelled containers without a row older than the
// grace period are removed, and instances stuck in "starting" are failed.
func (m *Manager) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	rep := &ReconcileReport{}
	if m.Engine.Simulated() {
		return rep, nil
	}
	rows, err := m.Repo.ListAllWithContainer(ctx)
	if err != nil {
		return nil, err
	}
	// group the rows by the server they run on; every server is reconciled against its own Docker only
	byNode := map[string][]*store.Instance{"": nil}
	for i := range rows {
		byNode[rows[i].NodeID] = append(byNode[rows[i].NodeID], &rows[i])
	}
	for nodeID, group := range byNode {
		eng, _, nerr := m.nodeEngine(ctx, nodeID)
		if nerr != nil {
			m.Log.Warn("reconcile: server unavailable, skipped", "node", nodeID, "err", nerr)
			continue
		}
		list, lerr := eng.List(ctx, "jp.managed=true")
		if lerr != nil {
			if nodeID == "" {
				return nil, lerr
			}
			m.Log.Warn("reconcile: server unreachable, skipped (nothing is marked missing)", "node", nodeID, "err", lerr)
			continue
		}
		m.reconcileOne(ctx, eng, list, group, rep)
	}
	if n, err := m.Repo.FailStaleJobs(ctx, time.Now().Add(-m.Cfg.JobMaxTimeout-time.Minute)); err == nil {
		rep.StaleJobs = int(n)
	}
	return rep, nil
}

func (m *Manager) reconcileOne(ctx context.Context, eng *dockerx.Engine, list []dockerx.ContainerSummary, rows []*store.Instance, rep *ReconcileReport) {
	byID := map[string]dockerx.ContainerSummary{}
	for _, c := range list {
		byID[c.ID] = c
	}
	known := map[string]bool{}
	for _, in := range rows {
		known[in.ContainerID] = true
		_, exists := byID[in.ContainerID]
		if !exists && in.DesiredState == "running" && in.Status != "failed" && in.Status != "starting" {
			in.Status, in.HealthStatus, in.Error = "failed", "unhealthy", "container missing"
			_ = m.Repo.Update(ctx, in)
			rep.MarkedMissing++
		}
		if in.Status == "starting" && time.Since(in.UpdatedAt) > 15*time.Minute {
			in.Status, in.HealthStatus, in.Error = "failed", "unhealthy", "stuck starting"
			_ = m.Repo.Update(ctx, in)
			rep.StuckFailed++
		}
	}
	for _, c := range list {
		kind := c.Labels["jp.kind"]
		if kind == "job" || known[c.ID] {
			continue
		}
		if time.Since(time.Unix(c.Created, 0)) < orphanGrace {
			continue
		}
		rep.OrphansFound++
		if err := eng.Remove(ctx, c.ID, false); err == nil {
			rep.OrphansRemoved++
		}
	}
}

// RunReconcile runs Reconcile every interval until ctx is cancelled.
func (m *Manager) RunReconcile(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			rep, err := m.Reconcile(rctx)
			cancel()
			if err != nil {
				m.Log.Warn("reconcile failed", "err", err)
			} else if rep.MarkedMissing+rep.OrphansRemoved+rep.StuckFailed > 0 {
				m.Log.Info("reconcile", "missing", rep.MarkedMissing, "orphans_removed", rep.OrphansRemoved, "stuck", rep.StuckFailed)
			}
		}
	}
}

// ErrDockerDown reports whether err means the daemon is unreachable.
func ErrDockerDown(err error) bool { return errors.Is(err, dockerx.ErrUnavailable) }
