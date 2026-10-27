"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { EmptyState, Alert } from "@/components/ui/page";
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
        description="Projects live inside an organization workspace. Set one up to get started."
        action={
          <Link href="/orgs/new">
            <Button>New Organization</Button>
          </Link>
        }
      />
    );
  }

  return (
    <div className="space-y-6 animate-fade-up">
      {/* Crezumic-Style Projects Header */}
      <div className="bg-white rounded-xl border border-slate-200/90 p-6 shadow-xs flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl md:text-3xl font-extrabold text-slate-900 tracking-tight">
              Projects & Applications
            </h1>
            <span className="rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-bold text-slate-700 border border-slate-200">
              {projects.length}
            </span>
          </div>
          <p className="text-xs text-slate-500 mt-1">
            Active workspace for <strong className="text-slate-800">{currentOrg?.name || "your organization"}</strong>.
          </p>
        </div>

        <div className="flex items-center gap-2.5 shrink-0">
          <Link href="/projects/new">
            <button
              type="button"
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-xs font-bold text-white shadow-xs hover:bg-blue-700 transition-all"
            >
              <span>+</span>
              <span>New Project</span>
            </button>
          </Link>
        </div>
      </div>

      {error ? (
        <Alert tone="error">{error}</Alert>
      ) : null}

      {loading || orgLoading ? (
        <div className="flex h-48 items-center justify-center text-sm font-semibold text-slate-400">
          Loading projects…
        </div>
      ) : projects.length === 0 ? (
        <EmptyState
          title="No projects yet"
          description="Create your first project to deploy code repositories, Docker containers, and databases."
          action={
            <Link href="/projects/new">
              <Button>Create Project</Button>
            </Link>
          }
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {projects.map((project) => (
            <Link
              key={project.id}
              href={`/projects/${project.id}`}
              className="bg-white rounded-xl border border-slate-200/90 p-5 shadow-xs hover:shadow-md hover:border-blue-400 transition-all flex flex-col justify-between group cursor-pointer"
            >
              <div>
                <div className="flex items-start justify-between gap-2 mb-2">
                  <div className="flex items-center gap-2.5">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gradient-to-br from-blue-600 to-indigo-700 text-white font-extrabold text-sm shadow-xs">
                      {project.name.slice(0, 2).toUpperCase()}
                    </div>
                    <div>
                      <h3 className="font-extrabold text-slate-900 text-sm group-hover:text-blue-600 transition-colors">
                        {project.name}
                      </h3>
                      <p className="text-[10px] text-slate-400 font-mono">
                        {project.slug || project.id.slice(0, 8)}
                      </p>
                    </div>
                  </div>
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 px-2 py-0.5 text-[10px] font-bold text-emerald-700 border border-emerald-200 shrink-0">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                    Active
                  </span>
                </div>
                <p className="text-xs text-slate-600 font-medium line-clamp-2 my-2 leading-relaxed">
                  {project.description || "Production container and web application infrastructure."}
                </p>
              </div>

              <div className="pt-3 border-t border-slate-100 flex items-center justify-between text-xs mt-3">
                <span className="inline-flex items-center gap-1 font-mono text-[10px] bg-slate-50 text-slate-600 font-semibold px-2 py-0.5 rounded border border-slate-200">
                  <span>🐳</span> Docker Native
                </span>
                <span className="font-bold text-xs text-blue-600 group-hover:translate-x-0.5 transition-transform flex items-center gap-1">
                  <span>Manage</span>
                  <span>&rarr;</span>
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
