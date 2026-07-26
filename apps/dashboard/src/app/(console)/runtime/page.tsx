"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project, RuntimeInstance } from "@/lib/types";

export default function RuntimePage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [instances, setInstances] = useState<RuntimeInstance[]>([]);
  const [mode, setMode] = useState("simulate");
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

  async function loadRuntime(pid: string) {
    if (!currentOrg || !pid) return;
    const data = await api.listRuntimeInstances(currentOrg.id, pid);
    setInstances(data.instances);
    setMode(data.mode || "simulate");
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadRuntime(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load runtime");
        }
      }
    })();
    const t = setInterval(() => {
      loadRuntime(projectId).catch(() => undefined);
    }, 5000);
    return () => {
      mounted = false;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onStart(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.startRuntimeInstance(currentOrg.id, projectId, id);
      await loadRuntime(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Start failed");
    } finally {
      setBusy(false);
    }
  }

  async function onStop(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.stopRuntimeInstance(currentOrg.id, projectId, id);
      await loadRuntime(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Stop failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Runtime requires an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Runtime"
        description={`Containers and desired state. Mode: ${mode}`}
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      <label className="mb-6 block max-w-sm text-sm text-[var(--ink-muted)]">
        Project
        <select
          className="mt-1 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[var(--ink)]"
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

      {instances.length === 0 ? (
        <EmptyState
          title="No runtime instances"
          description="Deploy a build; the scheduler starts a runtime instance when the deploy becomes ready."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
                <th className="py-2 pr-3 font-medium">Status</th>
                <th className="py-2 pr-3 font-medium">Kind</th>
                <th className="py-2 pr-3 font-medium">Health</th>
                <th className="py-2 pr-3 font-medium">Image</th>
                <th className="py-2 pr-3 font-medium">Mode</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {instances.map((in_) => (
                <tr key={in_.id} className="border-b border-[var(--border)]">
                  <td className="py-3 pr-3 font-medium text-[var(--ink)]">
                    {in_.status}
                    <span className="ml-1 text-xs text-[var(--ink-faint)]">
                      (want {in_.desired_state})
                    </span>
                  </td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{in_.kind}</td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{in_.health_status}</td>
                  <td className="py-3 pr-3 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {in_.image_ref || "—"}
                  </td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{in_.mode}</td>
                  <td className="py-3 text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        type="button"
                        variant="secondary"
                        disabled={busy}
                        onClick={() => onStart(in_.id)}
                      >
                        Start
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => onStop(in_.id)}
                      >
                        Stop
                      </Button>
                    </div>
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
