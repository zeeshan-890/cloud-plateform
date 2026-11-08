package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/gateway/internal/proxy"
	"github.com/jp-cloud/go-common/httpx"
)

// One-command server join.
//
// An admin creates a short-lived, single-use join token in the dashboard and gets a one-line command. Running it on
// the new server downloads a script that (1) reads the server's facts, (2) opens the planned ports in the server's own
// firewall, (3) tests every peer port over the private and the public network and (4) reports back. The gateway then
// dials the new server's ports itself and turns everything into plain-language findings (diagnose.go).
// There is no resident agent: the checks run when the command is run, or when "Check now" is pressed.

// dialTimeout bounds each control-plane -> server connection attempt (a variable so tests stay fast).
var dialTimeout = 3 * time.Second

const (
	joinPrefix = "jp:cluster:join:"
	joinTTL    = time.Hour
)

type joinToken struct {
	Name     string       `json:"name"`
	Role     string       `json:"role"`
	Prefer   string       `json:"prefer"`
	Ports    []serverPort `json:"ports"`
	Existing bool         `json:"existing"`
	Created  time.Time    `json:"created"`
	// Node: also install the node agent so projects can run on this server. AgentToken is the secret the control
	// plane and the agent share; it is generated per join (re-running the command rotates it).
	Node       bool   `json:"node"`
	AgentToken string `json:"agent_token,omitempty"`
}

const agentPort = 2377

