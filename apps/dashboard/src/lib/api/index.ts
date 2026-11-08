import { ApiError, client, entityOf, listOf, slugify, type RequestOptions } from "./http";
import type {
  AddonCatalogItem,
  AddonConnection,
  AiConfig,
  AiStatus,
  CreateUserInput,
  CreatedUser,
  Grant,
  ManagedUser,
  AuthResponse,
  AvailableRepo,
  Backup,
  BootstrapStatus,
  Build,
  Certificate,
  ClusterInfo,
  ServerEntry,
  RemoteNode,
  JoinToken,
  ServerInput,
  ConnectedRepo,
  DeployKey,
  Deployment,
  DnsRecord,
  Domain,
  DomainDetail,
  GitInstallStart,
  GitInstallation,
  Invite,
  LogEntry,
  LoginResult,
  ManagedAddon,
  ManagedDatabase,
  MetricSummary,
  MfaSetup,
  Org,
  OrgMember,
  Pat,
  Project,
  RepoFolders,
  RepoSettings,
  RuntimeContainer,
  RuntimeInstance,
  RuntimeLogs,
  SecretMeta,
  SecurityReport,
  SecurityCheck,
  Session,
  SettingField,
  StorageBucket,
  StorageObject,
  StorageUsage,
  User,
  WebhookInfo,
  WebhookInstall,
} from "./types";

export * from "./types";
export { ApiError, errorMessage, isApiError, slugify, onSessionExpired, localTokenStore, client } from "./http";

type Sig = { signal?: AbortSignal };
const r = client.request;

const org = (o: string) => `/orgs/${o}`;
const proj = (o: string, p: string) => `/orgs/${o}/projects/${p}`;
const enc = encodeURIComponent;

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === "object" ? (v as Record<string, unknown>) : {};
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v ? v : undefined;
}

/* ------------------------------------------------------------------ auth */
export const auth = {
  bootstrapStatus: (o: Sig = {}) => r<BootstrapStatus>("/auth/bootstrap/status", { anon: true, ...o }),
  bootstrap: (b: { email: string; password: string; name: string }) =>
    r<AuthResponse>("/auth/bootstrap", { method: "POST", body: b, anon: true }),
  register: (b: { email: string; password: string; name: string; invite_token?: string }) =>
    r<AuthResponse>("/auth/register", { method: "POST", body: b, anon: true }),
  /** `login` is a username or an email. */
  login: (b: { login: string; password: string; totp_code?: string; mfa_token?: string }) =>
    r<LoginResult>("/auth/login", { method: "POST", body: { ...b, ...(b.login.includes("@") ? { email: b.login } : {}) }, anon: true }),
  logout: () => r<void>("/auth/logout", { method: "POST" }),
  async me(o: Sig = {}): Promise<User> {
    const raw = await r<unknown>("/auth/me", o);
    const user = entityOf<User>(raw, "user");
    // the API sends `memberships` next to `user`, not inside it
    const top = (raw as { memberships?: User["memberships"] } | null)?.memberships;
    return top && !user.memberships ? { ...user, memberships: top } : user;
  },
  async sessions(o: Sig = {}): Promise<Session[]> {
    return listOf<Session>(await r<unknown>("/auth/sessions", o), "sessions");
  },
  revokeSession: (id: string) => r<void>(`/auth/sessions/${enc(id)}`, { method: "DELETE" }),
  async pats(o: Sig = {}): Promise<Pat[]> {
    return listOf<Pat>(await r<unknown>("/auth/pats", o), "pats");
  },
  createPat: (b: { name: string; scopes?: string[]; expires_in_days?: number; expires_at?: string }) =>
    r<Pat>("/auth/pats", { method: "POST", body: b }),
  revokePat: (id: string) => r<void>(`/auth/pats/${enc(id)}`, { method: "DELETE" }),
  changePassword: (b: { current_password: string; new_password: string }) =>
    r<void>("/auth/password", { method: "POST", body: b }),
  updateProfile: (b: { name: string }) => r<User>("/auth/me", { method: "PATCH", body: b }),
  mfaSetup: () => r<MfaSetup>("/auth/mfa/setup", { method: "POST", body: {} }),
  mfaEnable: (code: string) =>
    r<{ enabled: boolean; backup_codes: string[] }>("/auth/mfa/enable", { method: "POST", body: { code } }),
  mfaDisable: (code: string, password?: string) =>
    r<{ enabled: boolean }>("/auth/mfa/disable", { method: "POST", body: { code, password } }),
};

