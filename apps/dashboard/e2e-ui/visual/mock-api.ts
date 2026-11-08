import type { Page, Route } from "@playwright/test";

/**
 * Network mock used ONLY for visual iteration (`npm run e2e:visual`).
 * The real end-to-end specs in e2e-ui/*.spec.ts talk to a real backend.
 */
const now = Date.now();
const iso = (minAgo: number) => new Date(now - minAgo * 60000).toISOString();

const ORG = "11111111-1111-4111-8111-111111111111";
export const P1 = "22222222-2222-4222-8222-222222222221";
const P2 = "22222222-2222-4222-8222-222222222222";

const user = { id: "u1", email: "ada@example.com", name: "Ada Lovelace", is_admin: true, totp_enabled: false };
const project = (id: string, name: string, slug: string) => ({ id, org_id: ORG, name, slug, description: `${name} production service`, created_at: iso(60 * 24 * 12) });

const flatDomain = (id: string, host: string, over: Record<string, unknown> = {}) => ({
  id,
  org_id: ORG,
  project_id: P1,
  hostname: host,
  kind: "custom",
  status: "pending",
  verification_type: "txt",
  routed: false,
  ssl_status: "none",
  created_at: iso(30),
  verification: {
    token: "abc123",
    accepted_methods: ["txt", "cname"],
    records: [
      { purpose: "ownership", method: "txt", type: "TXT", name: `_jp-challenge.${host}`, value: "jp-verify=abc123" },
      { purpose: "routing", method: "cname", type: "CNAME", name: host, value: "edge.jp.example.net" },
    ],
  },
  ...over,
});

