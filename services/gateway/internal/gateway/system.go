package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/middleware"
)

// Check is one real security check.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // pass | warn | fail
	Detail string `json:"detail"`
}

func (s *Server) upstreamGet(ctx context.Context, svc, path string, hdr map[string]string, out any) (int, error) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(s.cfg.Upstreams[svc], "/")+path, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// systemSettings returns the effective, NON-secret platform configuration plus the persisted,
// editable settings (SMTP, AI keys - secret values are never returned).
func (s *Server) systemSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"platform": map[string]any{
			"environment":          s.cfg.Env,
			"public_base_url":      s.cfg.PublicBaseURL,
			"public_dashboard_url": s.cfg.PublicDashboardURL,
			"root_domain":          os.Getenv("ROOT_DOMAIN"),
			"domain_cname_target":  os.Getenv("DOMAIN_CNAME_TARGET"),
			"registration_mode":    s.cfg.RegistrationMode,
			"tls_enabled":          envBool("TLS_ENABLED"),
			"cors_origins":         s.cfg.CORSOrigins,
			"version":              firstNonEmpty(os.Getenv("JP_VERSION"), "unknown"),
		},
		"read_only_note": "Only the 'editable' settings can be changed here; everything under 'platform' comes from deployment configuration.",
	}
	var ident struct {
		Settings     map[string]any `json:"settings"`
		EditableKeys []string       `json:"editable_keys"`
	}
	code, err := s.upstreamGet(r.Context(), "identity", "/system/settings", map[string]string{"Authorization": r.Header.Get("Authorization")}, &ident)
	switch {
	case err != nil || code == 0:
		out["editable_error"] = "identity service unavailable"
	case code != http.StatusOK:
		out["editable_error"] = "identity service returned an error"
	default:
		out["editable"] = ident.Settings
		out["editable_keys"] = ident.EditableKeys
	}
	httpx.JSON(w, http.StatusOK, out)
}