/* ------------------------------------------------------------------ orgs */
export const orgs = {
  async list(o: Sig = {}): Promise<Org[]> {
    return listOf<Org>(await r<unknown>("/orgs", o), "orgs");
  },
  async create(b: { name: string; slug?: string }): Promise<Org> {
    const body = { name: b.name, slug: b.slug?.trim() || slugify(b.name) };
    return entityOf<Org>(await r<unknown>("/orgs", { method: "POST", body }), "org");
  },
  async get(id: string, o: Sig = {}): Promise<Org> {
    return entityOf<Org>(await r<unknown>(org(id), o), "org");
  },
  remove: (id: string) => r<void>(org(id), { method: "DELETE" }),
  invite: (id: string, b: { email: string; role: string }) => r<Invite>(`${org(id)}/invites`, { method: "POST", body: b }),
  async invites(id: string, o: Sig = {}): Promise<Invite[]> {
    return listOf<Invite>(await r<unknown>(`${org(id)}/invites`, o), "invites");
  },
  revokeInvite: (id: string, inviteId: string) => r<void>(`${org(id)}/invites/${enc(inviteId)}`, { method: "DELETE" }),
  async acceptInvite(token: string): Promise<Org> {
    return entityOf<Org>(await r<unknown>("/orgs/invites/accept", { method: "POST", body: { token } }), "org");
  },
  async members(id: string, o: Sig = {}): Promise<OrgMember[]> {
    return listOf<OrgMember>(await r<unknown>(`${org(id)}/members`, o), "members");
  },
  changeRole: (id: string, userId: string, role: string) =>
    r<unknown>(`${org(id)}/members/${enc(userId)}`, { method: "PATCH", body: { role } }),
  removeMember: (id: string, userId: string) => r<void>(`${org(id)}/members/${enc(userId)}`, { method: "DELETE" }),
};

/* ------------------------------------------------------- managed users (admin) */
const usersBase = (o: string) => `/orgs/${o}/users`;
export const users = {
  async list(orgId: string, o: Sig = {}): Promise<ManagedUser[]> {
    return listOf<ManagedUser & { user_id?: string }>(await r<unknown>(usersBase(orgId), o), "users").map(withUserId);
  },
  async create(orgId: string, b: CreateUserInput): Promise<CreatedUser> {
    const res = await r<CreatedUser>(usersBase(orgId), { method: "POST", body: b });
    return res?.user ? { ...res, user: withUserId(res.user as ManagedUser & { user_id?: string }) } : res;
  },
  update: (orgId: string, id: string, b: { name?: string; disabled?: boolean; role?: string }) =>
    r<unknown>(`${usersBase(orgId)}/${enc(id)}`, { method: "PATCH", body: b }),
  setGrants: (orgId: string, id: string, grants: Grant[]) => r<unknown>(`${usersBase(orgId)}/${enc(id)}/grants`, { method: "PUT", body: { grants } }),
  resetPassword: (orgId: string, id: string) => r<{ generated_password?: string; password?: string }>(`${usersBase(orgId)}/${enc(id)}/reset-password`, { method: "POST", body: {} }),
  remove: (orgId: string, id: string) => r<void>(`${usersBase(orgId)}/${enc(id)}`, { method: "DELETE" }),
  async sessions(orgId: string, id: string, o: Sig = {}): Promise<Session[]> {
    return listOf<Session>(await r<unknown>(`${usersBase(orgId)}/${enc(id)}/sessions`, o), "sessions");
  },
  revokeSession: (orgId: string, id: string, sessionId: string) => r<void>(`${usersBase(orgId)}/${enc(id)}/sessions/${enc(sessionId)}`, { method: "DELETE" }),
};

/* -------------------------------------------------------------- projects */
export const projects = {
  async list(orgId: string, o: Sig = {}): Promise<Project[]> {
    return listOf<Project>(await r<unknown>(`${org(orgId)}/projects`, o), "projects");
  },
  async create(orgId: string, b: { name: string; slug?: string; description?: string }): Promise<Project> {
    const body = { name: b.name, slug: b.slug?.trim() || slugify(b.name), description: b.description };
    return entityOf<Project>(await r<unknown>(`${org(orgId)}/projects`, { method: "POST", body }), "project");
  },
  async get(orgId: string, id: string, o: Sig = {}): Promise<Project> {
    return entityOf<Project>(await r<unknown>(proj(orgId, id), o), "project");
  },
  async update(orgId: string, id: string, b: { name?: string; description?: string }): Promise<Project> {
    return entityOf<Project>(await r<unknown>(proj(orgId, id), { method: "PATCH", body: b }), "project");
  },
  remove: (orgId: string, id: string) => r<unknown>(proj(orgId, id), { method: "DELETE" }),
};

