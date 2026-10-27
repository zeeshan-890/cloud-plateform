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
        description="View build pipeline execution, compiler outputs, and image compilation logs."
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

      <div className="jp-card p-4 mb-6 flex flex-wrap items-center justify-between gap-4">
        <label className="flex items-center gap-2 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
          Target Project:
          <select
            className="rounded-md border border-[var(--border)] bg-white px-3 py-1.5 text-xs font-semibold text-[var(--ink)] shadow-2xs focus:border-[var(--accent)] focus:outline-none"
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
      </div>

      {builds.length === 0 ? (
        <EmptyState
          title="No builds yet"
          description="Trigger a deployment to enqueue a build job."
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
              <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Build Queue ({builds.length})
              </h4>
            </div>
            <ul className="divide-y divide-[var(--border)]">
              {builds.map((b) => {
                const isSuccess = b.status === "succeeded" || b.status === "ready";
                const isFailed = b.status === "failed";
                const isRunning = b.status === "running" || b.status === "building";
                return (
                  <li
                    key={b.id}
                    className={`flex items-center justify-between gap-3 p-3.5 transition-colors ${
                      selected === b.id ? "bg-blue-50/70" : "hover:bg-slate-50/50"
                    }`}
                  >
                    <div>
                      <div className="flex items-center gap-2">
                        <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-bold ${
                          isSuccess
                            ? "bg-emerald-100 text-emerald-800"
                            : isFailed
                            ? "bg-red-100 text-red-800"
                            : isRunning
                            ? "bg-blue-100 text-blue-800"
                            : "bg-slate-100 text-slate-800"
                        }`}>
                          <span className={`h-1.5 w-1.5 rounded-full ${
                            isSuccess ? "bg-emerald-600" : isFailed ? "bg-red-600" : isRunning ? "bg-blue-600 animate-pulse" : "bg-slate-500"
                          }`} />
                          {b.status}
                        </span>
                        {b.framework && (
                          <span className="text-xs font-bold text-[var(--ink)]">
                            {b.framework}
                          </span>
                        )}
                      </div>
                      <p className="mt-1 font-mono text-[11px] text-[var(--ink-muted)]">
                        SHA: {(b.git_sha || "head").slice(0, 8)} &bull;{" "}
                        {b.created_at ? new Date(b.created_at).toLocaleTimeString() : "—"}
                      </p>
                    </div>
                    <div className="flex shrink-0 flex-wrap justify-end gap-1.5">
                      <Button
                        type="button"
                        size="sm"
                        variant={selected === b.id ? "primary" : "secondary"}
                        onClick={() => showLogs(b.id)}
                      >
                        Logs
                      </Button>
                      {b.status === "failed" ? (
                        <Button
                          type="button"
                          size="sm"
                          variant="secondary"
                          disabled={busy}
                          onClick={() => onExplain(b.id)}
                        >
                          {busy && selected === b.id ? "Analyzing…" : "Explain"}
                        </Button>
                      ) : null}
                    </div>
                  </li>
                );
              })}
            </ul>
          </div>

          <div className="jp-card overflow-hidden flex flex-col">
            <div className="border-b border-slate-700 bg-slate-900 px-4 py-3 flex items-center justify-between">
              <span className="font-mono text-xs font-bold text-slate-200">Build Output Terminal</span>
              <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-slate-400">
                {selected ? `Build ${selected.slice(0, 8)}` : "Live Stream"}
              </span>
            </div>
            <pre className="h-[460px] overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400/90 whitespace-pre-wrap leading-relaxed shadow-inner">
              {logs || "// Select a build from the queue to view real-time compilation logs."}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
