package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

// Spec is the per-deployment runtime configuration (from jp.yaml / the deployment).
type Spec struct {
	Port                int               `json:"port"`
	HealthPath          string            `json:"health_path"`
	HealthTimeoutSecond int               `json:"health_timeout_seconds"`
	MemoryMB            int               `json:"memory_mb"`
	CPUs                float64           `json:"cpus"`
	RestartPolicy       string            `json:"restart_policy"`
	Replicas            int               `json:"replicas"`
	Env                 map[string]string `json:"env"`
	Command             []string          `json:"command"`
}

// DeployRequest asks for a new version of a project to be started.
type DeployRequest struct {
	OrgID        string
	ProjectID    string
	DeploymentID string
	ImageRef     string
	Strategy     string // rolling | blue_green
	Preview      bool
	// NoSecrets starts the container WITHOUT any platform secrets (code from forks).
	NoSecrets bool
	Spec      Spec

	// resolved by Deploy: where the new containers go (nil node = this server)
	eng  *dockerx.Engine
	node *store.Node
}

// DeployResult describes a finished deployment.
type DeployResult struct {
	Instances  []store.Instance `json:"instances"`
	Service    string           `json:"service"`
	Color      string           `json:"color,omitempty"`
	Port       int              `json:"port"`
	Replaced   []string         `json:"replaced"`
	Idempotent bool             `json:"idempotent,omitempty"`
}

// DeployError is a deployment that did not go live. The previous version (if any) is
// untouched and still serving.
type DeployError struct {
	Stage     string
	Err       error
	Instances []store.Instance
}

func (e *DeployError) Error() string { return fmt.Sprintf("deploy failed at %s: %v", e.Stage, e.Err) }
func (e *DeployError) Unwrap() error { return e.Err }

const (
	envProduction = "production"
	envPreview    = "preview"
)

func (r *DeployRequest) normalize() error {
	if !dockerx.ValidUUID(r.OrgID) || !dockerx.ValidUUID(r.ProjectID) {
		return bad("org_id and project_id must be UUIDs")
	}
	if r.DeploymentID != "" && !dockerx.ValidUUID(r.DeploymentID) {
		return bad("deployment_id must be a UUID")
	}
	if r.Preview && r.DeploymentID == "" {
		return bad("preview deployments require deployment_id")
	}
	if err := dockerx.ValidImageRef(r.ImageRef); err != nil {
		return bad("%v", err)
	}
	switch strings.ToLower(strings.TrimSpace(r.Strategy)) {
	case "", "rolling":
		r.Strategy = "rolling"
	case "blue_green", "blue-green", "bluegreen":
		r.Strategy = "blue_green"
	default:
		return bad("unknown strategy %q", r.Strategy)
	}
	if r.Preview {
		r.Strategy = "rolling" // previews have their own service and never switch routes
	}
	if r.Spec.Port < 0 || r.Spec.Port > 65535 {
		return bad("port out of range")
	}
	if r.Spec.Replicas <= 0 {
		r.Spec.Replicas = 1
	}
	if r.Spec.Replicas > 10 {
		return bad("replicas must be 1-10")
	}
	if r.Preview {
		r.Spec.Replicas = 1
	}
	if r.Spec.HealthPath != "" && (!strings.HasPrefix(r.Spec.HealthPath, "/") || strings.ContainsAny(r.Spec.HealthPath, " \r\n\x00")) {
		return bad("health_path must be an absolute path")
	}
	for k := range r.Spec.Env {
		if !dockerx.ValidEnvKey(k) {
			return bad("invalid env name %q", k)
		}
	}
	if len(r.Spec.Command) > 64 {
		return bad("command too long")
	}
	return nil
}