export async function mockApi(page: Page, opts: { admin?: boolean; role?: string; restricted?: boolean } = {}) {
  const role = opts.restricted ? "member" : (opts.role ?? "owner");
  await page.addInitScript(() => {
    localStorage.setItem("jp_access_token", "mock-access");
    localStorage.setItem("jp_refresh_token", "mock-refresh");
  });
  await page.route("**/api/v1/**", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname.replace(/^.*\/api\/v1/, "");
    const m = req.method();
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

    if (path === "/auth/me") {
      if (opts.restricted) {
        return json({ user: { id: "u9", name: "Rita Restricted", username: "rita", is_admin: false, memberships: [{ org_id: ORG, org_name: "Acme Robotics", role: "member", full: false, projects: [{ project_id: P1, name: "storefront-api", areas: { overview: "view", deployments: "view", logs: "view", secrets: "view", domains: "manage" }, secrets_reveal: false }] }] } });
      }
      return json({ user: { ...user, is_admin: opts.admin ?? true, memberships: [{ org_id: ORG, org_name: "Acme Robotics", role: "owner", full: true }] } });
    }
    if (path === `/orgs/${ORG}/users`)
      return json({
        users: [
          { id: "u1", name: "Ada Lovelace", username: "ada", email: "ada@example.com", role: "owner", last_login_at: iso(5), status: "active" },
          { id: "u2", name: "Grace Hopper", username: "grace", role: "member", last_login_at: iso(3000), status: "active", projects_count: 2, grants: [{ project_id: P1, preset: "developer" }, { project_id: P2, preset: "viewer" }] },
          { id: "u3", name: "Linus T", username: "linus", role: "member", status: "active", must_change_password: true, grants: [{ project_id: P1, preset: "custom", areas: { logs: "view", storage: "manage" } }] },
          { id: "u4", name: "Old Account", username: "old.acct", role: "member", disabled: true, last_login_at: iso(90000), grants: [] },
        ],
      });
    if (path === "/auth/bootstrap/status") return json({ initialized: true, registration: "invite" });
    if (path === "/auth/sessions")
      return json({
        sessions: [
          { id: "s1", user_agent: "Mozilla/5.0 Chrome/126", ip: "203.0.113.7", created_at: iso(120), last_active_at: iso(1), current: true },
          { id: "s2", user_agent: "curl/8.5", ip: "198.51.100.4", created_at: iso(3000), last_active_at: iso(900) },
        ],
      });
    if (path === "/auth/pats") return json({ pats: [{ id: "k1", name: "ci-pipeline", token_prefix: "jpat_8f3a", scopes: ["read", "deploy"], created_at: iso(5000), last_used_at: iso(20), expires_at: null }] });
    if (path === "/orgs") return json({ orgs: [{ id: ORG, name: "Acme Robotics", slug: "acme", role }] });
    if (path === `/orgs/${ORG}/members`)
      return json({
        members: [
          { user_id: "u1", email: "ada@example.com", name: "Ada Lovelace", role: "owner", joined_at: iso(9999) },
          { user_id: "u2", email: "grace@example.com", name: "Grace Hopper", role: "member", joined_at: iso(4000) },
        ],
      });
    if (path === `/orgs/${ORG}/invites`) return json({ invites: [{ id: "i1", email: "linus@example.com", role: "viewer", created_at: iso(200) }] });
    if (path === `/orgs/${ORG}/projects` && m === "GET") return json({ projects: [project(P1, "storefront-api", "storefront-api"), project(P2, "marketing-site", "marketing-site")] });
    if (path === `/orgs/${ORG}/projects/${P1}`) return json({ project: project(P1, "storefront-api", "storefront-api") });
    if (path === `/orgs/${ORG}/projects/${P2}`) return json({ project: project(P2, "marketing-site", "marketing-site") });
    if (/\/deployments$/.test(path))
      return json({
        deployments: [
          { id: "d1aaaaaaaa", org_id: ORG, project_id: P1, status: "running", git_branch: "main", git_sha: "9f2c1ab77de", message: "Fix cart rounding", strategy: "rolling", created_at: iso(14), build_id: "b1" },
          { id: "d2bbbbbbbb", org_id: ORG, project_id: P1, status: "failed", git_branch: "feature/search", git_sha: "1ab44cd90", error: "health check failed on /healthz", strategy: "rolling", created_at: iso(300) },
          { id: "d3cccccccc", org_id: ORG, project_id: P1, status: "superseded", git_branch: "main", git_sha: "77de01aa3", strategy: "blue_green", created_at: iso(1500) },
        ],
      });
    if (/\/builds$/.test(path)) return json({ builds: [{ id: "b1", org_id: ORG, project_id: P1, status: "succeeded", git_branch: "main", git_sha: "9f2c1ab77de", framework: "node", created_at: iso(15) }] });
    if (/\/builds\/b1\/logs$/.test(path)) return json({ build_id: "b1", status: "succeeded", logs: "Cloning repository...\nDetected framework: node\nnpm ci\nadded 412 packages in 9s\nnpm run build\nwarn: deprecated package left-pad\nBuild complete. Image pushed.\n" });
    if (/\/builds\/b1$/.test(path))
      return json({ build: { id: "b1", org_id: ORG, project_id: P1, status: "succeeded", git_branch: "main", git_sha: "9f2c1ab77de", framework: "node", created_at: iso(15), security_scan: { scanner: "trivy", critical: 0, high: 1, medium: 3, low: 5, summary: "1 high severity in openssl" } } });
    if (/\/runtime\/instances$/.test(path))
      return json({ instances: [{ id: "r1", org_id: ORG, project_id: P1, kind: "app", role: "app", image_ref: "registry/acme/storefront:9f2c1ab", status: "running", desired_state: "running", container_name: "jp-app-22222222-1", health_status: "healthy", restart_count: 0, updated_at: iso(2) }] });
    if (/\/runtime\/containers$/.test(path))
      return json({ containers: [{ id: "c1", name: "jp-app-22222222-1", image: "registry/acme/storefront:9f2c1ab", state: "running" }, { id: "c2", name: "jp-addon-22222222-redis", image: "redis:7-alpine", state: "running" }] });
    if (/\/runtime\/instances\/r1\/logs$/.test(path))
      return json({ entries: [{ source: "app", level: "info", message: "listening on :3000", logged_at: iso(10) }, { source: "app", level: "error", message: "payment webhook timeout", logged_at: iso(4) }] });
    if (/\/domains$/.test(path))
      return json({
        domains: [
          flatDomain("m0", "storefront-api-acme.jp.example.net", { kind: "system", status: "active", routed: true, ssl_status: "active", url: "https://storefront-api-acme.jp.example.net", verification: undefined }),
          flatDomain("m1", "shop.example.com"),
          flatDomain("m2", "www.example.org", { status: "active", routed: true, ssl_status: "pending", url: "https://www.example.org", verification: undefined }),
        ],
      });
    if (/\/domains\/m1\/verify$/.test(path))
      return json({ verified: false, dns: { ok: false, method: "txt", detail: "TXT record _jp-challenge.shop.example.com not found (NXDOMAIN)" }, domain: flatDomain("m1", "shop.example.com") }, 400);
    if (/\/environments\/\w+\/secrets$/.test(path))
      return json({
        secrets: [
          { id: "s1", environment: "production", name: "DATABASE_URL", current_version: 3, value_hint: "po****es", updated_at: iso(60) },
          { id: "s2", environment: "production", name: "STRIPE_KEY", current_version: 1, value_hint: "sk****9z", updated_at: iso(6000) },
        ],
      });
    if (/\/secrets\/DATABASE_URL$/.test(path)) return json({ secret: { value: "postgres://app:s3cret@addon-pg:5432/app" } });
    if (/\/addons\/catalog$/.test(path))
      return json({
        catalog: [
          { id: "postgres", name: "PostgreSQL", category: "database", description: "Relational database. Dedicated container with generated credentials.", default_port: 5432, secret_keys: ["DATABASE_URL"] },
          { id: "redis", name: "Redis", category: "cache", description: "In-memory cache and queue, password protected.", secret_keys: ["REDIS_URL"] },
          { id: "mongodb", name: "MongoDB", category: "database", description: "Document database.", secret_keys: ["MONGODB_URI"] },
          { id: "rabbitmq", name: "RabbitMQ", category: "queue", description: "Message broker.", provisionable: false, unavailable_reason: "No runnable image configured." },
        ],
      });
    if (/\/addons$/.test(path) && m === "GET")
      return json({
        addons: [
          { id: "a1", org_id: ORG, project_id: P1, engine: "postgres", name: "pg-main", status: "ready", endpoint: "addon-pg-main:5432", injected_env: ["DATABASE_URL"], created_at: iso(500) },
          { id: "a2", org_id: ORG, project_id: P1, engine: "redis", name: "cache", status: "pending", created_at: iso(1) },
        ],
      });
    if (/\/addons\/a1\/connection$/.test(path)) return json({ masked_url: "postgres://app:********@addon-pg-main:5432/app", host: "addon-pg-main", port: 5432, username: "app", database: "app", env_vars: ["DATABASE_URL"] });
    if (/\/addons\/a1\/backups$/.test(path)) return json({ backups: [] });
    if (/\/storage\/bucket$/.test(path)) return json({ bucket: { id: "bk", org_id: ORG, project_id: P1, name: "storefront-api-files" } });
    if (/\/storage\/objects$/.test(path)) return json({ objects: [{ key: "uploads/logo.png", size_bytes: 48213, content_type: "image/png", updated_at: iso(50) }] });
    if (/\/storage\/usage$/.test(path)) return json({ used_bytes: 48213, quota_bytes: 1073741824, object_count: 1 });
    if (/\/metrics$/.test(path))
      return json({
        metrics: [
          { name: "http_requests", latest: 1284, count: 40, recorded_at: iso(1), series: Array.from({ length: 24 }, (_, i) => ({ t: iso(24 * 5 - i * 5), v: 600 + Math.round(Math.sin(i / 3) * 300 + i * 20) })) },
          { name: "latency_ms", latest: 84.2, count: 40, recorded_at: iso(1), unit: "ms" },
        ],
      });
    if (/\/logs$/.test(path))
      return json({ entries: [{ source: "app", level: "info", message: "server started", logged_at: iso(30) }, { source: "build", level: "warn", message: "deprecated package", logged_at: iso(29) }, { source: "app", level: "error", message: "upstream timeout", logged_at: iso(3) }] });
    if (/\/repos$/.test(path) && m === "GET")
      return json({ repos: [{ id: "g1", org_id: ORG, project_id: P1, full_name: "acme/storefront", clone_url: "https://github.com/acme/storefront.git", default_branch: "main", created_at: iso(9000) }] });
    if (/\/github\/installations$/.test(path)) return json({ installations: [] });
    if (/\/deploy-key$/.test(path))
      return json({ deploy_key: { id: "k", project_id: P1, key_type: "ed25519", public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockMockMockMockMockMockMockMockMockMock jp-deploy", created_at: iso(900) } });
    if (/\/ai\/status$/.test(path)) return json({ provider: "gemini", model: "gemini-2.0-flash", configured: true });
    if (/\/ai\/config$/.test(path)) return json({ has_key: true, masked_key: "AIza****Qw", provider: "gemini" });
    if (/\/databases$/.test(path)) return json({ databases: [] });
    if (path === "/system/security")
      return json({
        checks: [
          { id: "jwt", title: "JWT secret strength", status: "pass", detail: "64 byte secret in use" },
          { id: "internal", title: "Internal service token", status: "pass", detail: "INTERNAL_TOKEN is set" },
          { id: "tls", title: "HTTPS enabled", status: "fail", detail: "TLS_ENABLED is false; sessions travel in clear text", remediation: "Set TLS_ENABLED=true and ROOT_DOMAIN, then restart traefik and domain." },
          { id: "reg", title: "Registration mode", status: "warn", detail: "open", remediation: "Set REGISTRATION_MODE=invite." },
        ],
        generated_at: iso(0),
      });
    if (path === "/system/cluster")
      return json({ node: { hostname: "oracle-2nd", os: "linux", arch: "arm64", cpu_count: 2, cpu_percent: 37, load: [0.72, 0.6, 0.5], memory_total_bytes: 11811160064, memory_used_bytes: 5100273664, disk_total_bytes: 107374182400, disk_used_bytes: 41875931136, uptime_seconds: 864000, docker_version: "29.0.1", containers_running: 31, containers_total: 34 } });
    if (path === "/system/settings")
      return json({
        settings: [
          { key: "smtp_host", label: "SMTP host", value: "smtp.example.com", editable: true, group: "Email (SMTP)" },
          { key: "smtp_password", label: "SMTP password", secret: true, is_set: true, editable: true, group: "Email (SMTP)" },
          { key: "ai_api_key", label: "Platform AI key", secret: true, is_set: false, editable: true, group: "AI" },
          { key: "root_domain", label: "Root domain", value: "jp.example.net", editable: false, group: "Effective configuration" },
        ],
      });
    return json({ error: { code: "not_found", message: `mock: no handler for ${m} ${path}` } }, 404);
  });
}
