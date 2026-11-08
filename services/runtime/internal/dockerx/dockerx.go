// Package dockerx is a minimal, hardened client for the Docker Engine API.
//
// Every path segment built from caller data goes through net/url escaping and a
// strict name validator; every query goes through url.Values; every response read
// is size limited. Nothing here silently falls back to a simulation: the engine is
// either configured as "docker" (errors are returned when the daemon is down) or as
// "simulate" (explicit, reported via Mode()).
package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ModeDocker   = "docker"
	ModeSimulate = "simulate"

	maxJSON        = 4 << 20
	defaultTimeout = 30 * time.Second
)

// ErrUnavailable is returned (wrapped) when the Docker daemon cannot be reached.
var ErrUnavailable = errors.New("docker engine unavailable")

// ErrNotFound is returned for 404 responses.
var ErrNotFound = errors.New("not found")

// APIError is a non-2xx answer from the daemon.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker api %d: %s", e.Status, e.Message) }

// Engine talks to the Docker Engine API.
type Engine struct {
	mode string
	base string
	HTTP *http.Client
	// dial opens a raw connection to the daemon (exec streaming needs a hijacked connection).
	dial func(ctx context.Context) (net.Conn, error)
	// authHeader is sent on every request, including hijacked ones (remote nodes: "X-Node-Token: <secret>").
	authHeader [2]string
	remote     bool

	mu  sync.Mutex
	sim map[string]*simContainer
}

type simContainer struct {
	name    string
	running bool
}

// New builds an engine for mode ("docker" | "simulate"). Empty means "docker".
// The daemon address comes from DOCKER_HOST (unix:// or tcp://).
func New(mode string) *Engine {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeDocker
	}
	if mode == ModeSimulate {
		return NewSimulated()
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	tr := &http.Transport{MaxIdleConns: 8, IdleConnTimeout: 60 * time.Second}
	base := "http://docker"
	var dial func(ctx context.Context) (net.Conn, error)
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "unix", path)
		}
		dial = func(ctx context.Context) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "unix", path)
		}
	case strings.HasPrefix(host, "tcp://"):
		base = "http://" + strings.TrimPrefix(host, "tcp://")
		addr := strings.TrimPrefix(host, "tcp://")
		dial = func(ctx context.Context) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "tcp", addr)
		}
	default:
		base = host
	}
	return &Engine{mode: ModeDocker, base: base, HTTP: &http.Client{Transport: tr}, dial: dial}
}

// NewWithClient builds a docker-mode engine against baseURL using hc (tests inject an
// httptest server here).
func NewWithClient(baseURL string, hc *http.Client) *Engine {
	if hc == nil {
		hc = &http.Client{}
	}
	base := strings.TrimRight(baseURL, "/")
	addr := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	return &Engine{mode: ModeDocker, base: base, HTTP: hc, dial: func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: 3 * time.Second}
		return d.DialContext(ctx, "tcp", addr)
	}}
}

// tokenTransport adds the node token to every request.
type tokenTransport struct {
	next http.RoundTripper
	key  string
	val  string
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(t.key, t.val)
	return t.next.RoundTrip(r)
}

// NewRemote builds an engine for another server's Docker daemon, reached through that server's node agent
// (host:port on the private network) and authenticated with token. All hardening stays on the caller side
// (ContainerSpec); the agent adds its own allow-list.
func NewRemote(addr, token string) *Engine {
	d := net.Dialer{Timeout: 4 * time.Second}
	tr := &http.Transport{MaxIdleConns: 8, IdleConnTimeout: 60 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) }}
	return &Engine{
		mode: ModeDocker, base: "http://" + addr, remote: true,
		HTTP:       &http.Client{Transport: tokenTransport{next: tr, key: "X-Node-Token", val: token}},
		authHeader: [2]string{"X-Node-Token", token},
		dial:       func(ctx context.Context) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) },
	}
}

// Remote reports whether this engine drives another server (apps there are reached over a published port).
func (e *Engine) Remote() bool { return e.remote }

