"use client";

import { levelOf, type OrgAccess } from "@/lib/access";
import { deployments, domains, runtime, type Deployment, type DomainDetail, type Project, type RuntimeInstance } from "@/lib/api";
import { useResource } from "@/lib/use-resource";

export interface ProjectSnapshot {
  project: Project;
  instances: RuntimeInstance[] | null;
  deployments: Deployment[] | null;
  domains: DomainDetail[] | null;
  /** names of upstream calls that failed for this project */
  failed: string[];
}

const MAX_PROJECTS = 16;

async function settle<T>(p: Promise<T>): Promise<T | null> {
  try {
    return await p;
  } catch {
    return null;
  }
}

/** Real per-project state, fetched in parallel. A failing service marks the field null (never fabricated). */
export function useSnapshots(orgId: string | undefined, projects: Project[], access?: OrgAccess) {
  const ids = projects.slice(0, MAX_PROJECTS).map((p) => p.id).join(",");
  return useResource(orgId ? `snap:${orgId}:${ids}` : null, async (signal) => {
    const list = projects.slice(0, MAX_PROJECTS);
    const out = await Promise.all(
      list.map(async (project): Promise<ProjectSnapshot> => {
        // Restricted users are only asked about areas they may view; a missing grant is not a failure.
        const ok = (a: "runtime" | "deployments" | "domains") => !access || levelOf(access, project.id, a) !== "none";
        const [i, d, dm] = await Promise.all([
          ok("runtime") ? settle(runtime.instances(orgId!, project.id, { signal })) : Promise.resolve(null),
          ok("deployments") ? settle(deployments.list(orgId!, project.id, { signal })) : Promise.resolve(null),
          ok("domains") ? settle(domains.list(orgId!, project.id, { signal })) : Promise.resolve(null),
        ]);
        const failed: string[] = [];
        if (i === null && ok("runtime")) failed.push("runtime");
        if (d === null && ok("deployments")) failed.push("deployments");
        if (dm === null && ok("domains")) failed.push("domains");
        return { project, instances: i, deployments: d, domains: dm, failed };
      }),
    );
    return out;
  }, { interval: 15000 });
}

export function projectUrl(s: ProjectSnapshot): string | null {
  const live = s.domains?.map((d) => d.domain).filter((d) => ["active", "verified"].includes(d.status)) ?? [];
  const sys = live.find((d) => d.kind === "system") ?? live[0];
  const host = sys?.hostname ?? s.project.system_domain;
  if (!host) return null;
  const tls = s.domains?.some((d) => d.domain.hostname === host && (d.domain.ssl_status === "active" || d.domain.status === "active"));
  return `${tls ? "https" : "http"}://${host}`;
}

export type AppState = "running" | "stopped" | "failed" | "none" | "unknown";

export function appState(s: ProjectSnapshot): AppState {
  return instancesState(s.instances);
}

export function instancesState(instances: RuntimeInstance[] | null | undefined): AppState {
  if (!instances) return "unknown";
  const apps = instances.filter((i) => (i.role ?? "app") === "app" && !i.is_preview);
  if (apps.length === 0) return "none";
  if (apps.some((i) => i.status === "running")) return "running";
  if (apps.some((i) => i.status === "failed" || i.status === "error")) return "failed";
  return "stopped";
}
