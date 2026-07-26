export type Role = "owner" | "admin" | "member" | "viewer";

export interface User {
  id: string;
  email: string;
  name: string;
  created_at?: string;
}

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type?: string;
  expires_in?: number;
}

export interface AuthResponse extends AuthTokens {
  user: User;
}

export interface Session {
  id: string;
  user_agent?: string;
  ip?: string;
  created_at: string;
  last_active_at?: string;
  current?: boolean;
}

export interface Pat {
  id: string;
  name: string;
  token_prefix?: string;
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
  created_at?: string;
}

export interface Project {
  id: string;
  org_id: string;
  name: string;
  slug?: string;
  description?: string;
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

export interface ConnectedRepo {
  id: string;
  org_id: string;
  project_id: string;
  full_name: string;
  clone_url: string;
  default_branch: string;
  provider?: string;
  created_at?: string;
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
  build_id?: string;
  rollback_of?: string;
  error?: string;
  created_at?: string;
  updated_at?: string;
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
  created_at?: string;
  started_at?: string;
  finished_at?: string;
}

export interface Domain {
  id: string;
  org_id: string;
  project_id: string;
  hostname: string;
  status: string;
  verification_type?: string;
  verification_token?: string;
  force_verified?: boolean;
  certificate_id?: string;
  created_at?: string;
  verified_at?: string;
}

export interface RuntimeInstance {
  id: string;
  org_id: string;
  project_id: string;
  deployment_id?: string;
  kind: string;
  image_ref: string;
  status: string;
  desired_state: string;
  container_id?: string;
  container_name?: string;
  slot?: string;
  mode?: string;
  health_status?: string;
  error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface Certificate {
  id: string;
  org_id: string;
  project_id: string;
  hostname: string;
  status: string;
  provider?: string;
  expires_at?: string;
  renewed_at?: string;
  created_at?: string;
}

export type EnvironmentName = "development" | "preview" | "staging" | "production";

export interface ProjectEnvironment {
  id: string;
  org_id: string;
  project_id: string;
  name: EnvironmentName | string;
  created_at?: string;
}

export interface SecretMeta {
  id: string;
  org_id: string;
  project_id: string;
  environment: string;
  name: string;
  current_version: number;
  value_hint: string;
  created_at?: string;
  updated_at?: string;
}

export interface LogEntry {
  id: string;
  org_id: string;
  project_id: string;
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
}

export interface StorageBucket {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  mode: string;
  created_at?: string;
}

export interface StorageObject {
  id: string;
  key: string;
  size_bytes: number;
  content_type: string;
  etag?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ManagedDatabase {
  id: string;
  org_id: string;
  project_id: string;
  name: string;
  mode: string;
  status: string;
  schema_name?: string;
  secret_ref?: string;
  connection_hint?: string;
  created_at?: string;
}

export interface BillingPlan {
  id: string;
  name: string;
  price_usd: number;
  build_minutes: number;
  runtime_hours: number;
  description?: string;
}

export interface BillingUsageRow {
  metric: string;
  quantity: number;
  unit: string;
}

export interface ProjectConfigResponse {
  config?: Record<string, unknown>;
  raw?: string;
  hash?: string;
  applied_at?: string;
  drift?: boolean;
  stub?: boolean;
  details?: string[];
  message?: string;
}

export interface ApiErrorBody {
  error?: string | { code?: string; message?: string };
  message?: string;
  detail?: string;
}
