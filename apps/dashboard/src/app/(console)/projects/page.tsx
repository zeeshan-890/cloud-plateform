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
        <ul className="divide-y divide-[var(--border)] border-y border-[var(--border)]">
          {projects.map((project) => (
            <li key={project.id}>
              <Link
                href={`/projects/${project.id}`}
                className="flex flex-col gap-1 py-4 transition-colors hover:bg-[var(--surface)] sm:flex-row sm:items-center sm:justify-between sm:px-2"
              >
                <div>
                  <p className="font-medium text-[var(--ink)]">{project.name}</p>
                  {project.description ? (
                    <p className="mt-0.5 text-sm text-[var(--ink-muted)]">
                      {project.description}
                    </p>
                  ) : null}
                </div>
                <span className="font-[family-name:var(--font-mono)] text-xs text-[var(--ink-faint)]">
                  {project.slug || project.id.slice(0, 8)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