/* ------------------------------------------------------------------- git */
export const git = {
  startInstall: (orgId: string) =>
    r<GitInstallStart>(`${org(orgId)}/github/install/start`, { method: "POST", body: {} }),
  async completeInstall(
    orgId: string,
    b: { installation_id?: string; account_login?: string; state?: string },
  ): Promise<GitInstallation> {
    return entityOf<GitInstallation>(
      await r<unknown>(`${org(orgId)}/github/install/callback`, { method: "POST", body: b }),
      "installation",
    );
  },
  async installations(orgId: string, o: Sig = {}): Promise<GitInstallation[]> {
    return listOf<GitInstallation>(await r<unknown>(`${org(orgId)}/github/installations`, o), "installations");
  },
  async available(orgId: string, o: Sig = {}): Promise<AvailableRepo[]> {
    return listOf<AvailableRepo>(await r<unknown>(`${org(orgId)}/github/repos`, o), "repos");
  },
  async repos(orgId: string, projectId: string, o: Sig = {}): Promise<ConnectedRepo[]> {
    return listOf<ConnectedRepo>(await r<unknown>(`${proj(orgId, projectId)}/repos`, o), "repos");
  },
  /** Connects a repository. The webhook secret/url are only present for connections that need a manual (or token-installed) webhook. */
  async connect(
    orgId: string,
    projectId: string,
    b: { full_name: string; clone_url?: string; default_branch?: string; installation_id?: string; auth_type?: string; token?: string } & Partial<RepoSettings>,
  ): Promise<{ repo: ConnectedRepo; webhook: WebhookInfo | null }> {
    const res = await r<{ repo: ConnectedRepo; webhook_secret?: string; webhook_url?: string; webhook_note?: string }>(`${proj(orgId, projectId)}/repos`, { method: "POST", body: b });
    const webhook = res.webhook_secret && res.webhook_url ? { webhook_secret: res.webhook_secret, webhook_url: res.webhook_url, webhook_note: res.webhook_note } : null;
    return { repo: res.repo, webhook };
  },
  async updateRepo(orgId: string, projectId: string, repoId: string, b: Partial<RepoSettings>): Promise<ConnectedRepo> {
    return entityOf<ConnectedRepo>(await r<unknown>(`${proj(orgId, projectId)}/repos/${enc(repoId)}`, { method: "PATCH", body: b }), "repo");
  },
  rotateWebhook: (orgId: string, projectId: string, repoId: string) =>
    r<WebhookInfo>(`${proj(orgId, projectId)}/repos/${enc(repoId)}/webhook/rotate`, { method: "POST", body: {} }),
  installWebhook: (orgId: string, projectId: string, repoId: string) =>
    r<WebhookInstall>(`${proj(orgId, projectId)}/repos/${enc(repoId)}/webhook/install`, { method: "POST", body: {} }),
  folders: (orgId: string, projectId: string, repoId: string, path: string, o: Sig = {}) =>
    r<RepoFolders>(`${proj(orgId, projectId)}/repos/${enc(repoId)}/folders${path ? `?path=${encodeURIComponent(path)}` : ""}`, o),
  disconnect: (orgId: string, projectId: string, repoId: string) =>
    r<void>(`${proj(orgId, projectId)}/repos/${enc(repoId)}`, { method: "DELETE" }),
  async deployKey(orgId: string, projectId: string, o: Sig = {}): Promise<DeployKey> {
    return entityOf<DeployKey>(await r<unknown>(`${proj(orgId, projectId)}/deploy-key`, o), "deploy_key");
  },
  async rotateDeployKey(orgId: string, projectId: string): Promise<DeployKey> {
    return entityOf<DeployKey>(
      await r<unknown>(`${proj(orgId, projectId)}/deploy-key/generate`, { method: "POST", body: {} }),
      "deploy_key",
    );
  },
};

