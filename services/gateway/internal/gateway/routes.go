package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/membership"
)

// Route is one public API route: "METHOD /path" (relative to /api/v1) served by an upstream.
type Route struct {
	Upstream string
	Pattern  string
	o        opts
}

var (
	pub      = opts{public: true}
	long     = opts{timeout: 10 * time.Minute}
	streamed = opts{stream: true}
	upload   = opts{timeout: 30 * time.Minute, maxBody: 1 << 30}
	// streamUp: unbuffered upload, no deadline; the body cap still applies.
	streamUp = opts{stream: true, maxBody: 1 << 30}
)

func rl(name string, limit int64, window time.Duration) opts {
	return opts{public: true, rlName: name, limit: limit, window: window}
}

func r(up, pattern string, o ...opts) Route {
	rt := Route{Upstream: up, Pattern: pattern}
	if len(o) > 0 {
		rt.o = o[0]
	}
	return rt
}

// leave lets a plain viewer call the route (service enforces who may do what).
var leave = opts{minRole: membership.Viewer}

const (
	org  = "/orgs/{orgId}"
	proj = "/orgs/{orgId}/projects/{projectId}"
)

// Routes is the complete public surface. /internal/* of any service is never listed here.
func Routes() []Route {
	return []Route{
		// identity (public)
		r("identity", "GET /auth/bootstrap/status", pub),
		r("identity", "POST /auth/bootstrap", rl("bootstrap", 20, time.Hour)),
		r("identity", "POST /auth/register", rl("register", 30, time.Hour)),
		r("identity", "POST /auth/login", rl("login", 100, 10*time.Minute)),
		r("identity", "POST /auth/refresh", rl("refresh", 300, 10*time.Minute)),
		// identity (authenticated)
		r("identity", "POST /auth/logout"),
		r("identity", "POST /auth/logout-all"),
		r("identity", "GET /auth/me"),
		r("identity", "POST /auth/password", opts{limit: 20, window: time.Hour, perUser: true, rlName: "password"}),
		r("identity", "GET /auth/sessions"),
		r("identity", "DELETE /auth/sessions/{id}"),
		r("identity", "POST /auth/pats"),
		r("identity", "GET /auth/pats"),
		r("identity", "DELETE /auth/pats/{id}"),
		r("identity", "POST /auth/mfa/setup"),
		r("identity", "POST /auth/mfa/enable"),
		r("identity", "POST /auth/mfa/disable"),
		// platform admin
		r("identity", "PATCH /system/settings", opts{admin: true}),

		// organizations
		r("organization", "POST /orgs"),
		r("organization", "GET /orgs"),
		r("organization", "POST /orgs/invites/accept"),
		r("organization", "GET "+org),
		r("organization", "DELETE "+org, long),
		r("organization", "POST "+org+"/invites"),
		r("organization", "GET "+org+"/invites"),
		r("organization", "DELETE "+org+"/invites/{inviteId}"),
		r("organization", "POST "+org+"/users"),
		r("organization", "GET "+org+"/users"),
		r("organization", "PATCH "+org+"/users/{userId}"),
		r("organization", "DELETE "+org+"/users/{userId}"),
		r("organization", "PUT "+org+"/users/{userId}/grants"),
		r("organization", "POST "+org+"/users/{userId}/reset-password"),
		r("organization", "GET "+org+"/users/{userId}/sessions"),
		r("organization", "DELETE "+org+"/users/{userId}/sessions/{sessionId}"),
		r("organization", "GET "+org+"/access-presets"),
		r("organization", "GET "+org+"/members"),
		r("organization", "PATCH "+org+"/members/{userId}"),
		r("organization", "DELETE "+org+"/members/{userId}", leave),

		// projects
		r("project", "POST "+org+"/projects"),
		r("project", "GET "+org+"/projects"),
		r("project", "GET "+proj),
		r("project", "PATCH "+proj),
		r("project", "DELETE "+proj, long),
		r("project", "PUT "+proj+"/config"),
		r("project", "POST "+proj+"/config"),
		r("project", "GET "+proj+"/summary"),
		r("project", "GET "+proj+"/config"),
		r("project", "GET "+proj+"/config/drift"),
		r("project", "POST "+proj+"/config/drift"),

		// git / repositories
		r("repository", "POST "+org+"/github/install/start"),
		r("repository", "POST "+org+"/github/install/callback"),
		r("repository", "GET "+org+"/github/installations"),
		r("repository", "GET "+org+"/github/repos"),
		r("repository", "POST "+proj+"/repos"),
		r("repository", "GET "+proj+"/repos"),
		r("repository", "DELETE "+proj+"/repos/{repoId}"),
		r("repository", "PATCH "+proj+"/repos/{repoId}"),
		r("repository", "POST "+proj+"/repos/{repoId}/webhook/rotate"),
		r("repository", "POST "+proj+"/repos/{repoId}/webhook/install"),
		r("repository", "GET "+proj+"/repos/{repoId}/folders"),
		r("repository", "GET "+proj+"/deploy-key"),
		r("repository", "POST "+proj+"/deploy-key/generate"),
		r("repository", "POST /webhooks/github", opts{public: true, maxBody: 26 << 20}),
		r("repository", "GET /github/setup", pub),

		// deployments / builds / registry
		r("deployment", "POST "+proj+"/deployments"),
		r("deployment", "GET "+proj+"/deployments"),
		r("deployment", "GET "+proj+"/deployments/{deploymentId}"),
		r("deployment", "POST "+proj+"/deployments/rollback"),
		r("deployment", "POST "+proj+"/deployments/{deploymentId}/rollback"),
		r("deployment", "POST "+proj+"/deployments/{deploymentId}/cancel"),
		r("build", "GET "+proj+"/builds"),
		r("build", "GET "+proj+"/builds/{buildId}"),
		r("build", "GET "+proj+"/builds/{buildId}/logs", streamed),
		r("build", "POST "+proj+"/builds/{buildId}/retry", long),
		r("build", "POST "+proj+"/builds/{buildId}/cancel"),
		r("registry", "GET "+proj+"/images"),

		// runtime
		r("runtime", "GET "+proj+"/runtime/instances"),
		r("runtime", "POST "+proj+"/runtime/instances"),
		r("runtime", "GET "+proj+"/runtime/instances/{instanceId}"),
		r("runtime", "POST "+proj+"/runtime/instances/{instanceId}/start"),
		r("runtime", "POST "+proj+"/runtime/instances/{instanceId}/stop"),
		r("runtime", "GET "+proj+"/runtime/instances/{instanceId}/logs", streamed),
		r("runtime", "GET "+proj+"/runtime/containers"),
		r("runtime", "POST "+proj+"/runtime/instances/{instanceId}/restart", long),
		r("runtime", "DELETE "+proj+"/runtime/instances/{instanceId}", long),
		r("runtime", "GET "+proj+"/runtime/jobs"),
		r("runtime", "GET "+proj+"/runtime/jobs/{jobId}"),
		r("runtime", "POST "+proj+"/runtime/jobs", long),
		r("runtime", "GET "+proj+"/runtime/logs", streamed),

		// domains / certificates
		r("domain", "GET "+proj+"/domains"),
		r("domain", "POST "+proj+"/domains"),
		r("domain", "GET "+proj+"/domains/{domainId}"),
		r("domain", "POST "+proj+"/domains/{domainId}/verify"),
		r("domain", "DELETE "+proj+"/domains/{domainId}"),
		r("domain", "POST "+proj+"/domains/{domainId}/reissue", long),
		r("certificate", "GET "+proj+"/certificates"),
		r("certificate", "GET "+proj+"/certificates/{certId}"),
		r("certificate", "POST "+proj+"/certificates/{certId}/renew"),

		// secrets & environments
		r("secret", "GET "+proj+"/environments"),
		r("secret", "POST "+proj+"/environments"),
		r("secret", "GET "+proj+"/secrets"),
		r("secret", "GET "+proj+"/secrets/audit"),
		r("secret", "POST "+proj+"/secrets/reencrypt", long),
		r("secret", "GET "+proj+"/environments/{env}/secrets"),
		r("secret", "POST "+proj+"/environments/{env}/secrets"),
		r("secret", "GET "+proj+"/environments/{env}/secrets/{name}"),
		r("secret", "PUT "+proj+"/environments/{env}/secrets/{name}"),
		r("secret", "DELETE "+proj+"/environments/{env}/secrets/{name}"),
		r("secret", "GET "+proj+"/env/{env}"),
		r("secret", "PUT "+proj+"/env/{env}/{name}"),
		r("secret", "DELETE "+proj+"/env/{env}/{name}"),

		// logs & metrics
		r("logging", "GET "+proj+"/logs"),
		r("logging", "POST "+proj+"/logs"),
		r("logging", "POST "+proj+"/logs/ingest"),
		r("metrics", "GET "+proj+"/metrics"),
		r("metrics", "POST "+proj+"/metrics"),

		// storage
		r("storage", "GET "+proj+"/storage/bucket"),
		r("storage", "POST "+proj+"/storage/bucket"),
		r("storage", "GET "+proj+"/storage/objects", long),
		r("storage", "POST "+proj+"/storage/objects", upload),
		r("storage", "POST "+proj+"/storage/signed-url"),
		r("storage", "PUT "+proj+"/storage/objects", streamUp),
		r("storage", "GET "+proj+"/storage/download", streamed),
		r("storage", "DELETE "+proj+"/storage/bucket", long),
		r("storage", "GET "+proj+"/storage/usage"),
		r("storage", "DELETE "+proj+"/storage/objects", long),

		// managed databases
		r("database", "GET "+proj+"/databases"),
		r("database", "POST "+proj+"/databases", long),
		r("database", "GET "+proj+"/databases/{dbId}"),
		r("database", "DELETE "+proj+"/databases/{dbId}", long),
		r("database", "GET "+proj+"/databases/{dbId}/slow-queries"),
		r("database", "GET "+proj+"/databases/{dbId}/connection"),
		r("database", "GET "+proj+"/databases/{dbId}/diagnostics/slow-queries"),
		r("database", "GET "+proj+"/databases/{dbId}/backups"),
		r("database", "POST "+proj+"/databases/{dbId}/backups", long),
		r("database", "GET "+proj+"/databases/{dbId}/backups/{backupId}"),
		r("database", "POST "+proj+"/databases/{dbId}/backups/{backupId}/restore", long),

		// add-ons
		r("addon", "GET "+org+"/addons"),
		r("addon", "GET "+org+"/addons/catalog"),
		r("addon", "GET "+proj+"/addons/catalog"),
		r("addon", "GET "+proj+"/addons"),
		r("addon", "POST "+proj+"/addons", long),
		r("addon", "GET "+proj+"/addons/{addonId}"),
		r("addon", "DELETE "+proj+"/addons/{addonId}", long),
		r("addon", "POST "+proj+"/addons/custom", long),
		r("addon", "GET "+proj+"/addons/{addonId}/connection"),
		r("addon", "POST "+proj+"/addons/{addonId}/start", long),
		r("addon", "POST "+proj+"/addons/{addonId}/stop", long),
		r("addon", "POST "+proj+"/addons/{addonId}/restart", long),
		r("addon", "GET "+proj+"/addons/{addonId}/backups"),
		r("addon", "POST "+proj+"/addons/{addonId}/backups", long),
		r("addon", "POST "+proj+"/addons/{addonId}/backups/{backupId}/restore", long),

		// cron / queues
		r("scheduler", "GET "+proj+"/cron"),
		r("scheduler", "POST "+proj+"/cron"),
		r("scheduler", "DELETE "+proj+"/cron/{cronId}"),
		r("scheduler", "GET "+proj+"/queues"),

		// AI
		r("ai", "POST "+proj+"/ai/explain", long),
		r("ai", "POST "+proj+"/ai/analyze-logs", long),
		r("ai", "POST "+proj+"/ai/overview-suggestions", long),
		r("ai", "POST "+org+"/ai/ask", long),
		r("ai", "GET "+org+"/ai/status"),
		r("ai", "GET "+org+"/ai/config"),
		r("ai", "POST "+org+"/ai/config"),
		r("ai", "PUT "+org+"/ai/config"),
		r("ai", "DELETE "+org+"/ai/config"),
	}
}

