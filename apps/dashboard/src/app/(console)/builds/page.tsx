"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { setVisibilityInterval } from "@/lib/visibility";
import type { Build, Project } from "@/lib/types";

export default function BuildsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [builds, setBuilds] = useState<Build[]>([]);
  const [logs, setLogs] = useState("");
  const [selected, setSelected] = useState<string>("");
  const [error, setError] = useState("");
  const [explainText, setExplainText] = useState("");
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
          setError(
            err instanceof ApiError ? err.message : "Failed to load projects",
          );
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  async function loadBuilds(pid: string) {
    if (!currentOrg || !pid) return;
    setBuilds(await api.listBuilds(currentOrg.id, pid));
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadBuilds(projectId);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load builds",
          );
        }
      }
    })();
    const cancelInterval = setVisibilityInterval(() => {
      loadBuilds(projectId).catch(() => undefined);
    }, 4000);
    return () => {
      mounted = false;
      cancelInterval();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function showLogs(buildId: string) {
    if (!currentOrg || !projectId) return;
    setSelected(buildId);
    setExplainText("");
    try {
      const res = await api.getBuildLogs(currentOrg.id, projectId, buildId);
      setLogs(res.logs || "");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load logs");
    }
  }

  async function onExplain(buildId: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setExplainText("");
    setSelected(buildId);
    try {
      const [res, logRes] = await Promise.all([
        api.explainFailure(currentOrg.id, projectId, {
          build_id: buildId,
          prompt: "Explain why this build failed and how to fix it.",
        }),
        api.getBuildLogs(currentOrg.id, projectId, buildId),
      ]);
      setLogs(logRes.logs || "");
      setExplainText(`(${res.mode}) ${res.explanation}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Explain failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Builds require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Builds"
        description="Build farm jobs from Redis Streams — status, logs, and AI explain on failures."
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      {explainText ? (
        <pre className="mb-6 max-w-3xl whitespace-pre-wrap rounded-md border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)]">
          {explainText}
        </pre>
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

      {builds.length === 0 ? (
        <EmptyState
          title="No builds yet"
          description="Trigger a deployment to enqueue a build job."
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-2">
          <ul className="divide-y divide-[var(--border)]">
            {builds.map((b) => (
              <li key={b.id} className="flex items-center justify-between gap-3 py-3">
                <div>
                  <p className="text-sm font-medium text-[var(--ink)]">
                    {b.status}
                    {b.framework ? ` · ${b.framework}` : ""}
                  </p>
                  <p className="font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {(b.git_sha || "").slice(0, 12)} ·{" "}
                    {b.created_at ? new Date(b.created_at).toLocaleString() : "—"}
                  </p>
                </div>
                <div className="flex shrink-0 flex-wrap justify-end gap-2">
                  <Button
                    type="button"
                    variant={selected === b.id ? "primary" : "secondary"}
                    onClick={() => showLogs(b.id)}
                  >
                    Logs
                  </Button>
                  {b.status === "failed" ? (
                    <Button
                      type="button"
                      variant="secondary"
                      disabled={busy}
                      onClick={() => onExplain(b.id)}
                    >
                      {busy && selected === b.id ? "Analyzing…" : "Explain failure"}
                    </Button>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
          <pre className="max-h-[480px] overflow-auto rounded-md border border-[var(--border)] bg-[var(--surface)] p-4 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)] whitespace-pre-wrap">
            {logs || "Select a build to view logs."}
          </pre>
        </div>
      )}
    </div>
  );
}