/* ----------------------------------------------- deployments and builds */
export const deployments = {
  async list(orgId: string, projectId: string, o: Sig = {}): Promise<Deployment[]> {
    return listOf<Deployment>(await r<unknown>(`${proj(orgId, projectId)}/deployments`, o), "deployments");
  },
  async create(
    orgId: string,
    projectId: string,
    b: { git_branch?: string; git_sha?: string; repo_id?: string; message?: string } = {},
  ): Promise<Deployment> {
    return entityOf<Deployment>(await r<unknown>(`${proj(orgId, projectId)}/deployments`, { method: "POST", body: b }), "deployment");
  },
  async rollback(orgId: string, projectId: string, deploymentId?: string): Promise<Deployment> {
    const path = deploymentId
      ? `${proj(orgId, projectId)}/deployments/${enc(deploymentId)}/rollback`
      : `${proj(orgId, projectId)}/deployments/rollback`;
    return entityOf<Deployment>(await r<unknown>(path, { method: "POST", body: {} }), "deployment");
  },
};

export const builds = {
  async list(orgId: string, projectId: string, o: Sig = {}): Promise<Build[]> {
    return listOf<Build>(await r<unknown>(`${proj(orgId, projectId)}/builds`, o), "builds");
  },
  async get(orgId: string, projectId: string, buildId: string, o: Sig = {}): Promise<Build> {
    return entityOf<Build>(await r<unknown>(`${proj(orgId, projectId)}/builds/${enc(buildId)}`, o), "build");
  },
  logs: (orgId: string, projectId: string, buildId: string, o: Sig = {}) =>
    r<{ build_id: string; status: string; logs: string }>(`${proj(orgId, projectId)}/builds/${enc(buildId)}/logs`, o),
};

/* --------------------------------------------------------------- domains */
function normalizeRecords(d: Record<string, unknown>, hostname: string): DnsRecord[] {
  const fromApi = d.dns_records ?? d.records ?? asRecord(d.verification).records;
  if (Array.isArray(fromApi)) {
    return fromApi
      .map((x) => asRecord(x))
      .filter((x) => str(x.type) && str(x.value))
      .map((x) => ({
        type: String(x.type).toUpperCase(),
        name: str(x.name) ?? str(x.host) ?? hostname,
        value: String(x.value),
        purpose: str(x.purpose) ?? str(x.description),
        ttl: typeof x.ttl === "number" ? x.ttl : undefined,
      }));
  }
  return [];
}

export function normalizeDomain(raw: unknown): DomainDetail {
  const top = asRecord(raw);
  const d = asRecord(top.domain ?? raw) as unknown as Domain & Record<string, unknown>;
  const records = normalizeRecords({ ...top, ...asRecord(d) }, d.hostname);
  const dns = asRecord(top.dns ?? top.diagnostics);
  return {
    domain: { ...d, dns_records: records },
    records,
    diagnostics: Object.keys(dns).length
      ? {
          ok: typeof dns.ok === "boolean" ? dns.ok : undefined,
          method: str(dns.method),
          detail: str(dns.detail) ?? str(dns.message),
          records: Array.isArray(dns.records) ? (dns.records as unknown[]).map(String) : undefined,
          expected: str(dns.expected),
        }
      : undefined,
  };
}

export const domains = {
  async list(orgId: string, projectId: string, o: Sig = {}): Promise<DomainDetail[]> {
    const data = await r<unknown>(`${proj(orgId, projectId)}/domains`, o);
    return listOf<unknown>(data, "domains").map((x) => normalizeDomain(x));
  },
  async add(orgId: string, projectId: string, b: { hostname: string; verification_type?: string }): Promise<DomainDetail> {
    const data = await r<unknown>(`${proj(orgId, projectId)}/domains`, { method: "POST", body: b });
    return normalizeDomain(data);
  },
  async get(orgId: string, projectId: string, id: string, o: Sig = {}): Promise<DomainDetail> {
    return normalizeDomain(await r<unknown>(`${proj(orgId, projectId)}/domains/${enc(id)}`, o));
  },
  /** A failed DNS check is a result (with diagnostics), not an exception. */
  async verify(orgId: string, projectId: string, id: string): Promise<DomainDetail & { verified: boolean }> {
    try {
      const data = await r<unknown>(`${proj(orgId, projectId)}/domains/${enc(id)}/verify`, { method: "POST", body: {} });
      return { ...normalizeDomain(data), verified: asRecord(data).verified !== false };
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && asRecord(e.body).dns) {
        return { ...normalizeDomain(e.body), verified: false };
      }
      throw e;
    }
  },
  remove: (orgId: string, projectId: string, id: string) => r<void>(`${proj(orgId, projectId)}/domains/${enc(id)}`, { method: "DELETE" }),
  async certificates(orgId: string, projectId: string, o: Sig = {}): Promise<Certificate[]> {
    return listOf<Certificate>(await r<unknown>(`${proj(orgId, projectId)}/certificates`, o), "certificates");
  },
  async renewCertificate(orgId: string, projectId: string, certId: string): Promise<Certificate> {
    return entityOf<Certificate>(
      await r<unknown>(`${proj(orgId, projectId)}/certificates/${enc(certId)}/renew`, { method: "POST", body: {} }),
      "certificate",
    );
  },
};

