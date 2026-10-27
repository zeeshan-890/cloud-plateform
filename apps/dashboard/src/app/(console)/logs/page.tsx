"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";
import { setVisibilityInterval } from "@/lib/visibility";
import type { LogEntry, Project, RuntimeInstance } from "@/lib/types";

export default function LogsPage() {
  const { currentOrg } = useOrg();
  const { rootDomain } = useProject();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [source, setSource] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [instances, setInstances] = useState<RuntimeInstance[]>([]);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [copied, setCopied] = useState(false);
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

  async function loadLogs(pid: string) {
    if (!currentOrg || !pid) return;
    try {
      const [logsData, instData] = await Promise.all([
        api.queryLogs(currentOrg.id, pid, {
          source: source || undefined,
          q: searchQuery || undefined,
          limit: 150,
        }),
        api.listRuntimeInstances(currentOrg.id, pid).catch(() => ({ instances: [], mode: "simulate" })),
      ]);
      setEntries(logsData.entries || []);
      setInstances(instData.instances || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load logs");
    }
  }

  useEffect(() => {
    if (!projectId) return;
    loadLogs(projectId);

    const cancelInterval = setVisibilityInterval(() => {
      if (autoRefresh) {
        loadLogs(projectId).catch(() => undefined);
      }
    }, 3000);

    return () => {
      cancelInterval();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId, source, searchQuery, autoRefresh]);

  function copyAllLogs() {
    const text = entries
      .map((e) => `[${e.logged_at}] [${e.source}/${e.level}] ${e.message}`)
      .join("\n");
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Logs require an active organization."
      />
    );
  }

  const currentProject = projects.find((p) => p.id === projectId);
  const currentAppDomain = currentProject
    ? `https://${(currentProject.name || currentProject.id).toLowerCase().replace(/[^a-z0-9-]/g, "-")}.${rootDomain}`
    : `https://${rootDomain}`;

  return (
    <div className="animate-fade-up space-y-6">
      <PageHeader
        title="Application & Runtime Logs"
        description="Real-time log stream from running container instances and build execution."
        actions={
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={copyAllLogs}
              disabled={entries.length === 0}
              className="text-xs font-semibold px-3 py-1.5 rounded-md border border-[var(--border)] bg-white hover:bg-slate-50 text-[var(--ink)] shadow-2xs transition-colors"
            >
              {copied ? "✓ Copied All Logs" : "📋 Copy All Logs"}
            </button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => loadLogs(projectId)}
              disabled={busy}
              className="font-bold text-xs"
            >
              ↻ Refresh
            </Button>
          </div>
        }
      />

      {error ? <Alert tone="error">{error}</Alert> : null}

      {/* Project Selector & Runtime Status Banner */}
      <div className="jp-card p-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-4">
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

          <div className="flex items-center gap-3 text-xs">
            <div className="flex items-center gap-1.5">
              <span className="font-bold text-[var(--ink-muted)]">Container:</span>
              {instances.length > 0 ? (
                <span className="rounded bg-emerald-100 text-emerald-800 font-bold px-2 py-0.5 text-[10px] uppercase">
                  {instances[0].status || "Running"} ({instances[0].container_name || instances[0].id.slice(0, 10)})
                </span>
              ) : (
                <span className="rounded bg-slate-100 text-slate-700 font-bold px-2 py-0.5 text-[10px] uppercase">
                  Standby
                </span>
              )}
            </div>

            <div className="flex items-center gap-1 text-[var(--ink-muted)] border-l border-[var(--border)] pl-3">
              <span className="font-bold">App:</span>
              <a
                href={currentAppDomain}
                target="_blank"
                rel="noreferrer"
                className="font-mono font-bold text-[var(--accent)] hover:underline"
              >
                {currentAppDomain} ↗
              </a>
            </div>
          </div>
        </div>

        {/* Filter Bar */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] pt-3">
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-1.5 text-xs text-[var(--ink-secondary)] font-semibold">
              <span>Source:</span>
              <select
                className="rounded-md border border-[var(--border)] bg-white px-2.5 py-1 text-xs font-bold text-[var(--ink)] shadow-2xs"
                value={source}
                onChange={(e) => setSource(e.target.value)}
              >
                <option value="">All Sources</option>
                <option value="runtime">Runtime Engine</option>
                <option value="app">Application</option>
                <option value="build">Build Logs</option>
              </select>
            </label>

            <Input
              placeholder="Search in log lines..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="w-56 text-xs h-8"
            />
          </div>

          <label className="flex items-center gap-1.5 text-xs text-[var(--ink-secondary)] font-semibold cursor-pointer">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              className="rounded border-[var(--border)]"
            />
            Live Streaming {autoRefresh ? "(3s active)" : "(paused)"}
          </label>
        </div>
      </div>

      {/* Terminal Logs Output */}
      <div className="jp-card overflow-hidden">
        <div className="border-b border-slate-800 bg-slate-900 px-4 py-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-emerald-400 animate-pulse" />
            <span className="font-mono text-xs font-bold text-slate-200">
              Live Container Terminal Stream
            </span>
          </div>
          <span className="font-mono text-[10px] text-slate-400">
            {entries.length} log lines
          </span>
        </div>

        {entries.length === 0 ? (
          <div className="h-80 flex flex-col items-center justify-center bg-slate-950 p-6 text-center text-xs text-slate-500 font-mono">
            <p className="mb-2">No log lines found for the current query.</p>
            <p className="text-[11px] text-slate-600">
              Trigger a build or make requests to your live application URL to see output streamed here.
            </p>
          </div>
        ) : (
          <div className="h-[460px] overflow-auto bg-slate-950 p-4 font-mono text-xs text-slate-200 leading-relaxed space-y-1">
            {entries.map((e) => {
              const isErr = e.level === "error" || e.message.toLowerCase().includes("error");
              const isWarn = e.level === "warn" || e.message.toLowerCase().includes("warn");
              return (
                <div key={e.id} className="flex items-start gap-2 hover:bg-slate-900/60 py-0.5 rounded px-1">
                  <span className="text-slate-500 shrink-0 select-none text-[10px]">
                    {e.logged_at ? new Date(e.logged_at).toLocaleTimeString() : "--:--:--"}
                  </span>
                  <span
                    className={`rounded px-1 text-[10px] uppercase font-bold shrink-0 ${
                      isErr
                        ? "bg-red-950 text-red-400 border border-red-800/50"
                        : isWarn
                        ? "bg-amber-950 text-amber-400 border border-amber-800/50"
                        : "bg-slate-800 text-slate-300"
                    }`}
                  >
                    {e.source}/{e.level}
                  </span>
                  <span className={`break-all ${isErr ? "text-red-300 font-semibold" : isWarn ? "text-amber-300" : "text-emerald-400"}`}>
                    {e.message}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
