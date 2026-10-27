"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";

// Main Platform-Level Navigation (Global System View)
const platformNav = [
  { label: "Projects & Apps", href: "/projects", icon: "📦" },
  { label: "Add-ons Marketplace", href: "/addons", icon: "🧩" },
  { label: "Cluster & Health", href: "/cluster", icon: "⚡" },
  { label: "Team & Access", href: "/team", icon: "👥" },
  { label: "System Settings", href: "/settings", icon: "⚙️" },
  { label: "Security & Sessions", href: "/security", icon: "🛡️" },
  { label: "API Keys", href: "/keys", icon: "🔑" },
  { label: "Runtime Instances", href: "/runtime", icon: "🖥️" },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth();
  const { orgs, currentOrg, setOrg, loading } = useOrg();
  const { activeProject, setActiveProject, rootDomain, getAppPublicUrl } = useProject();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [orgMenuOpen, setOrgMenuOpen] = useState(false);

  async function handleLogout() {
    await logout();
    router.replace("/login");
  }

  // Normalize pathname to prevent trailing slash mismatches (e.g., /projects/ -> /projects)
  const normalizedPath =
    pathname.length > 1 && pathname.endsWith("/") ? pathname.slice(0, -1) : pathname;
  const segments = normalizedPath.split("/").filter(Boolean);

  // User is inside a specific project if and only if route matches /projects/:id and :id is not "new"
  const isInsideProject =
    segments[0] === "projects" &&
    segments.length >= 2 &&
    segments[1] !== "new";
  const urlProjectId = isInsideProject ? segments[1] : null;
  const currentTab = searchParams.get("tab") || "overview";

  // Automatically reset activeProject context when leaving project-scoped routes
  useEffect(() => {
    if (!isInsideProject && activeProject !== null) {
      setActiveProject(null);
    }
  }, [isInsideProject, activeProject, setActiveProject]);

  // Project-Scoped Navigation items
  const projectNav = [
    { label: "Overview", tab: "overview", icon: "📊" },
    { label: "Deployments", tab: "deployments", icon: "🚀" },
    { label: "Git Repository", tab: "git", icon: "🐙" },
    { label: "Builds & Logs", tab: "builds", icon: "🛠️" },
    { label: "Custom Domains", tab: "domains", icon: "🌐" },
    { label: "Secrets & Env", tab: "secrets", icon: "🔐" },
    { label: "Managed Databases", tab: "databases", icon: "🗄️" },
    { label: "Project Settings", tab: "settings", icon: "⚙️" },
  ];

  const projectName = activeProject?.name || "Application";
  const defaultAppDomain = getAppPublicUrl(projectName);

  const sidebar = (
    <aside className="flex h-full w-64 flex-col border-r border-[var(--border)] bg-white shadow-xs">
      {/* Brand Header */}
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3.5">
        <BrandMark size="md" href="/projects" onClick={() => setActiveProject(null)} />
        <button
          type="button"
          className="lg:hidden rounded-md p-1.5 text-[var(--ink-muted)] hover:bg-slate-100"
          onClick={() => setMobileOpen(false)}
          aria-label="Close menu"
        >
          ✕
        </button>
      </div>

      {isInsideProject ? (
        /* ========================================================
           PROJECT-SPECIFIC SIDEBAR (App Workspace View)
           ======================================================== */
        <div className="flex flex-1 flex-col overflow-y-auto">
          {/* Back to Projects Button */}
          <div className="border-b border-[var(--border)] p-3">
            <Link
              href="/projects"
              onClick={() => setActiveProject(null)}
              className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-slate-50 px-3 py-2 text-xs font-bold text-[var(--ink)] shadow-2xs hover:bg-slate-100 hover:border-[var(--border-strong)] transition-all"
            >
              <span className="text-base leading-none text-[var(--accent)]">←</span>
              <span>Back to All Projects</span>
            </Link>
          </div>

          {/* Active Project Identification Card */}
          <div className="border-b border-[var(--border)] bg-slate-50/70 p-3.5 space-y-1.5">
            <div className="flex items-center gap-2.5">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-[var(--accent)] text-xs font-bold text-white shadow-xs shrink-0">
                {projectName.slice(0, 1).toUpperCase()}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-xs font-bold text-[var(--ink)]">
                  {projectName}
                </p>
                <div className="flex items-center gap-1.5">
                  <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                  <span className="text-[10px] font-semibold text-emerald-700">Live Active</span>
                </div>
              </div>
            </div>

            {/* Default Public App Subdomain */}
            <div className="pt-1">
              <a
                href={defaultAppDomain}
                target="_blank"
                rel="noreferrer"
                className="flex items-center justify-between rounded border border-blue-200 bg-blue-50/70 px-2 py-1 text-[11px] font-mono font-medium text-blue-900 hover:bg-blue-100 transition-colors"
                title={`Live URL: ${defaultAppDomain}`}
              >
                <span className="truncate">{projectName.toLowerCase()}.{rootDomain}</span>
                <span className="text-blue-600 text-[10px]">↗</span>
              </a>
            </div>
          </div>

          {/* Project Management Navigation */}
          <div className="flex-1 px-3 py-3 space-y-1">
            <p className="px-2.5 pb-1.5 text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">
              App Management
            </p>
            {projectNav.map((item) => {
              const active =
                item.tab === "overview"
                  ? currentTab === "overview" && !searchParams.get("tab")
                  : currentTab === item.tab;
              const targetUrl =
                item.tab === "overview"
                  ? `/projects/${urlProjectId}`
                  : `/projects/${urlProjectId}?tab=${item.tab}`;

              return (
                <Link
                  key={item.tab}
                  href={targetUrl}
                  className={`flex items-center justify-between rounded-md px-3 py-2 text-xs font-semibold transition-all ${
                    active
                      ? "bg-[var(--accent)] text-white shadow-xs font-bold"
                      : "text-[var(--ink-secondary)] hover:bg-slate-100 hover:text-[var(--ink)]"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <span className="text-xs">{item.icon}</span>
                    <span>{item.label}</span>
                  </div>
                  {active && <span className="h-1.5 w-1.5 rounded-full bg-white animate-pulse" />}
                </Link>
              );
            })}
          </div>
        </div>
      ) : (
        /* ========================================================
           MAIN PLATFORM SIDEBAR (Global System View)
           ======================================================== */
        <div className="flex flex-1 flex-col overflow-y-auto">
          {/* Organization Switcher */}
          <div className="relative border-b border-[var(--border)] p-3">
            <button
              type="button"
              onClick={() => setOrgMenuOpen((v) => !v)}
              className="flex w-full items-center justify-between rounded-md border border-[var(--border)] bg-slate-50 px-3 py-2 text-left text-xs font-bold text-[var(--ink)] shadow-2xs hover:bg-slate-100 transition-all"
            >
              <div className="flex items-center gap-1.5 truncate">
                <span className="h-2 w-2 rounded-full bg-[var(--accent)] shrink-0" />
                <span className="truncate">{currentOrg?.name || "Select Workspace"}</span>
              </div>
              <span className="text-[10px] text-[var(--ink-muted)]">▼</span>
            </button>
            {orgMenuOpen && (
              <div className="absolute left-3 right-3 top-full z-40 mt-1 rounded-md border border-[var(--border)] bg-white p-1 shadow-lg">
                {orgs.map((org) => (
                  <button
                    key={org.id}
                    type="button"
                    className={`block w-full rounded px-2.5 py-1.5 text-left text-xs ${
                      org.id === currentOrg?.id ? "bg-slate-100 font-bold" : "hover:bg-slate-50"
                    }`}
                    onClick={() => {
                      setOrg(org.id);
                      setOrgMenuOpen(false);
                    }}
                  >
                    {org.name}
                  </button>
                ))}
                <Link
                  href="/orgs/new"
                  className="block border-t border-[var(--border)] mt-1 pt-1.5 px-2.5 py-1 text-xs font-bold text-[var(--accent)]"
                  onClick={() => setOrgMenuOpen(false)}
                >
                  + New Organization
                </Link>
              </div>
            )}
          </div>

          {/* Main System Navigation */}
          <div className="flex-1 px-3 py-3 space-y-1">
            <p className="px-2.5 pb-1.5 text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">
              Platform & System
            </p>
            {platformNav.map((item) => {
              const active =
                normalizedPath === item.href ||
                (item.href !== "/" && normalizedPath.startsWith(item.href + "/"));

              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`flex items-center justify-between rounded-md px-3 py-2 text-xs font-semibold transition-all ${
                    active
                      ? "bg-[var(--accent)] text-white shadow-xs font-bold"
                      : "text-[var(--ink-secondary)] hover:bg-slate-100 hover:text-[var(--ink)]"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <span className="text-xs">{item.icon}</span>
                    <span>{item.label}</span>
                  </div>
                  {active && <span className="h-1.5 w-1.5 rounded-full bg-white animate-pulse" />}
                </Link>
              );
            })}
          </div>
        </div>
      )}

      {/* Footer Profile & Logout */}
      <div className="border-t border-[var(--border)] bg-slate-50/80 p-3">
        <div className="flex items-center justify-between gap-2">
          <div className="truncate">
            <p className="truncate text-xs font-bold text-[var(--ink)]">
              {user?.email || "Admin User"}
            </p>
            <p className="text-[10px] text-[var(--ink-muted)] font-mono">
              JP Self-Hosted
            </p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleLogout}
            className="text-xs text-[var(--ink-muted)] hover:text-red-600 shrink-0"
          >
            Logout
          </Button>
        </div>
      </div>
    </aside>
  );

  return (
    <div className="flex min-h-screen bg-[var(--canvas)]">
      {/* Desktop Sidebar */}
      <div className="hidden lg:block shrink-0">{sidebar}</div>

      {/* Mobile Sidebar Overlay */}
      {mobileOpen && (
        <div className="fixed inset-0 z-50 flex lg:hidden">
          <div
            className="fixed inset-0 bg-slate-900/60 backdrop-blur-xs"
            onClick={() => setMobileOpen(false)}
          />
          <div className="relative z-50 flex">{sidebar}</div>
        </div>
      )}

      {/* Main Content Area */}
      <div className="flex flex-1 flex-col min-w-0">
        {/* Universal Top Navigation Header */}
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-[var(--border)] bg-white/95 px-4 lg:px-8 backdrop-blur-xs shadow-2xs">
          {/* Left: Mobile hamburger + Breadcrumbs navigation */}
          <div className="flex items-center gap-3 min-w-0">
            {/* Mobile menu toggle */}
            <button
              type="button"
              className="lg:hidden rounded-md border border-[var(--border)] p-1.5 text-[var(--ink)] hover:bg-slate-100"
              onClick={() => setMobileOpen(true)}
              aria-label="Open navigation"
            >
              ☰
            </button>

            {/* Breadcrumb Path */}
            <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 text-xs text-[var(--ink-muted)] truncate">
              <Link
                href="/projects"
                onClick={() => setActiveProject(null)}
                className="flex items-center gap-1.5 font-bold text-[var(--ink)] hover:text-[var(--accent)] transition-colors shrink-0"
              >
                <span>🏠</span>
                <span>Console</span>
              </Link>
              <span className="text-slate-300">/</span>

              {isInsideProject ? (
                <>
                  <Link
                    href="/projects"
                    onClick={() => setActiveProject(null)}
                    className="hover:text-[var(--ink)] transition-colors shrink-0"
                  >
                    Projects
                  </Link>
                  <span className="text-slate-300">/</span>
                  <span className="font-bold text-[var(--ink)] truncate max-w-[120px] sm:max-w-[200px]">
                    {projectName}
                  </span>
                  {currentTab && currentTab !== "overview" && (
                    <>
                      <span className="text-slate-300">/</span>
                      <span className="capitalize font-semibold text-[var(--accent)]">
                        {currentTab}
                      </span>
                    </>
                  )}
                </>
              ) : normalizedPath === "/projects" ? (
                <span className="font-bold text-[var(--ink)]">Projects & Apps</span>
              ) : (
                <span className="font-bold text-[var(--ink)] truncate">
                  {platformNav.find((p) => p.href === normalizedPath)?.label || normalizedPath.replace("/", "")}
                </span>
              )}
            </nav>
          </div>

          {/* Right: Workspace Pill */}
          <div className="flex items-center gap-2 shrink-0">
            <span className="rounded-md border border-[var(--border)] bg-slate-100 px-2.5 py-1 text-[11px] font-semibold text-[var(--ink-secondary)]">
              {currentOrg?.name || "Workspace"}
            </span>
          </div>
        </header>

        <main className="flex-1 p-6 lg:p-8 max-w-7xl w-full mx-auto">
          {children}
        </main>
      </div>
    </div>
  );
}
