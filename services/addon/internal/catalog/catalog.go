// Package catalog is the IMMUTABLE marketplace catalog. It is built once at start-up (built-in
// entries plus an optional operator-controlled ADDON_CATALOG_DIR); request handlers can only read it.
// Custom (user supplied) images never touch the catalog: they are stored per project.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Probe describes how readiness is checked (evaluated by the runtime on the platform side).
type Probe struct {
	Type string   `json:"type"` // tcp | http | cmd
	Path string   `json:"path,omitempty"`
	Port int      `json:"port,omitempty"`
	Cmd  []string `json:"cmd,omitempty"`
}

// Item describes a one-click add-on engine in the marketplace catalog.
type Item struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Description  string   `json:"description"`
	Logo         string   `json:"logo,omitempty"`
	DefaultPort  int      `json:"default_port,omitempty"`
	Scheme       string   `json:"scheme,omitempty"`
	EnvPrefix    string   `json:"env_prefix,omitempty"`
	DefaultImage string   `json:"default_image,omitempty"`
	SecretKeys   []string `json:"secret_keys"`

	// Provisioning (set by finalize / ADDON_CATALOG_DIR files).
	Driver        string `json:"driver,omitempty"`
	Volume        string `json:"volume,omitempty"`
	Probe         *Probe `json:"probe,omitempty"`
	Provisionable bool   `json:"provisionable"`
	Reason        string `json:"reason,omitempty"` // why not provisionable
	ArchOK        bool   `json:"arch_ok"`          // image verified for linux/amd64 AND linux/arm64
	Arch          string `json:"arch,omitempty"`   // e.g. "amd64,arm64"
	Backup        bool   `json:"backup_supported"`
	Restore       bool   `json:"restore_supported"`
	NoAuth        bool   `json:"no_auth,omitempty"` // engine has no credentials (private network only)
	MinMemoryMB   int    `json:"min_memory_mb,omitempty"`
}

type archInfo struct {
	Checked bool
	AMD64   bool
	ARM64   bool
	Err     string
}

var (
	mu       sync.RWMutex
	registry = map[string]Item{}
	order    []string
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
)

func init() {
	for _, it := range defaultItems() {
		add(finalize(it))
	}
}

func add(it Item) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[it.ID]; !ok {
		order = append(order, it.ID)
	}
	registry[it.ID] = it
}

// Get returns the catalog item for an engine ID.
func Get(id string) (Item, bool) {
	mu.RLock()
	defer mu.RUnlock()
	it, ok := registry[strings.ToLower(strings.TrimSpace(id))]
	return it, ok
}

// All returns a copy of the catalog in registration order.
func All() []Item {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Item, 0, len(order))
	for _, id := range order {
		out = append(out, registry[id])
	}
	return out
}

// ValidEngine reports whether engine is in the catalog.
func ValidEngine(engine string) bool { _, ok := Get(engine); return ok }

// Counts returns total / provisionable entries.
func Counts() (total, provisionable int) {
	for _, it := range All() {
		total++
		if it.Provisionable {
			provisionable++
		}
	}
	return
}

// SecretName builds the per-add-on unique secret key (<PREFIX>_<NAME>), e.g. POSTGRES_URL_PRIMARY.
func SecretName(engine, name string) string {
	safe := sanitizeName(name)
	if it, ok := Get(engine); ok && it.EnvPrefix != "" {
		return strings.TrimRight(it.EnvPrefix, "_") + "_" + safe
	}
	return fmt.Sprintf("%s_URL_%s", strings.ToUpper(sanitizeName(engine)), safe)
}

// LoadCatalogDir loads operator-provided catalog JSON files at START-UP ONLY (the directory is on the
// platform host, not user controlled). Entries must carry a complete spec to be provisionable;
// built-in entries cannot be overridden. Invalid files are reported, never silently half-applied.
func LoadCatalogDir(dir string) (loaded int, err error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, e.Name()+": "+err.Error())
			continue
		}
		var list []Item
		var single Item
		if json.Unmarshal(raw, &single) == nil && single.ID != "" {
			list = []Item{single}
		} else if err := json.Unmarshal(raw, &list); err != nil {
			errs = append(errs, e.Name()+": "+err.Error())
			continue
		}
		for _, it := range list {
			it.ID = strings.ToLower(strings.TrimSpace(it.ID))
			if !idRe.MatchString(it.ID) {
				errs = append(errs, e.Name()+": invalid id "+it.ID)
				continue
			}
			if _, exists := Get(it.ID); exists {
				errs = append(errs, e.Name()+": "+it.ID+" is built in and cannot be overridden")
				continue
			}
			if it.Provisionable {
				if msg := validateProvisionable(&it); msg != "" {
					it.Provisionable, it.Reason = false, msg
				}
			} else if it.Reason == "" {
				it.Reason = ReasonUnsupported
			}
			if it.Driver == "" {
				it.Driver = "generic"
			}
			add(it)
			loaded++
		}
	}
	if len(errs) > 0 {
		return loaded, fmt.Errorf("catalog dir: %s", strings.Join(errs, "; "))
	}
	return loaded, nil
}

func validateProvisionable(it *Item) string {
	switch {
	case it.DefaultImage == "":
		return "no image"
	case it.DefaultPort <= 0 || it.DefaultPort > 65535:
		return "invalid port"
	case it.Probe == nil || (it.Probe.Type != "tcp" && it.Probe.Type != "http" && it.Probe.Type != "cmd"):
		return "no readiness probe"
	case it.Volume != "" && !strings.HasPrefix(it.Volume, "/"):
		return "volume must be an absolute path"
	}
	return ""
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "APP"
	}
	return b.String()
}