// Deploy runs the real deployment flow:
//
//	new container(s) start (same Traefik service) -> health gate over the project
//	network -> [blue/green: flip routes] -> drain -> stop+remove the old ones.
//
// Any failure before the gate passes stops the NEW containers and leaves the OLD
// ones untouched (no downtime, real rollback).
func (m *Manager) Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error) {
	if err := req.normalize(); err != nil {
		return nil, err
	}
	ctx, cancel := detach(ctx, 15*time.Minute)
	defer cancel()
	unlock := m.locks.lock(req.ProjectID)
	defer unlock()

	// Idempotency: a retried event for a deployment that already went live is a no-op.
	if req.DeploymentID != "" {
		existing, err := m.Repo.FindByDeploymentID(ctx, req.DeploymentID)
		if err != nil {
			return nil, err
		}
		var live []store.Instance
		for _, in := range existing {
			if in.Role == "app" && in.DesiredState == "running" && in.Status == "running" {
				live = append(live, in)
			}
		}
		if len(live) > 0 {
			return &DeployResult{Instances: live, Service: live[0].Service, Color: live[0].Color, Port: live[0].Port, Idempotent: true}, nil
		}
		// leftovers of an earlier failed attempt: remove them before retrying
		for i := range existing {
			m.discard(ctx, &existing[i])
		}
	}

	netName := dockerx.ProjectNetwork(req.ProjectID)
	req.eng, req.node = m.Engine, nil
	if !req.Preview { // previews always run on this server (their routes use Traefik's docker provider)
		var perr error
		if req.eng, req.node, perr = m.placement(ctx, req.ProjectID); perr != nil {
			return nil, &DeployError{Stage: "placement", Err: perr}
		}
	}
	if err := m.ensureNetwork(ctx, req.eng, req.node, req.OrgID, req.ProjectID); err != nil {
		return nil, &DeployError{Stage: "network", Err: nodeDown(req.node, err)}
	}

	envName := envProduction
	if req.Preview {
		envName = envPreview
	}
	secretEnv := map[string]string{}
	if !req.NoSecrets {
		var err error
		if secretEnv, err = m.Secrets.Env(ctx, req.OrgID, req.ProjectID, envName); err != nil {
			return nil, &DeployError{Stage: "secrets", Err: err}
		}
	}

	port, err := m.detectPort(ctx, req, secretEnv)
	if err != nil {
		return nil, &DeployError{Stage: "image", Err: err}
	}

	env := map[string]string{"HOST": "0.0.0.0", "NODE_ENV": "production"}
	for k, v := range req.Spec.Env {
		env[k] = v
	}
	for k, v := range secretEnv {
		env[k] = v
	}
	env["PORT"] = strconv.Itoa(port)

	// decide service / colour
	var old []store.Instance
	if !req.Preview {
		old, err = m.Repo.ListActiveProduction(ctx, req.OrgID, req.ProjectID)
		if err != nil {
			return nil, err
		}
	}
	// STAGED ROUTING: every production deploy gets its own unique Traefik service. No router points at
	// it, so the new container receives zero traffic until the health gate passed and the domain
	// service atomically moved the routers to it.
	token := dockerx.Short8(req.DeploymentID)
	if req.DeploymentID == "" {
		token = dockerx.Short8(uuid.NewString())
	}
	service := dockerx.AppService(req.ProjectID) + "-" + token
	color := ""
	switch {
	case req.Preview:
		service = dockerx.PreviewService(req.DeploymentID)
	case req.Strategy == "blue_green":
		color = "blue"
		if len(old) > 0 && old[0].Color == "blue" {
			color = "green"
		}
	}

	// start the new containers
	var fresh []*store.Instance
	for i := 0; i < req.Spec.Replicas; i++ {
		in, serr := m.startOne(ctx, req, env, netName, service, color, port, i)
		if in != nil {
			fresh = append(fresh, in)
		}
		if serr != nil {
			m.rollbackNew(ctx, fresh, serr)
			return nil, &DeployError{Stage: "start", Err: serr, Instances: derefAll(fresh)}
		}
	}

	// health gate (all replicas, concurrently)
	timeout := m.Cfg.HealthTimeout
	if req.Spec.HealthTimeoutSecond > 0 {
		timeout = time.Duration(req.Spec.HealthTimeoutSecond) * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	var wg sync.WaitGroup
	gateErrs := make([]error, len(fresh))
	for i := range fresh {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gateErrs[i] = m.healthGate(ctx, fresh[i], timeout)
		}(i)
	}
	wg.Wait()
	for _, ge := range gateErrs {
		if ge != nil {
			m.rollbackNew(ctx, fresh, ge)
			return nil, &DeployError{Stage: "health_gate", Err: ge, Instances: derefAll(fresh)}
		}
	}

	// atomic flip (production only): routers now point at the new, already healthy service
	if !req.Preview {
		var rerr error
		if req.node != nil {
			rerr = m.Routes.SetUpstreams(ctx, req.ProjectID, service, upstreamURLs(req.node, fresh))
		} else {
			rerr = m.Routes.SetService(ctx, req.ProjectID, service)
		}
		if rerr != nil {
			m.rollbackNew(ctx, fresh, rerr)
			return nil, &DeployError{Stage: "route_switch", Err: rerr, Instances: derefAll(fresh)}
		}
	}

	for _, in := range fresh {
		in.Status, in.HealthStatus, in.Error = "running", "healthy", ""
		in.LastHealthAt = nowPtr()
		if err := m.Repo.Update(ctx, in); err != nil {
			m.Log.Error("persist running instance", "instance", in.ID, "err", err)
		}
	}

	// drain + retire the old version
	res := &DeployResult{Service: service, Color: color, Port: port}
	if len(old) > 0 {
		m.sleep(ctx, m.Cfg.DrainTime)
		for i := range old {
			o := &old[i]
			m.retire(ctx, o)
			res.Replaced = append(res.Replaced, o.ID)
		}
	}
	res.Instances = derefAll(fresh)
	return res, nil
}

