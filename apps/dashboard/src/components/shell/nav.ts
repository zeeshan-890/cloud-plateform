import type { IconName } from "@/components/ui/icons";

/* ---------------------------------------------------------------- Level 1: organization console */

export interface NavItem {
  label: string;
  href: string;
  icon: IconName;
  /** only for platform administrators */
  platformAdmin?: boolean;
  /** only for org owners/admins (hidden from restricted users) */
  orgAdmin?: boolean;
  keywords?: string;
  /** one-line description used as the page subtitle fallback */
  hint?: string;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export const ORG_NAV: NavGroup[] = [
  {
    label: "Workspace",
    items: [
      { label: "Overview", href: "/", icon: "home", keywords: "home dashboard health" },
      { label: "Projects", href: "/projects", icon: "folder", keywords: "apps" },
      { label: "Team & access", href: "/team", icon: "users", orgAdmin: true, keywords: "team users members invites roles access grants" },
      { label: "Git connections", href: "/git", icon: "branch", orgAdmin: true, keywords: "github repositories" },
      { label: "AI assistant", href: "/ai", icon: "chip", keywords: "ai help" },
    ],
  },
  {
    label: "Account",
    items: [
      { label: "Access keys", href: "/keys", icon: "key", keywords: "tokens pat api" },
      { label: "Sessions", href: "/sessions", icon: "lock", keywords: "devices sign out" },
      { label: "Account settings", href: "/settings", icon: "gear", keywords: "profile password mfa two-step" },
    ],
  },
  {
    label: "Platform",
    items: [
      { label: "System health", href: "/cluster", icon: "server", platformAdmin: true, keywords: "cluster node cpu memory disk" },
      { label: "Security", href: "/security", icon: "shield", platformAdmin: true, keywords: "checks hardening" },
      { label: "Platform settings", href: "/platform", icon: "gear", platformAdmin: true, keywords: "smtp email ai key system" },
    ],
  },
];

export function orgNavFor(isPlatformAdmin: boolean, orgAdmin = true): NavGroup[] {
  return ORG_NAV.map((g) => ({ ...g, items: g.items.filter((i) => (!i.platformAdmin || isPlatformAdmin) && (!i.orgAdmin || orgAdmin)) })).filter((g) => g.items.length > 0);
}

export function flatOrgNav(isPlatformAdmin: boolean, orgAdmin = true): NavItem[] {
  return orgNavFor(isPlatformAdmin, orgAdmin).flatMap((g) => g.items);
}

export function isActive(pathname: string, href: string): boolean {
  const p = pathname.replace(/\/+$/, "") || "/";
  if (href === "/") return p === "/";
  return p === href || p.startsWith(href + "/");
}

/* ---------------------------------------------------------------- Level 2: project workspace */

export const PROJECT_TABS = [
  "overview",
  "deployments",
  "builds",
  "runtime",
  "domains",
  "secrets",
  "addons",
  "databases",
  "storage",
  "logs",
  "metrics",
  "repository",
  "settings",
] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];

export interface ProjectNavItem {
  id: ProjectTab;
  label: string;
  icon: IconName;
  description: string;
  keywords?: string;
}

export const PROJECT_NAV_ITEMS: Record<ProjectTab, ProjectNavItem> = {
  overview: { id: "overview", label: "Overview", icon: "home", description: "How this project is doing right now." },
  deployments: { id: "deployments", label: "Deployments", icon: "rocket", description: "Every release. Deploy a new version or roll back.", keywords: "release rollback deploy" },
  builds: { id: "builds", label: "Builds", icon: "hammer", description: "Build output and security scan results.", keywords: "build log scan" },
  runtime: { id: "runtime", label: "Runtime", icon: "server", description: "The containers serving your app.", keywords: "instances containers start stop restart" },
  domains: { id: "domains", label: "Domains", icon: "globe", description: "Put your app on your own web address.", keywords: "dns ssl certificate" },
  secrets: { id: "secrets", label: "Environment & secrets", icon: "key", description: "Settings your app reads at startup, encrypted.", keywords: "env variables secrets" },
  addons: { id: "addons", label: "Add-ons", icon: "puzzle", description: "One-click databases, caches and queues.", keywords: "marketplace redis postgres mongo" },
  databases: { id: "databases", label: "Databases", icon: "database", description: "Managed PostgreSQL for this project." },
  storage: { id: "storage", label: "Storage", icon: "bucket", description: "Private file storage for uploads and assets.", keywords: "files objects bucket" },
  logs: { id: "logs", label: "Logs", icon: "logs", description: "Search and follow what your app prints." },
  metrics: { id: "metrics", label: "Metrics", icon: "chart", description: "Numbers your app reports over time." },
  repository: { id: "repository", label: "Repository", icon: "branch", description: "Where the code comes from.", keywords: "git github deploy key" },
  settings: { id: "settings", label: "Settings", icon: "gear", description: "Name, description and deleting the project." },
};

export const PROJECT_NAV_GROUPS: Array<{ label: string; items: ProjectTab[] }> = [
  { label: "Ship", items: ["overview", "deployments", "builds", "runtime"] },
  { label: "Connect", items: ["domains", "secrets", "addons", "databases", "storage"] },
  { label: "Observe", items: ["logs", "metrics"] },
  { label: "Source", items: ["repository"] },
];

export function projectTabFrom(raw: string | null): ProjectTab {
  if (raw === "git") return "repository"; // old links
  return (PROJECT_TABS as readonly string[]).includes(raw ?? "") ? (raw as ProjectTab) : "overview";
}

export function projectHref(id: string, tab: ProjectTab = "overview"): string {
  return `/project?id=${encodeURIComponent(id)}&tab=${tab}`;
}

export function inProjectWorkspace(pathname: string): boolean {
  const p = pathname.replace(/\/+$/, "");
  return p === "/project";
}

/** Breadcrumb page name for level-1 routes. */
export function orgPageTitle(pathname: string): string | null {
  const p = pathname.replace(/\/+$/, "") || "/";
  if (p === "/projects/new") return "New project";
  const hit = ORG_NAV.flatMap((g) => g.items).find((i) => (i.href === "/" ? p === "/" : p === i.href || p.startsWith(i.href + "/")));
  if (hit) return hit.label;
  if (p === "/orgs/new") return "New organization";
  if (p === "/invite/accept") return "Accept invitation";
  return null;
}
