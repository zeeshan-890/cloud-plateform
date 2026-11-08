// Package traefikcfg renders and reconciles Traefik file-provider router files.
//
// Safety rules (see CONTRACT.md):
//   - every hostname is validated again here before it reaches a rule;
//   - YAML is produced only by yaml.Marshal of typed structs (no string concatenation,
//     no comments carrying user data);
//   - file names are domain-<sha256(host)[:16]>.yml;
//   - writes are atomic (tmp file + rename) and skipped when unchanged.
package traefikcfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/jp-cloud/domain/internal/hostname"
)

// Route is one hostname -> service mapping.
type Route struct {
	Hostname string // validated hostname (without "*.")
	Wildcard bool   // match any single label under Hostname
	Service  string // traefik service name; "@docker" is appended when missing
	TLS      bool   // add websecure + certResolver
	// Upstreams, when non-empty, routes to these http://ip:port servers (load balanced) through a
	// file-provider service defined in the same file instead of the @docker Service.
	Upstreams []string
}

type dynamicFile struct {
	HTTP httpBlock `yaml:"http"`
}

type httpBlock struct {
	Routers  map[string]router  `yaml:"routers"`
	Services map[string]service `yaml:"services,omitempty"`
}

type service struct {
	LoadBalancer loadBalancer `yaml:"loadBalancer"`
}

type loadBalancer struct {
	Servers        []server `yaml:"servers"`
	PassHostHeader bool     `yaml:"passHostHeader"`
}

type server struct {
	URL string `yaml:"url"`
}

type router struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
	TLS         *tlsCfg  `yaml:"tls,omitempty"`
	Middlewares []string `yaml:"middlewares,omitempty"`
}

type tlsCfg struct {
	CertResolver string `yaml:"certResolver"`
}

var (
	serviceRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}(@docker)?$`)
	fileNameRe = regexp.MustCompile(`^domain-[0-9a-f]{16}\.yml$`)
)

// Manager owns a dynamic-config directory.
type Manager struct {
	Dir          string
	CertResolver string
	mu           sync.Mutex
}

// New returns a Manager. certResolver defaults to "letsencrypt".
func New(dir, certResolver string) *Manager {
	if certResolver == "" {
		certResolver = "letsencrypt"
	}
	return &Manager{Dir: dir, CertResolver: certResolver}
}

// FileName is the canonical file name for a hostname.
func FileName(host string, wildcard bool) string {
	key := host
	if wildcard {
		key = "*." + host
	}
	sum := sha256.Sum256([]byte(key))
	return "domain-" + hex.EncodeToString(sum[:])[:16] + ".yml"
}

// RouterName is the Traefik router key for a hostname.
func RouterName(host string, wildcard bool) string {
	return "jp-d-" + strings.TrimSuffix(strings.TrimPrefix(FileName(host, wildcard), "domain-"), ".yml")
}

// Render builds the YAML document for a route.
func Render(r Route, certResolver string) ([]byte, error) {
	if err := hostname.ValidSyntax(r.Hostname); err != nil {
		return nil, fmt.Errorf("refusing to render invalid hostname: %w", err)
	}
	var svc string
	var services map[string]service
	if len(r.Upstreams) > 0 {
		ups, err := ValidateUpstreams(r.Upstreams)
		if err != nil {
			return nil, err
		}
		name := UpstreamServiceName(r.Hostname, r.Wildcard)
		svc = name + "@file"
		lb := loadBalancer{PassHostHeader: true}
		for _, u := range ups {
			lb.Servers = append(lb.Servers, server{URL: u})
		}
		services = map[string]service{name: {LoadBalancer: lb}}
	} else {
		if !serviceRe.MatchString(r.Service) {
			return nil, fmt.Errorf("invalid traefik service name %q", r.Service)
		}
		svc = r.Service
		if !strings.Contains(svc, "@") {
			svc += "@docker"
		}
	}
	rule := "Host(`" + r.Hostname + "`)"
	if r.Wildcard {
		rule = "HostRegexp(`^[a-z0-9-]+\\." + regexp.QuoteMeta(r.Hostname) + "$`)"
	}
	rt := router{Rule: rule, EntryPoints: []string{"web"}, Service: svc, Middlewares: []string{RetryMiddleware}}
	if r.TLS {
		if certResolver == "" {
			certResolver = "letsencrypt"
		}
		rt.EntryPoints = []string{"web", "websecure"}
		rt.TLS = &tlsCfg{CertResolver: certResolver}
	}
	doc := dynamicFile{HTTP: httpBlock{Routers: map[string]router{RouterName(r.Hostname, r.Wildcard): rt}, Services: services}}
	return yaml.Marshal(doc)
}

// Write atomically writes the router file; it returns the path and whether it changed.
// RetryMiddleware is the shared retry middleware every router uses: when the chosen backend is gone (a container
// was just stopped and its IP no longer answers) Traefik retries the request on another server instead of failing it.
const RetryMiddleware = "jp-retry@file"

// middlewaresFile is NOT a domain-*.yml file, so Reconcile never treats it as stale.
const middlewaresFile = "jp-middlewares.yml"

const middlewaresYAML = `http:
  middlewares:
    jp-retry:
      retry:
        attempts: 3
        initialInterval: 100ms