func envBool(k string) bool {
	switch strings.ToLower(os.Getenv(k)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// systemSecurity computes real checks from real state; there are no hard-coded grades.
func (s *Server) systemSecurity(w http.ResponseWriter, r *http.Request) {
	prod := strings.EqualFold(s.cfg.Env, "production")
	var checks []Check
	add := func(id, title, status, detail string) { checks = append(checks, Check{id, title, status, detail}) }

	tok := strings.TrimSpace(os.Getenv("INTERNAL_TOKEN"))
	switch {
	case tok == "":
		add("internal_token", "Service-to-service token", pick(prod, "fail", "warn"), "INTERNAL_TOKEN is not set: /internal/* endpoints are "+pick(prod, "unusable", "open (development mode)"))
	case len(tok) < 32 || distinct(tok) < 8:
		add("internal_token", "Service-to-service token", "warn", "INTERNAL_TOKEN is set but weak (use at least 32 random characters)")
	default:
		add("internal_token", "Service-to-service token", "pass", "INTERNAL_TOKEN is set and strong")
	}

	sec := s.cfg.JWTSecret
	switch {
	case len(sec) < 32 || strings.Contains(sec, "change-me") || distinct(sec) < 8:
		add("jwt_secret", "JWT signing secret", "fail", "JWT_SECRET is short, low-entropy or a known default")
	default:
		add("jwt_secret", "JWT signing secret", "pass", "JWT_SECRET is at least 32 characters")
	}

	add("environment", "Production mode", pick(prod, "pass", "warn"), "JP_ENV="+firstNonEmpty(s.cfg.Env, "(unset)"))

	switch s.cfg.RegistrationMode {
	case "open":
		add("registration", "Registration mode", "warn", "REGISTRATION_MODE=open: anyone who can reach the platform can create an account")
	case "closed", "invite":
		add("registration", "Registration mode", "pass", "REGISTRATION_MODE="+s.cfg.RegistrationMode)
	default:
		add("registration", "Registration mode", "warn", "unknown mode "+s.cfg.RegistrationMode)
	}

	if envBool("TLS_ENABLED") {
		add("tls", "HTTPS / TLS", "pass", "TLS_ENABLED=true (Traefik terminates TLS with ACME certificates)")
	} else {
		add("tls", "HTTPS / TLS", pick(prod, "fail", "warn"), "TLS_ENABLED is not true: traffic, tokens and cookies travel unencrypted")
	}

	wild, local := false, false
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" {
			wild = true
		}
		if strings.Contains(o, "localhost") || strings.Contains(o, "127.0.0.1") {
			local = true
		}
	}
	switch {
	case wild:
		add("cors", "CORS allowlist", "fail", "CORS_ORIGINS contains '*'")
	case local && prod:
		add("cors", "CORS allowlist", "warn", "CORS_ORIGINS contains localhost origins in production")
	default:
		add("cors", "CORS allowlist", "pass", "explicit origin allowlist")
	}

	if len(s.cfg.TrustedProxies) > 0 {
		add("trusted_proxies", "Trusted proxy list", "pass", "TRUSTED_PROXIES configured: client IPs are resolved from X-Forwarded-For only for these peers")
	} else {
		add("trusted_proxies", "Trusted proxy list", "warn", "TRUSTED_PROXIES is empty: all clients appear as the proxy address (per-IP rate limits are shared)")
	}

	switch v := strings.TrimSpace(os.Getenv("INFRA_BIND_ADDR")); {
	case v == "127.0.0.1" || v == "localhost":
		add("infra_ports", "Infrastructure port exposure", "pass", "INFRA_BIND_ADDR="+v+": databases/queues listen on loopback only")
	case v == "":
		add("infra_ports", "Infrastructure port exposure", "warn", "INFRA_BIND_ADDR is not declared; cannot verify that postgres/redis/minio are bound to 127.0.0.1")
	default:
		add("infra_ports", "Infrastructure port exposure", "fail", "INFRA_BIND_ADDR="+v+": infrastructure ports are reachable from the network")
	}

	pctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	rerr := s.d.Redis.Ping(pctx).Err()
	cancel()
	if rerr != nil {
		add("token_revocation", "Token revocation store", "fail", "Redis is unreachable: logged-out tokens cannot be checked")
	} else {
		add("token_revocation", "Token revocation store", "pass", "Redis reachable; revoked access tokens are rejected at the gateway")
	}

	var st struct {
		Users, Admins, MFAEnabled, AdminsMFAEnabled, LegacyPlain int
		Bootstrap                                                bool
		Weak                                                     []string
	}
	var raw struct {
		Users                int      `json:"users"`
		Admins               int      `json:"admins"`
		MFAEnabled           int      `json:"mfa_enabled"`
		AdminsMFAEnabled     int      `json:"admins_mfa_enabled"`
		BootstrapInitialized bool     `json:"bootstrap_initialized"`
		LegacyPlaintextTOTP  int      `json:"legacy_plaintext_totp"`
		AdminsDefaultPass    []string `json:"admins_default_password"`
	}
	code, err := s.upstreamGet(r.Context(), "identity", "/internal/security/stats", nil, &raw)
	if err != nil || code != http.StatusOK {
		add("accounts", "Account security", "warn", "could not read account statistics from the identity service")
	} else {
		st.Users, st.Admins, st.MFAEnabled, st.AdminsMFAEnabled = raw.Users, raw.Admins, raw.MFAEnabled, raw.AdminsMFAEnabled
		st.LegacyPlain, st.Bootstrap, st.Weak = raw.LegacyPlaintextTOTP, raw.BootstrapInitialized, raw.AdminsDefaultPass
		switch {
		case st.Admins == 0:
			add("admin_mfa", "Administrator MFA", "warn", "no platform administrator exists yet")
		case st.AdminsMFAEnabled == st.Admins:
			add("admin_mfa", "Administrator MFA", "pass", "all platform administrators have MFA enabled")
		case st.AdminsMFAEnabled == 0:
			add("admin_mfa", "Administrator MFA", "fail", "no platform administrator has MFA enabled")
		default:
			add("admin_mfa", "Administrator MFA", "warn", "some platform administrators have no MFA")
		}
		add("mfa_adoption", "MFA adoption", pick(st.Users > 0 && st.MFAEnabled > 0, "pass", "warn"), itoa(st.MFAEnabled)+" of "+itoa(st.Users)+" users have MFA enabled")
		if len(st.Weak) > 0 {
			add("default_credentials", "Default credentials", "fail", "administrator account(s) use a guessable password: "+strings.Join(st.Weak, ", "))
		} else {
			add("default_credentials", "Default credentials", "pass", "no administrator uses a known default password")
		}
		add("bootstrap", "Initial setup", pick(st.Bootstrap, "pass", "warn"), pick(st.Bootstrap, "platform is initialised; the bootstrap endpoint is closed", "platform is not initialised: anyone can create the first administrator"))
		if st.LegacyPlain > 0 {
			add("totp_at_rest", "MFA secrets at rest", "fail", itoa(st.LegacyPlain)+" TOTP secret(s) are stored unencrypted")
		} else {
			add("totp_at_rest", "MFA secrets at rest", "pass", "TOTP secrets are AES-GCM encrypted; backup codes are hashed")
		}
	}

	var ident struct {
		Settings map[string]struct {
			Set bool `json:"set"`
		} `json:"settings"`
	}
	smtpOK := os.Getenv("SMTP_HOST") != ""
	if c, e := s.upstreamGet(r.Context(), "identity", "/system/settings", map[string]string{"Authorization": r.Header.Get("Authorization")}, &ident); e == nil && c == http.StatusOK {
		smtpOK = smtpOK || ident.Settings["smtp_host"].Set
	}
	add("smtp", "Outgoing e-mail (invitations)", pick(smtpOK, "pass", "warn"), pick(smtpOK, "SMTP is configured", "SMTP is not configured: invitation e-mails cannot be delivered"))

	sum := map[string]int{"pass": 0, "warn": 0, "fail": 0}
	for _, c := range checks {
		sum[c.Status]++
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"checks": checks, "summary": sum, "generated_at": time.Now().UTC()})
}

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func itoa(n int) string { return strconv.Itoa(n) }

