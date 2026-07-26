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

      <div className="mb-6 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Project</span>
          <select
            className="rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2"
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        {mode ? (
          <p className="text-sm text-[var(--ink-muted)]">
            Mode <span className="font-medium text-[var(--ink)]">{mode}</span>
          </p>
        ) : null}
      </div>

      <div className="mb-8 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Name</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="app" />
        </label>
        <Button onClick={onCreate} disabled={busy || !name.trim()}>
          Provision
        </Button>
      </div>

      {databases.length === 0 ? (
        <EmptyState title="No databases" description="Provision a Postgres schema for this project." />
      ) : (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-[var(--ink-muted)]">
              <th className="py-2 font-medium">Name</th>
              <th className="py-2 font-medium">Status</th>
              <th className="py-2 font-medium">Hint</th>
              <th className="py-2 font-medium">Secret</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {databases.map((d) => (
              <tr key={d.id} className="border-b border-[var(--border)]">
                <td className="py-2 font-medium">{d.name}</td>
                <td className="py-2 text-[var(--ink-muted)]">
                  {d.status} · {d.mode}
                </td>
                <td className="max-w-xs truncate py-2 text-[var(--ink-muted)]" title={d.connection_hint}>
                  {d.connection_hint}
                </td>
                <td className="max-w-[160px] truncate py-2 text-[var(--ink-muted)]" title={d.secret_ref}>
                  {d.secret_ref}
                </td>
                <td className="py-2 text-right">
                  <Button variant="ghost" onClick={() => onDelete(d.id)} disabled={busy}>
                    Delete
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
