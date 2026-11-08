"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { createPortal } from "react-dom";
import { Icon, type IconName } from "@/components/ui/icons";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useTheme } from "@/lib/theme";
import { levelOf } from "@/lib/access";
import type { Area } from "@/lib/api";
import { PROJECT_NAV_ITEMS, PROJECT_TABS, flatOrgNav, projectHref } from "./nav";

interface Cmd {
  id: string;
  label: string;
  hint?: string;
  icon: IconName;
  keywords?: string;
  run: () => void;
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const router = useRouter();
  const { isPlatformAdmin, orgs, setCurrentOrgId, currentOrg, can, access } = useOrg();
  const { projects, activeProject } = useProjects();
  const { toggle, theme } = useTheme();
  const { logout } = useAuth();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  const commands = useMemo<Cmd[]>(() => {
    const go = (href: string) => () => router.push(href);
    const nav: Cmd[] = flatOrgNav(isPlatformAdmin, can("admin")).map((n) => ({
      id: `nav:${n.href}`,
      label: n.label,
      hint: "Organization",
      icon: n.icon,
      keywords: n.keywords,
      run: go(n.href),
    }));
    const proj: Cmd[] = projects.map((p) => ({
      id: `proj:${p.id}`,
      label: p.name,
      hint: "Open project",
      icon: "folder",
      keywords: "project",
      run: go(projectHref(p.id)),
    }));
    const target = projects.find((p) => p.id === activeProject?.id);
    const tabs: Cmd[] = target
      ? PROJECT_TABS.filter((t) => t !== "overview" && levelOf(access, target.id, t as Area) !== "none").map((t) => ({
          id: `tab:${t}`,
          label: `${target.name} / ${PROJECT_NAV_ITEMS[t].label}`,
          hint: "Project page",
          icon: PROJECT_NAV_ITEMS[t].icon,
          keywords: PROJECT_NAV_ITEMS[t].keywords,
          run: go(projectHref(target.id, t)),
        }))
      : [];
    const orgCmds: Cmd[] = orgs
      .filter((o) => o.id !== currentOrg?.id)
      .map((o) => ({ id: `org:${o.id}`, label: `Switch to ${o.name}`, hint: "Organization", icon: "users", run: () => setCurrentOrgId(o.id) }));
    return [
      ...nav,
      ...proj,
      ...tabs,
      ...(can("admin") ? [{ id: "act:new-project", label: "New project", hint: "Action", icon: "plus" as const, run: go("/projects/new") }] : []),
      ...orgCmds,
      { id: "act:theme", label: theme === "dark" ? "Switch to daylight theme" : "Switch to night shift theme", hint: "Action", icon: theme === "dark" ? "sun" : "moon", keywords: "dark light", run: toggle },
      { id: "act:logout", label: "Sign out", hint: "Action", icon: "logout", run: () => void logout() },
    ];
  }, [isPlatformAdmin, can, access, projects, activeProject?.id, orgs, currentOrg?.id, setCurrentOrgId, theme, toggle, logout, router]);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands;
    return commands.filter((c) => `${c.label} ${c.hint ?? ""} ${c.keywords ?? ""}`.toLowerCase().includes(q));
  }, [commands, query]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  if (!open) return null;
  const idx = Math.min(active, Math.max(0, results.length - 1));
  const choose = (c: Cmd | undefined) => {
    if (!c) return;
    onClose();
    setQuery("");
    setActive(0);
    c.run();
  };

  return createPortal(
    <div
      className="backdrop"
      style={{ alignItems: "start", paddingTop: "12vh" }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div role="dialog" aria-modal="true" aria-label="Command palette" className="panel dialog !p-0" onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        }
      }}>
        <div className="p-3">
          <div className="well flex items-center gap-3 !p-0 !pl-3">
            <Icon name="search" size={18} />
            <input
              ref={inputRef}
              role="combobox"
              aria-expanded="true"
              aria-controls={listId}
              aria-activedescendant={results[idx] ? `${listId}-${idx}` : undefined}
              aria-label="Search commands"
              placeholder="Jump to a page, project or action..."
              className="mono min-h-[44px] w-full bg-transparent pr-3 text-[14px] outline-none placeholder:opacity-60"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  setActive((idx + 1) % Math.max(1, results.length));
                } else if (e.key === "ArrowUp") {
                  e.preventDefault();
                  setActive((idx - 1 + results.length) % Math.max(1, results.length));
                } else if (e.key === "Enter") {
                  e.preventDefault();
                  choose(results[idx]);
                }
              }}
            />
          </div>
        </div>
        <ul id={listId} role="listbox" aria-label="Results" className="scroll-thin m-0 max-h-[50vh] list-none overflow-auto px-2 pb-3">
          {results.length === 0 && <li className="muted px-3 py-6 text-center">No match for &ldquo;{query}&rdquo;.</li>}
          {results.map((c, i) => (
            <li
              key={c.id}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === idx}
              onMouseMove={() => setActive(i)}
              onClick={() => choose(c)}
              className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2.5"
              style={i === idx ? { background: "linear-gradient(170deg, var(--well-1), var(--well-2))", color: "var(--well-text)", boxShadow: "var(--dent)" } : undefined}
            >
              <Icon name={c.icon} size={18} />
              <span className="flex-1 text-[15px] font-medium">{c.label}</span>
              {c.hint && <span className="label !text-[11px]" style={i === idx ? { color: "inherit", opacity: 0.8 } : undefined}>{c.hint}</span>}
            </li>
          ))}
        </ul>
      </div>
    </div>,
    document.body,
  );
}