func derefAll(in []*store.Instance) []store.Instance {
	out := make([]store.Instance, 0, len(in))
	for _, i := range in {
		out = append(out, *i)
	}
	return out
}

func (m *Manager) sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// EnsureProjectNetwork creates the project's private network on the server the project is placed on.
func (m *Manager) EnsureProjectNetwork(ctx context.Context, orgID, projectID string) error {
	eng, node, err := m.placement(ctx, projectID)
	if err != nil {
		return err
	}
	return m.ensureNetwork(ctx, eng, node, orgID, projectID)
}

// ensureNetwork creates the private network on eng. On this server Traefik and the prober join it; on a remote
// node nothing else joins (the control plane reaches the app over its published private-IP port instead).
func (m *Manager) ensureNetwork(ctx context.Context, eng *dockerx.Engine, node *store.Node, orgID, projectID string) error {
	name := dockerx.ProjectNetwork(projectID)
	if _, err := eng.EnsureNetwork(ctx, name, map[string]string{
		"jp.managed": "true", "jp.org": orgID, "jp.project": projectID,
	}); err != nil {
		return fmt.Errorf("create network %s: %w", name, err)
	}
	if node != nil {
		return nil
	}
	// health probes by container IP run inside the prober sidecar (preferred) or, in dev, the runtime itself
	for _, probe := range []string{m.Cfg.ProberContainer, m.Cfg.SelfContainer} {
		if probe != "" && dockerx.ValidName(probe) {
			if err := eng.ConnectNetwork(ctx, name, probe, nil); err != nil {
				return fmt.Errorf("attach prober (%s) to %s: %w", probe, name, err)
			}
		}
	}
	if m.Cfg.SkipTraefikAttach {
		return nil
	}
	if err := eng.ConnectNetwork(ctx, name, m.Cfg.TraefikContainer, nil); err != nil {
		return fmt.Errorf("attach traefik (%s) to %s: %w", m.Cfg.TraefikContainer, name, err)
	}
	return nil
}

// upstreamURLs are the addresses Traefik uses for apps on a remote node.
func upstreamURLs(node *store.Node, ins []*store.Instance) []string {
	out := make([]string, 0, len(ins))
	for _, in := range ins {
		if in.HostPort > 0 {
			out = append(out, "http://"+net.JoinHostPort(node.PrivateIP, strconv.Itoa(in.HostPort)))
		}
	}
	return out
}

// detectPort: jp.yaml spec -> PORT secret -> image ExposedPorts -> image PORT env -> 3000.
func (m *Manager) detectPort(ctx context.Context, req DeployRequest, secretEnv map[string]string) (int, error) {
	if req.Spec.Port > 0 {
		return req.Spec.Port, nil
	}
	if p, err := strconv.Atoi(strings.TrimSpace(secretEnv["PORT"])); err == nil && p > 0 && p < 65536 {
		return p, nil
	}
	if p, err := strconv.Atoi(strings.TrimSpace(req.Spec.Env["PORT"])); err == nil && p > 0 && p < 65536 {
		return p, nil
	}
	eng := req.eng
	if eng == nil {
		eng = m.Engine
	}
	info, err := eng.InspectImage(ctx, req.ImageRef)
	if err != nil {
		if errors.Is(err, dockerx.ErrNotFound) {
			if perr := eng.PullImage(ctx, req.ImageRef); perr != nil {
				return 0, fmt.Errorf("image %s not found locally and pull failed: %w", req.ImageRef, friendlyImageErr(req.ImageRef, perr))
			}
			info, err = eng.InspectImage(ctx, req.ImageRef)
		}
		if err != nil {
			return 0, fmt.Errorf("inspect image: %w", err)
		}
	}
	if len(info.ExposedPorts) > 0 {
		return info.ExposedPorts[0], nil
	}
	for _, e := range info.Env {
		if strings.HasPrefix(e, "PORT=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(e, "PORT=")); err == nil && p > 0 && p < 65536 {
				return p, nil
			}
		}
	}
	return 3000, nil
}

