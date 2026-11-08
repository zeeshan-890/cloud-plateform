"use client";

import Link from "next/link";
import { Fragment, useEffect, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Wordmark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/ui/icons";
import { ErrorState } from "@/components/ui/states";
import { Lamp } from "@/components/ui/status";
import { Tooltip } from "@/components/ui/tooltip";
import { instancesState } from "@/features/snapshots";
import { runtime } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useResource } from "@/lib/use-resource";
import { useStoredValue } from "@/lib/persist";
import { useTheme } from "@/lib/theme";
import { CommandPalette } from "./command-palette";
import { levelOf } from "@/lib/access";
import type { Area } from "@/lib/api";
import { PROJECT_NAV_GROUPS, PROJECT_NAV_ITEMS, inProjectWorkspace, isActive, orgNavFor, orgPageTitle, projectHref, projectTabFrom, type ProjectTab } from "./nav";

/** Which level are we on, and (level 2) which project/tab. */
function useWorkspace() {
  const pathname = usePathname();
  const params = useSearchParams();
  const project = inProjectWorkspace(pathname);
  return { pathname, project, projectId: project ? params.get("id") : null, tab: projectTabFrom(params.get("tab")) };
}

/* ------------------------------------------------------------------ Level 1 sidebar */
function OrgNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  const { isPlatformAdmin, can } = useOrg();
  return (
    <nav aria-label="Organization" className="flex flex-col">
      {orgNavFor(isPlatformAdmin, can("admin")).map((group) => (
        <div key={group.label}>
          <p className="label nav-group-label">{group.label}</p>
          {group.items.map((item) => (
            <Link key={item.href} href={item.href} className="nav-item" aria-current={isActive(pathname, item.href) ? "page" : undefined} onClick={onNavigate}>
              <Icon name={item.icon} size={19} />
              {item.label}
            </Link>
          ))}
        </div>
      ))}
    </nav>
  );
}

