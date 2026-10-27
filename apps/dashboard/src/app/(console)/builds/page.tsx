"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { setVisibilityInterval } from "@/lib/visibility";
import type { Build, LogEntry, Project, RuntimeInstance } from "@/lib/types";

export default function BuildsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [logsTab, setLogsTab] = useState<"runtime" | "builds">("runtime");
  const [builds, setBuilds] = useState<Build[]>([]);
  const [logs, setLogs] = useState("");
  const [selected, setSelected] = useState<string>("");
  const [runtimeLogs, setRuntimeLogs] = useState<LogEntry[]>([]);
  const [instances, setInstances] = useState<RuntimeInstance[]>([]);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [explainText, setExplainText] = useState("");
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

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
    const list = await api.listBuilds(currentOrg.id, pid);
    setBuilds(list);
    if (!selected && list.length > 0) {
      setSelected(list[0].id);
      showLogs(list[0].id).catch(() => undefined);
    }
  }

  async function loadRuntime(pid: string) {
    if (!currentOrg || !pid) return;
    try {
      const [logsData, instData] = await Promise.all([
        api.queryLogs(currentOrg.id, pid, { limit: 150 }).catch(() => ({ entries: [] })),
        api.listRuntimeInstances(currentOrg.id, pid).catch(() => ({ instances: [], mode: "simulate" })),
      ]);
      setRuntimeLogs(logsData.entries || []);
      setInstances(instData.instances || []);
    } catch {}
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await Promise.all([loadBuilds(projectId), loadRuntime(projectId)]);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load data",
          );
        }
      }
    })();
    const cancelInterval = setVisibilityInterval(() => {
      if (logsTab === "runtime") {
        loadRuntime(projectId).catch(() => undefined);
      } else {
        loadBuilds(projectId).catch(() => undefined);
        if (currentOrg && selected) {
          api.getBuildLogs(currentOrg.id, projectId, selected)
            .then((res) => {
              if (res && res.logs) setLogs(res.logs);
            })
            .catch(() => undefined);
        }
      }
    }, 2500);
    return () => {
      mounted = false;
      cancelInterval();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId, selected, logsTab]);

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

  async function onTriggerBuild() {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const repos = await api.listRepos(currentOrg.id, projectId);
      const repo = repos[0];
      await api.createDeployment(currentOrg.id, projectId, {
        git_branch: repo?.default_branch || "main",
        git_sha: "HEAD",
        clone_url: repo?.clone_url,
        full_name: repo?.full_name,
        repo_id: repo?.id,
        message: "Triggered from builds console",
      });
      setSuccess("New build queued. Compilation starting momentarily.");
      await loadBuilds(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to trigger build");
    } finally {
      setBusy(false);
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
        description="View real-time build pipeline execution, compiler outputs, and Docker container packaging."
        actions={
          <div className="flex items-center gap-2">
            <Link
              href="/git"
              className="inline-flex items-center justify-center rounded-md border border-[var(--border)] bg-white px-3 py-1.5 text-xs font-bold text-[var(--ink)] hover:bg-slate-50 transition-colors shadow-2xs"
            >
              Git Settings
            </Link>
            <Button
              type="button"
              disabled={busy || !projectId}
              onClick={onTriggerBuild}
              className="font-bold"
            >
              {busy ? "Queuing..." : "⚡ Trigger Build"}
            </Button>
          </div>
        }
      />

      {error ? (
        <div className="mb-4">
          <Alert tone="error">{error}</Alert>
        </div>
      ) : null}

      {success ? (
        <div className="mb-4">
          <Alert tone="success">{success}</Alert>
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

        {/* Segmented Log View Selector (Defaults to Container Logs) */}
        <div className="flex items-center rounded-lg border border-[var(--border)] bg-white p-1 shadow-2xs text-xs">
          <button
            type="button"
            onClick={() => setLogsTab("runtime")}
            className={`px-3 py-1.5 font-bold rounded-md transition-all ${
              logsTab === "runtime"
                ? "bg-[var(--accent)] text-white shadow-2xs"
                : "text-[var(--ink-secondary)] hover:text-[var(--ink)]"
            }`}
          >
            🟢 Running App Container Logs
          </button>
          <button
            type="button"
            onClick={() => setLogsTab("builds")}
            className={`px-3 py-1.5 font-bold rounded-md transition-all ${
              logsTab === "builds"
                ? "bg-[var(--accent)] text-white shadow-2xs"
                : "text-[var(--ink-secondary)] hover:text-[var(--ink)]"
            }`}
          >
            🏗️ Compiler & Build Stream
          </button>
        </div>
      </div>

      {/* Tab 1: Live Container Logs (Default) */}
      {logsTab === "runtime" && (
        <div className="space-y-4">
          {/* Active Container Summary */}
          {instances.length > 0 && (
            <div className="jp-card p-4 border border-emerald-500/30 bg-gradient-to-r from-emerald-50/60 to-white">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2.5">
                  <span className="text-xl">🐳</span>
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs font-bold text-slate-900">
                        {instances[0].container_name || instances[0].id.slice(0, 12)}
                      </span>
                      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 text-emerald-800 border border-emerald-300 px-2 py-0.5 text-[10px] font-bold uppercase">
                        <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                        {instances[0].status || "running"}
                      </span>
                      {instances[0].port && (
                        <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px] text-slate-700">
                          Port :{instances[0].port}
                        </span>
                      )}
                    </div>
                    <p className="font-mono text-[11px] text-slate-500 truncate max-w-md mt-0.5">
                      Image: {instances[0].image_ref}
                    </p>
                  </div>
                </div>

                <button
                  type="button"
                  onClick={() => {
                    const text = runtimeLogs.map((e) => `[${e.logged_at}] [${e.source || "app"}] ${e.message}`).join("\n");
                    navigator.clipboard.writeText(text);
                    setCopied(true);
                    setTimeout(() => setCopied(false), 2000);
                  }}
                  disabled={runtimeLogs.length === 0}
                  className="text-xs font-semibold px-3 py-1.5 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-slate-800 shadow-2xs transition-colors"
                >
                  {copied ? "✓ Copied Logs" : "📋 Copy All Logs"}
                </button>
              </div>
            </div>
          )}

          <div className="jp-card overflow-hidden flex flex-col">
            <div className="border-b border-slate-700 bg-slate-900 px-4 py-3 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-emerald-400 animate-ping" />
                <span className="font-mono text-xs font-bold text-slate-200">
                  Container Standard Output & Error (stdout/stderr)
                </span>
              </div>
              <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-emerald-400">
                Live Auto-Refresh Active
              </span>
            </div>
            <pre className="h-[460px] overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400/90 whitespace-pre-wrap leading-relaxed shadow-inner">
              {runtimeLogs.length > 0
                ? runtimeLogs.map((e) => `[${e.logged_at || new Date().toISOString()}] [${(e.source || "app").toUpperCase()}] ${e.message}`).join("\n")
                : "// No container logs received yet. If the application just started, output will appear here momentarily."}
            </pre>
          </div>
        </div>
      )}

      {/* Tab 2: Build Pipeline & Compiler Stream */}
      {logsTab === "builds" && (
        <div>
          {builds.length === 0 ? (
            <EmptyState
              title="No builds recorded"
              description="Connect a repository in Git Settings or click Trigger Build to start a build job."
            />
          ) : (
            <div className="grid gap-6 lg:grid-cols-2">
              <div className="jp-card overflow-hidden">
                <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
                  <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    Build Queue ({builds.length})
                  </h4>
                </div>
                <ul className="divide-y divide-[var(--border)] max-h-[500px] overflow-y-auto">
                  {builds.map((b) => {
                    const isSuccess = b.status === "succeeded" || b.status === "ready";
                    const isFailed = b.status === "failed";
                    const isRunning = b.status === "running" || b.status === "building";
                    return (
                      <li
                        key={b.id}
                        className={`flex items-center justify-between gap-3 p-3.5 transition-colors cursor-pointer ${
                          selected === b.id ? "bg-blue-50/70" : "hover:bg-slate-50/50"
                        }`}
                        onClick={() => showLogs(b.id)}
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
                            onClick={(e) => {
                              e.stopPropagation();
                              showLogs(b.id);
                            }}
                          >
                            Logs
                          </Button>
                          {b.status === "failed" ? (
                            <Button
                              type="button"
                              size="sm"
                              variant="secondary"
                              disabled={busy}
                              onClick={(e) => {
                                e.stopPropagation();
                                onExplain(b.id);
                              }}
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
                  <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-emerald-400">
                    {selected ? `Build ${selected.slice(0, 8)}` : "Live Stream"}
                  </span>
                </div>
                <pre className="h-[460px] overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400/90 whitespace-pre-wrap leading-relaxed shadow-inner">
                  {logs || "// Select a build from the queue to stream real-time compilation logs."}
                </pre>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
