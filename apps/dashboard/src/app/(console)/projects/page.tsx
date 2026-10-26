"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { EmptyState, PageHeader, Alert } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project } from "@/lib/types";

export default function ProjectsPage() {
  const { currentOrg, loading: orgLoading } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    if (orgLoading) return;
    if (!currentOrg) {
      setProjects([]);
      setLoading(false);
      return;
    }
    let mounted = true;
    (async () => {
      setLoading(true);
      setError("");
      try {
        const list = await api.listProjects(currentOrg.id);
        if (mounted) setProjects(list);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load projects",
          );
        }
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg, orgLoading]);

  if (!orgLoading && !currentOrg) {
    return (
      <EmptyState
        title="Create an organization"
        description="Projects live inside an organization. Set one up to continue."
        action={
          <Link href="/orgs/new">
            <Button>New organization</Button>
          </Link>
        }
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Projects"
        description={`Workspace for ${currentOrg?.name || "your org"}.`}
        actions={
          <Link href="/projects/new">
            <Button>New project</Button>
          </Link>
        }
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      {loading || orgLoading ? (
        <p className="text-sm text-[var(--ink-muted)]">Loading projects…</p>
      ) : projects.length === 0 ? (
        <EmptyState
          title="No projects yet"
          description="Create a project to group apps, environments, and resources."
          action={
            <Link href="/projects/new">
              <Button>Create project</Button>
            </Link>
          }
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3.5">
          {projects.map((project) => (
            <Link
              key={project.id}
              href={`/projects/${project.id}`}
              className="jp-card p-4 flex flex-col justify-between group hover:border-[var(--accent)] cursor-pointer"
            >
              <div>
                <div className="flex items-start justify-between gap-2 mb-1.5">
                  <div className="flex items-center gap-2">
                    <div className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-50 text-blue-600 ring-1 ring-blue-500/20 font-bold text-xs">
                      {project.name.slice(0, 2).toUpperCase()}
                    </div>
                    <h3 className="font-bold text-[var(--ink)] text-sm group-hover:text-[var(--accent)] transition-colors">
                      {project.name}
                    </h3>
                  </div>
                  <span className="inline-flex items-center gap-1 rounded bg-emerald-50 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-700 ring-1 ring-emerald-600/20 shrink-0">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                    Active
                  </span>
                </div>
                <p className="text-xs text-[var(--ink-secondary)] font-medium line-clamp-2 mb-3 mt-1">
                  {project.description || "Container and service infrastructure workspace."}
                </p>
              </div>

              <div className="pt-2.5 border-t border-[var(--border)] flex items-center justify-between text-xs">
                <span className="font-mono text-[10px] bg-slate-100 text-slate-700 font-semibold px-2 py-0.5 rounded ring-1 ring-slate-200">
                  {project.slug || project.id.slice(0, 8)}
                </span>
                <span className="font-semibold text-xs text-[var(--accent)] group-hover:translate-x-0.5 transition-transform">
                  Open console &rarr;
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