func distinct(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}

// ---------------------------------------------------------------- cluster

type nodeInfo struct {
	ID         string         `json:"id"`
	Hostname   string         `json:"hostname"`
	Role       string         `json:"role"`
	Status     string         `json:"status"`
	OS         string         `json:"os"`
	Arch       string         `json:"arch"`
	CPU        map[string]any `json:"cpu"`
	Memory     map[string]any `json:"memory"`
	Disk       map[string]any `json:"disk"`
	Containers map[string]any `json:"containers"`

	// Flat copies of the figures above, in the shape the dashboard's System health page reads.
	CPUCount          int       `json:"cpu_count"`
	CPUPercent        float64   `json:"cpu_percent"`
	Load              []float64 `json:"load,omitempty"`
	MemoryTotalBytes  uint64    `json:"memory_total_bytes"`
	MemoryUsedBytes   uint64    `json:"memory_used_bytes"`
	DiskTotalBytes    uint64    `json:"disk_total_bytes"`
	DiskUsedBytes     uint64    `json:"disk_used_bytes"`
	UptimeSeconds     uint64    `json:"uptime_seconds"`
	DockerVersion     string    `json:"docker_version,omitempty"`
	ContainersRunning *int      `json:"containers_running,omitempty"`
	ContainersTotal   *int      `json:"containers_total,omitempty"`
	ContainersStopped *int      `json:"containers_stopped,omitempty"`
	Images            *int      `json:"images,omitempty"`
}

// flatten copies the nested host figures into the flat fields.
func (n *nodeInfo) flatten() {
	u64 := func(m map[string]any, k string) uint64 { v, _ := m[k].(uint64); return v }
	f64 := func(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }
	n.CPUCount, _ = n.CPU["cores"].(int)
	n.CPUPercent = f64(n.CPU, "usage_percent")
	if _, ok := n.CPU["load1"]; ok {
		n.Load = []float64{f64(n.CPU, "load1"), f64(n.CPU, "load5"), f64(n.CPU, "load15")}
	}
	n.MemoryTotalBytes, n.MemoryUsedBytes = u64(n.Memory, "total_bytes"), u64(n.Memory, "used_bytes")
	n.DiskTotalBytes, n.DiskUsedBytes = u64(n.Disk, "total_bytes"), u64(n.Disk, "used_bytes")
}

func (s *Server) localNode(ctx context.Context) nodeInfo {
	host, _ := os.Hostname()
	n := nodeInfo{ID: host, Hostname: host, Role: "all-in-one", Status: "ready", OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPU: map[string]any{"cores": runtime.NumCPU()}, Memory: map[string]any{}, Disk: map[string]any{}, Containers: map[string]any{}}
	collectHost(&n)

	var st struct {
		Running int   `json:"containers_running"`
		Total   int   `json:"containers_total"`
		CPUm    int64 `json:"cpu_millicores_used"`
		Mem     int64 `json:"mem_used_bytes"`

		DockerVersion string `json:"docker_version"`
		HostTotal     int    `json:"containers_total_host"`
		HostRunning   int    `json:"containers_running_host"`
		Images        int    `json:"images"`
	}
	code, err := s.upstreamGet(ctx, "runtime", "/internal/runtime/stats", nil, &st)
	if err != nil || code != http.StatusOK {
		n.Containers["error"] = "runtime service did not return container statistics"
	} else {
		n.Containers["running"], n.Containers["total"] = st.Running, st.Total
		n.Containers["cpu_millicores_used"], n.Containers["mem_used_bytes"] = st.CPUm, st.Mem
		// System health shows every container on the host (platform services included), not just tenant ones.
		stopped := st.HostTotal - st.HostRunning
		n.DockerVersion, n.ContainersRunning, n.ContainersTotal, n.ContainersStopped, n.Images =
			st.DockerVersion, &st.HostRunning, &st.HostTotal, &stopped, &st.Images
	}
	n.flatten()
	return n
}

// systemCluster reports the single, real local node. Multi-node clustering is not implemented.
func (s *Server) systemCluster(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"mode": "single-node",
		"note": "JP Cloud runs on one node. Multi-node scheduling is not implemented.",
		"node": s.localNode(r.Context()),
	})
}

// clusterNodes keeps the legacy list shape with the one real node.
func (s *Server) clusterNodes(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"mode": "single-node", "nodes": []nodeInfo{s.localNode(r.Context())}})
}

// maintenanceCleanup asks the runtime service to prune jp-managed dangling images/containers.
func (s *Server) maintenanceCleanup(w http.ResponseWriter, r *http.Request) {
	c, _ := middleware.ClaimsFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.Upstreams["runtime"], "/")+"/internal/runtime/prune", strings.NewReader("{}"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "runtime service unavailable")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "runtime prune failed")
		return
	}
	s.d.Log.Info("maintenance cleanup executed", "by", c.UserID)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