/* --------------------------------------------------------------- runtime */
export const runtime = {
  async instances(orgId: string, projectId: string, o: Sig = {}): Promise<RuntimeInstance[]> {
    return listOf<RuntimeInstance>(await r<unknown>(`${proj(orgId, projectId)}/runtime/instances`, o), "instances");
  },
  async containers(orgId: string, projectId: string, o: Sig = {}): Promise<RuntimeContainer[]> {
    return listOf<RuntimeContainer>(await r<unknown>(`${proj(orgId, projectId)}/runtime/containers`, o), "containers");
  },
  action: (orgId: string, projectId: string, instanceId: string, action: "start" | "stop" | "restart") =>
    r<unknown>(`${proj(orgId, projectId)}/runtime/instances/${enc(instanceId)}/${action}`, { method: "POST", body: {} }),
  remove: (orgId: string, projectId: string, instanceId: string) =>
    r<unknown>(`${proj(orgId, projectId)}/runtime/instances/${enc(instanceId)}`, { method: "DELETE" }),
  async logs(orgId: string, projectId: string, instanceId: string | null, tail = 200, o: Sig = {}): Promise<RuntimeLogs> {
    const path = instanceId
      ? `${proj(orgId, projectId)}/runtime/instances/${enc(instanceId)}/logs`
      : `${proj(orgId, projectId)}/runtime/logs`;
    const d = asRecord(await r<unknown>(path, { query: { tail }, ...o }));
    return { ...(d as object), entries: Array.isArray(d.entries) ? (d.entries as LogEntry[]) : [] } as RuntimeLogs;
  },
};

/* --------------------------------------------------------------- secrets */
export const secrets = {
  async list(orgId: string, projectId: string, env: string, o: Sig = {}): Promise<SecretMeta[]> {
    return listOf<SecretMeta>(await r<unknown>(`${proj(orgId, projectId)}/environments/${enc(env)}/secrets`, o), "secrets");
  },
  /** admin+ only; server writes an audit row `secret.reveal`. */
  async reveal(orgId: string, projectId: string, env: string, name: string): Promise<string> {
    const d = asRecord(await r<unknown>(`${proj(orgId, projectId)}/environments/${enc(env)}/secrets/${enc(name)}`));
    const s = asRecord(d.secret ?? d);
    return typeof s.value === "string" ? s.value : "";
  },
  async create(orgId: string, projectId: string, env: string, b: { name: string; value: string }): Promise<SecretMeta> {
    return entityOf<SecretMeta>(
      await r<unknown>(`${proj(orgId, projectId)}/environments/${enc(env)}/secrets`, { method: "POST", body: b }),
      "secret",
    );
  },
  async update(
    orgId: string,
    projectId: string,
    env: string,
    name: string,
    b: { value?: string; new_name?: string },
  ): Promise<SecretMeta> {
    return entityOf<SecretMeta>(
      await r<unknown>(`${proj(orgId, projectId)}/environments/${enc(env)}/secrets/${enc(name)}`, { method: "PUT", body: b }),
      "secret",
    );
  },
  remove: (orgId: string, projectId: string, env: string, name: string) =>
    r<void>(`${proj(orgId, projectId)}/environments/${enc(env)}/secrets/${enc(name)}`, { method: "DELETE" }),
};