// NewSimulated returns an explicit simulation engine (dev/tests only).
func NewSimulated() *Engine {
	return &Engine{mode: ModeSimulate, sim: map[string]*simContainer{}}
}

// Mode returns the configured mode. There is no silent fallback between modes.
func (e *Engine) Mode() string { return e.mode }

// Simulated reports whether the engine is the explicit simulation.
func (e *Engine) Simulated() bool { return e.mode == ModeSimulate }

// call performs a request and returns the (limited) body. Non-2xx answers become *APIError.
func (e *Engine) call(ctx context.Context, method, path string, q url.Values, body any, okStatus ...int) ([]byte, int, error) {
	if e.Simulated() {
		return nil, 0, errors.New("simulate mode has no docker api")
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	u := e.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(cctx, method, u, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxJSON))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	for _, s := range okStatus {
		if resp.StatusCode == s {
			return b, resp.StatusCode, nil
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(okStatus) == 0 {
		return b, resp.StatusCode, nil
	}
	msg := strings.TrimSpace(string(b))
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, fmt.Errorf("%w: %s", ErrNotFound, truncate(msg, 300))
	}
	return nil, resp.StatusCode, &APIError{Status: resp.StatusCode, Message: truncate(msg, 300)}
}

func segment(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid docker name %q", truncate(name, 60))
	}
	return url.PathEscape(name), nil
}

// Ping checks the daemon.
func (e *Engine) Ping(ctx context.Context) error {
	if e.Simulated() {
		return nil
	}
	_, _, err := e.call(ctx, http.MethodGet, "/_ping", nil, nil)
	return err
}

// ---------------------------------------------------------------- images

// ImageInfo is the subset of image inspect the runtime needs.
type ImageInfo struct {
	ExposedPorts []int
	Env          []string
	User         string
}

