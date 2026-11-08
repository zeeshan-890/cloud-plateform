"use client";

import { createContext, useContext, useMemo } from "react";
import { useOrg } from "@/lib/org-context";
import { levelOf, revealsSecrets } from "@/lib/access";
import type { Area, Level, Project, Role } from "@/lib/api";

interface Scope {
  orgId: string;
  projectId: string;
  project: Project;
  role: Role | undefined;
  /** area the current panel belongs to (restricted users are checked against it) */
  area: Area | undefined;
  level: Level;
  /** may create / change / delete in this area. Restricted users: level "manage". Others: role based. */
  can: (min: Role) => boolean;
  /** may read plaintext secret values and add-on/database passwords */
  canReveal: boolean;
  /** view-only restricted user */
  readOnly: boolean;
}

const Ctx = createContext<Scope | null>(null);

export function ScopeProvider({ project, area, children }: { project: Project; area?: Area; children: React.ReactNode }) {
  const { currentOrg, role, can: roleCan, access } = useOrg();
  const value = useMemo<Scope | null>(() => {
    if (!currentOrg) return null;
    const restricted = access.known && !access.full;
    const level: Level = area ? levelOf(access, project.id, area) : "manage";
    const can = (min: Role) => (restricted ? (min === "viewer" ? level !== "none" : level === "manage") : roleCan(min));
    const canReveal = restricted ? revealsSecrets(access, project.id) : roleCan("admin");
    return { orgId: currentOrg.id, projectId: project.id, project, role, area, level, can, canReveal, readOnly: restricted && level === "view" };
  }, [currentOrg, project, area, role, roleCan, access]);
  if (!value) return null;
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useScope(): Scope {
  const v = useContext(Ctx);
  if (!v) throw new Error("useScope must be used inside ScopeProvider");
  return v;
}