/* ------------------------------------------------------- logs / metrics */
export const logs = {
  async query(
    orgId: string,
    projectId: string,
    q: { source?: string; level?: string; q?: string; limit?: number; build_id?: string; since?: string },
    o: Sig = {},
  ): Promise<LogEntry[]> {
    const d = await r<unknown>(`${proj(orgId, projectId)}/logs`, { query: q, ...o });
    return listOf<LogEntry>(d, "entries");
  },
};

export const metrics = {
  async project(orgId: string, projectId: string, o: Sig = {}): Promise<MetricSummary[]> {
    return listOf<MetricSummary>(await r<unknown>(`${proj(orgId, projectId)}/metrics`, o), "metrics");
  },
};

/* --------------------------------------------------------------- storage */
export const storage = {
  async bucket(orgId: string, projectId: string, o: Sig = {}): Promise<StorageBucket | null> {
    try {
      const d = asRecord(await r<unknown>(`${proj(orgId, projectId)}/storage/bucket`, o));
      return (d.bucket as StorageBucket) ?? null;
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return null;
      throw e;
    }
  },
  createBucket: (orgId: string, projectId: string) =>
    r<unknown>(`${proj(orgId, projectId)}/storage/bucket`, { method: "POST", body: {} }),
  async objects(orgId: string, projectId: string, prefix?: string, o: Sig = {}): Promise<StorageObject[]> {
    return listOf<StorageObject>(await r<unknown>(`${proj(orgId, projectId)}/storage/objects`, { query: { prefix }, ...o }), "objects");
  },
  usage: (orgId: string, projectId: string, o: Sig = {}) => r<StorageUsage>(`${proj(orgId, projectId)}/storage/usage`, o),
  /** Streaming upload (PUT body = file). XHR gives real upload progress. */
  upload(
    orgId: string,
    projectId: string,
    key: string,
    file: File,
    onProgress: (fraction: number) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const url = `${client.baseUrl}${proj(orgId, projectId)}/storage/objects?key=${enc(key)}`;
      xhr.open("PUT", url);
      const token = (typeof window !== "undefined" && window.localStorage.getItem("jp_access_token")) || "";
      if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total);
      };
      xhr.onload = async () => {
        if (xhr.status >= 200 && xhr.status < 300) return resolve();
        if (xhr.status === 401) {
          const ok = await client.refreshSingleFlight();
          if (ok) return storage.upload(orgId, projectId, key, file, onProgress, signal).then(resolve, reject);
        }
        let msg = `Upload failed (${xhr.status})`;
        try {
          const j = JSON.parse(xhr.responseText) as { error?: { message?: string } | string };
          msg = typeof j.error === "string" ? j.error : j.error?.message || msg;
        } catch {
          /* not json */
        }
        reject(new ApiError(xhr.status, "upload_failed", msg));
      };
      xhr.onerror = () => {
        reject(new ApiError(0, "network", "Upload failed: network error"));
      };
      signal?.addEventListener("abort", () => xhr.abort());
      xhr.send(file);
    });
  },
  async download(orgId: string, projectId: string, key: string): Promise<Blob> {
    const res = await client.blob(`${proj(orgId, projectId)}/storage/download`, { query: { key } });
    return res.blob();
  },
  signedUrl: (orgId: string, projectId: string, key: string, expires = "15m") =>
    r<{ url: string; expires_in_seconds: number }>(`${proj(orgId, projectId)}/storage/signed-url`, {
      method: "POST",
      body: { key, expires },
    }),
  remove: (orgId: string, projectId: string, key: string) =>
    r<void>(`${proj(orgId, projectId)}/storage/objects`, { method: "DELETE", query: { key } }),
};

