"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { ManagedDatabase, Project } from "@/lib/types";

export default function DatabasesPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [databases, setDatabases] = useState<ManagedDatabase[]>([]);
  const [mode, setMode] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        const list = await api.listProjects(currentOrg.id);
        if (!mounted) return;
        setProjects(list);
        if (list[0]) setProjectId(list[0].id);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load projects");
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  async function load(pid: string) {
    if (!currentOrg || !pid) return;
    const res = await api.listDatabases(currentOrg.id, pid);
    setDatabases(res.databases);
    setMode(res.mode || "");
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await load(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load databases");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onCreate() {
    if (!currentOrg || !projectId || !name.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.createDatabase(currentOrg.id, projectId, name.trim());
      setName("");
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(dbId: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteDatabase(currentOrg.id, projectId, dbId);
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Databases require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Databases"
        description="One-click Postgres for a project. Connection strings are stored as secret refs."
      />
      {error ? <Alert>{error}</Alert> : null}

      <div className="mb-6 grid gap-4 md:grid-cols-2">
        <div className="jp-card p-4 space-y-3">
          <h3 className="text-sm font-bold text-[var(--ink)]">Target Project</h3>
          <div className="flex items-center gap-3">
            <select
              className="flex-1 rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm font-semibold text-[var(--ink)] shadow-2xs focus:border-[var(--accent)] focus:outline-none"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
            >
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
            {mode ? (
              <span className="rounded border border-[var(--border)] bg-slate-100 px-2.5 py-1 text-xs font-bold text-[var(--ink-secondary)]">
                {mode}
              </span>
            ) : null}
          </div>
        </div>

        <div className="jp-card p-4 space-y-3">
          <h3 className="text-sm font-bold text-[var(--ink)]">Provision Postgres</h3>
          <div className="flex items-center gap-2">
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. production_db"
            />
            <Button onClick={onCreate} disabled={busy || !name.trim()}>
              Provision
            </Button>
          </div>
        </div>
      </div>

      {databases.length === 0 ? (
        <EmptyState title="No databases" description="Provision a Postgres schema for this project." />
      ) : (
        <div className="jp-card overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-slate-50 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                <th className="px-4 py-3">Name</th>
                <th className="px-4 py-3">Status</th>
                <th className="px-4 py-3">Connection Hint</th>
                <th className="px-4 py-3">Secret Ref</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {databases.map((d) => (
                <tr key={d.id} className="hover:bg-slate-50/50 transition-colors">
                  <td className="px-4 py-3 font-bold text-[var(--ink)]">{d.name}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-300 bg-emerald-50 px-2 py-0.5 text-xs font-bold text-emerald-800">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                      {d.status} · {d.mode}
                    </span>
                  </td>
                  <td className="max-w-xs truncate px-4 py-3 font-mono text-xs text-[var(--ink-secondary)]" title={d.connection_hint}>
                    {d.connection_hint}
                  </td>
                  <td className="max-w-[160px] truncate px-4 py-3 font-mono text-xs text-[var(--ink-secondary)]" title={d.secret_ref}>
                    {d.secret_ref}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <Button variant="danger" size="sm" onClick={() => onDelete(d.id)} disabled={busy}>
                      Delete
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