`

func (m *Manager) ensureMiddlewaresLocked() error {
	_, _, err := m.writeLocked(middlewaresFile, []byte(middlewaresYAML))
	return err
}

func (m *Manager) Write(r Route) (string, bool, error) {
	body, err := Render(r, m.CertResolver)
	if err != nil {
		return "", false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureMiddlewaresLocked(); err != nil {
		return "", false, err
	}
	return m.writeLocked(FileName(r.Hostname, r.Wildcard), body)
}

func (m *Manager) writeLocked(name string, body []byte) (string, bool, error) {
	if m.Dir == "" {
		return "", false, errors.New("TRAEFIK_DYNAMIC_DIR is not configured")
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return "", false, err
	}
	path := filepath.Join(m.Dir, name)
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, body) {
		return path, false, nil
	}
	tmp, err := os.CreateTemp(m.Dir, ".domain-*.tmp")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", false, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return "", false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return "", false, err
	}
	return path, true, nil
}

// Remove deletes the router file of a hostname (missing file is not an error).
func (m *Manager) Remove(host string, wildcard bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := os.Remove(filepath.Join(m.Dir, FileName(host, wildcard)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReconcileResult summarises a Reconcile run.
type ReconcileResult struct {
	Written   int
	Unchanged int
	Removed   int
	Errors    []string
}

// Reconcile makes the directory contain exactly the files for routes: it writes missing or
// changed files and deletes every other domain-*.yml (including legacy names) and stale temp
// files. Files that do not start with "domain-" (e.g. gateway.yml) are never touched.
func (m *Manager) Reconcile(routes []Route) (ReconcileResult, error) {
	var res ReconcileResult
	if m.Dir == "" {
		return res, errors.New("TRAEFIK_DYNAMIC_DIR is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return res, err
	}
	if err := m.ensureMiddlewaresLocked(); err != nil {
		res.Errors = append(res.Errors, err.Error())
	}
	want := map[string]bool{}
	for _, r := range routes {
		body, err := Render(r, m.CertResolver)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		name := FileName(r.Hostname, r.Wildcard)
		want[name] = true
		_, changed, err := m.writeLocked(name, body)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if changed {
			res.Written++
		} else {
			res.Unchanged++
		}
	}
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		stale := false
		switch {
		case strings.HasPrefix(n, ".domain-") && strings.HasSuffix(n, ".tmp"):
			stale = true
		case strings.HasPrefix(n, "domain-") && (strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml")):
			stale = !want[n] || !fileNameRe.MatchString(n)
		}
		if stale {
			if err := os.Remove(filepath.Join(m.Dir, n)); err != nil && !os.IsNotExist(err) {
				res.Errors = append(res.Errors, err.Error())
				continue
			}
			res.Removed++
		}
	}
	return res, nil
}
