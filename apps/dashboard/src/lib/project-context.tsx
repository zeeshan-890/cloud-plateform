"use client";

import { createContext, useContext, useMemo } from "react";
import { projects, type Project } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useStoredValue } from "@/lib/persist";
import { useResource } from "@/lib/use-resource";
import type { ApiError } from "@/lib/api/http";

interface ProjectContextValue {
  projects: Project[];
  activeProject: Project | null;
  setActiveProjectId: (id: string) => void;
  loading: boolean;
  error: ApiError | undefined;
  reload: () => void;
}

const Ctx = createContext<ProjectContextValue | null>(null);

export function ProjectProvider({ children }: { children: React.ReactNode }) {
  const { currentOrg, access } = useOrg();
  const [storedId, setStoredId] = useStoredValue("jp_active_project");
  const list = useResource(currentOrg ? `projects:${currentOrg.id}` : null, (signal) =>
    projects.list(currentOrg!.id, { signal }),
  );

  // Restricted users only ever see projects they were granted, even if an upstream list is broader.
  const visible = useMemo(() => {
    const l = list.data;
    if (!l) return l;
    return access.known && !access.full ? l.filter((p) => access.projects.has(p.id)) : l;
  }, [list.data, access]);

  const activeProject = useMemo(() => {
    const l = visible;
    if (!l || l.length === 0) return null;
    return l.find((p) => p.id === storedId) ?? l[0];
  }, [visible, storedId]);

  const value = useMemo<ProjectContextValue>(
    () => ({
      projects: visible ?? [],
      activeProject,
      setActiveProjectId: setStoredId,
      loading: list.loading,
      error: list.error,
      reload: list.reload,
    }),
    [visible, activeProject, setStoredId, list.loading, list.error, list.reload],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useProjects(): ProjectContextValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useProjects must be used inside ProjectProvider");
  return v;
}
