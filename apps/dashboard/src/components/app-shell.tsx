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
  { label: "Billing", href: "/billing" },
  { label: "AI", href: "/ai" },
  { label: "Team", href: "/team" },
  { label: "API keys", href: "/keys" },
  { label: "Sessions", href: "/sessions" },
];

function NavLink({ item }: { item: NavItem }) {
  const pathname = usePathname();
  if (item.soon) {
    return (
      <span
        className="flex items-center justify-between rounded-md px-3 py-2 text-sm text-[var(--ink-faint)] cursor-not-allowed"
        title="Coming soon"
      >
        {item.label}
        <span className="text-[10px] uppercase tracking-wider text-[var(--ink-faint)]">
          Soon
        </span>
      </span>
    );
  }
  const active = pathname === item.href || pathname.startsWith(item.href + "/");
  return (
    <Link
      href={item.href}
      className={`block rounded-md px-3 py-2 text-sm transition-colors duration-200 ${
        active
          ? "bg-[var(--ink)] text-[var(--paper)]"
          : "text-[var(--ink-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--ink)]"
      }`}
    >
      {item.label}
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
    <aside className="flex h-full w-64 flex-col border-r border-[var(--border)] bg-[var(--paper)]">
      <div className="flex items-center justify-between px-5 py-5">
        <BrandMark size="md" href="/projects" />
        <button
          type="button"
          className="lg:hidden rounded-md p-2 text-[var(--ink-muted)] hover:bg-[var(--surface)]"
          onClick={() => setMobileOpen(false)}
          aria-label="Close menu"
        >
          Close
        </button>
      </div>

      <div className="relative px-3 pb-4">
        <button
          type="button"
          onClick={() => setOrgMenuOpen((v) => !v)}
          className="flex w-full items-center justify-between rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2.5 text-left text-sm transition-colors hover:bg-[var(--surface-hover)]"
          disabled={loading}
        >
          <span className="truncate font-medium text-[var(--ink)]">
            {currentOrg?.name || (loading ? "Loading…" : "No organization")}
          </span>
          <span className="text-[var(--ink-faint)]" aria-hidden>
            ▾
          </span>
        </button>
        {orgMenuOpen ? (
          <div className="absolute left-3 right-3 z-20 mt-1 overflow-hidden rounded-md border border-[var(--border)] bg-[var(--paper)] shadow-lg">
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

      <div className="border-t border-[var(--border)] px-4 py-4">
        <p className="truncate text-sm font-medium text-[var(--ink)]">
          {user?.name}
        </p>
        <p className="truncate text-xs text-[var(--ink-muted)]">{user?.email}</p>
        <Button
          variant="ghost"
          className="mt-2 w-full justify-start px-0"
          onClick={handleLogout}
        >
          Sign out
        </Button>
      </div>
    </aside>
  );

  return (
    <div className="flex min-h-screen bg-[var(--paper)]">
      <div className="hidden lg:block">{sidebar}</div>

      {mobileOpen ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <button
            type="button"
            className="absolute inset-0 bg-[var(--ink)]/40"
            aria-label="Close overlay"
            onClick={() => setMobileOpen(false)}
          />
          <div className="relative z-10 h-full animate-slide-in">{sidebar}</div>
        </div>
      ) : null}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-3 border-b border-[var(--border)] px-4 py-3 lg:hidden">
          <button
            type="button"
            className="rounded-md border border-[var(--border)] px-3 py-2 text-sm text-[var(--ink)]"
            onClick={() => setMobileOpen(true)}
            aria-label="Open menu"
          >
            Menu
          </button>
          <BrandMark size="sm" href="/projects" />
        </header>
        <main className="flex-1 px-4 py-6 sm:px-8 lg:px-10">{children}</main>
      </div>
    </div>
  );
}
