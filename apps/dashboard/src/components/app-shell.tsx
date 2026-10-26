"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useState } from "react";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";

type NavItem =
  | { label: string; href: string; soon?: false }
  | { label: string; soon: true };

const primaryNav: NavItem[] = [
  { label: "Projects", href: "/projects" },
  { label: "Connect Git", href: "/git" },
  { label: "Deployments", href: "/deployments" },
  { label: "Builds", href: "/builds" },
  { label: "Runtime", href: "/runtime" },
  { label: "Domains", href: "/domains" },
  { label: "Secrets", href: "/secrets" },
  { label: "Logs", href: "/logs" },
  { label: "Metrics", href: "/metrics" },
  { label: "Storage", href: "/storage" },
  { label: "Databases", href: "/databases" },
  { label: "Add-ons", href: "/addons" },
  { label: "Apps", href: "/apps" },
  { label: "Billing", href: "/billing" },
  { label: "Security", href: "/security" },
  { label: "Cluster", href: "/cluster" },
  { label: "AI", href: "/ai" },
  { label: "Team", href: "/team" },
  { label: "API keys", href: "/keys" },
  { label: "Sessions", href: "/sessions" },
  { label: "Settings", href: "/settings" },
];

function NavLink({ item }: { item: NavItem }) {
  const pathname = usePathname();
  if (item.soon) {
    return (
      <span
        className="flex items-center justify-between rounded-md px-3 py-1.5 text-xs text-[var(--ink-faint)] cursor-not-allowed opacity-60 font-medium"
        title="Coming soon"
      >
        {item.label}
        <span className="text-[9px] uppercase tracking-wider bg-slate-100 px-1.5 py-0.5 rounded text-[var(--ink-faint)]">
          Soon
        </span>
      </span>
    );
  }
  const active = pathname === item.href || (item.href !== "/" && pathname.startsWith(item.href + "/"));
  return (
    <Link
      href={item.href}
      className={`flex items-center justify-between rounded-md px-3 py-1.5 text-xs font-semibold transition-all duration-150 ${
        active
          ? "bg-[var(--accent)] text-white shadow-xs"
          : "text-[var(--ink-secondary)] hover:bg-slate-100 hover:text-[var(--ink)]"
      }`}
    >
      <span>{item.label}</span>
      {active && <span className="h-1.5 w-1.5 rounded-full bg-white/90 animate-pulse" />}
    </Link>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth();
  const { orgs, currentOrg, setOrg, loading } = useOrg();
  const router = useRouter();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [orgMenuOpen, setOrgMenuOpen] = useState(false);

  async function handleLogout() {
    await logout();
    router.replace("/login");
  }

  const sidebar = (
    <aside className="flex h-full w-60 flex-col border-r border-[var(--border)] bg-[var(--surface)] shadow-2xs">
      <div className="flex items-center justify-between px-4 py-4 border-b border-[var(--border)] mb-3">
        <BrandMark size="md" href="/projects" />
        <button
          type="button"
          className="lg:hidden rounded-md p-1.5 text-[var(--ink-muted)] hover:bg-[var(--surface-hover)]"
          onClick={() => setMobileOpen(false)}
          aria-label="Close menu"
        >
          ✕
        </button>
      </div>

      <div className="relative px-3 pb-3">
        <button
          type="button"
          onClick={() => setOrgMenuOpen((v) => !v)}
          className="flex w-full items-center justify-between rounded-md border border-[var(--border)] bg-slate-50 px-3 py-2 text-left text-xs font-bold transition-all hover:bg-slate-100 hover:border-[var(--border-strong)] shadow-2xs"
          disabled={loading}
        >
          <div className="flex items-center gap-1.5 truncate">
            <span className="flex h-2 w-2 rounded-full bg-[var(--accent)] shrink-0" />
            <span className="truncate text-[var(--ink)]">
              {currentOrg?.name || (loading ? "Loading…" : "No organization")}
            </span>
          </div>
          <span className="text-[var(--ink-faint)] text-[10px]" aria-hidden>
            ▼
          </span>
        </button>
        {orgMenuOpen ? (
          <div className="absolute left-3 right-3 z-30 mt-1 overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)] shadow-md">
            {orgs.map((org) => (
              <button
                key={org.id}
                type="button"
                className={`block w-full px-3 py-2.5 text-left text-sm hover:bg-[var(--surface)] ${
                  org.id === currentOrg?.id
                    ? "font-medium text-[var(--ink)]"
                    : "text-[var(--ink-muted)]"
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
              className="block border-t border-[var(--border)] px-3 py-2.5 text-sm text-[var(--accent-deep)] hover:bg-[var(--surface)]"
              onClick={() => setOrgMenuOpen(false)}
            >
              Create organization
            </Link>
          </div>
        ) : null}
      </div>

      <nav className="flex-1 overflow-y-auto px-3 pb-4">
        <p className="mb-1 px-3 text-[10px] font-semibold uppercase tracking-[0.14em] text-[var(--ink-faint)]">
          Workspace
        </p>
        <div className="flex flex-col gap-0.5">
          {primaryNav.map((item) => (
            <NavLink key={item.label} item={item} />
          ))}
        </div>
      </nav>

      <div className="border-t border-[var(--border)] px-3.5 py-3 bg-slate-50/80">
        <div className="flex items-center gap-2 mb-2">
          <div className="flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent)] text-white text-xs font-bold shrink-0">
            {user?.name?.slice(0, 1).toUpperCase() || "A"}
          </div>
          <div className="truncate flex-1">
            <p className="truncate text-xs font-bold text-[var(--ink)] leading-tight">
              {user?.name || "Administrator"}
            </p>
            <p className="truncate text-[10px] text-[var(--ink-secondary)] font-medium">
              {user?.email || "admin@local"}
            </p>
          </div>
        </div>
        <Button
          variant="secondary"
          size="sm"
          className="w-full text-xs"
          onClick={handleLogout}
        >
          Sign out
        </Button>
      </div>
    </aside>
  );

  return (
    <div className="flex min-h-screen bg-[var(--paper)]">
      <div className="hidden lg:block shrink-0">{sidebar}</div>

      {mobileOpen ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <button
            type="button"
            className="absolute inset-0 bg-slate-900/50 backdrop-blur-xs"
            aria-label="Close overlay"
            onClick={() => setMobileOpen(false)}
          />
          <div className="relative z-10 h-full animate-slide-in">{sidebar}</div>
        </div>
      ) : null}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-3 border-b border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 lg:hidden shadow-xs">
          <button
            type="button"
            className="rounded-md border border-[var(--border)] px-2.5 py-1 text-xs font-semibold text-[var(--ink)] hover:bg-slate-50"
            onClick={() => setMobileOpen(true)}
            aria-label="Open menu"
          >
            Menu
          </button>
          <BrandMark size="sm" href="/projects" />
        </header>
        <main className="flex-1 px-4 py-5 sm:px-6 lg:px-8 max-w-7xl w-full">{children}</main>
      </div>
    </div>
  );
}