func tokenKey(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return joinPrefix + hex.EncodeToString(h[:])
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{40,60}$`)

func (s *Server) loadToken(ctx context.Context, tok string) (*joinToken, bool) {
	if !tokenRe.MatchString(tok) {
		return nil, false
	}
	v, err := s.d.Redis.Get(ctx, tokenKey(tok)).Result()
	if err != nil {
		return nil, false
	}
	var jt joinToken
	if json.Unmarshal([]byte(v), &jt) != nil {
		return nil, false
	}
	return &jt, true
}

func (s *Server) baseURL(r *http.Request) string {
	if b := strings.TrimRight(s.cfg.PublicBaseURL, "/"); b != "" {
		return b
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---------------------------------------------------------------- admin: create a token

func (s *Server) createJoinToken(w http.ResponseWriter, r *http.Request) {
	var in serverEntry
	if err := httpx.DecodeLimit(r, &in, 64<<10); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	jt := joinToken{Created: time.Now().UTC()}
	if ex, err := s.getServer(r.Context(), in.Name); err == nil && ex != nil {
		jt.Existing, jt.Name = true, ex.Name
		jt.Node = ex.Node || ex.NodeWanted || in.Node
	} else {
		jt.Node = in.Node
		if err := validateServer(&in, true); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		jt.Name, jt.Role, jt.Prefer, jt.Ports = in.Name, in.Role, in.Prefer, in.Ports
	}
	if jt.Node {
		if s.runtimeURL() == "" {
			httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "the runtime service is not configured, so servers cannot run projects")
			return
		}
		jt.AgentToken = newToken() + newToken()[:8] // 51 chars, well above the agent's 32 minimum
	}
	tok := newToken()
	b, _ := json.Marshal(jt)
	if err := s.d.Redis.Set(r.Context(), tokenKey(tok), string(b), joinTTL).Err(); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not create a join token")
		return
	}
	base := s.baseURL(r)
	url := base + "/api/v1/cluster/join/" + tok + "/script"
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"token":      tok,
		"name":       jt.Name,
		"existing":   jt.Existing,
		"node":       jt.Node,
		"expires_at": time.Now().Add(joinTTL).UTC(),
		"command":    "curl -fsSL " + url + " | sudo bash",
		"script_url": url,
	})
}

// ---------------------------------------------------------------- public (token-gated)

func (s *Server) joinScript(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if _, ok := s.loadToken(r.Context(), tok); !ok {
		http.Error(w, "echo 'This join command has expired or is invalid. Create a new one in the control panel.' >&2; exit 1\n", http.StatusNotFound)
		return
	}
	body := strings.NewReplacer("__BASE__", s.baseURL(r), "__TOKEN__", tok).Replace(joinScriptTemplate)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

// joinPlan is a line-oriented plan the shell script can parse without jq.
func (s *Server) joinPlan(w http.ResponseWriter, r *http.Request) {
	jt, ok := s.loadToken(r.Context(), r.PathValue("token"))
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "join command expired or invalid")
		return
	}
	self := serverEntry{Name: jt.Name, Ports: jt.Ports}
	if jt.Existing {
		if ex, err := s.getServer(r.Context(), jt.Name); err == nil && ex != nil {
			self = *ex
		}
	}
	peers, _ := s.allServers(r.Context())
	var b strings.Builder
	fmt.Fprintf(&b, "name=%s\n", self.Name)
	for _, p := range self.Ports {
		fmt.Fprintf(&b, "open=%d %s\n", p.Port, p.Scope)
	}
	if jt.Node {
		fmt.Fprintf(&b, "node=1\nagent_token=%s\nagent_port=%d\nagent_url=%s\n", jt.AgentToken, agentPort, s.baseURL(r)+"/api/v1/cluster/join/"+r.PathValue("token")+"/agent")
		if v := strings.TrimSpace(os.Getenv("CONTROL_PLANE_PRIVATE_IP")); net.ParseIP(v) != nil {
			fmt.Fprintf(&b, "agent_source=%s\n", v)
		}
		if v := strings.TrimSpace(os.Getenv("NODE_REGISTRY_UPSTREAM")); v != "" {
			fmt.Fprintf(&b, "registry=%s\n", v)
		}
	}
	for _, p := range peers {
		if p.Name == self.Name {
			continue
		}
		for _, pp := range p.Ports {
			if p.PrivateAddr != "" {
				fmt.Fprintf(&b, "test=%s private %s %d\n", p.Name, p.PrivateAddr, pp.Port)
			}
			if p.PublicAddr != "" && pp.Scope != "private" {
				fmt.Fprintf(&b, "test=%s public %s %d\n", p.Name, p.PublicAddr, pp.Port)
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

type reportIn struct {
	Hostname    string        `json:"hostname"`
	PrivateIP   string        `json:"private_ip"`
	PrivateCIDR string        `json:"private_cidr"`
	Provider    string        `json:"provider"`
	OS          string        `json:"os"`
	Firewall    string        `json:"firewall"`
	Docker      bool          `json:"docker"`
	Opened      []int         `json:"opened_ports"`
	Listening   []int         `json:"listening_ports"`
	Checks      []checkResult `json:"checks"`
}

var cleanRe = regexp.MustCompile(`[^A-Za-z0-9 ._:()/+-]`)

func clean(s string, max int) string {
	s = cleanRe.ReplaceAllString(s, "")
	if len(s) > max {
		s = s[:max]
	}
	return strings.TrimSpace(s)
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 || (ip4[0] == 172 && ip4[1]&0xf0 == 16) || (ip4[0] == 192 && ip4[1] == 168) || (ip4[0] == 100 && ip4[1]&0xc0 == 64)
	}
	return len(ip) == 16 && ip[0]&0xfe == 0xfc
}

// dialable reports whether the control plane may open a connection to this address. Loopback, link-local (which
// includes cloud metadata endpoints), multicast and unspecified addresses never are.
func dialable(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

func ports(in []int) []int {
	out := []int{}
	seen := map[int]bool{}
	for _, p := range in {
		if p >= 1 && p <= 65535 && !seen[p] && len(out) < 256 {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

func (s *Server) joinReport(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	jt, ok := s.loadToken(r.Context(), tok)
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "This join command expired or was already used. Create a new one in the control panel (use Re-check on an existing server).")
		return
	}
	var in reportIn
	if err := httpx.DecodeLimit(r, &in, 256<<10); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid report")
		return
	}
	rep := &joinReport{At: time.Now().UTC(), Hostname: clean(in.Hostname, 63), Provider: clean(in.Provider, 20), OS: clean(in.OS, 60),
		Firewall: clean(in.Firewall, 20), Docker: in.Docker, Opened: ports(in.Opened), Listening: ports(in.Listening)}
	if ip := net.ParseIP(strings.TrimSpace(in.PrivateIP)); isPrivateIP(ip) {
		rep.PrivateIP = ip.String()
		if _, n, err := net.ParseCIDR(strings.TrimSpace(in.PrivateCIDR)); err == nil && n.Contains(ip) {
			rep.PrivateCIDR = n.String()
		}
	}
	if ip := net.ParseIP(proxy.ClientIPFrom(r.Context())); ip != nil && !isPrivateIP(ip) && dialable(ip) {
		rep.PublicIP = ip.String()
	}
	for _, c := range in.Checks {
		if len(rep.Checks) >= 256 {
			break
		}
		c.Peer, c.Error = clean(c.Peer, 32), clean(c.Error, 40)
		if net.ParseIP(c.Addr) == nil && !srvHostRe.MatchString(c.Addr) {
			continue
		}
		if (c.Path != "private" && c.Path != "public") || c.Port < 1 || c.Port > 65535 || c.Ms < 0 || c.Ms > 60000 {
			continue
		}
		rep.Checks = append(rep.Checks, c)
	}

	var self serverEntry
	if jt.Existing {
		ex, err := s.getServer(r.Context(), jt.Name)
		if err != nil || ex == nil {
			httpx.Error(w, http.StatusNotFound, "not_found", "that server no longer exists")
			return
		}
		self = *ex
		if self.PublicAddr == "" {
			self.PublicAddr = rep.PublicIP
		}
		if self.PrivateAddr == "" {
			self.PrivateAddr = rep.PrivateIP
		}
	} else {
		self = serverEntry{Name: jt.Name, Role: jt.Role, Prefer: jt.Prefer, Ports: jt.Ports, PublicAddr: rep.PublicIP, PrivateAddr: rep.PrivateIP}
		if self.PublicAddr == "" && self.PrivateAddr == "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "could not determine this server's address")
			return
		}
		if err := validateServer(&self, true); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	peers, _ := s.allServers(r.Context())
	others := peers[:0:0]
	for _, p := range peers {
		if p.Name != self.Name {
			others = append(others, p)
		}
	}
	rep.Inbound = dialServer(r.Context(), self)
	rep.Diagnostics = diagnose(self, rep, others)
	if jt.Node {
		self.NodeWanted = true
		rep.Diagnostics = append(rep.Diagnostics, s.registerNode(r.Context(), &self, rep, jt.AgentToken)...)
	}
	rep.Status = summarize(rep.Diagnostics)
	self.Report = rep
	self.UpdatedAt = time.Now().UTC()
	self.normalize()
	if err := s.saveServer(r.Context(), self); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not save the server")
		return
	}
	_ = s.d.Redis.Del(r.Context(), tokenKey(tok)).Err() // single use
	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(textReport(self.Name, rep)))
		return
	}
	httpx.JSON(w, http.StatusOK, self)
}

// ---------------------------------------------------------------- node agent

// joinAgent serves the node agent binary built into this gateway image. The join token is the only credential.
func (s *Server) joinAgent(w http.ResponseWriter, r *http.Request) {
	jt, ok := s.loadToken(r.Context(), r.PathValue("token"))
	if !ok || !jt.Node {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	arch := r.URL.Query().Get("arch")
	if arch != "amd64" && arch != "arm64" {
		http.Error(w, "arch must be amd64 or arm64", http.StatusBadRequest)
		return
	}
	dir := os.Getenv("NODE_AGENT_DIR")
	if dir == "" {
		dir = "/app/agents"
	}
	f, err := os.Open(filepath.Join(dir, "node-linux-"+arch))
	if err != nil {
		http.Error(w, "this installation has no node agent build for "+arch, http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

func (s *Server) runtimeURL() string { return strings.TrimRight(s.cfg.Upstreams["runtime"], "/") }

// registerNode tells the runtime about the new server (it proves the agent answers before saving anything) and
// returns the findings. On success the server is marked as able to run projects.
func (s *Server) registerNode(ctx context.Context, self *serverEntry, rep *joinReport, agentToken string) []diagnostic {
	var out []diagnostic
	if rep.PrivateIP == "" {
		return []diagnostic{{Level: "error", Code: "node_no_private_ip", Message: "This server has no private address, so projects cannot be placed on it.",
			Fix: "Put it on the same private network as the control plane, then run the command again."}}
	}
	body, _ := json.Marshal(map[string]any{"addr": net.JoinHostPort(rep.PrivateIP, strconv.Itoa(agentPort)), "private_ip": rep.PrivateIP, "token": agentToken})
	code, resp, err := s.runtimeCall(ctx, http.MethodPut, "/internal/runtime/nodes/"+self.Name, body)
	if err != nil || code != http.StatusOK {
		msg := "the runtime could not be asked to register this server"
		if err == nil {
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(resp, &e)
			if e.Error.Message != "" {
				msg = e.Error.Message
			}
		}
		self.Node = false
		return []diagnostic{{Level: "error", Code: "node_register_failed", Message: "Could not register this server to run projects: " + msg,
			Fix: "Make sure the node agent is running on this server and that TCP " + strconv.Itoa(agentPort) + " is open from the control plane (" + cloudFixShort(rep.Provider) + "), then run the command again."}}
	}
	self.Node = true
	var v struct {
		DockerVer string `json:"docker_version"`
		Arch      string `json:"architecture"`
		Control   string `json:"control_architecture"`
		CPUs      int    `json:"cpus"`
		Mem       int64  `json:"memory_total_bytes"`
		Latency   int64  `json:"latency_ms"`
	}
	_ = json.Unmarshal(resp, &v)
	out = append(out, diagnostic{Level: "ok", Code: "node_registered", Message: fmt.Sprintf("Registered: projects can now run on this server (Docker %s, %d CPU, %d MB RAM, agent answered in %d ms).", v.DockerVer, v.CPUs, v.Mem>>20, v.Latency)})
	if v.Arch != "" && v.Control != "" && v.Arch != v.Control {
		out = append(out, diagnostic{Level: "warn", Code: "node_arch_mismatch",
			Message: fmt.Sprintf("This server is %s but the control plane is %s. Images the platform builds run only on the control plane's architecture, so projects built here will fail to start on this server.", v.Arch, v.Control),
			Fix:     "Use servers with the same CPU architecture as the control plane for projects built on the platform, or deploy multi-architecture images."})
	}
	out = append(out, diagnostic{Level: "info", Code: "node_app_ports", Message: "Apps on this server are published on its private address, TCP 32768-60999, and the control plane connects to them there.",
		Fix: "If a deployment later fails its health check with a connection error, open TCP 32768-60999 from the control plane in " + cloudFixShort(rep.Provider) + "."})
	return out
}

func cloudFixShort(p string) string {
	switch p {
	case "oracle":
		return "the Oracle Security List or a Network Security Group"
	case "aws":
		return "the AWS Security Group"
	case "gcp":
		return "a Google Cloud VPC firewall rule"
	case "azure":
		return "the Azure Network Security Group"
	}
	return "your cloud firewall / security group"
}

// runtimeCall performs an internal call to the runtime service.
func (s *Server) runtimeCall(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if s.runtimeURL() == "" {
		return 0, nil, errors.New("runtime not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(cctx, method, s.runtimeURL()+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

// passRuntime forwards an admin request to a runtime internal route and relays the answer.
func (s *Server) passRuntime(method string, path func(r *http.Request) string, readBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if readBody {
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "invalid_request", "invalid body")
				return
			}
			body = b
		}
		code, resp, err := s.runtimeCall(r.Context(), method, path(r), body)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "runtime service unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(resp)
	}
}

// deleteNode removes the node from the runtime and clears the node flag in the inventory.
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	code, resp, err := s.runtimeCall(r.Context(), http.MethodDelete, "/internal/runtime/nodes/"+name, nil)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "runtime service unavailable")
		return
	}
	if code == http.StatusNoContent {
		if e, err := s.getServer(r.Context(), name); err == nil && e != nil && (e.Node || e.NodeWanted) {
			e.Node, e.NodeWanted = false, false
			_ = s.saveServer(r.Context(), *e)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}

// ---------------------------------------------------------------- admin: check now

func (s *Server) checkServer(w http.ResponseWriter, r *http.Request) {
	self, err := s.getServer(r.Context(), r.PathValue("name"))
	if err != nil || self == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	rep := self.Report
	if rep == nil {
		rep = &joinReport{}
	}
	rep.At = time.Now().UTC()
	rep.Inbound = dialServer(r.Context(), *self)
	peers, _ := s.allServers(r.Context())
	others := peers[:0:0]
	for _, p := range peers {
		if p.Name != self.Name {
			others = append(others, p)
		}
	}
	rep.Diagnostics = diagnose(*self, rep, others)
	rep.Status = summarize(rep.Diagnostics)
	self.Report = rep
	if err := s.saveServer(r.Context(), *self); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "could not save the result")
		return
	}
	httpx.JSON(w, http.StatusOK, self)
}

// dialServer connects from the control plane to every configured port of a server, over each address that port is
// meant to be open on.
func dialServer(ctx context.Context, e serverEntry) []checkResult {
	type job struct {
		path, addr string
		port       int
	}
	var jobs []job
	for _, p := range e.Ports {
		if e.PrivateAddr != "" {
			jobs = append(jobs, job{"private", e.PrivateAddr, p.Port})
		}
		if e.PublicAddr != "" && p.Scope != "private" {
			jobs = append(jobs, job{"public", e.PublicAddr, p.Port})
		}
	}
	res := make([]checkResult, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res[i] = dialOnce(ctx, e.Name, j.path, j.addr, j.port)
		}(i, j)
	}
	wg.Wait()
	return res
}

func dialOnce(ctx context.Context, name, path, addr string, port int) checkResult {
	c := checkResult{Peer: name, Path: path, Addr: addr, Port: port}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, addr)
	if err != nil || len(ips) == 0 {
		c.Error = "dns"
		return c
	}
	for _, ia := range ips {
		if !dialable(ia.IP) {
			c.Error = "address not allowed"
			return c
		}
	}
	d := net.Dialer{Timeout: dialTimeout}
	st := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ips[0].IP.String(), fmt.Sprint(port)))
	if err != nil {
		c.Error = classifyDial(err)
		return c
	}
	_ = conn.Close()
	c.OK, c.Ms = true, float64(time.Since(st).Microseconds())/1000
	return c
}

func classifyDial(err error) string {
	var ne net.Error
	switch {
	case strings.Contains(err.Error(), "refused"):
		return "refused"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "unreachable"
}
