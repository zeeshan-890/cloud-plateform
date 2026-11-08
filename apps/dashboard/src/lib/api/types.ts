export type Role = "owner" | "admin" | "member" | "viewer";

export type Area =
  | "overview"
  | "deployments"
  | "builds"
  | "runtime"
  | "domains"
  | "secrets"
  | "addons"
  | "databases"
  | "storage"
  | "logs"
  | "metrics"
  | "repository"
  | "settings";
export type Level = "none" | "view" | "manage";
export type Preset = "viewer" | "developer" | "operator" | "project_admin" | "custom";

export interface MembershipProject {
  project_id: string;
  name?: string;
  areas: Partial<Record<Area, Level>>;
  secrets_reveal?: boolean;
}

export interface Membership {
  org_id: string;
  org_name?: string;
  role: Role;
  /** owner/admin: everything on every project */
  full?: boolean;
  projects?: MembershipProject[];
}

export interface User {
  id: string;
  email?: string;
  username?: string;
  name: string;
  is_admin?: boolean;
  totp_enabled?: boolean;
  must_change_password?: boolean;
  memberships?: Membership[];
  created_at?: string;
}

export interface Grant {
  project_id: string;
  preset?: Preset;
  areas?: Partial<Record<Area, Level>>;
  secrets_reveal?: boolean;
}

export interface ManagedUser {
  id: string;
  name: string;
  username?: string;
  email?: string;
  role: Role;
  disabled?: boolean;
  status?: string;
  must_change_password?: boolean;
  last_login_at?: string | null;
  projects_count?: number;
  grants?: Grant[];
  created_at?: string;
}

export interface CreatedUser {
  user: ManagedUser;
  /** shown once; the server never returns it again (`generated_password` is the real field; `password` kept for compatibility) */
  generated_password?: string;
  password?: string;
}

export interface CreateUserInput {
  name: string;
  username: string;
  email?: string;
  password?: string;
  role: "admin" | "member";
  must_change_password: boolean;
  grants: Grant[];
}

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type?: string;
  expires_in?: number;
}

export interface AuthResponse extends AuthTokens {
  user: User;
  /** limited token: only change-password and /auth/me work until the password is changed */
  must_change_password?: boolean;
}

/** Login first step answer when the account has MFA on. */
export interface MfaChallenge {
  mfa_required: true;
  mfa_token: string;
}

export type LoginResult = AuthResponse | MfaChallenge;

export function isMfaChallenge(r: LoginResult): r is MfaChallenge {
  return (r as MfaChallenge).mfa_required === true;
}

export type RegistrationMode = "open" | "invite" | "closed";

export interface BootstrapStatus {
  initialized: boolean;
  registration?: RegistrationMode;
}

export interface MfaSetup {
  secret: string;
  otpauth_uri: string;
}

export interface Session {
  id: string;
  user_agent?: string;
  ip?: string;
  created_at: string;
  last_active_at?: string;
  expires_at?: string;
  current?: boolean;
}

export interface Pat {
  id: string;
  name: string;
  token_prefix?: string;
  scopes?: string[];
  token?: string;
  created_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
}

export interface Org {
  id: string;
  name: string;
  slug?: string;
  role?: Role;
  created_at?: string;
}

export interface OrgMember {
  user_id: string;
  email: string;
  name: string;
  role: Role;
  joined_at?: string;
}

export interface Invite {
  id: string;
  email: string;
  role: Role;
  token?: string;
  status?: string;
  expires_at?: string;
  created_at?: string;
}

