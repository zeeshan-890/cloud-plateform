"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { MetricSummary, Project } from "@/lib/types";

export default function MetricsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [metrics, setMetrics] = useState<MetricSummary[]>([]);
  const [mode, setMode] = useState("simulate");
  const [promURL, setPromURL] = useState("");
  const [error, setError] = useState("");

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
    const data = await api.projectMetrics(currentOrg.id, pid);
    setMetrics(data.metrics);
    setMode(data.mode);
    setPromURL(data.prometheus_url || "");
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await load(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load metrics");
        }
      }
    })();
    const t = setInterval(() => {
      load(projectId).catch(() => {});
    }, 10000);
    return () => {
      mounted = false;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Metrics require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Metrics"
        description={`Project series (${mode}). Prometheus/Grafana optional under --profile monitoring.`}
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
        <Button variant="ghost" onClick={() => load(projectId)}>
          Refresh
        </Button>
      </div>

      {promURL ? (
        <p className="mb-4 text-sm text-[var(--ink-muted)]">
          Prometheus: <span className="font-mono">{promURL}</span> (host: http://localhost:9090 when
          monitoring profile is up)
        </p>
      ) : null}

      {metrics.length === 0 ? (
        <EmptyState title="No metrics" description="Simulate mode seeds sample series on refresh." />
      ) : (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-[var(--ink-muted)]">
              <th className="py-2 font-medium">Name</th>
              <th className="py-2 font-medium">Latest</th>
              <th className="py-2 font-medium">Samples</th>
            </tr>
          </thead>
          <tbody>
            {metrics.map((m) => (
              <tr key={m.name} className="border-b border-[var(--border)]">
                <td className="py-2 font-mono">{m.name}</td>
                <td className="py-2">{m.latest}</td>
                <td className="py-2">{m.count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
