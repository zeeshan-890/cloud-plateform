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

      {error ? (
        <Alert tone="error">{error}</Alert>
      ) : null}

      {loading || orgLoading ? (
        <div className="flex h-48 items-center justify-center text-sm font-semibold text-slate-500">
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
              className="bg-white rounded-xl border-2 border-slate-300 p-5 shadow-sm hover:shadow-md hover:border-slate-400 transition-all flex flex-col justify-between group cursor-pointer"
            >
              <div>
                <div className="flex items-start justify-between gap-2 mb-2">
                  <div className="flex items-center gap-2.5">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gradient-to-br from-blue-700 to-indigo-800 text-white font-extrabold text-sm shadow-xs">
                      {project.name.slice(0, 2).toUpperCase()}
                    </div>
                    <div>
                      <h3 className="font-extrabold text-[#0F1B3D] text-sm group-hover:text-[#1E3A8A] transition-colors">
                        {project.name}
                      </h3>
                      <p className="text-[10px] text-slate-400 font-mono">
                        {project.slug || project.id.slice(0, 8)}
                      </p>
                    </div>
                  </div>
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 px-2.5 py-0.5 text-[10px] font-bold text-emerald-800 border-2 border-emerald-200 shrink-0">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                    Active
                  </span>
                </div>
                <p className="text-xs text-slate-600 font-medium line-clamp-2 my-2 leading-relaxed">
                  {project.description || "Production container and web application infrastructure."}
                </p>
              </div>

              <div className="pt-3 border-t-2 border-slate-100 flex items-center justify-between text-xs mt-3">
                <span className="inline-flex items-center gap-1 font-mono text-[10px] bg-slate-50 text-slate-700 font-bold px-2 py-0.5 rounded-lg border-2 border-slate-300">
                  <span>🐳</span> Docker Native
                </span>
                <span className="font-bold text-xs text-[#1E3A8A] group-hover:translate-x-0.5 transition-transform flex items-center gap-1">
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