func (s *Server) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	for _, rt := range Routes() {
		parts := strings.SplitN(rt.Pattern, " ", 2)
		path := parts[1]
		pattern := parts[0] + " /api/v1" + path
		orgScoped := strings.Contains(path, "{orgId}")
		mux.Handle(pattern, s.wrap(rt.Upstream, rt.o, orgScoped, s.forward(rt.Upstream)))
		// The GitHub App setup redirect is also reachable at the unprefixed path.
		if path == "/github/setup" {
			mux.Handle("GET /github/setup", s.wrap(rt.Upstream, rt.o, false, s.forward(rt.Upstream)))
		}
	}

	// Platform-admin routes served by the gateway itself.
	adm := opts{admin: true}
	mux.Handle("GET /api/v1/system/settings", s.wrap("", adm, false, http.HandlerFunc(s.systemSettings)))
	mux.Handle("GET /api/v1/system/security", s.wrap("", adm, false, http.HandlerFunc(s.systemSecurity)))
	mux.Handle("GET /api/v1/system/cluster", s.wrap("", adm, false, http.HandlerFunc(s.systemCluster)))
	mux.Handle("GET /api/v1/cluster/nodes", s.wrap("", adm, false, http.HandlerFunc(s.clusterNodes)))
	mux.Handle("GET /api/v1/cluster/servers", s.wrap("", adm, false, http.HandlerFunc(s.listServers)))
	mux.Handle("PUT /api/v1/cluster/servers/{name}", s.wrap("", adm, false, http.HandlerFunc(s.putServer)))
	mux.Handle("DELETE /api/v1/cluster/servers/{name}", s.wrap("", adm, false, http.HandlerFunc(s.deleteServer)))
	mux.Handle("POST /api/v1/cluster/servers/{name}/check", s.wrap("", opts{admin: true, timeout: 30 * time.Second}, false, http.HandlerFunc(s.checkServer)))
	mux.Handle("POST /api/v1/cluster/join-tokens", s.wrap("", adm, false, http.HandlerFunc(s.createJoinToken)))
	jo := rl("clusterjoin", 60, time.Minute)
	jo.maxBody = 256 << 10
	jo.timeout = 90 * time.Second
	mux.Handle("GET /api/v1/cluster/join/{token}/script", s.wrap("", jo, false, http.HandlerFunc(s.joinScript)))
	mux.Handle("GET /api/v1/cluster/join/{token}/plan", s.wrap("", jo, false, http.HandlerFunc(s.joinPlan)))
	mux.Handle("GET /api/v1/cluster/join/{token}/agent", s.wrap("", opts{public: true, rlName: "clusterjoin", limit: 30, window: time.Minute, stream: true}, false, http.HandlerFunc(s.joinAgent)))
	mux.Handle("GET /api/v1/cluster/remote-nodes", s.wrap("", opts{admin: true, timeout: 30 * time.Second}, false, s.passRuntime(http.MethodGet, func(*http.Request) string { return "/internal/runtime/nodes" }, false)))
	mux.Handle("DELETE /api/v1/cluster/remote-nodes/{name}", s.wrap("", adm, false, http.HandlerFunc(s.deleteNode)))
	mux.Handle("GET /api/v1/cluster/placements/{projectId}", s.wrap("", adm, false, s.passRuntime(http.MethodGet, func(r *http.Request) string { return "/internal/runtime/placements/" + r.PathValue("projectId") }, false)))
	mux.Handle("PUT /api/v1/cluster/placements/{projectId}", s.wrap("", adm, false, s.passRuntime(http.MethodPut, func(r *http.Request) string { return "/internal/runtime/placements/" + r.PathValue("projectId") }, true)))
	mux.Handle("POST /api/v1/cluster/join/{token}/report", s.wrap("", jo, false, http.HandlerFunc(s.joinReport)))
	mux.Handle("POST /api/v1/system/maintenance/cleanup", s.wrap("", opts{admin: true, timeout: 5 * time.Minute}, false, http.HandlerFunc(s.maintenanceCleanup)))

	// Unknown API paths are a real 404 (never the dashboard HTML).
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		mux.HandleFunc(m+" /api/", func(w http.ResponseWriter, r *http.Request) {
			httpx.Error(w, http.StatusNotFound, "not_found", "no such API route")
		})
	}
	if s.dashboard != nil {
		mux.Handle("GET /", s.dashboard)
	}
}
