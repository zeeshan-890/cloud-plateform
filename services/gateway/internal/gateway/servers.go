package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
)

// Server inventory: the servers of an installation, how to reach each one (public / private address), which
// path to prefer and which ports are meant to be open to which network. This is configuration only; the platform
// itself still runs on one node (see docs/ROADMAP.md). It lives in Redis as one hash so it survives restarts.
const serversKey = "jp:cluster:servers"

type serverPort struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // tcp | udp
	Scope string `json:"scope"` // private | public | both
	Label string `json:"label"`
}

type serverEntry struct {
	Name        string       `json:"name"`
	Role        string       `json:"role"` // control-plane | database | api | worker | storage | other
	PublicAddr  string       `json:"public_addr"`
	PrivateAddr string       `json:"private_addr"`
	Prefer      string       `json:"prefer"` // private | public | auto
	Ports       []serverPort `json:"ports"`
	Notes       string       `json:"notes"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Report      *joinReport  `json:"report,omitempty"`
	// Node is true once this server runs the node agent and is registered with the runtime, so projects can be
	// placed on it. It is set only by the join flow, never by a client edit.
	Node bool `json:"node"`
	// NodeWanted remembers that this server was asked to run projects, so re-running the join command after a failed
	// registration (for example a blocked port) finishes the job without the operator repeating the choice.
	NodeWanted bool `json:"node_wanted"`
}

var (
	srvNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	srvHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	srvRoles  = map[string]bool{"control-plane": true, "database": true, "api": true, "worker": true, "storage": true, "other": true}
)

// validateServer checks and normalises an entry. lenient (used for servers created by the join command) allows
// entries whose addresses are not known yet or whose preferred path has no address: those are reported as findings
// by diagnose() instead of being refused.
func validateServer(e *serverEntry, lenient bool) error {
	e.Name = strings.ToLower(strings.TrimSpace(e.Name))
	e.PublicAddr = strings.TrimSpace(e.PublicAddr)
	e.PrivateAddr = strings.TrimSpace(e.PrivateAddr)
	e.Notes = strings.TrimSpace(e.Notes)
	if !srvNameRe.MatchString(e.Name) {
		return errors.New("name must be 1-32 characters: lowercase letters, digits and hyphens")
	}
	if e.Role == "" {
		e.Role = "other"
	}
	if !srvRoles[e.Role] {
		return errors.New("role must be control-plane, database, api, worker, storage or other")
	}
	if e.Prefer == "" {
		e.Prefer = "private"
	}
	if e.Prefer != "private" && e.Prefer != "public" && e.Prefer != "auto" {
		return errors.New("prefer must be private, public or auto")
	}
	if !lenient && e.PublicAddr == "" && e.PrivateAddr == "" {
		return errors.New("a server needs a public or a private address")
	}
	for _, h := range []string{e.PublicAddr, e.PrivateAddr} {
		if h != "" && net.ParseIP(h) == nil && !srvHostRe.MatchString(h) {
			return errors.New("invalid address " + h)
		}
	}
	if !lenient && e.Prefer == "private" && e.PrivateAddr == "" {
		return errors.New("prefer=private needs a private address")
	}
	if !lenient && e.Prefer == "public" && e.PublicAddr == "" {
		return errors.New("prefer=public needs a public address")
	}
	if len(e.Notes) > 500 {
		return errors.New("notes are limited to 500 characters")
	}
	if len(e.Ports) > 64 {
		return errors.New("at most 64 ports per server")
	}
	seen := map[string]bool{}
	for i := range e.Ports {
		p := &e.Ports[i]
		if p.Proto == "" {
			p.Proto = "tcp"
		}
		if p.Scope == "" {
			p.Scope = "private"
		}
		p.Label = strings.TrimSpace(p.Label)
		if p.Port < 1 || p.Port > 65535 {
			return errors.New("port must be 1-65535")
		}
		if p.Proto != "tcp" && p.Proto != "udp" {
			return errors.New("port protocol must be tcp or udp")
		}
		if p.Scope != "private" && p.Scope != "public" && p.Scope != "both" {
			return errors.New("port scope must be private, public or both")
		}
		if !lenient && p.Scope != "private" && e.PublicAddr == "" {
			return errors.New("a public port needs a public address")
		}
		if len(p.Label) > 60 {
			return errors.New("port labels are limited to 60 characters")
		}
		k := p.Proto + "/" + strconv.Itoa(p.Port)
		if seen[k] {
			return errors.New("duplicate port")
		}
		seen[k] = true
	}
	sort.Slice(e.Ports, func(i, j int) bool { return e.Ports[i].Port < e.Ports[j].Port })
	return nil
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	out, err := s.allServers(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "server inventory is unavailable")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"servers": out})
}

func (s *Server) putServer(w http.ResponseWriter, r *http.Request) {
	var e serverEntry
	if err := httpx.DecodeLimit(r, &e, 64<<10); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if e.Name == "" {
		e.Name = r.PathValue("name")
	}
	if strings.ToLower(e.Name) != r.PathValue("name") {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "name in the body must match the URL")
		return
	}
	if err := validateServer(&e, false); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	n, err := s.d.Redis.HLen(r.Context(), serversKey).Result()
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "server inventory is unavailable")
		return
	}
	if n >= 32 {
		if ex, _ := s.d.Redis.HExists(r.Context(), serversKey, e.Name).Result(); !ex {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "at most 32 servers")
			return
		}
	}
	e.UpdatedAt = time.Now().UTC()
	e.normalize()
	e.Report, e.Node, e.NodeWanted = nil, false, false
	if old, _ := s.getServer(r.Context(), e.Name); old != nil {
		e.Report = old.Report // editing the configuration must not erase the last connectivity report
		e.Node, e.NodeWanted = old.Node, old.NodeWanted
	}
	if err := s.saveServer(r.Context(), e); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not save the server")
		return
	}
	httpx.JSON(w, http.StatusOK, e)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	n, err := s.d.Redis.HDel(r.Context(), serversKey, r.PathValue("name")).Result()
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "server inventory is unavailable")
		return
	}
	if n == 0 {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalize makes sure list fields are never JSON null (clients iterate over them).
func (e *serverEntry) normalize() {
	if e.Ports == nil {
		e.Ports = []serverPort{}
	}
	if e.Report != nil {
		if e.Report.Checks == nil {
			e.Report.Checks = []checkResult{}
		}
		if e.Report.Inbound == nil {
			e.Report.Inbound = []checkResult{}
		}
		if e.Report.Diagnostics == nil {
			e.Report.Diagnostics = []diagnostic{}
		}
	}
}

func (s *Server) allServers(ctx context.Context) ([]serverEntry, error) {
	m, err := s.d.Redis.HGetAll(ctx, serversKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]serverEntry, 0, len(m))
	for _, v := range m {
		var e serverEntry
		if json.Unmarshal([]byte(v), &e) == nil {
			e.normalize()
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Server) getServer(ctx context.Context, name string) (*serverEntry, error) {
	v, err := s.d.Redis.HGet(ctx, serversKey, name).Result()
	if err != nil {
		return nil, err
	}
	var e serverEntry
	if err := json.Unmarshal([]byte(v), &e); err != nil {
		return nil, err
	}
	e.normalize()
	return &e, nil
}

func (s *Server) saveServer(ctx context.Context, e serverEntry) error {
	b, _ := json.Marshal(e)
	return s.d.Redis.HSet(ctx, serversKey, e.Name, string(b)).Err()
}
