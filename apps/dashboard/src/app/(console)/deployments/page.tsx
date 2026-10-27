"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { setVisibilityInterval } from "@/lib/visibility";
import type { Deployment, Project } from "@/lib/types";

function isPreviewDeploy(d: Deployment): boolean {
  const branch = (d.git_branch || "").toLowerCase();
  const message = (d.message || "").toLowerCase();
  const source = (d.source || "").toLowerCase();
  return (
    source.includes("preview") ||
    branch.startsWith("preview/") ||
    branch.includes("preview") ||
    message.includes("[preview]")
  );
}

export default function DeploymentsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [configHash, setConfigHash] = useState("");
  const [drift, setDrift] = useState<boolean | null>(null);
  const [explainText, setExplainText] = useState("");
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

  async function loadDeploys(pid: string) {
    if (!currentOrg || !pid) return;
    const list = await api.listDeployments(currentOrg.id, pid);
    setDeployments(list);
    try {
      const [cfg, d] = await Promise.all([
        api.getProjectConfig(currentOrg.id, pid),
        api.getConfigDrift(currentOrg.id, pid),
      ]);
      setConfigHash(cfg.hash || "");
      setDrift(Boolean(d.drift));
    } catch {
      setConfigHash("");
      setDrift(null);
    }
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadDeploys(projectId);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load deployments",
          );
        }
      }
    })();
    const cancelInterval = setVisibilityInterval(() => {
      loadDeploys(projectId).catch(() => undefined);
    }, 4000);
    return () => {
      mounted = false;
      cancelInterval();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onDeploy() {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      const repos = await api.listRepos(currentOrg.id, projectId);
      const repo = repos[0];
      await api.createDeployment(currentOrg.id, projectId, {
        git_branch: repo?.default_branch || "main",
        git_sha: "HEAD",
        clone_url: repo?.clone_url,
        full_name: repo?.full_name,
        repo_id: repo?.id,
        message: "manual deploy from dashboard",
      });
      await loadDeploys(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Deploy failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRollback(id?: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.rollbackDeployment(currentOrg.id, projectId, id);
      await loadDeploys(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Rollback failed");
    } finally {
      setBusy(false);
    }
  }

  async function onExplain(d: Deployment) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setExplainText("");
    try {
      const res = await api.explainFailure(currentOrg.id, projectId, {
        deployment_id: d.id,
        build_id: d.build_id,
        prompt: "Explain why this deployment failed.",
      });
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
        description="Deployments require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Deployments"
        description="Create deploys, watch status, and roll back to a previous ready image."
        actions={
          <div className="flex flex-wrap gap-2">
            <Button type="button" onClick={onDeploy} disabled={busy || !projectId}>
              {busy ? "Working…" : "Deploy"}
            </Button>
            <Button
              type="button"
              variant="secondary"
              onClick={() => onRollback()}
              disabled={busy || !projectId}
            >
              Rollback latest
            </Button>
          </div>
        }
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

        {(configHash || drift !== null) && (
          <div className="flex items-center gap-2 text-xs text-[var(--ink-muted)]">
            <span>Config SHA: <code className="font-mono text-[var(--ink)] font-bold">{configHash ? configHash.slice(0, 8) : "none"}</code></span>
            {drift !== null && (
              <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase ${drift ? "bg-amber-100 text-amber-800" : "bg-emerald-100 text-emerald-800"}`}>
                {drift ? "Drift Detected" : "In Sync"}
              </span>
            )}
          </div>
        )}
      </div>

      {deployments.length === 0 ? (
        <EmptyState
          title="No deployments yet"
          description="Connect a repo or click Deploy to trigger a build for this project."
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
            <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Deployment History ({deployments.length})
            </h4>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[700px] text-left text-sm">
              <thead>
                <tr className="border-b border-[var(--border)] bg-slate-50/70 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                  <th className="px-4 py-3">Status</th>
                  <th className="px-4 py-3">Kind</th>
                  <th className="px-4 py-3">URL / Target</th>
                  <th className="px-4 py-3">Branch & Commit</th>
                  <th className="px-4 py-3">Date</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)]">
                {deployments.map((d) => {
                  const isReady = d.status === "ready";
                  const isFailed = d.status === "failed";
                  const isBuilding = d.status === "building" || d.status === "deploying";
                  return (
                    <tr key={d.id} className="hover:bg-slate-50/50 transition-colors">
                      <td className="px-4 py-3">
                        <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-bold ${
                          isReady
                            ? "border-emerald-300 bg-emerald-50 text-emerald-800"
                            : isFailed
                            ? "border-red-300 bg-red-50 text-red-800"
                            : isBuilding
                            ? "border-blue-300 bg-blue-50 text-blue-800"
                            : "border-slate-300 bg-slate-100 text-slate-800"
                        }`}>
                          <span className={`h-1.5 w-1.5 rounded-full ${
                            isReady ? "bg-emerald-600" : isFailed ? "bg-red-600" : isBuilding ? "bg-blue-600 animate-pulse" : "bg-slate-500"
                          }`} />
                          {d.status}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        {isPreviewDeploy(d) ? (
                          <span className="rounded bg-indigo-50 border border-indigo-200 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-indigo-700">
                            Preview
                          </span>
                        ) : (
                          <span className="rounded bg-slate-100 border border-slate-200 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-slate-700">
                            Prod
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">
                        {isPreviewDeploy(d) && d.preview_url ? (
                          <a
                            href={d.preview_url}
                            target="_blank"
                            rel="noreferrer"
                            className="text-[var(--accent)] font-semibold underline-offset-2 hover:underline"
                          >
                            {d.preview_url.replace(/^https?:\/\//, "")} ↗
                          </a>
                        ) : (
                          <span className="text-[var(--ink-muted)]">{d.strategy || "rolling"} &bull; {d.source}</span>
                        )}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs text-[var(--ink-secondary)]">
                        <span className="font-bold text-[var(--ink)]">{d.git_branch || "main"}</span>
                        {d.git_sha && <span className="text-[var(--ink-muted)] ml-1.5">@{(d.git_sha).slice(0, 7)}</span>}
                      </td>
                      <td className="px-4 py-3 text-xs text-[var(--ink-muted)]">
                        {d.created_at ? new Date(d.created_at).toLocaleString() : "—"}
                      </td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex justify-end gap-2">
                          {d.status === "failed" ? (
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={busy}
                              onClick={() => onExplain(d)}
                            >
                              Explain Error
                            </Button>
                          ) : null}
                          {d.status === "ready" && d.image_ref ? (
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={busy}
                              onClick={() => onRollback(d.id)}
                            >
                              Rollback
                            </Button>
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
