// Package manager implements the runtime's orchestration: per-project networks, the
// real rolling / blue-green deploy flow with a health gate, supervision probes, add-on
// containers and one-off jobs. It is the only place that decides what runs where.
package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

// Repo is the persistence the manager needs (implemented by *store.Store, faked in tests).
type Repo interface {
	Create(ctx context.Context, in *store.Instance) error
	Update(ctx context.Context, in *store.Instance) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*store.Instance, error)
	GetByContainerID(ctx context.Context, containerID string) (*store.Instance, error)
	FindByDeploymentID(ctx context.Context, deploymentID string) ([]store.Instance, error)
	ListActiveProduction(ctx context.Context, orgID, projectID string) ([]store.Instance, error)
	ListByProject(ctx context.Context, projectID string) ([]store.Instance, error)
	DeleteByProject(ctx context.Context, projectID string) error
	ListDesiredRunning(ctx context.Context) ([]store.Instance, error)
	ListAllWithContainer(ctx context.Context) ([]store.Instance, error)
	CreateJob(ctx context.Context, j *store.JobRun) error
	FinishJob(ctx context.Context, j *store.JobRun) error
	FailStaleJobs(ctx context.Context, olderThan time.Time) (int64, error)
}

// EnvSource returns the merged environment of a project for an environment name.
type EnvSource interface {
	Env(ctx context.Context, orgID, projectID, environment string) (map[string]string, error)
}

// RouteSetter points a project's production routers at a Traefik service.
type RouteSetter interface {
	SetService(ctx context.Context, projectID, service string) error
	// SetUpstreams routes to apps on a remote server instead of a local Traefik service.
	SetUpstreams(ctx context.Context, projectID, service string, upstreams []string) error
}

// Prober performs one HTTP health probe.
type Prober interface {
	Probe(ctx context.Context, ip string, port int, path string) (status int, err error)
}

// Config tunes the manager.
type Config struct {
	TraefikContainer string
	// SelfContainer is this runtime's own container; it joins every project network so the health gate and
	// probes can reach app containers on their private IPs. Defaults to the hostname (= container id in Docker).
	SelfContainer string
	// ProberContainer is a tiny sidecar (busybox) attached to every project network; health probes run inside it
	// via docker exec, so the runtime itself never joins tenant networks. Env RUNTIME_PROBER_CONTAINER.
	ProberContainer      string
	SkipTraefikAttach    bool
	DrainTime            time.Duration
	StopTimeout          time.Duration
	HealthTimeout        time.Duration
	HealthInterval       time.Duration
	MaxMemoryMB          int
	MaxCPUs              float64
	AllowedImagePrefixes []string
	MaxJobs              int
	JobDefaultTimeout    time.Duration
	JobMaxTimeout        time.Duration
}

