"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { LogEntry, Project } from "@/lib/types";

export default function LogsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [source, setSource] = useState("");
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [buildLogs, setBuildLogs] = useState("");
  const [backend, setBackend] = useState("");
  const [message, setMessage] = useState("");
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
    const data = await api.queryLogs(currentOrg.id, pid, {
      source: source || undefined,
      limit: 100,
    });
    setEntries(data.entries || []);
    setBuildLogs(data.build_logs || "");
    setBackend(data.backend || "postgres");
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadLogs(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load logs");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId, source]);

  async function onIngest() {
    if (!currentOrg || !projectId || !message.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.ingestLog(currentOrg.id, projectId, {
        source: source || "runtime",
        level: "info",
        message: message.trim(),
      });
      setMessage("");
      await loadLogs(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Ingest failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState title="Select an organization" description="Logs require an active organization." />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Logs"
        description={`Build and runtime log query (${backend || "postgres"}). Loki is optional via monitoring profile.`}
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
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Source</span>
          <select
            className="rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2"
            value={source}
            onChange={(e) => setSource(e.target.value)}
          >
            <option value="">all</option>
            <option value="runtime">runtime</option>
            <option value="build">build</option>
            <option value="app">app</option>
          </select>
        </label>
        <Button variant="ghost" onClick={() => loadLogs(projectId)} disabled={busy}>
          Refresh
        </Button>
      </div>

      <div className="mb-8 flex flex-wrap items-end gap-3">
        <Input
          placeholder="Ingest a test log line"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          className="max-w-md"
        />
        <Button onClick={onIngest} disabled={busy || !message.trim()}>
          Ingest
        </Button>
      </div>

      {buildLogs ? (
        <pre className="mb-6 max-h-48 overflow-auto rounded-md border border-[var(--border)] bg-[var(--surface)] p-3 text-xs">
          {buildLogs}
        </pre>
      ) : null}

      {entries.length === 0 ? (
        <EmptyState title="No entries" description="Ingest a line or wait for build/runtime logs." />
      ) : (
        <ul className="space-y-2 font-mono text-xs">
          {entries.map((e) => (
            <li key={e.id} className="border-b border-[var(--border)] pb-2">
              <span className="text-[var(--ink-faint)]">{e.logged_at}</span>{" "}
              <span className="text-[var(--ink-muted)]">[{e.source}/{e.level}]</span> {e.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