/* ------------------------------------------------------------------ Level 2 sidebar */
function ProjectNav({ projectId, tab, onNavigate }: { projectId: string; tab: string; onNavigate?: () => void }) {
  const { projects } = useProjects();
  const { currentOrg, access } = useOrg();
  const router = useRouter();
  const project = projects.find((p) => p.id === projectId);
  const allowed = (t: ProjectTab) => levelOf(access, projectId, t as Area) !== "none";
  const canRuntime = allowed("runtime");
  const inst = useResource(currentOrg && canRuntime ? `side-inst:${projectId}` : null, (signal) => runtime.instances(currentOrg!.id, projectId, { signal }), { interval: 10000 });
  const state = canRuntime ? instancesState(inst.error ? null : inst.data) : "unknown";
  const lamp = state === "running" ? "ok" : state === "failed" ? "fail" : state === "unknown" ? "info" : "idle";
  const word = !canRuntime ? "project" : state === "none" ? "not deployed" : state === "unknown" ? "checking" : state;

  return (
    <nav aria-label="Project" className="flex flex-col">
      <div className="well side-card mb-2 flex flex-col gap-2 !p-3">
        <Link href="/projects" className="mono inline-flex items-center gap-1 text-[12.5px] no-underline" style={{ color: "inherit" }} onClick={onNavigate}>
          <Icon name="chevron" size={13} style={{ transform: "rotate(180deg)" }} /> All projects
        </Link>
        <div className="flex items-center gap-2">
          <Lamp state={lamp} pulse={state === "running"} label={`App is ${word}`} />
          <h2 className="m-0 min-w-0 flex-1 truncate" data-testid="project-name-side">{project?.name ?? "Project"}</h2>
        </div>
        <p className="mono m-0 text-[11.5px] opacity-70">{word}</p>
        {projects.length > 1 && (
          <select
            aria-label="Switch project"
            className="input !min-h-[32px] !py-0.5 text-[13px]"
            value={projectId}
            onChange={(e) => {
              router.push(projectHref(e.target.value, projectTabFrom(tab)));
              onNavigate?.();
            }}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
      </div>
      {PROJECT_NAV_GROUPS.map((g) => ({ ...g, items: g.items.filter(allowed) })).filter((g) => g.items.length > 0).map((g) => (
        <div key={g.label}>
          <p className="label nav-group-label">{g.label}</p>
          {g.items.map((t) => {
            const it = PROJECT_NAV_ITEMS[t];
            return (
              <Link key={t} href={projectHref(projectId, t)} className="nav-item" aria-current={tab === t ? "page" : undefined} onClick={onNavigate}>
                <Icon name={it.icon} size={19} />
                <span className="nav-label">{it.label}</span>
              </Link>
            );
          })}
        </div>
      ))}
      {allowed("settings") && (
        <>
      <div className="rule" />
      <Link href={projectHref(projectId, "settings")} className="nav-item" aria-current={tab === "settings" ? "page" : undefined} onClick={onNavigate}>
        <Icon name="gear" size={19} />
        <span className="nav-label">Settings</span>
        <span className="nav-label ml-auto text-[11px] opacity-60">danger zone</span>
      </Link>
        </>
      )}
    </nav>
  );
}

function SidebarBody({ onNavigate }: { onNavigate?: () => void }) {
  const ws = useWorkspace();
  return ws.project && ws.projectId ? <ProjectNav projectId={ws.projectId} tab={ws.tab} onNavigate={onNavigate} /> : <OrgNav onNavigate={onNavigate} />;
}

/* ------------------------------------------------------------------ top bar */
export function ThemeToggle() {
  const { theme, toggle } = useTheme();
  const night = theme === "dark";
  return (
    <Tooltip label={night ? "Switch to daylight" : "Switch to night shift"}>
      <button type="button" role="switch" aria-checked={night} aria-label="Night shift theme" className="switch" onClick={toggle} style={{ width: 62 }}>
        <span className="switch-thumb" style={{ transform: night ? "translateX(28px)" : undefined, display: "grid", placeItems: "center" }}>
          <Icon name={night ? "moon" : "sun"} size={14} style={{ color: "var(--ink)" }} />
        </span>
      </button>
    </Tooltip>
  );
}

function Breadcrumbs() {
  const ws = useWorkspace();
  const { orgs, currentOrg, setCurrentOrgId } = useOrg();
  const { projects } = useProjects();
  const router = useRouter();
  const project = projects.find((p) => p.id === ws.projectId);
  const page = ws.project ? PROJECT_NAV_ITEMS[ws.tab].label : orgPageTitle(ws.pathname);

  const crumbs: Array<{ key: string; node: React.ReactNode }> = [];
  if (currentOrg) {
    crumbs.push({
      key: "org",
      node:
        orgs.length > 1 ? (
          <select
            aria-label="Organization"
            className="input !min-h-[32px] !w-auto max-w-[120px] !py-0.5 text-[13px] sm:max-w-[200px]"
            value={currentOrg.id}
            onChange={(e) => {
              if (e.target.value === "__new") router.push("/orgs/new");
              else setCurrentOrgId(e.target.value);
            }}
          >
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
            <option value="__new">+ New organization</option>
          </select>
        ) : (
          <Link href="/" className="no-underline" style={{ color: "inherit" }}>
            {currentOrg.name}
          </Link>
        ),
    });
  }
  if (ws.project) {
    crumbs.push({ key: "projects", node: <Link href="/projects" className="no-underline" style={{ color: "inherit" }}>Projects</Link> });
    crumbs.push({ key: "proj", node: <span className="truncate">{project?.name ?? "Project"}</span> });
  }
  if (page && !(ws.project === false && page === "Overview")) crumbs.push({ key: "page", node: <span aria-current="page" className="truncate font-semibold">{page}</span> });

  return (
    <nav aria-label="Breadcrumb" className="min-w-0 flex-1">
      <ol className="m-0 flex min-w-0 list-none overflow-hidden items-center gap-1.5 p-0 text-[13.5px]">
        {crumbs.map((c, i) => (
          <Fragment key={c.key}>
            {i > 0 && (
              <li aria-hidden="true" className={`opacity-50 ${crumbs[i - 1].key === "org" || crumbs[i - 1].key === "projects" ? "hidden sm:block" : ""}`}>
                /
              </li>
            )}
            <li className={`min-w-0 ${c.key === "projects" || c.key === "org" ? "hidden sm:block" : "truncate"}`}>{c.node}</li>
          </Fragment>
        ))}
      </ol>
    </nav>
  );
}

function UserButton() {
  const { user, logout } = useAuth();
  const initials = (user?.name || user?.email || "?").split(/\s+/).map((s) => s[0]).slice(0, 2).join("").toUpperCase();
  return (
    <div className="flex items-center gap-1">
      <Tooltip label={user ? `${user.name} - account settings` : "Account"}>
        <Link href="/settings" className="btn btn--sm btn--icon" aria-label={`Account settings for ${user?.name ?? "you"}`}>
          <span className="mono text-[12px]">{initials}</span>
        </Link>
      </Tooltip>
      <Tooltip label="Sign out">
        <Button variant="ghost" size="sm" icon className="desktop-only" aria-label="Sign out" onClick={() => void logout()}>
          <Icon name="logout" size={17} />
        </Button>
      </Tooltip>
    </div>
  );
}

/* ------------------------------------------------------------------ shell */
export function AppShell({ children }: { children: React.ReactNode }) {
  const org = useOrg();
  const { user, logout } = useAuth();
  const ws = useWorkspace();
  const { pathname } = ws;
  const [collapsedRaw, setCollapsed] = useStoredValue("jp_sidebar");
  const collapsed = collapsedRaw === "collapsed";
  const [drawer, setDrawer] = useState(false);
  const [palette, setPalette] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && (e.key.toLowerCase() === "k" || e.code === "KeyK")) {
        e.preventDefault();
        setPalette((p) => !p);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const routeKey = `${pathname}?${ws.projectId ?? ""}${ws.tab}`;
  const [lastRoute, setLastRoute] = useState(routeKey);
  if (lastRoute !== routeKey) {
    setLastRoute(routeKey);
    if (drawer) setDrawer(false);
  }

  const pid = ws.projectId;
  const canTab = (t: ProjectTab) => !pid || levelOf(org.access, pid, t as Area) !== "none";
  const quick0 = ws.project && pid
    ? [
        { href: projectHref(pid, "overview"), label: "Overview", icon: "home" as const, on: ws.tab === "overview" },
        { href: projectHref(pid, "deployments"), label: "Deploys", icon: "rocket" as const, on: ws.tab === "deployments" },
        { href: projectHref(pid, "domains"), label: "Domains", icon: "globe" as const, on: ws.tab === "domains" },
        { href: projectHref(pid, "secrets"), label: "Env", icon: "key" as const, on: ws.tab === "secrets" },
      ]
    : [
        { href: "/", label: "Home", icon: "home" as const, on: isActive(pathname, "/") },
        { href: "/projects", label: "Projects", icon: "folder" as const, on: isActive(pathname, "/projects") },
        org.can("admin")
          ? { href: "/team", label: "Team", icon: "users" as const, on: isActive(pathname, "/team") }
          : { href: "/sessions", label: "Sessions", icon: "lock" as const, on: isActive(pathname, "/sessions") },
        { href: "/settings", label: "Account", icon: "gear" as const, on: isActive(pathname, "/settings") },
      ];
  const quick = ws.project && pid ? quick0.filter((q) => canTab(new URL(q.href, "http://x").searchParams.get("tab") as ProjectTab)) : quick0;

  return (
    <div className="rack-frame" data-collapsed={collapsed ? "true" : undefined}>
      <aside className="rack-side" aria-label="Sidebar">
        <Link href="/" className="px-2 py-1 no-underline" aria-label="JP Cloud overview" style={{ color: "var(--ink)" }}>
          <Wordmark />
        </Link>
        <div className="scroll-thin -mx-1 flex-1 overflow-y-auto px-1">
          <SidebarBody />
        </div>
        <button type="button" className="btn btn--ghost btn--sm" onClick={() => setCollapsed(collapsed ? null : "collapsed")} aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"} aria-pressed={collapsed} style={{ justifyContent: collapsed ? "center" : "flex-start" }}>
          <Icon name="menu" size={16} />
          <span className="nav-label">Collapse</span>
        </button>
      </aside>

      <div className="rack-main">
        <header className="topbar">
          <Button variant="ghost" icon className="mobile-only" aria-label="Open menu" onClick={() => setDrawer(true)}>
            <Icon name="menu" />
          </Button>
          <Breadcrumbs />
          <div className="ml-auto flex items-center gap-2">
            <Button size="sm" onClick={() => setPalette(true)} aria-label="Open command palette" className="desktop-only">
              <Icon name="search" size={15} />
              Search
              <span className="kbd ml-1">Ctrl K</span>
            </Button>
            <Button size="sm" icon onClick={() => setPalette(true)} aria-label="Open command palette" className="mobile-only">
              <Icon name="search" size={16} />
            </Button>
            <ThemeToggle />
            <UserButton />
          </div>
        </header>

        <main id="main" tabIndex={-1} className="page page-enter outline-none" key={routeKey}>
          {org.error && org.orgs.length === 0 ? <ErrorState error={org.error} onRetry={org.reload} /> : children}
        </main>
      </div>

      <nav className="bottom-rack" aria-label="Quick navigation" style={{ gridTemplateColumns: `repeat(${quick.length + 1}, 1fr)` }}>
        {quick.map((q) => (
          <Link key={q.href} href={q.href} aria-current={q.on ? "page" : undefined}>
            <Icon name={q.icon} />
            {q.label}
          </Link>
        ))}
        <button type="button" onClick={() => setDrawer(true)} aria-label="Open full menu">
          <Icon name="menu" />
          More
        </button>
      </nav>

      {drawer && (
        <>
          <div className="backdrop" style={{ zIndex: 160 }} onMouseDown={() => setDrawer(false)} aria-hidden="true" />
          <div className="drawer" role="dialog" aria-modal="true" aria-label="Menu">
            <div className="mb-2 flex items-center justify-between px-1">
              <Wordmark />
              <Button variant="ghost" icon size="sm" aria-label="Close menu" onClick={() => setDrawer(false)}>
                <Icon name="x" />
              </Button>
            </div>
            <SidebarBody onNavigate={() => setDrawer(false)} />
            <div className="well mt-4 flex items-center gap-3 !p-3">
              <div className="min-w-0 flex-1">
                <p className="m-0 truncate text-[14px] font-semibold">{user?.name}</p>
                <p className="mono m-0 truncate text-[11.5px] opacity-75">{user?.email}</p>
              </div>
              <Button size="sm" onClick={() => void logout()}>
                Sign out
              </Button>
            </div>
          </div>
        </>
      )}

      {palette && <CommandPalette open onClose={() => setPalette(false)} />}
    </div>
  );
}