// ConfigFromEnv reads runtime tuning from environment variables.
func ConfigFromEnv() Config {
	c := Config{
		TraefikContainer:  "jp-traefik-1",
		DrainTime:         envDur("RUNTIME_DRAIN_TIME", 10*time.Second),
		StopTimeout:       envDur("RUNTIME_STOP_TIMEOUT", 10*time.Second),
		HealthTimeout:     envDur("RUNTIME_HEALTH_TIMEOUT", 90*time.Second),
		HealthInterval:    envDur("RUNTIME_HEALTH_INTERVAL", time.Second),
		MaxMemoryMB:       envInt("RUNTIME_MAX_MEMORY_MB", 4096),
		MaxCPUs:           float64(envInt("RUNTIME_MAX_CPUS", 4)),
		MaxJobs:           envInt("RUNTIME_MAX_JOBS", 4),
		JobDefaultTimeout: envDur("RUNTIME_JOB_TIMEOUT", 10*time.Minute),
		JobMaxTimeout:     envDur("RUNTIME_JOB_MAX_TIMEOUT", time.Hour),
	}
	c.ProberContainer = strings.TrimSpace(os.Getenv("RUNTIME_PROBER_CONTAINER"))
	if v, ok := os.LookupEnv("TRAEFIK_CONTAINER"); ok {
		if strings.TrimSpace(v) == "" {
			c.SkipTraefikAttach = true
		} else {
			c.TraefikContainer = strings.TrimSpace(v)
		}
	}
	for _, p := range strings.Split(os.Getenv("RUNTIME_ALLOWED_IMAGE_PREFIXES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.AllowedImagePrefixes = append(c.AllowedImagePrefixes, p)
		}
	}
	return c
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func (c *Config) defaults() {
	if c.SelfContainer == "" {
		c.SelfContainer = strings.TrimSpace(os.Getenv("RUNTIME_SELF_CONTAINER")) // dev only: join project nets directly
	}
	if c.ProberContainer == "" {
		c.ProberContainer = strings.TrimSpace(os.Getenv("RUNTIME_PROBER_CONTAINER"))
	}
	if c.TraefikContainer == "" && !c.SkipTraefikAttach {
		c.TraefikContainer = "jp-traefik-1"
	}
	if c.StopTimeout <= 0 {
		c.StopTimeout = 10 * time.Second
	}
	if c.HealthTimeout <= 0 {
		c.HealthTimeout = 90 * time.Second
	}
	if c.HealthInterval <= 0 {
		c.HealthInterval = time.Second
	}
	if c.MaxMemoryMB <= 0 {
		c.MaxMemoryMB = 4096
	}
	if c.MaxCPUs <= 0 {
		c.MaxCPUs = 4
	}
	if c.MaxJobs <= 0 {
		c.MaxJobs = 4
	}
	if c.JobDefaultTimeout <= 0 {
		c.JobDefaultTimeout = 10 * time.Minute
	}
	if c.JobMaxTimeout <= 0 {
		c.JobMaxTimeout = time.Hour
	}
}

// Manager orchestrates containers.
type Manager struct {
	Cfg     Config
	Engine  *dockerx.Engine
	Repo    Repo
	Secrets EnvSource
	Routes  RouteSetter
	Prober  Prober
	// TCP overrides the TCP readiness dial (the runtime is not on tenant networks; tests inject).
	TCP func(ctx context.Context, ip string, port int) error
	Log *slog.Logger
	// Nodes and NodeKey enable multi-server placement (nil Nodes = everything runs on this server).
	Nodes   NodeRepo
	NodeKey []byte

	engines engineCache
	locks   *keyedLock
	jobSem  chan struct{}
	jobs    sync.WaitGroup
	baseCtx context.Context
}

// New builds a manager. baseCtx bounds background jobs (cancel it on shutdown, then
// call Wait).
func New(baseCtx context.Context, cfg Config, eng *dockerx.Engine, repo Repo, secrets EnvSource, routes RouteSetter, log *slog.Logger) *Manager {
	cfg.defaults()
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{
		Cfg: cfg, Engine: eng, Repo: repo, Secrets: secrets, Routes: routes, Log: log,
		Prober:  HTTPProber{},
		locks:   newKeyedLock(),
		jobSem:  make(chan struct{}, cfg.MaxJobs),
		baseCtx: baseCtx,
	}
}

// Wait blocks until running background jobs finished.
func (m *Manager) Wait() { m.jobs.Wait() }

// HTTPProber is the production prober: a direct GET to the container IP on the project
// network with a short timeout, no redirects and no credentials.
type HTTPProber struct{}

var probeClient = &http.Client{
	Timeout: 4 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext},
}

// Probe implements Prober.
func (HTTPProber) Probe(ctx context.Context, ip string, port int, path string) (int, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/"
	}
	u := "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "jp-runtime-healthcheck/1")
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

// TCPProbe checks that something accepts connections. It is a variable so the production wiring can route
// the dial through the prober sidecar (see ExecProber); the default dials directly (dev/tests).
var TCPProbe = tcpProbeDirect

func tcpProbeDirect(ctx context.Context, ip string, port int) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return c.Close()
}

// keyedLock serialises work per key and forgets idle keys (bounded memory).
type keyedLock struct {
	mu sync.Mutex
	m  map[string]*lockEntry
}

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

func newKeyedLock() *keyedLock { return &keyedLock{m: map[string]*lockEntry{}} }

func (k *keyedLock) lock(key string) func() {
	k.mu.Lock()
	e := k.m[key]
	if e == nil {
		e = &lockEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// ---------------------------------------------------------------- helpers

// ErrBadRequest marks caller mistakes (HTTP 400).
type ErrBadRequest struct{ Msg string }

func (e *ErrBadRequest) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &ErrBadRequest{Msg: fmt.Sprintf(format, a...)} }

func (m *Manager) imageAllowed(ref string) bool {
	if len(m.Cfg.AllowedImagePrefixes) == 0 {
		return true
	}
	for _, p := range m.Cfg.AllowedImagePrefixes {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// CheckUserImage validates an image reference supplied through the public API.
func (m *Manager) CheckUserImage(ref string) error {
	if err := dockerx.ValidImageRef(ref); err != nil {
		return bad("%v", err)
	}
	if !m.imageAllowed(ref) {
		return bad("image registry not allowed")
	}
	return nil
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func detach(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

func nowPtr() *time.Time { t := time.Now().UTC(); return &t }
