import { clearTokens, getAccessToken, getRefreshToken, setTokens } from "./storage";
import type {
  AuthResponse,
  AuthTokens,
  Invite,
  Org,
  OrgMember,
  Pat,
  Project,
  Session,
  User,
  ApiErrorBody,
  GitInstallation,
  ConnectedRepo,
  AvailableRepo,
  Deployment,
  Build,
  Domain,
  RuntimeInstance,
  Certificate,
  ProjectEnvironment,
  SecretMeta,
  LogEntry,
  MetricSummary,
  StorageBucket,
  StorageObject,
  ManagedDatabase,
  AddonCatalogItem,
  ManagedAddon,
  BillingPlan,
  BillingUsageRow,
  ProjectConfigResponse,
} from "./types";

const API_URL =
  process.env.NEXT_PUBLIC_API_URL?.replace(/\/$/, "") ||
  "http://localhost:8000/api/v1";

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type RequestOptions = Omit<RequestInit, "body"> & {
  body?: unknown;
  auth?: boolean;
  skipRefresh?: boolean;
};

let refreshPromise: Promise<boolean> | null = null;

/** Slugify for org/project create (API requires slug). */
export function slugify(input: string): string {
  const base = input
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  return base || `item-${Date.now().toString(36)}`;
}

function errorMessage(data: unknown, status: number): string {
  const err = data as ApiErrorBody | null;
  if (!err) return `Request failed (${status})`;
  if (typeof err.error === "object" && err.error?.message) {
    return err.error.message;
  }
  if (typeof err.error === "string" && err.error) return err.error;
  return err.message || err.detail || `Request failed (${status})`;
}