// InspectImage returns image metadata, pulling nothing.
func (e *Engine) InspectImage(ctx context.Context, ref string) (*ImageInfo, error) {
	if err := ValidImageRef(ref); err != nil {
		return nil, err
	}
	if e.Simulated() {
		return &ImageInfo{}, nil
	}
	b, _, err := e.call(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Config struct {
			ExposedPorts map[string]any `json:"ExposedPorts"`
			Env          []string       `json:"Env"`
			User         string         `json:"User"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	info := &ImageInfo{Env: raw.Config.Env, User: raw.Config.User}
	for p := range raw.Config.ExposedPorts {
		var n int
		if _, err := fmt.Sscanf(strings.SplitN(p, "/", 2)[0], "%d", &n); err == nil && n > 0 && n < 65536 {
			info.ExposedPorts = append(info.ExposedPorts, n)
		}
	}
	sortInts(info.ExposedPorts)
	return info, nil
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// PullImage pulls ref (used when the image is not present locally).
func (e *Engine) PullImage(ctx context.Context, ref string) error {
	if err := ValidImageRef(ref); err != nil {
		return err
	}
	if e.Simulated() {
		return nil
	}
	name, tag := splitRef(ref)
	q := url.Values{}
	q.Set("fromImage", name)
	q.Set("tag", tag)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, e.base+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	// The pull stream reports errors in-band; read it with a cap.
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: truncate(strings.TrimSpace(string(data)), 300)}
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var m struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(line, &m) == nil && m.Error != "" {
			return fmt.Errorf("pull %s: %s", ref, truncate(m.Error, 300))
		}
	}
	return nil
}

func splitRef(ref string) (name, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	// tag is after last ':' only if that colon is after the last '/'.
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// ---------------------------------------------------------------- networks

// EnsureNetwork creates the private bridge network if it does not exist and returns its id.
func (e *Engine) EnsureNetwork(ctx context.Context, name string, labels map[string]string) (string, error) {
	seg, err := segment(name)
	if err != nil {
		return "", err
	}
	if e.Simulated() {
		return "sim-net-" + name, nil
	}
	if b, _, err := e.call(ctx, http.MethodGet, "/networks/"+seg, nil, nil); err == nil {
		var n struct {
			ID string `json:"Id"`
		}
		_ = json.Unmarshal(b, &n)
		return n.ID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	body := map[string]any{
		"Name":           name,
		"Driver":         "bridge",
		"CheckDuplicate": true,
		"Attachable":     false,
		"Internal":       false,
		"Labels":         labels,
		"Options":        map[string]string{"com.docker.network.bridge.enable_ip_masquerade": "true"},
	}
	b, _, err := e.call(ctx, http.MethodPost, "/networks/create", nil, body)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusConflict {
			return name, nil // lost a creation race
		}
		return "", err
	}
	var n struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(b, &n)
	return n.ID, nil
}

// ConnectNetwork attaches a container to a network; it is idempotent.
func (e *Engine) ConnectNetwork(ctx context.Context, network, container string, aliases []string) error {
	nseg, err := segment(network)
	if err != nil {
		return err
	}
	if !ValidName(container) {
		return fmt.Errorf("invalid container name %q", truncate(container, 60))
	}
	if e.Simulated() {
		return nil
	}
	body := map[string]any{"Container": container}
	if len(aliases) > 0 {
		body["EndpointConfig"] = map[string]any{"Aliases": aliases}
	}
	_, _, err = e.call(ctx, http.MethodPost, "/networks/"+nseg+"/connect", nil, body)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Message), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// DisconnectNetwork detaches a container from a network (force); missing is not an error.
func (e *Engine) DisconnectNetwork(ctx context.Context, network, container string) error {
	nseg, err := segment(network)
	if err != nil {
		return err
	}
	if !ValidName(container) {
		return fmt.Errorf("invalid container name")
	}
	if e.Simulated() {
		return nil
	}
	_, _, err = e.call(ctx, http.MethodPost, "/networks/"+nseg+"/disconnect", nil, map[string]any{"Container": container, "Force": true})
	if err != nil && !errors.Is(err, ErrNotFound) {
		var ae *APIError
		if errors.As(err, &ae) && (strings.Contains(strings.ToLower(ae.Message), "is not connected") || ae.Status == http.StatusInternalServerError) {
			return nil
		}
		return err
	}
	return nil
}

// RemoveNetwork deletes a network; missing is not an error.
func (e *Engine) RemoveNetwork(ctx context.Context, name string) error {
	seg, err := segment(name)
	if err != nil {
		return err
	}
	if e.Simulated() {
		return nil
	}
	_, _, err = e.call(ctx, http.MethodDelete, "/networks/"+seg, nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- volumes

// RemoveVolume deletes a named volume; missing is not an error.
func (e *Engine) RemoveVolume(ctx context.Context, name string) error {
	seg, err := segment(name)
	if err != nil {
		return err
	}
	if e.Simulated() {
		return nil
	}
	q := url.Values{}
	q.Set("force", "true")
	_, _, err = e.call(ctx, http.MethodDelete, "/volumes/"+seg, q, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// ListVolumes lists volumes matching all label filters.
func (e *Engine) ListVolumes(ctx context.Context, labels ...string) ([]string, error) {
	if e.Simulated() {
		return nil, nil
	}
	q := url.Values{}
	q.Set("filters", filterJSON(map[string][]string{"label": labels}))
	b, _, err := e.call(ctx, http.MethodGet, "/volumes", q, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Volumes []struct {
			Name string `json:"Name"`
		} `json:"Volumes"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Volumes))
	for _, v := range out.Volumes {
		names = append(names, v.Name)
	}
	return names, nil
}

func filterJSON(f map[string][]string) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// ---------------------------------------------------------------- containers

// ContainerSummary is one row of the container list.
type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Created int64             `json:"Created"`
}

// List returns containers (all states) matching every label filter ("k" or "k=v").
func (e *Engine) List(ctx context.Context, labels ...string) ([]ContainerSummary, error) {
	if e.Simulated() {
		return []ContainerSummary{}, nil
	}
	q := url.Values{}
	q.Set("all", "true")
	q.Set("filters", filterJSON(map[string][]string{"label": labels}))
	b, _, err := e.call(ctx, http.MethodGet, "/containers/json", q, nil)
	if err != nil {
		return nil, err
	}
	var out []ContainerSummary
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ContainerSummary{}
	}
	return out, nil
}

