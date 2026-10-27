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
  { label: "AI Ops & Engine", href: "/ai", icon: "✨" },
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
    { label: "Add-ons & Databases", tab: "addons", icon: "🧩" },
    { label: "Project Settings", tab: "settings", icon: "⚙️" },
  ];

  const projectName = activeProject?.name || "Application";
  const projectSlug = projectName.toLowerCase().replace(/[^a-z0-9-]/g, "-").replace(/^-+|-+$/g, "");
  const defaultAppDomain = getAppPublicUrl(projectName);

  const getInitials = (text: string) => {
    return text
      .split(/[\s@._-]+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0].toUpperCase())
      .join("");
  };

  const sidebar = (
    <aside className="flex h-full w-64 flex-col border-r border-white/10 bg-[#0F1B3D] text-slate-100 shadow-xl select-none">
      {/* Brand Header */}
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-4">
        <BrandMark size="md" href="/projects" dark={true} onClick={() => setActiveProject(null)} />
        <button
          type="button"
          className="lg:hidden rounded-lg p-1.5 text-slate-400 hover:bg-white/10 hover:text-white transition-colors"
          onClick={() => setMobileOpen(false)}
          aria-label="Close menu"
        >
          ✕
        </button>
      </div>

      {isInsideProject ? (
        /* ========================================================
           PROJECT-SPECIFIC SIDEBAR (App Workspace View - Crezumic Style)
           ======================================================== */
        <div className="flex flex-1 flex-col overflow-y-auto">
          {/* Back to Projects Button */}
          <div className="border-b border-white/10 p-3">
            <Link
              href="/projects"
              onClick={() => setActiveProject(null)}
              className="flex items-center gap-2 rounded-lg border border-white/15 bg-white/5 px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/10 hover:text-white hover:border-white/25 transition-all"
            >
              <span className="text-base leading-none text-blue-400">←</span>
              <span>Back to All Projects</span>
            </Link>
          </div>

          {/* Active Project Identification Card */}
          <div className="border-b border-white/10 bg-white/5 p-3.5 space-y-2">
            <div className="flex items-center gap-2.5">
              <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-600 text-xs font-bold text-white shadow-sm shrink-0">
                {projectName.slice(0, 1).toUpperCase()}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-xs font-bold text-white">
                  {projectName}
                </p>
                <div className="flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-full bg-emerald-400 animate-pulse" />
                  <span className="text-[10px] font-semibold text-emerald-300">Live Active</span>
                </div>
              </div>
            </div>

            {/* Default Public App Subdomain */}
            <div className="pt-0.5">
              <a
                href={defaultAppDomain}
                target="_blank"
                rel="noreferrer"
                className="flex items-center justify-between rounded-lg border border-blue-400/30 bg-blue-950/60 px-2.5 py-1.5 text-[11px] font-mono font-medium text-blue-200 hover:bg-blue-900/60 transition-colors"
                title={`Live URL: ${defaultAppDomain}`}
              >
                <span className="truncate">{projectSlug}.{rootDomain.replace(/^panel\./, "")}</span>
                <span className="text-blue-300 text-[10px]">↗</span>
              </a>
            </div>
          </div>

          {/* Project Management Navigation */}
          <div className="flex-1 px-3 py-3 space-y-1">
            <p className="px-2 pb-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">
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
                  className={`relative flex items-center justify-between rounded-lg px-3 py-2 text-xs transition-all ${
                    active
                      ? "bg-white/15 text-white font-bold shadow-xs before:absolute before:left-0 before:top-2 before:bottom-2 before:w-1 before:rounded-r before:bg-blue-400"
                      : "text-slate-300 hover:bg-white/10 hover:text-white font-medium"
                  }`}
                >
                  <div className="flex items-center gap-2.5">
                    <span className="text-sm">{item.icon}</span>
                    <span>{item.label}</span>
                  </div>
                  {active && <span className="h-1.5 w-1.5 rounded-full bg-blue-400 animate-pulse" />}
                </Link>
              );
            })}
          </div>
        </div>
      ) : (
        /* ========================================================
           MAIN PLATFORM SIDEBAR (Global System View - Crezumic Style)
           ======================================================== */
        <div className="flex flex-1 flex-col overflow-y-auto">
          {/* Organization Switcher */}
          <div className="relative border-b border-white/10 p-3">
            <button
              type="button"
              onClick={() => setOrgMenuOpen((v) => !v)}
              className="flex w-full items-center justify-between rounded-lg border border-white/15 bg-white/5 px-3 py-2 text-left text-xs font-bold text-white shadow-xs hover:bg-white/10 transition-all"
            >
              <div className="flex items-center gap-2 truncate">
                <span className="h-2 w-2 rounded-full bg-blue-400 shrink-0" />
                <span className="truncate">{currentOrg?.name || "Select Workspace"}</span>
              </div>
              <span className="text-[10px] text-slate-400">▼</span>
            </button>
            {orgMenuOpen && (
              <div className="absolute left-3 right-3 top-full z-40 mt-1 rounded-xl border border-white/20 bg-[#0F1B3D] p-1 shadow-2xl backdrop-blur-md">
                {orgs.map((org) => (
                  <button
                    key={org.id}
                    type="button"
                    className={`block w-full rounded-lg px-2.5 py-1.5 text-left text-xs ${
                      org.id === currentOrg?.id ? "bg-white/15 font-bold text-white" : "text-slate-300 hover:bg-white/10 hover:text-white"
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
                  className="block border-t border-white/10 mt-1 pt-1.5 px-2.5 py-1 text-xs font-bold text-blue-400 hover:text-blue-300"
                  onClick={() => setOrgMenuOpen(false)}
                >
                  + New Organization
                </Link>
              </div>
            )}
          </div>

          {/* AI Status Chip in Sidebar */}
          <div className="px-3 pt-3">
            <div className="flex items-center justify-between rounded-lg border border-indigo-400/30 bg-gradient-to-r from-blue-900/40 to-indigo-900/40 px-3 py-1.5 text-[11px] text-blue-200">
              <span className="flex items-center gap-1.5 font-semibold">
                <span>✨</span> Gemini 3.5 Active
              </span>
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
            </div>
          </div>

          {/* Main System Navigation */}
          <div className="flex-1 px-3 py-3 space-y-1">
            <p className="px-2 pb-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">
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
                  className={`relative flex items-center justify-between rounded-lg px-3 py-2 text-xs transition-all ${
                    active
                      ? "bg-white/15 text-white font-bold shadow-xs before:absolute before:left-0 before:top-2 before:bottom-2 before:w-1 before:rounded-r before:bg-blue-400"
                      : "text-slate-300 hover:bg-white/10 hover:text-white font-medium"
                  }`}
                >
                  <div className="flex items-center gap-2.5">
                    <span className="text-sm">{item.icon}</span>
                    <span>{item.label}</span>
                  </div>
                  {active && <span className="h-1.5 w-1.5 rounded-full bg-blue-400 animate-pulse" />}
                </Link>
              );
            })}
          </div>
        </div>
      )}

      {/* Footer Profile & Logout - Crezumic Style */}
      <div className="border-t border-white/10 bg-black/15 p-3">
        <div className="flex items-center justify-between gap-2.5">
          <div className="flex items-center gap-2.5 min-w-0">
            <div className="flex h-8 w-8 items-center justify-center rounded-full bg-blue-600/80 ring-2 ring-white/20 text-xs font-bold text-white shrink-0">
              {getInitials(user?.email || "Admin")}
            </div>
            <div className="truncate">
              <p className="truncate text-xs font-bold text-white">
                {user?.email || "Admin User"}
              </p>
              <p className="text-[10px] text-slate-400 font-mono">
                JP Self-Hosted
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={handleLogout}
            className="rounded-lg p-1.5 text-xs text-slate-400 hover:bg-rose-500/20 hover:text-rose-300 transition-colors shrink-0"
            title="Log out"
          >
            Sign out
          </button>
        </div>
      </div>
    </aside>
  );

  return (
    <div className="flex min-h-screen bg-[#D6DEEB] font-[family-name:var(--font-body)]">
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
        {/* Universal Top Navigation Header — Crezumic Style */}
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b-2 border-slate-300 bg-white/95 px-4 lg:px-8 backdrop-blur-md shadow-xs">
          {/* Left: Mobile hamburger + Breadcrumbs navigation */}
          <div className="flex items-center gap-3 min-w-0">
            {/* Mobile menu toggle */}
            <button
              type="button"
              className="lg:hidden rounded-lg border-2 border-slate-300 p-1.5 text-slate-700 hover:bg-slate-100"
              onClick={() => setMobileOpen(true)}
              aria-label="Open navigation"
            >
              ☰
            </button>

            {/* Breadcrumb Path & Prominent Page Title */}
            <nav aria-label="Breadcrumb" className="flex items-center gap-2 text-xs font-semibold text-slate-500 truncate">
              <Link
                href="/projects"
                onClick={() => setActiveProject(null)}
                className="font-bold text-slate-500 hover:text-[#1E3A8A] transition-colors shrink-0 hidden sm:inline"
              >
                Console
              </Link>
              <span className="text-slate-300 font-bold hidden sm:inline">/</span>

              {isInsideProject ? (
                <>
                  <Link
                    href="/projects"
                    onClick={() => setActiveProject(null)}
                    className="hover:text-slate-900 transition-colors shrink-0 text-slate-500 font-bold hidden md:inline"
                  >
                    Projects
                  </Link>
                  <span className="text-slate-300 font-bold hidden md:inline">/</span>
                  <div className="flex items-center gap-2 shrink-0">
                    <h1 className="text-sm sm:text-base md:text-lg font-black text-[#0F1B3D] tracking-tight truncate max-w-[140px] sm:max-w-[220px]">
                      {projectName}
                    </h1>
                    <span className="inline-flex items-center gap-1 rounded-full border-2 border-emerald-300 bg-emerald-50 px-2 py-0.2 text-[10px] font-bold text-emerald-800 shrink-0">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                      Live
                    </span>
                    <a
                      href={defaultAppDomain}
                      target="_blank"
                      rel="noreferrer"
                      className="hidden lg:inline-flex items-center gap-1 text-[11px] font-mono font-bold text-[#1E3A8A] bg-slate-100 hover:bg-slate-200 border-2 border-slate-300 rounded-lg px-2 py-0.5 transition-colors"
                      title={defaultAppDomain}
                    >
                      <span className="truncate max-w-[160px]">{projectSlug}.{rootDomain.replace(/^panel\./, "")}</span>
                      <span className="text-[10px]">↗</span>
                    </a>
                  </div>
                  {currentTab && currentTab !== "overview" && (
                    <>
                      <span className="text-slate-300 font-bold text-xs">/</span>
                      <span className="capitalize font-black text-[#1E3A8A] text-sm sm:text-base shrink-0">
                        {projectNav.find((p) => p.tab === currentTab)?.label || currentTab}
                      </span>
                    </>
                  )}
                </>
              ) : (
                <h1 className="text-sm sm:text-base md:text-lg font-black text-[#0F1B3D] tracking-tight truncate">
                  {normalizedPath === "/projects"
                    ? "Projects & Applications"
                    : platformNav.find((p) => p.href === normalizedPath)?.label || normalizedPath.replace("/", "")}
                </h1>
              )}
            </nav>
          </div>

          {/* Right: Primary Page Action Button (Replaces static workspace pill) */}
          <div className="flex items-center gap-2.5 shrink-0">
            {isInsideProject ? (
              <>
                <Link
                  href={`/projects/${urlProjectId}?tab=deployments`}
                  className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-3.5 py-1.5 text-xs font-bold text-white shadow-sm transition-all"
                >
                  <span>🚀</span>
                  <span>Deploy App</span>
                </Link>
                <a
                  href={defaultAppDomain}
                  target="_blank"
                  rel="noreferrer"
                  className="hidden sm:inline-flex items-center gap-1 rounded-lg border-2 border-slate-300 bg-white hover:bg-slate-50 px-3 py-1.5 text-xs font-bold text-slate-800 shadow-2xs transition-all"
                  title="Open Live App"
                >
                  <span>Open App</span>
                  <span className="text-[10px]">↗</span>
                </a>
              </>
            ) : normalizedPath === "/projects" ? (
              <Link
                href="/projects/new"
                className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-3.5 py-1.5 text-xs font-bold text-white shadow-sm transition-all"
              >
                <span>+</span>
                <span>New Project</span>
              </Link>
            ) : normalizedPath === "/keys" ? (
              <Link
                href="/keys"
                className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-3.5 py-1.5 text-xs font-bold text-white shadow-sm transition-all"
              >
                <span>+ Create API Key</span>
              </Link>
            ) : normalizedPath === "/domains" ? (
              <Link
                href="/domains"
                className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-3.5 py-1.5 text-xs font-bold text-white shadow-sm transition-all"
              >
                <span>+ Add Domain</span>
              </Link>
            ) : normalizedPath === "/team" ? (
              <Link
                href="/team"
                className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-3.5 py-1.5 text-xs font-bold text-white shadow-sm transition-all"
              >
                <span>+ Invite Member</span>
              </Link>
            ) : normalizedPath === "/addons" ? (
              <span className="inline-flex items-center gap-1.5 rounded-lg border-2 border-slate-300 bg-white px-3 py-1.5 text-xs font-bold text-slate-800 shadow-2xs">
                <span>🧩 Add-ons Marketplace</span>
              </span>
            ) : normalizedPath === "/cluster" ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border-2 border-emerald-300 bg-emerald-50 px-2.5 py-1 text-xs font-bold text-emerald-800">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                Cluster Healthy
              </span>
            ) : normalizedPath === "/ai" ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border-2 border-blue-200 bg-blue-50 px-2.5 py-1 text-xs font-bold text-[#1E3A8A]">
                <span className="h-1.5 w-1.5 rounded-full bg-blue-600 animate-pulse" />
                Gemini 3.5 Active
              </span>
            ) : null}
          </div>
        </header>

        <main className="flex-1 p-5 md:p-8 max-w-7xl w-full mx-auto space-y-6">
          {children}
        </main>
      </div>
    </div>
  );
}