async function refreshAccessToken(): Promise<boolean> {
  const refresh = getRefreshToken();
  if (!refresh) return false;

  try {
    const res = await fetch(`${API_URL}/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refresh }),
    });
    if (!res.ok) {
      clearTokens();
      return false;
    }
    const data = (await res.json()) as AuthTokens;
    setTokens(data.access_token, data.refresh_token || refresh);
    return true;
  } catch {
    clearTokens();
    return false;
  }
}

async function ensureRefreshed(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = refreshAccessToken().finally(() => {
      refreshPromise = null;
    });
  }
  return refreshPromise;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, auth = true, skipRefresh = false, headers, ...rest } = options;
  const finalHeaders = new Headers(headers);

  if (body !== undefined) {
    finalHeaders.set("Content-Type", "application/json");
  }

  if (auth) {
    const token = getAccessToken();
    if (token) finalHeaders.set("Authorization", `Bearer ${token}`);
  }

  const res = await fetch(`${API_URL}${path}`, {
    ...rest,
    headers: finalHeaders,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 401 && auth && !skipRefresh) {
    const ok = await ensureRefreshed();
    if (ok) {
      return request<T>(path, { ...options, skipRefresh: true });
    }
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { message: text };
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, errorMessage(data, res.status));
  }

  return data as T;
}

function unwrap<T>(data: unknown, key: string): T {
  if (data && typeof data === "object" && key in (data as object)) {
    return (data as Record<string, T>)[key];
  }
  return data as T;
}

export const api = {
  baseUrl: API_URL,

  // Auth
  register(payload: { email: string; password: string; name: string }) {
    return request<AuthResponse>("/auth/register", {
      method: "POST",
      body: payload,
      auth: false,
    });
  },
  login(payload: { email: string; password: string }) {
    return request<AuthResponse>("/auth/login", {
      method: "POST",
      body: payload,
      auth: false,
    });
  },
  logout() {
    return request<void>("/auth/logout", { method: "POST" });
  },
  async me() {
    const data = await request<{ user: User } | User>("/auth/me");
    return unwrap<User>(data, "user");
  },
  async listSessions() {
    const data = await request<{ sessions: Session[] } | Session[]>(
      "/auth/sessions",
    );
    return unwrap<Session[]>(data, "sessions") || [];
  },
  deleteSession(sessionId: string) {
    return request<void>(`/auth/sessions/${sessionId}`, { method: "DELETE" });
  },
  async deleteAllSessions() {
    const sessions = await this.listSessions();
    await Promise.all(sessions.map((s) => this.deleteSession(s.id)));
  },
  async listPats() {
    const data = await request<{ pats: Pat[] } | Pat[]>("/auth/pats");
    return unwrap<Pat[]>(data, "pats") || [];
  },
  createPat(payload: { name: string; scopes?: string[] }) {
    return request<Pat>("/auth/pats", { method: "POST", body: payload });
  },
  deletePat(patId: string) {
    return request<void>(`/auth/pats/${patId}`, { method: "DELETE" });
  },

  // Orgs
  async listOrgs() {
    const data = await request<{ orgs: Org[] } | Org[]>("/orgs");
    return unwrap<Org[]>(data, "orgs") || [];
  },
  async createOrg(payload: { name: string; slug?: string }) {
    const body = {
      name: payload.name,
      slug: payload.slug?.trim() || slugify(payload.name),
    };
    const data = await request<{ org: Org } | Org>("/orgs", {
      method: "POST",
      body,
    });
    return unwrap<Org>(data, "org");
  },
  async getOrg(orgId: string) {
    const data = await request<{ org: Org } | Org>(`/orgs/${orgId}`);
    return unwrap<Org>(data, "org");
  },
  inviteMember(orgId: string, payload: { email: string; role: string }) {
    return request<Invite>(`/orgs/${orgId}/invites`, {
      method: "POST",
      body: payload,
    });
  },
  async acceptInvite(token: string) {
    const data = await request<{ org: Org } | Org>("/orgs/invites/accept", {
      method: "POST",
      body: { token },
    });
    return unwrap<Org>(data, "org");
  },
  async listMembers(orgId: string) {
    const data = await request<{ members: OrgMember[] } | OrgMember[]>(
      `/orgs/${orgId}/members`,
    );
    return unwrap<OrgMember[]>(data, "members") || [];
  },

  // Projects
  async listProjects(orgId: string) {
    const data = await request<{ projects: Project[] } | Project[]>(
      `/orgs/${orgId}/projects`,
    );
    return unwrap<Project[]>(data, "projects") || [];
  },
  async createProject(
    orgId: string,
    payload: { name: string; slug?: string; description?: string },
  ) {
    const body = {
      name: payload.name,
      slug: payload.slug?.trim() || slugify(payload.name),
      description: payload.description,
    };
    const data = await request<{ project: Project } | Project>(
      `/orgs/${orgId}/projects`,
      { method: "POST", body },
    );
    return unwrap<Project>(data, "project");
  },
  async getProject(orgId: string, projectId: string) {
    const data = await request<{ project: Project } | Project>(
      `/orgs/${orgId}/projects/${projectId}`,
    );
    return unwrap<Project>(data, "project");
  },
  async updateProject(
    orgId: string,
    projectId: string,
    payload: { name?: string; description?: string; slug?: string },
  ) {
    const data = await request<{ project: Project } | Project>(
      `/orgs/${orgId}/projects/${projectId}`,
      { method: "PATCH", body: payload },
    );
    return unwrap<Project>(data, "project");
  },
  deleteProject(orgId: string, projectId: string) {
    return request<void>(`/orgs/${orgId}/projects/${projectId}`, {
      method: "DELETE",
    });
  },

  // Git / repos
  async startGitInstall(orgId: string) {
    return request<{
      install_url: string;
      state: string;
      mode: string;
      message?: string;
    }>(`/orgs/${orgId}/github/install/start`, { method: "POST", body: {} });
  },
  async completeGitInstall(
    orgId: string,
    payload?: { installation_id?: string; account_login?: string },
  ) {
    const data = await request<{ installation: import("./types").GitInstallation }>(
      `/orgs/${orgId}/github/install/callback`,
      { method: "POST", body: payload || {} },
    );
    return unwrap(data, "installation") as import("./types").GitInstallation;
  },
  async listGitInstallations(orgId: string) {
    const data = await request<{ installations: import("./types").GitInstallation[] }>(
      `/orgs/${orgId}/github/installations`,
    );
    const list = unwrap<import("./types").GitInstallation[]>(data, "installations");
    return Array.isArray(list) ? list : [];
  },
  async connectRepo(
    orgId: string,
    projectId: string,
    payload: {
      full_name: string;
      clone_url?: string;
      default_branch?: string;
      installation_id?: string;
    },
  ) {
    const data = await request<{ repo: import("./types").ConnectedRepo }>(
      `/orgs/${orgId}/projects/${projectId}/repos`,
      { method: "POST", body: payload },
    );
    return unwrap(data, "repo") as import("./types").ConnectedRepo;
  },
  async listRepos(orgId: string, projectId: string) {
    const data = await request<{ repos: ConnectedRepo[] }>(
      `/orgs/${orgId}/projects/${projectId}/repos`,
    );
    return (unwrap(data, "repos") as ConnectedRepo[]) || [];
  },
  async listAvailableRepos(orgId: string) {
    const data = await request<{ repos: AvailableRepo[]; mode?: string }>(
      `/orgs/${orgId}/github/repos`,
    );
    return {
      repos: (unwrap(data, "repos") as AvailableRepo[]) || [],
      mode: (data as { mode?: string }).mode || "mock",
    };
  },
  disconnectRepo(orgId: string, projectId: string, repoId: string) {
    return request<void>(`/orgs/${orgId}/projects/${projectId}/repos/${repoId}`, {
      method: "DELETE",
    });
  },

  // Deployments
  async listDeployments(orgId: string, projectId: string) {
    const data = await request<{ deployments: Deployment[] }>(
      `/orgs/${orgId}/projects/${projectId}/deployments`,
    );
    return (unwrap(data, "deployments") as Deployment[]) || [];
  },
  async createDeployment(
    orgId: string,
    projectId: string,
    payload?: {
      git_sha?: string;
      git_branch?: string;
      clone_url?: string;
      full_name?: string;
      message?: string;
      repo_id?: string;
    },
  ) {
    const data = await request<{ deployment: Deployment }>(
      `/orgs/${orgId}/projects/${projectId}/deployments`,
      { method: "POST", body: payload || {} },
    );
    return unwrap(data, "deployment") as Deployment;
  },
  async rollbackDeployment(orgId: string, projectId: string, deploymentId?: string) {
    const path = deploymentId
      ? `/orgs/${orgId}/projects/${projectId}/deployments/${deploymentId}/rollback`
      : `/orgs/${orgId}/projects/${projectId}/deployments/rollback`;
    const data = await request<{ deployment: Deployment }>(path, {
      method: "POST",
      body: {},
    });
    return unwrap(data, "deployment") as Deployment;
  },

  // Builds
  async listBuilds(orgId: string, projectId: string) {
    const data = await request<{ builds: Build[] }>(
      `/orgs/${orgId}/projects/${projectId}/builds`,
    );
    return (unwrap(data, "builds") as Build[]) || [];
  },
  async getBuildLogs(orgId: string, projectId: string, buildId: string) {
    return request<{ build_id: string; status: string; logs: string }>(
      `/orgs/${orgId}/projects/${projectId}/builds/${buildId}/logs`,
    );
  },

  // Domains
  async listDomains(orgId: string, projectId: string) {
    const data = await request<{ domains: Domain[] }>(
      `/orgs/${orgId}/projects/${projectId}/domains`,
    );
    return (unwrap(data, "domains") as Domain[]) || [];
  },
  async addDomain(
    orgId: string,
    projectId: string,
    payload: { hostname: string; verification_type?: string; deployment_id?: string },
  ) {
    const data = await request<{ domain: Domain }>(
      `/orgs/${orgId}/projects/${projectId}/domains`,
      { method: "POST", body: payload },
    );
    return unwrap(data, "domain") as Domain;
  },
  async verifyDomain(orgId: string, projectId: string, domainId: string, force?: boolean) {
    const data = await request<{ domain: Domain; verified: boolean }>(
      `/orgs/${orgId}/projects/${projectId}/domains/${domainId}/verify`,
      { method: "POST", body: { force: !!force } },
    );
    return data;
  },
  deleteDomain(orgId: string, projectId: string, domainId: string) {
    return request<void>(`/orgs/${orgId}/projects/${projectId}/domains/${domainId}`, {
      method: "DELETE",
    });
  },

  // Runtime
  async listRuntimeInstances(orgId: string, projectId: string) {
    const data = await request<{ instances: RuntimeInstance[]; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/runtime/instances`,
    );
    return {
      instances: (unwrap(data, "instances") as RuntimeInstance[]) || [],
      mode: data.mode || "simulate",
    };
  },
  async startRuntimeInstance(orgId: string, projectId: string, instanceId: string) {
    const data = await request<{ instance: RuntimeInstance }>(
      `/orgs/${orgId}/projects/${projectId}/runtime/instances/${instanceId}/start`,
      { method: "POST", body: {} },
    );
    return unwrap(data, "instance") as RuntimeInstance;
  },
  async stopRuntimeInstance(orgId: string, projectId: string, instanceId: string) {
    const data = await request<{ instance: RuntimeInstance }>(
      `/orgs/${orgId}/projects/${projectId}/runtime/instances/${instanceId}/stop`,
      { method: "POST", body: {} },
    );
    return unwrap(data, "instance") as RuntimeInstance;
  },

  // Certificates
  async listCertificates(orgId: string, projectId: string) {
    const data = await request<{ certificates: Certificate[] }>(
      `/orgs/${orgId}/projects/${projectId}/certificates`,
    );
    return (unwrap(data, "certificates") as Certificate[]) || [];
  },

  // Secrets & environments
  async listEnvironments(orgId: string, projectId: string) {
    const data = await request<{ environments: ProjectEnvironment[] }>(
      `/orgs/${orgId}/projects/${projectId}/environments`,
    );
    return (unwrap(data, "environments") as ProjectEnvironment[]) || [];
  },
  async listSecrets(orgId: string, projectId: string, env: string) {
    const data = await request<{ secrets: SecretMeta[] }>(
      `/orgs/${orgId}/projects/${projectId}/environments/${env}/secrets`,
    );
    return (unwrap(data, "secrets") as SecretMeta[]) || [];
  },
  async setSecret(orgId: string, projectId: string, env: string, name: string, value: string) {
    const data = await request<{ secret: SecretMeta }>(
      `/orgs/${orgId}/projects/${projectId}/environments/${env}/secrets/${encodeURIComponent(name)}`,
      { method: "PUT", body: { value } },
    );
    return unwrap(data, "secret") as SecretMeta;
  },
  deleteSecret(orgId: string, projectId: string, env: string, name: string) {
    return request<void>(
      `/orgs/${orgId}/projects/${projectId}/environments/${env}/secrets/${encodeURIComponent(name)}`,
      { method: "DELETE" },
    );
  },

  // Logs
  async queryLogs(
    orgId: string,
    projectId: string,
    params?: { source?: string; build_id?: string; q?: string; limit?: number },
  ) {
    const qs = new URLSearchParams();
    if (params?.source) qs.set("source", params.source);
    if (params?.build_id) qs.set("build_id", params.build_id);
    if (params?.q) qs.set("q", params.q);
    if (params?.limit) qs.set("limit", String(params.limit));
    const suffix = qs.toString() ? `?${qs}` : "";
    return request<{ entries: LogEntry[]; build_logs?: string; backend?: string }>(
      `/orgs/${orgId}/projects/${projectId}/logs${suffix}`,
    );
  },
  async ingestLog(
    orgId: string,
    projectId: string,
    payload: { source?: string; level?: string; message: string },
  ) {
    return request<{ ingested: number }>(`/orgs/${orgId}/projects/${projectId}/logs`, {
      method: "POST",
      body: payload,
    });
  },

  // Metrics
  async projectMetrics(orgId: string, projectId: string) {
    const data = await request<{
      metrics: MetricSummary[];
      mode?: string;
      prometheus_url?: string;
    }>(`/orgs/${orgId}/projects/${projectId}/metrics`);
    return {
      metrics: (unwrap(data, "metrics") as MetricSummary[]) || [],
      mode: data.mode || "simulate",
      prometheus_url: data.prometheus_url,
    };
  },

  // Phase 6 — Storage
  async getStorageBucket(orgId: string, projectId: string) {
    return request<{ bucket: StorageBucket; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/storage/bucket`,
    );
  },
  async listStorageObjects(orgId: string, projectId: string, prefix?: string) {
    const qs = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
    const data = await request<{ objects: StorageObject[]; bucket?: string; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/storage/objects${qs}`,
    );
    return {
      objects: (unwrap(data, "objects") as StorageObject[]) || [],
      bucket: data.bucket,
      mode: data.mode,
    };
  },
  async uploadStorageObject(
    orgId: string,
    projectId: string,
    key: string,
    dataBase64: string,
    contentType?: string,
  ) {
    const data = await request<{ object: StorageObject; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/storage/objects`,
      { method: "POST", body: { key, data_base64: dataBase64, content_type: contentType } },
    );
    return unwrap(data, "object") as StorageObject;
  },
  async signedStorageURL(orgId: string, projectId: string, key: string, expires?: string) {
    return request<{ url: string; key: string; expires_in_seconds: number; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/storage/signed-url`,
      { method: "POST", body: { key, expires } },
    );
  },
  deleteStorageObject(orgId: string, projectId: string, key: string) {
    return request<void>(
      `/orgs/${orgId}/projects/${projectId}/storage/objects?key=${encodeURIComponent(key)}`,
      { method: "DELETE" },
    );
  },

  // Phase 6 — Databases
  async listDatabases(orgId: string, projectId: string) {
    const data = await request<{ databases: ManagedDatabase[]; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/databases`,
    );
    return {
      databases: (unwrap(data, "databases") as ManagedDatabase[]) || [],
      mode: data.mode,
    };
  },
  async createDatabase(orgId: string, projectId: string, name: string, env?: string) {
    const data = await request<{ database: ManagedDatabase }>(
      `/orgs/${orgId}/projects/${projectId}/databases`,
      { method: "POST", body: { name, env: env || "development" } },
    );
    return unwrap(data, "database") as ManagedDatabase;
  },
  deleteDatabase(orgId: string, projectId: string, dbId: string) {
    return request<void>(`/orgs/${orgId}/projects/${projectId}/databases/${dbId}`, {
      method: "DELETE",
    });
  },

  // Phase 6 — Add-ons marketplace
  async addonCatalog(orgId: string, projectId: string) {
    const data = await request<{ catalog: AddonCatalogItem[]; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/addons/catalog`,
    );
    return {
      catalog: (unwrap(data, "catalog") as AddonCatalogItem[]) || [],
      mode: data.mode,
    };
  },
  async listAddons(orgId: string, projectId: string, engine?: string) {
    const q = engine ? `?engine=${encodeURIComponent(engine)}` : "";
    const data = await request<{ addons: ManagedAddon[]; mode?: string }>(
      `/orgs/${orgId}/projects/${projectId}/addons${q}`,
    );
    return {
      addons: (unwrap(data, "addons") as ManagedAddon[]) || [],
      mode: data.mode,
    };
  },
  async createAddon(
    orgId: string,
    projectId: string,
    engine: string,
    name: string,
    env?: string,
  ) {
    const data = await request<{ addon: ManagedAddon }>(
      `/orgs/${orgId}/projects/${projectId}/addons`,
      {
        method: "POST",
        body: { engine, name, env: env || "development" },
      },
    );
    return unwrap(data, "addon") as ManagedAddon;
  },
  deleteAddon(orgId: string, projectId: string, addonId: string) {
    return request<void>(`/orgs/${orgId}/projects/${projectId}/addons/${addonId}`, {
      method: "DELETE",
    });
  },

  // Phase 7 — Config / AI / Billing
  async getProjectConfig(orgId: string, projectId: string) {
    return request<ProjectConfigResponse>(`/orgs/${orgId}/projects/${projectId}/config`);
  },
  async getConfigDrift(orgId: string, projectId: string) {
    return request<ProjectConfigResponse>(`/orgs/${orgId}/projects/${projectId}/config/drift`);
  },
  async explainFailure(
    orgId: string,
    projectId: string,
    payload?: { deployment_id?: string; build_id?: string; prompt?: string },
  ) {
    return request<{ explanation: string; mode: string }>(
      `/orgs/${orgId}/projects/${projectId}/ai/explain`,
      { method: "POST", body: payload || {} },
    );
  },
  async aiAsk(orgId: string, prompt: string, projectId?: string) {
    return request<{ answer: string; mode: string }>(`/orgs/${orgId}/ai/ask`, {
      method: "POST",
      body: { prompt, project_id: projectId },
    });
  },
  async aiStatus(orgId: string) {
    return request<{ mode: string; model?: string; tools?: string[] }>(
      `/orgs/${orgId}/ai/status`,
    );
  },
  async billingPlans(orgId: string) {
    const data = await request<{ plans: BillingPlan[] }>(`/orgs/${orgId}/billing/plans`);
    return (unwrap(data, "plans") as BillingPlan[]) || [];
  },
  async billingUsage(orgId: string) {
    return request<{
      usage: BillingUsageRow[];
      plan_id?: string;
      plan?: BillingPlan;
      stub_note?: string;
    }>(`/orgs/${orgId}/billing/usage`);
  },
};