// ContainerState is the live state of one container.
type ContainerState struct {
	Exists     bool
	ID         string
	Running    bool
	Status     string
	ExitCode   int
	OOMKilled  bool
	Restarting bool
	StartedAt  time.Time
	Error      string
	IPs        map[string]string // network -> IP
	Labels     map[string]string
	HostPorts  map[int]int // container port -> published host port (remote nodes)
}

// Inspect returns the live state. A missing container yields Exists=false (no error).
func (e *Engine) Inspect(ctx context.Context, id string) (*ContainerState, error) {
	seg, err := segment(id)
	if err != nil {
		return nil, err
	}
	if e.Simulated() {
		e.mu.Lock()
		defer e.mu.Unlock()
		c, ok := e.sim[id]
		if !ok {
			return &ContainerState{Exists: false}, nil
		}
		return &ContainerState{Exists: true, ID: id, Running: c.running, Status: map[bool]string{true: "running", false: "exited"}[c.running],
			IPs: map[string]string{}}, nil
	}
	b, _, err := e.call(ctx, http.MethodGet, "/containers/"+seg+"/json", nil, nil)
	if errors.Is(err, ErrNotFound) {
		return &ContainerState{Exists: false}, nil
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		ID    string `json:"Id"`
		State struct {
			Running    bool   `json:"Running"`
			Status     string `json:"Status"`
			ExitCode   int    `json:"ExitCode"`
			OOMKilled  bool   `json:"OOMKilled"`
			Restarting bool   `json:"Restarting"`
			Error      string `json:"Error"`
			StartedAt  string `json:"StartedAt"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	st := &ContainerState{HostPorts: map[int]int{},
		Exists: true, ID: raw.ID, Running: raw.State.Running, Status: raw.State.Status,
		ExitCode: raw.State.ExitCode, OOMKilled: raw.State.OOMKilled, Restarting: raw.State.Restarting,
		Error: raw.State.Error, Labels: raw.Config.Labels, IPs: map[string]string{},
	}
	if t, err := time.Parse(time.RFC3339Nano, raw.State.StartedAt); err == nil {
		st.StartedAt = t
	}
	for n, v := range raw.NetworkSettings.Networks {
		st.IPs[n] = v.IPAddress
	}
	for k, binds := range raw.NetworkSettings.Ports {
		cp, err := strconv.Atoi(strings.TrimSuffix(k, "/tcp"))
		if err != nil || len(binds) == 0 {
			continue
		}
		if hp, err := strconv.Atoi(binds[0].HostPort); err == nil {
			st.HostPorts[cp] = hp
		}
	}
	return st, nil
}

// Create creates (does not start) a container from spec and returns its id.
func (e *Engine) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	if e.Simulated() {
		e.mu.Lock()
		defer e.mu.Unlock()
		id := "sim-" + spec.Name
		e.sim[id] = &simContainer{name: spec.Name}
		e.sim[spec.Name] = e.sim[id]
		return id, nil
	}
	q := url.Values{}
	q.Set("name", spec.Name)
	b, _, err := e.call(ctx, http.MethodPost, "/containers/create", q, spec.CreateBody())
	if err != nil {
		var ae *APIError
		if errors.Is(err, ErrNotFound) || (errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Message), "no such image")) {
			// image not present locally: pull once and retry
			if perr := e.PullImage(ctx, spec.Image); perr != nil {
				return "", fmt.Errorf("image %s not available: %w", spec.Image, perr)
			}
			b, _, err = e.call(ctx, http.MethodPost, "/containers/create", q, spec.CreateBody())
		}
		if err != nil {
			return "", err
		}
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("docker create: bad response")
	}
	return out.ID, nil
}

// Start starts a created container. Already-started (304) is fine.
func (e *Engine) Start(ctx context.Context, id string) error {
	seg, err := segment(id)
	if err != nil {
		return err
	}
	if e.Simulated() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if c, ok := e.sim[id]; ok {
			c.running = true
		}
		return nil
	}
	_, _, err = e.call(ctx, http.MethodPost, "/containers/"+seg+"/start", nil, nil, 204, 304)
	return err
}

// Stop stops a container gracefully (SIGTERM then SIGKILL after timeout). Missing/stopped is fine.
func (e *Engine) Stop(ctx context.Context, id string, timeout time.Duration) error {
	seg, err := segment(id)
	if err != nil {
		return err
	}
	if e.Simulated() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if c, ok := e.sim[id]; ok {
			c.running = false
		}
		return nil
	}
	q := url.Values{}
	q.Set("t", fmt.Sprint(int(timeout.Seconds())))
	cctx, cancel := context.WithTimeout(ctx, timeout+defaultTimeout)
	defer cancel()
	_, _, err = e.call(cctx, http.MethodPost, "/containers/"+seg+"/stop", q, nil, 204, 304)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Restart restarts a container in place.
func (e *Engine) Restart(ctx context.Context, id string, timeout time.Duration) error {
	seg, err := segment(id)
	if err != nil {
		return err
	}
	if e.Simulated() {
		return e.Start(ctx, id)
	}
	q := url.Values{}
	q.Set("t", fmt.Sprint(int(timeout.Seconds())))
	cctx, cancel := context.WithTimeout(ctx, timeout+defaultTimeout)
	defer cancel()
	_, _, err = e.call(cctx, http.MethodPost, "/containers/"+seg+"/restart", q, nil, 204)
	return err
}

// Remove force-removes a container (optionally with anonymous volumes). Missing is fine.
func (e *Engine) Remove(ctx context.Context, id string, removeVolumes bool) error {
	seg, err := segment(id)
	if err != nil {
		return err
	}
	if e.Simulated() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if c, ok := e.sim[id]; ok {
			delete(e.sim, c.name)
			delete(e.sim, id)
		}
		return nil
	}
	q := url.Values{}
	q.Set("force", "true")
	if removeVolumes {
		q.Set("v", "true")
	}
	_, _, err = e.call(ctx, http.MethodDelete, "/containers/"+seg, q, nil, 204)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	var ae *APIError
	if errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Message), "already in progress") {
		return nil
	}
	return err
}

// Waiter reads the exit code of a wait request that was registered before start.
type Waiter struct {
	resp   *http.Response
	cancel context.CancelFunc
}

// Wait blocks until the container exits (or ctx ends) and returns the exit code.
func (w *Waiter) Wait() (int, error) {
	defer w.cancel()
	defer w.resp.Body.Close()
	var out struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := json.NewDecoder(io.LimitReader(w.resp.Body, 64<<10)).Decode(&out); err != nil {
		return -1, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return out.StatusCode, errors.New(truncate(out.Error.Message, 300))
	}
	return out.StatusCode, nil
}

// Close abandons the wait request.
func (w *Waiter) Close() { w.cancel(); _ = w.resp.Body.Close() }

// WaitRegister registers a wait (condition "removed" suits AutoRemove containers) and
// returns once the daemon has acknowledged it, i.e. it is safe to Start the container.
func (e *Engine) WaitRegister(ctx context.Context, id, condition string) (*Waiter, error) {
	seg, err := segment(id)
	if err != nil {
		return nil, err
	}
	if e.Simulated() {
		return nil, errors.New("wait not supported in simulate mode")
	}
	q := url.Values{}
	q.Set("condition", condition)
	cctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, e.base+"/containers/"+seg+"/wait?"+q.Encode(), nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		return nil, &APIError{Status: resp.StatusCode, Message: truncate(string(b), 300)}
	}
	return &Waiter{resp: resp, cancel: cancel}, nil
}

// ---------------------------------------------------------------- info / prune

// Info is a trimmed `docker info`.
type Info struct {
	ServerVersion     string `json:"ServerVersion"`
	NCPU              int    `json:"NCPU"`
	MemTotal          int64  `json:"MemTotal"`
	Containers        int    `json:"Containers"`
	ContainersRunning int    `json:"ContainersRunning"`
	Images            int    `json:"Images"`
	OperatingSystem   string `json:"OperatingSystem"`
	Architecture      string `json:"Architecture"` // x86_64, aarch64, ...
}

// Info returns daemon info.
func (e *Engine) Info(ctx context.Context) (*Info, error) {
	if e.Simulated() {
		return &Info{ServerVersion: "simulate"}, nil
	}
	b, _, err := e.call(ctx, http.MethodGet, "/info", nil, nil)
	if err != nil {
		return nil, err
	}
	var i Info
	if err := json.Unmarshal(b, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// PruneResult reports reclaimed space.
type PruneResult struct {
	ContainersDeleted int   `json:"containers_deleted"`
	ImagesDeleted     int   `json:"images_deleted"`
	NetworksDeleted   int   `json:"networks_deleted"`
	SpaceReclaimed    int64 `json:"space_reclaimed"`
}

// Prune removes stopped platform containers, dangling images (older than until) and
// unused platform networks. Only objects labelled jp.managed=true are touched for
// containers/networks.
func (e *Engine) Prune(ctx context.Context, containers, images, networks bool, until time.Duration) (*PruneResult, error) {
	res := &PruneResult{}
	if e.Simulated() {
		return res, nil
	}
	untilStr := ""
	if until > 0 {
		untilStr = fmt.Sprintf("%dh", int(until.Hours()))
	}
	do := func(path string, f map[string][]string, parse func([]byte)) error {
		q := url.Values{}
		q.Set("filters", filterJSON(f))
		b, _, err := e.call(ctx, http.MethodPost, path, q, nil)
		if err != nil {
			return err
		}
		parse(b)
		return nil
	}
	if containers {
		f := map[string][]string{"label": {"jp.managed=true"}}
		if untilStr != "" {
			f["until"] = []string{untilStr}
		}
		if err := do("/containers/prune", f, func(b []byte) {
			var r struct {
				ContainersDeleted []string `json:"ContainersDeleted"`
				SpaceReclaimed    int64    `json:"SpaceReclaimed"`
			}
			if json.Unmarshal(b, &r) == nil {
				res.ContainersDeleted = len(r.ContainersDeleted)
				res.SpaceReclaimed += r.SpaceReclaimed
			}
		}); err != nil {
			return res, err
		}
	}
	if images {
		f := map[string][]string{"dangling": {"true"}}
		if untilStr != "" {
			f["until"] = []string{untilStr}
		}
		if err := do("/images/prune", f, func(b []byte) {
			var r struct {
				ImagesDeleted  []any `json:"ImagesDeleted"`
				SpaceReclaimed int64 `json:"SpaceReclaimed"`
			}
			if json.Unmarshal(b, &r) == nil {
				res.ImagesDeleted = len(r.ImagesDeleted)
				res.SpaceReclaimed += r.SpaceReclaimed
			}
		}); err != nil {
			return res, err
		}
	}
	if networks {
		f := map[string][]string{"label": {"jp.managed=true"}}
		if err := do("/networks/prune", f, func(b []byte) {
			var r struct {
				NetworksDeleted []string `json:"NetworksDeleted"`
			}
			if json.Unmarshal(b, &r) == nil {
				res.NetworksDeleted = len(r.NetworksDeleted)
			}
		}); err != nil {
			return res, err
		}
	}
	return res, nil
}

type failTransport struct{ err error }

func (t failTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

// NewUnavailable returns an engine whose every call fails with ErrUnavailable and the given reason. It stands in
// for a node that is disabled, deleted or cannot be set up, so callers never need a nil check and nothing is
// ever silently run on the wrong server.
func NewUnavailable(reason error) *Engine {
	return &Engine{mode: ModeDocker, base: "http://unavailable.invalid", remote: true, HTTP: &http.Client{Transport: failTransport{err: reason}}}
}