func (m *Manager) clampMemory(mb int) int64 {
	if mb <= 0 {
		return dockerx.DefaultMemory
	}
	if mb < 64 {
		mb = 64
	}
	if mb > m.Cfg.MaxMemoryMB {
		mb = m.Cfg.MaxMemoryMB
	}
	return int64(mb) << 20
}

func (m *Manager) clampCPU(c float64) int64 {
	if c <= 0 {
		return dockerx.DefaultNanoCPUs
	}
	if c < 0.1 {
		c = 0.1
	}
	if c > m.Cfg.MaxCPUs {
		c = m.Cfg.MaxCPUs
	}
	return int64(c * 1e9)
}

func (m *Manager) startOne(ctx context.Context, req DeployRequest, env map[string]string, netName, service, color string, port, replica int) (*store.Instance, error) {
	in := &store.Instance{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Kind: inferKind(req.ImageRef), Role: "app",
		ImageRef: req.ImageRef, Status: "starting", DesiredState: "running", Port: port,
		RestartPolicy: normRestart(req.Spec.RestartPolicy), Mode: req.eng.Mode(), IsPreview: req.Preview,
		Service: service, Color: color, Network: netName, HealthPath: orSlash(req.Spec.HealthPath), Replica: replica,
	}
	if req.DeploymentID != "" {
		d := req.DeploymentID
		in.DeploymentID = &d
	}
	if req.node != nil {
		in.NodeID = req.node.ID
	}
	if err := m.Repo.Create(ctx, in); err != nil {
		return nil, fmt.Errorf("persist instance: %w", err)
	}
	in.ContainerName = "jp-" + dockerx.Short8(req.ProjectID) + "-" + dockerx.Short8(in.ID)

	kind := "app"
	if req.Preview {
		kind = "preview"
	}
	labels := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": netName,
		"traefik.http.services." + service + ".loadbalancer.server.port": strconv.Itoa(port),
		"jp.managed":    "true",
		"jp.org":        req.OrgID,
		"jp.project":    req.ProjectID,
		"jp.deployment": req.DeploymentID,
		"jp.kind":       kind,
	}
	spec := dockerx.ContainerSpec{
		Name: in.ContainerName, Image: req.ImageRef, Env: envList(env), Labels: labels, Network: netName, Port: port,
		Memory: m.clampMemory(req.Spec.MemoryMB), NanoCPUs: m.clampCPU(req.Spec.CPUs),
		RestartPolicy: in.RestartPolicy, Cmd: req.Spec.Command,
	}
	if req.node != nil {
		spec.PublishIP = req.node.PrivateIP // the control plane reaches the app at <node private ip>:<host port>
	}
	tried := map[int]bool{}
	var id string
	var err error
	for attempt := 0; ; attempt++ {
		if req.node != nil {
			hp, aerr := m.allocHostPort(ctx, req.node.ID, tried)
			if aerr != nil {
				err = aerr
				break
			}
			spec.PublishPort, in.HostPort = hp, hp
			tried[hp] = true
		}
		if id, err = req.eng.Create(ctx, spec); err != nil {
			err = nodeDown(req.node, friendlyImageErr(req.ImageRef, err))
			in.Status, in.Error, in.HealthStatus = "failed", err.Error(), "unhealthy"
			in.DesiredState = "stopped"
			_ = m.Repo.Update(ctx, in)
			return in, fmt.Errorf("create container: %w", err)
		}
		in.ContainerID = id
		_ = m.Repo.Update(ctx, in)
		if err = req.eng.Start(ctx, id); err == nil {
			break
		}
		if req.node != nil && portTaken(err) && attempt < 4 {
			// something else on that server holds the port: drop the created container and try another port
			_ = req.eng.Remove(ctx, id, false)
			in.ContainerID = ""
			continue
		}
		break
	}
	if err != nil {
		in.Status, in.Error, in.HealthStatus = "failed", err.Error(), "unhealthy"
		in.DesiredState = "stopped"
		_ = m.Repo.Update(ctx, in)
		return in, fmt.Errorf("start container: %w", err)
	}
	return in, nil
}

func orSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func normRestart(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "never", "no", "none":
		return "no"
	case "always", "unless-stopped":
		return "always"
	default:
		return "on-failure"
	}
}

func inferKind(imageRef string) string {
	l := strings.ToLower(imageRef)
	switch {
	case strings.Contains(l, "nginx"), strings.Contains(l, "caddy"), strings.Contains(l, "static"):
		return "static"
	case strings.Contains(l, "node"):
		return "node"
	default:
		return "container"
	}
}

// healthGate waits until the container answers on its project-network IP (status < 500)
// twice in a row, fails fast if it exits, and times out otherwise.
func (m *Manager) healthGate(ctx context.Context, in *store.Instance, timeout time.Duration) error {
	if m.Engine.Simulated() {
		return nil
	}
	deadline := time.Now().Add(timeout)
	ok := 0
	var last string
	for {
		st, err := m.engOf(ctx, in).Inspect(ctx, in.ContainerID)
		switch {
		case err != nil:
			last = "inspect: " + err.Error()
		case !st.Exists:
			return fmt.Errorf("container %s disappeared", in.ContainerName)
		case !st.Running && !st.Restarting:
			msg := fmt.Sprintf("container exited with code %d", st.ExitCode)
			if st.OOMKilled {
				msg += " (out of memory)"
			}
			return errors.New(msg)
		default:
			ip, port := st.IPs[in.Network], in.Port
			if in.NodeID != "" { // remote: probe the published private-IP port, the same path Traefik will use
				if hp := st.HostPorts[in.Port]; hp > 0 {
					in.HostPort = hp
				}
				if _, node, nerr := m.nodeEngine(ctx, in.NodeID); nerr == nil && node != nil && in.HostPort > 0 {
					ip, port = node.PrivateIP, in.HostPort
				} else {
					ip = ""
				}
			}
			if ip == "" {
				last = "no address to probe yet"
				ok = 0
			} else if status, perr := m.Prober.Probe(ctx, ip, port, in.HealthPath); perr != nil {
				last = "probe: " + perr.Error()
				ok = 0
			} else if status >= 500 {
				last = fmt.Sprintf("probe status %d", status)
				ok = 0
			} else {
				ok++
				if ok >= 2 {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health gate timed out after %s (%s)", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.Cfg.HealthInterval):
		}
	}
}

// rollbackNew stops and removes containers of a failed attempt and records why.
func (m *Manager) rollbackNew(ctx context.Context, fresh []*store.Instance, cause error) {
	for _, in := range fresh {
		if in.ContainerID != "" {
			_ = m.engOf(ctx, in).Stop(ctx, in.ContainerID, 3*time.Second)
			_ = m.engOf(ctx, in).Remove(ctx, in.ContainerID, false)
		}
		in.Status, in.DesiredState, in.HealthStatus = "failed", "stopped", "unhealthy"
		if in.Error == "" {
			in.Error = truncateStr(cause.Error(), 500)
		}
		_ = m.Repo.Update(ctx, in)
	}
}

// retire gracefully stops and removes an old instance and records it as stopped.
func (m *Manager) retire(ctx context.Context, in *store.Instance) {
	in.DesiredState = "stopped"
	if in.ContainerID != "" {
		if err := m.engOf(ctx, in).Stop(ctx, in.ContainerID, m.Cfg.StopTimeout); err != nil {
			m.Log.Warn("stop old container", "container", in.ContainerName, "err", err)
		}
		if err := m.engOf(ctx, in).Remove(ctx, in.ContainerID, false); err != nil {
			m.Log.Warn("remove old container", "container", in.ContainerName, "err", err)
		}
	}
	in.Status, in.HealthStatus, in.Error = "stopped", "stopped", ""
	_ = m.Repo.Update(ctx, in)
}

// discard force-removes leftovers (failed attempt) without recording history.
func (m *Manager) discard(ctx context.Context, in *store.Instance) {
	if in.ContainerID != "" {
		_ = m.engOf(ctx, in).Remove(ctx, in.ContainerID, false)
	}
	_ = m.Repo.Delete(ctx, in.ID)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