export interface Project {
  id: string;
  org_id: string;
  name: string;
  slug?: string;
  description?: string;
  system_domain?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GitInstallation {
  id: string;
  org_id: string;
  installation_id: string;
  account_login: string;
  status: string;
  created_at?: string;
}

export interface GitInstallStart {
  install_url: string;
  state: string;
  mode?: string;
  message?: string;
}

export interface DeployKey {
  id: string;
  project_id: string;
  public_key: string;
  key_type: string;
  fingerprint?: string;
  created_at: string;
}

export interface ConnectedRepo {
  id: string;
  org_id: string;
  project_id: string;
  full_name: string;
  clone_url: string;
  default_branch: string;
  provider?: string;
  created_at?: string;
  /** app | token | deploy_key | none */
  auth_type?: string;
  allow_forks?: boolean;
  /** continuous deployment (see Project > Repository > Deployment settings) */
  root_dir?: string;
  auto_deploy?: boolean;
  deploy_branch?: string;
  branch_previews?: boolean;
  pr_previews?: boolean;
  watch_paths?: string[];
  ignore_paths?: string[];
  require_ci?: boolean;
}

/** The editable continuous-deployment settings of a connection. */
export interface RepoSettings {
  root_dir: string;
  auto_deploy: boolean;
  deploy_branch: string;
  branch_previews: boolean;
  pr_previews: boolean;
  watch_paths: string[];
  ignore_paths: string[];
  require_ci: boolean;
}

/** Shown exactly once, when a connection is created or its webhook secret is rotated. */
export interface WebhookInfo {
  webhook_secret: string;
  webhook_url: string;
  webhook_note?: string;
}

export interface WebhookInstall {
  installed: boolean;
  created: boolean;
  hook_id: number;
  events: string[];
  webhook_url: string;
}

export interface RepoFolders {
  path: string;
  ref: string;
  folders: { name: string; path: string }[];
  markers: string[];
}

export interface AvailableRepo {
  full_name: string;
  clone_url: string;
  default_branch: string;
  private?: boolean;
}

export interface Deployment {
  id: string;
  org_id: string;
  project_id: string;
  status: string;
  source?: string;
  strategy?: string;
  git_sha?: string;
  git_branch?: string;
  full_name?: string;
  message?: string;
  image_ref?: string;
  commit_status?: string;
  preview_url?: string;
  build_id?: string;
  rollback_of?: string;
  error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface SecurityScan {
  status?: string;
  critical?: number;
  high?: number;
  medium?: number;
  low?: number;
  scanner?: string;
  summary?: string;
  findings?: Array<{ id?: string; severity?: string; package?: string; title?: string; fixed_in?: string }>;
}

export interface Build {
  id: string;
  org_id: string;
  project_id: string;
  deployment_id?: string;
  status: string;
  framework?: string;
  image_ref?: string;
  git_sha?: string;
  git_branch?: string;
  full_name?: string;
  logs?: string;
  error?: string;
  security_scan?: SecurityScan | null;
  created_at?: string;
  started_at?: string;
  finished_at?: string;
}

export interface DnsRecord {
  type: string;
  name: string;
  value: string;
  purpose?: string;
  ttl?: number;
}

export interface DnsDiagnostics {
  ok?: boolean;
  method?: string;
  detail?: string;
  records?: string[];
  expected?: string;
}

export interface Domain {
  id: string;
  org_id: string;
  project_id: string;
  hostname: string;
  kind?: string;
  status: string;
  verification_type?: string;
  verified_method?: string;
  verified_at?: string;
  certificate_id?: string;
  routed?: boolean;
  ssl_status?: string;
  ssl_expires_at?: string;
  ssl_error?: string;
  url?: string;
  dns_last_checked?: string;
  dns_error?: string;
  dns_records?: DnsRecord[];
  created_at?: string;
}

export interface DomainDetail {
  domain: Domain;
  records: DnsRecord[];
  diagnostics?: DnsDiagnostics;
}

export interface Certificate {
  id: string;
  org_id: string;
  project_id: string;
  domain_id?: string;
  hostname: string;
  status: string;
  provider?: string;
  expires_at?: string;
  renewed_at?: string;
  created_at?: string;
}

export interface RuntimeInstance {
  id: string;
  org_id: string;
  project_id: string;
  deployment_id?: string;
  kind: string;
  role?: string;
  image_ref: string;
  status: string;
  desired_state: string;
  container_id?: string;
  container_name?: string;
  slot?: string;
  color?: string;
  mode?: string;
  health_status?: string;
  port?: number;
  restart_count?: number;
  is_preview?: boolean;
  error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface RuntimeContainer {
  id: string;
  name: string;
  image?: string;
  state: string;
  status?: string;
  kind?: string;
  project_id?: string;
  created_at?: string;
}

export interface RuntimeLogs {
  instance?: RuntimeInstance | null;
  container_id?: string;
  container_name?: string;
  status?: string;
  entries: LogEntry[];
}

export type EnvironmentName = "development" | "preview" | "staging" | "production";
export const ENVIRONMENTS: EnvironmentName[] = ["development", "preview", "staging", "production"];

export interface SecretMeta {
  id: string;
  org_id?: string;
  project_id?: string;
  environment: string;
  name: string;
  current_version: number;
  value_hint?: string;
  value?: string;
  source?: string;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
}

export interface LogEntry {
  id?: string;
  org_id?: string;
  project_id?: string;
  source: string;
  level: string;
  message: string;
  build_id?: string;
  instance_id?: string;
  request_id?: string;
  logged_at: string;
}

export interface MetricSummary {
  name: string;
  latest: number;
  count: number;
  recorded_at?: string;
  series?: Array<{ t: string; v: number }>;
  unit?: string;
}

export interface StorageBucket {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  mode?: string;
  created_at?: string;
}

export interface StorageObject {
  id?: string;
  key: string;
  size_bytes: number;
  content_type?: string;
  etag?: string;
  created_at?: string;
  updated_at?: string;
}

export interface StorageUsage {
  used_bytes: number;
  quota_bytes?: number;
  object_count?: number;
}

export interface ManagedDatabase {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  engine?: string;
  mode?: string;
  status: string;
  error?: string;
  endpoint?: string;
  secret_ref?: string;
  connection_hint?: string;
  created_at?: string;
}

export interface AddonCatalogItem {
  id: string;
  name: string;
  category: string;
  description: string;
  default_port?: number;
  default_image?: string;
  scheme?: string;
  logo?: string;
  secret_keys?: string[];
  modes_supported?: string[];
  provisionable?: boolean;
  unavailable_reason?: string;
}

export interface ManagedAddon {
  id: string;
  org_id: string;
  project_id: string;
  engine: string;
  name: string;
  mode?: string;
  status: string;
  error?: string;
  endpoint?: string;
  secret_ref?: string;
  connection_hint?: string;
  injected_env?: string[];
  environments?: string[];
  container_id?: string;
  metadata?: Record<string, unknown>;
  created_at?: string;
}

export interface AddonConnection {
  url?: string;
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  database?: string;
  env_vars?: string[];
  masked_url?: string;
}

export interface Backup {
  id: string;
  status: string;
  size_bytes?: number;
  error?: string;
  created_at?: string;
}

export interface AiStatus {
  mode?: string;
  provider?: string;
  model?: string;
  configured?: boolean;
  tools?: string[];
}

export interface AiConfig {
  mode?: string;
  provider?: string;
  model?: string;
  has_key?: boolean;
  masked_key?: string;
}

export interface SecurityCheck {
  id: string;
  title: string;
  status: "pass" | "warn" | "fail" | "unknown" | string;
  detail?: string;
  remediation?: string;
  category?: string;
  severity?: string;
}

export interface SecurityReport {
  checks: SecurityCheck[];
  generated_at?: string;
}

export interface SettingField {
  key: string;
  label?: string;
  value?: string | number | boolean | null;
  secret?: boolean;
  editable?: boolean;
  source?: string;
  group?: string;
  description?: string;
  is_set?: boolean;
}

export interface SystemSettings {
  settings?: SettingField[];
  [k: string]: unknown;
}

export interface ClusterNode {
  hostname?: string;
  os?: string;
  arch?: string;
  cpu_count?: number;
  cpu_percent?: number;
  load?: number[];
  memory_total_bytes?: number;
  memory_used_bytes?: number;
  disk_total_bytes?: number;
  disk_used_bytes?: number;
  uptime_seconds?: number;
  docker_version?: string;
  containers_running?: number;
  containers_total?: number;
  containers_stopped?: number;
  images?: number;
}

export interface ClusterInfo {
  node: ClusterNode;
  generated_at?: string;
}

export type ServerRole = "control-plane" | "database" | "api" | "worker" | "storage" | "other";
export type ServerPath = "private" | "public" | "auto";
export type PortScope = "private" | "public" | "both";
export interface ServerPort { port: number; proto: "tcp" | "udp"; scope: PortScope; label: string }
export interface ServerInput {
  name: string;
  role: ServerRole;
  public_addr: string;
  private_addr: string;
  prefer: ServerPath;
  ports: ServerPort[];
  notes: string;
}
export interface CheckResult { peer: string; path: "private" | "public"; addr: string; port: number; ok: boolean; ms?: number; error?: string }
export interface Diagnostic { level: "error" | "warn" | "ok" | "info"; code: string; message: string; fix?: string }
export interface ServerReport {
  at: string;
  hostname: string;
  private_ip: string;
  private_cidr: string;
  public_ip: string;
  provider: string;
  os: string;
  firewall: string;
  docker: boolean;
  checks: CheckResult[] | null;
  inbound: CheckResult[] | null;
  diagnostics: Diagnostic[] | null;
  status: "ok" | "warn" | "error";
}
export interface ServerEntry extends ServerInput { updated_at?: string; report?: ServerReport; node?: boolean }
export interface JoinToken { token: string; name: string; existing: boolean; node?: boolean; expires_at: string; command: string; script_url: string }

/** A server that can run projects (it has the node agent and is registered with the runtime). */
export interface RemoteNode {
  id: string;
  name: string;
  addr: string;
  private_ip: string;
  enabled: boolean;
  reachable: boolean;
  error?: string;
  docker_version?: string;
  architecture?: string;
  control_architecture?: string;
  cpus?: number;
  memory_total_bytes?: number;
  containers_running: number;
  projects: number;
  instances: number;
  latency_ms?: number;
}
