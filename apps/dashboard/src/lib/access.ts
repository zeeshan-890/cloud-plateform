import type { Area, Grant, Level, Membership, Preset, User } from "@/lib/api/types";

export const AREAS: Area[] = ["overview", "deployments", "builds", "runtime", "domains", "secrets", "addons", "databases", "storage", "logs", "metrics", "repository", "settings"];

export const AREA_LABELS: Record<Area, string> = {
  overview: "Overview",
  deployments: "Deployments",
  builds: "Builds",
  runtime: "Runtime",
  domains: "Domains",
  secrets: "Environment & secrets",
  addons: "Add-ons",
  databases: "Databases",
  storage: "Storage",
  logs: "Logs",
  metrics: "Metrics",
  repository: "Repository",
  settings: "Settings",
};

type AreaMap = Partial<Record<Area, Level>>;

const VIEWER: AreaMap = { overview: "view", deployments: "view", builds: "view", runtime: "view", logs: "view", metrics: "view", domains: "view" };
const DEVELOPER: AreaMap = { ...VIEWER, deployments: "manage", builds: "manage", runtime: "manage", storage: "manage", repository: "view", secrets: "view", addons: "view", databases: "view" };
const OPERATOR: AreaMap = { ...DEVELOPER, domains: "manage", addons: "manage", databases: "manage", secrets: "manage", repository: "manage" };
const ADMIN: AreaMap = Object.fromEntries(AREAS.map((a) => [a, "manage"])) as AreaMap;

export const PRESET_DEFS: Record<Exclude<Preset, "custom">, { label: string; blurb: string; areas: AreaMap; secrets_reveal: boolean }> = {
  viewer: { label: "Viewer", blurb: "Can look at deployments, runtime, logs, metrics and domains. Cannot change anything.", areas: VIEWER, secrets_reveal: false },
  developer: { label: "Developer", blurb: "Viewer, plus deploy, build, run and upload files.", areas: DEVELOPER, secrets_reveal: false },
  operator: { label: "Operator", blurb: "Developer, plus domains, add-ons, databases, variables and repository.", areas: OPERATOR, secrets_reveal: false },
  project_admin: { label: "Project admin", blurb: "Everything in this project, including settings and revealing secrets.", areas: ADMIN, secrets_reveal: true },
};

export const PRESET_ORDER: Preset[] = ["viewer", "developer", "operator", "project_admin", "custom"];

/** Full 13-area map for a preset (missing areas = none). */
export function presetAreas(p: Exclude<Preset, "custom">): Record<Area, Level> {
  return Object.fromEntries(AREAS.map((a) => [a, PRESET_DEFS[p].areas[a] ?? "none"])) as Record<Area, Level>;
}

export function grantAreas(g: Grant): Record<Area, Level> {
  if (g.preset && g.preset !== "custom") return presetAreas(g.preset);
  return Object.fromEntries(AREAS.map((a) => [a, g.areas?.[a] ?? "none"])) as Record<Area, Level>;
}

export function grantReveals(g: Grant): boolean {
  if (g.preset && g.preset !== "custom") return g.secrets_reveal ?? PRESET_DEFS[g.preset].secrets_reveal;
  return !!g.secrets_reveal;
}

/* ------------------------------------------------------------ what the signed-in user may see */

export interface OrgAccess {
  /** false when the API did not send memberships (older backend): fall back to role checks */
  known: boolean;
  /** owner/admin: everything everywhere */
  full: boolean;
  projects: Map<string, { areas: AreaMap; secrets_reveal: boolean }>;
}

export function orgAccess(user: User | null | undefined, orgId: string | undefined): OrgAccess {
  const m: Membership | undefined = user?.memberships?.find((x) => x.org_id === orgId);
  // fail CLOSED: with no membership data the UI shows nothing privileged (the backend enforces access either way)
  if (!user?.memberships || !m) return { known: false, full: false, projects: new Map() };
  const full = m.full === true || m.role === "owner" || m.role === "admin";
  const projects = new Map((m.projects ?? []).map((p) => [p.project_id, { areas: p.areas ?? {}, secrets_reveal: !!p.secrets_reveal }]));
  return { known: true, full, projects };
}

export function levelOf(acc: OrgAccess, projectId: string, area: Area): Level {
  if (acc.full) return "manage";
  return acc.projects.get(projectId)?.areas[area] ?? "none";
}

export function revealsSecrets(acc: OrgAccess, projectId: string): boolean {
  if (acc.full) return true;
  return acc.projects.get(projectId)?.secrets_reveal === true;
}

export function visibleAreas(acc: OrgAccess, projectId: string): Area[] {
  return AREAS.filter((a) => levelOf(acc, projectId, a) !== "none");
}

export function atLevel(have: Level, need: "view" | "manage"): boolean {
  return need === "view" ? have !== "none" : have === "manage";
}

export function summarizeGrant(g: Grant): string {
  if (g.preset && g.preset !== "custom") return PRESET_DEFS[g.preset].label;
  const a = grantAreas(g);
  const manage = AREAS.filter((x) => a[x] === "manage").length;
  const view = AREAS.filter((x) => a[x] === "view").length;
  return `Custom (${manage} manage, ${view} view)`;
}