/* ------------------------------------------------------------- databases */
export const databases = {
  async list(orgId: string, projectId: string, o: Sig = {}): Promise<ManagedDatabase[]> {
    return listOf<ManagedDatabase>(await r<unknown>(`${proj(orgId, projectId)}/databases`, o), "databases");
  },
  async create(orgId: string, projectId: string, b: { name: string; env?: string }): Promise<ManagedDatabase> {
    return entityOf<ManagedDatabase>(await r<unknown>(`${proj(orgId, projectId)}/databases`, { method: "POST", body: b }), "database");
  },
  async get(orgId: string, projectId: string, id: string, o: Sig = {}): Promise<ManagedDatabase> {
    return entityOf<ManagedDatabase>(await r<unknown>(`${proj(orgId, projectId)}/databases/${enc(id)}`, o), "database");
  },
  remove: (orgId: string, projectId: string, id: string) => r<void>(`${proj(orgId, projectId)}/databases/${enc(id)}`, { method: "DELETE" }),
  async connection(orgId: string, projectId: string, id: string, reveal: boolean, o: Sig = {}): Promise<AddonConnection> {
    const raw = await r<unknown>(`${proj(orgId, projectId)}/databases/${enc(id)}/connection`, { query: { reveal: reveal || undefined }, ...o });
    return normalizeConnection(entityOf<AddonConnection & { secret_keys?: string[] }>(raw, "connection"));
  },
  async backups(orgId: string, projectId: string, id: string, o: Sig = {}): Promise<Backup[]> {
    return listOf<Backup>(await r<unknown>(`${proj(orgId, projectId)}/databases/${enc(id)}/backups`, o), "backups");
  },
  createBackup: (orgId: string, projectId: string, id: string) =>
    r<unknown>(`${proj(orgId, projectId)}/databases/${enc(id)}/backups`, { method: "POST", body: {} }),
};

/** the API names the injected variables `secret_keys`; the UI calls them `env_vars` */
function normalizeConnection(c: AddonConnection & { secret_keys?: string[] }): AddonConnection {
  return { ...c, env_vars: c.env_vars ?? c.secret_keys };
}

/** the org service names the user id `user_id`; the UI addresses users by `id` */
function withUserId<T extends { id?: string; user_id?: string }>(u: T): T & { id: string } {
  return { ...u, id: u.id ?? u.user_id ?? "" };
}

/* ---------------------------------------------------------------- addons */
export const addons = {
  async catalog(orgId: string, projectId: string, o: Sig = {}): Promise<AddonCatalogItem[]> {
    return listOf<AddonCatalogItem>(await r<unknown>(`${proj(orgId, projectId)}/addons/catalog`, o), "catalog");
  },
  async list(orgId: string, projectId: string, o: Sig = {}): Promise<ManagedAddon[]> {
    return listOf<ManagedAddon>(await r<unknown>(`${proj(orgId, projectId)}/addons`, o), "addons");
  },
  async listOrg(orgId: string, o: Sig = {}): Promise<ManagedAddon[]> {
    return listOf<ManagedAddon>(await r<unknown>(`${org(orgId)}/addons`, o), "addons");
  },
  async get(orgId: string, projectId: string, id: string, o: Sig = {}): Promise<ManagedAddon> {
    return entityOf<ManagedAddon>(await r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}`, o), "addon");
  },
  async create(
    orgId: string,
    projectId: string,
    b: { engine: string; name: string; env?: string; envs?: string[]; secret_name?: string },
  ): Promise<ManagedAddon> {
    return entityOf<ManagedAddon>(await r<unknown>(`${proj(orgId, projectId)}/addons`, { method: "POST", body: b }), "addon");
  },
  remove: (orgId: string, projectId: string, id: string) => r<void>(`${proj(orgId, projectId)}/addons/${enc(id)}`, { method: "DELETE" }),
  action: (orgId: string, projectId: string, id: string, action: "start" | "stop" | "restart") =>
    r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}/${action}`, { method: "POST", body: {} }),
  async connection(orgId: string, projectId: string, id: string, reveal: boolean, o: Sig = {}): Promise<AddonConnection> {
    const raw = await r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}/connection`, { query: { reveal: reveal || undefined }, ...o });
    return normalizeConnection(entityOf<AddonConnection & { secret_keys?: string[] }>(raw, "connection"));
  },
  async backups(orgId: string, projectId: string, id: string, o: Sig = {}): Promise<Backup[]> {
    return listOf<Backup>(await r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}/backups`, o), "backups");
  },
  createBackup: (orgId: string, projectId: string, id: string) =>
    r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}/backups`, { method: "POST", body: {} }),
  restoreBackup: (orgId: string, projectId: string, id: string, backupId: string) =>
    r<unknown>(`${proj(orgId, projectId)}/addons/${enc(id)}/backups/${enc(backupId)}/restore`, { method: "POST", body: {} }),
};

/* -------------------------------------------------------------------- ai */
export interface AiAnswer {
  answer?: string;
  explanation?: string;
  analysis?: string;
  summary?: string;
  recommendations?: string[];
  severity?: string;
  mode?: string;
  model?: string;
}

export const ai = {
  status: (orgId: string, o: Sig = {}) => r<AiStatus>(`${org(orgId)}/ai/status`, o),
  config: (orgId: string, o: Sig = {}) => r<AiConfig>(`${org(orgId)}/ai/config`, o),
  setConfig: (orgId: string, b: { provider?: string; api_key?: string; model?: string }) =>
    r<unknown>(`${org(orgId)}/ai/config`, { method: "PUT", body: b }),
  clearConfig: (orgId: string) => r<void>(`${org(orgId)}/ai/config`, { method: "DELETE" }),
  ask: (orgId: string, prompt: string, projectId?: string) =>
    r<AiAnswer>(`${org(orgId)}/ai/ask`, { method: "POST", body: { prompt, project_id: projectId } }),
  analyzeLogs: (orgId: string, projectId: string, b: { source?: string; build_id?: string; limit?: number } = {}) =>
    r<AiAnswer>(`${proj(orgId, projectId)}/ai/analyze-logs`, { method: "POST", body: b }),
  explain: (orgId: string, projectId: string, b: { deployment_id?: string; build_id?: string; prompt?: string } = {}) =>
    r<AiAnswer>(`${proj(orgId, projectId)}/ai/explain`, { method: "POST", body: b }),
  overview: (orgId: string, projectId: string) =>
    r<AiAnswer & { overview?: string; suggestions?: string | string[]; risk_level?: string }>(
      `${proj(orgId, projectId)}/ai/overview-suggestions`,
      { method: "POST", body: {} },
    ),
};

/* --------------------------------------------------------------- system */
export const system = {
  async settings(o: Sig = {}): Promise<SettingField[]> {
    const d = asRecord(await r<unknown>("/system/settings", o));
    if (Array.isArray(d.settings)) return d.settings as SettingField[];
    // Tolerate a flat { key: value } shape.
    return Object.entries(d)
      .filter(([, v]) => typeof v !== "object" || v === null)
      .map(([key, value]) => ({ key, value: value as SettingField["value"], editable: false }));
  },
  patchSettings: (b: Record<string, string | number | boolean | null>) =>
    r<unknown>("/system/settings", { method: "PATCH", body: b }),
  async security(o: Sig = {}): Promise<SecurityReport> {
    const d = asRecord(await r<unknown>("/system/security", o));
    return { checks: (Array.isArray(d.checks) ? d.checks : []) as SecurityCheck[], generated_at: str(d.generated_at) };
  },
  cluster: (o: Sig = {}) => r<ClusterInfo>("/system/cluster", o),
  async servers(o: Sig = {}): Promise<ServerEntry[]> {
    const d = asRecord(await r<unknown>("/cluster/servers", o));
    return (Array.isArray(d.servers) ? d.servers : []) as ServerEntry[];
  },
  saveServer: (s: ServerInput) => r<ServerEntry>(`/cluster/servers/${encodeURIComponent(s.name)}`, { method: "PUT", body: s }),
  async remoteNodes(o: Sig = {}): Promise<RemoteNode[]> {
    const d = asRecord(await r<unknown>("/cluster/remote-nodes", o));
    return (Array.isArray(d.nodes) ? d.nodes : []) as RemoteNode[];
  },
  deleteNode: (name: string) => r<unknown>(`/cluster/remote-nodes/${encodeURIComponent(name)}`, { method: "DELETE" }),
  async placement(projectId: string, o: Sig = {}): Promise<string> {
    const d = asRecord(await r<unknown>(`/cluster/placements/${encodeURIComponent(projectId)}`, o));
    return str(d.node) ?? "";
  },
  setPlacement: (projectId: string, node: string) => r<unknown>(`/cluster/placements/${encodeURIComponent(projectId)}`, { method: "PUT", body: { node } }),
  createJoinToken: (b: Partial<ServerInput> & { name: string; node?: boolean }) => r<JoinToken>("/cluster/join-tokens", { method: "POST", body: b }),
  checkServer: (name: string) => r<ServerEntry>(`/cluster/servers/${encodeURIComponent(name)}/check`, { method: "POST", body: {} }),
  deleteServer: (name: string) => r<unknown>(`/cluster/servers/${encodeURIComponent(name)}`, { method: "DELETE" }),
};

export const api = {
  auth,
  orgs,
  users,
  projects,
  git,
  deployments,
  builds,
  domains,
  runtime,
  secrets,
  logs,
  metrics,
  storage,
  databases,
  addons,
  ai,
  system,
};

export type { RequestOptions };
