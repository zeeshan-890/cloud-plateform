"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";
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
  const { rootDomain } = useProject();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [configHash, setConfigHash] = useState("");
  const [drift, setDrift] = useState<boolean | null>(null);
  const [explainText, setExplainText] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // Build Logs modal state
  const [selectedDeployLogs, setSelectedDeployLogs] = useState<{
    deploymentId: string;
    buildId?: string;
    logs: string;
    loading: boolean;
  } | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  function copyToClipboard(text: string, id: string) {
    if (!text) return;
    navigator.clipboard.writeText(text);
    setCopiedKey(id);
    setTimeout(() => {
      setCopiedKey((curr) => (curr === id ? null : curr));
    }, 2000);
  }

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
      const dep = await api.createDeployment(currentOrg.id, projectId, {
        git_branch: repo?.default_branch || "main",
        git_sha: "HEAD",
        clone_url: repo?.clone_url,
        full_name: repo?.full_name,
        repo_id: repo?.id,
        message: repo ? `Deploy from ${repo.full_name}@${repo.default_branch || "main"}` : "Manual deployment",
      });
      await loadDeploys(projectId);
      if (dep.build_id) {
        handleViewLogs(dep);
      }
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
        prompt: "Explain why this deployment failed and recommend a fix.",
      });
      setExplainText(`(${res.mode}) ${res.explanation}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Explain failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleViewLogs(d: Deployment) {
    if (!currentOrg || !projectId) return;
    setSelectedDeployLogs({
      deploymentId: d.id,
      buildId: d.build_id,
      logs: "Loading compiler output...",
      loading: true,
    });
    if (d.build_id) {
      try {
        const res = await api.getBuildLogs(currentOrg.id, projectId, d.build_id);
        setSelectedDeployLogs({
          deploymentId: d.id,
          buildId: d.build_id,
          logs: res.logs || "// No logs recorded for this build.",
          loading: false,
        });
      } catch {
        setSelectedDeployLogs({
          deploymentId: d.id,
          buildId: d.build_id,
          logs: "// Failed to fetch build logs.",
          loading: false,
        });
      }
    } else {
      setSelectedDeployLogs({
        deploymentId: d.id,
        logs: "// No build ID associated with this deployment.",
        loading: false,
      });
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

  const currentProject = projects.find((p) => p.id === projectId);
  const currentAppDomain = currentProject
    ? `https://${(currentProject.name || currentProject.id).toLowerCase().replace(/[^a-z0-9-]/g, "-")}.${rootDomain}`
    : `https://${rootDomain}`;

  return (
    <div className="animate-fade-up space-y-6">
      <PageHeader
        title="Deployments"
        description="All deployment runs, compiler logs, and public application domain addresses."
        actions={
          <div className="flex flex-wrap gap-2">
            <Link
              href="/git"
              className="inline-flex items-center justify-center rounded-md border border-[var(--border)] bg-white px-3 py-1.5 text-xs font-bold text-[var(--ink)] hover:bg-slate-50 transition-colors shadow-2xs"
            >
              Git Settings
            </Link>
            <Button
              type="button"
              onClick={onDeploy}
              disabled={busy || !projectId}
              className="font-bold shadow-xs"
            >
              {busy ? "Deploying…" : "⚡ Deploy Now"}
            </Button>
            <Button
              type="button"
              variant="secondary"
              onClick={() => onRollback()}
              disabled={busy || !projectId}
            >
              Rollback
            </Button>
          </div>
        }
      />

      {error ? <Alert tone="error">{error}</Alert> : null}

      {explainText ? (
        <div className="jp-card p-4 border-amber-300 bg-amber-50/70 space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs font-bold uppercase tracking-wider text-amber-900 flex items-center gap-1.5">
              <span>🤖</span> AI Deployment Issue Diagnosis
            </span>
            <button
              type="button"
              onClick={() => setExplainText("")}
              className="text-amber-800 hover:text-amber-950 font-bold text-xs"
            >
              ✕ Close
            </button>
          </div>
          <pre className="text-xs font-mono text-amber-950 whitespace-pre-wrap leading-relaxed bg-white/80 p-3 rounded-lg border border-amber-200">
            {explainText}
          </pre>
        </div>
      ) : null}

      {/* Project Selector & Config Bar */}
      <div className="jp-card p-4 flex flex-wrap items-center justify-between gap-4">
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
          <div className="flex items-center gap-1 text-[var(--ink-muted)]">
            <span className="font-bold">Public Domain:</span>
            <a
              href={currentAppDomain}
              target="_blank"
              rel="noreferrer"
              className="font-mono font-bold text-[var(--accent)] hover:underline"
            >
              {currentAppDomain} ↗
            </a>
          </div>

          {(configHash || drift !== null) && (
            <div className="flex items-center gap-2 text-xs text-[var(--ink-muted)] border-l border-[var(--border)] pl-3">
              <span>Config: <code className="font-mono text-[var(--ink)] font-bold">{configHash ? configHash.slice(0, 8) : "none"}</code></span>
              {drift !== null && (
                <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase ${drift ? "bg-amber-100 text-amber-800" : "bg-emerald-100 text-emerald-800"}`}>
                  {drift ? "Drift Detected" : "In Sync"}
                </span>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Deployment List */}
      {deployments.length === 0 ? (
        <EmptyState
          title="No deployments yet"
          description="Connect a repository or click 'Deploy Now' to trigger a build for this project."
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Deployment History ({deployments.length})
              </h4>
              <span className="flex items-center gap-1 text-[11px] font-semibold text-emerald-700 bg-emerald-100/60 px-2 py-0.5 rounded-full">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                Live
              </span>
            </div>
            <button
              type="button"
              onClick={() => loadDeploys(projectId)}
              className="text-xs text-[var(--accent)] font-bold hover:underline"
            >
              ↻ Refresh
            </button>
          </div>

          <div className="divide-y divide-[var(--border)]">
            {deployments.map((d) => {
              const isReady = d.status === "ready";
              const isFailed = d.status === "failed";
              const isBuilding = d.status === "building" || d.status === "deploying";
              const targetUrl = d.preview_url || currentAppDomain;

              return (
                <div key={d.id} className="p-4 hover:bg-slate-50/50 transition-colors space-y-3">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    {/* Status & Badges */}
                    <div className="flex items-center gap-2.5">
                      <span
                        className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-bold ${
                          isReady
                            ? "border-emerald-300 bg-emerald-50 text-emerald-800"
                            : isFailed
                            ? "border-red-300 bg-red-50 text-red-800"
                            : isBuilding
                            ? "border-blue-300 bg-blue-50 text-blue-800"
                            : "border-slate-300 bg-slate-100 text-slate-800"
                        }`}
                      >
                        <span
                          className={`h-1.5 w-1.5 rounded-full ${
                            isReady
                              ? "bg-emerald-600"
                              : isFailed
                              ? "bg-red-600"
                              : isBuilding
                              ? "bg-blue-600 animate-pulse"
                              : "bg-slate-500"
                          }`}
                        />
                        {d.status.toUpperCase()}
                      </span>

                      {isPreviewDeploy(d) ? (
                        <span className="rounded bg-indigo-50 border border-indigo-200 px-1.5 py-0.5 text-[10px] font-bold uppercase text-indigo-700">
                          Preview
                        </span>
                      ) : (
                        <span className="rounded bg-slate-100 border border-slate-200 px-1.5 py-0.5 text-[10px] font-bold uppercase text-slate-700">
                          Production
                        </span>
                      )}

                      <span className="text-xs text-[var(--ink-muted)]">
                        {d.created_at ? new Date(d.created_at).toLocaleString() : "—"}
                      </span>
                    </div>

                    {/* Actions */}
                    <div className="flex items-center gap-2">
                      <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        onClick={() => handleViewLogs(d)}
                        className="text-xs font-semibold"
                      >
                        📋 View Build Logs
                      </Button>

                      {isFailed && (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          disabled={busy}
                          onClick={() => onExplain(d)}
                          className="text-xs font-semibold text-amber-700 border-amber-300 hover:bg-amber-50"
                        >
                          🤖 Explain Issue
                        </Button>
                      )}

                      {isReady && d.image_ref && (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          disabled={busy}
                          onClick={() => onRollback(d.id)}
                          className="text-xs font-semibold"
                        >
                          Rollback
                        </Button>
                      )}
                    </div>
                  </div>

                  {/* Public HTTPS Address & Git Bar */}
                  <div className="grid gap-2 sm:grid-cols-2 text-xs">
                    <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5 flex items-center justify-between">
                      <div className="flex items-center gap-2 overflow-hidden">
                        <span className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)] shrink-0">
                          App URL:
                        </span>
                        {isReady ? (
                          <a
                            href={targetUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="font-mono font-bold text-[var(--accent)] hover:underline truncate"
                          >
                            {targetUrl} ↗
                          </a>
                        ) : (
                          <span className="font-mono text-slate-500 truncate">
                            {isBuilding ? `Deploying to ${targetUrl}` : `Offline (${d.status})`}
                          </span>
                        )}
                      </div>
                      {isReady && (
                        <button
                          type="button"
                          onClick={() => copyToClipboard(targetUrl, `url-${d.id}`)}
                          className="text-[10px] text-[var(--accent)] font-bold hover:underline shrink-0 ml-2"
                        >
                          {copiedKey === `url-${d.id}` ? "✓ Copied" : "Copy"}
                        </button>
                      )}
                    </div>

                    <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5 flex items-center justify-between">
                      <div className="flex items-center gap-2 overflow-hidden">
                        <span className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)] shrink-0">
                          Git:
                        </span>
                        <span className="font-mono font-bold text-[var(--ink)] shrink-0">
                          {d.git_branch || "main"}
                        </span>
                        {d.git_sha && (
                          <code className="text-[10px] font-mono text-[var(--ink-muted)] shrink-0">
                            @{d.git_sha.slice(0, 7)}
                          </code>
                        )}
                        <span className="text-slate-500 truncate text-[11px]">
                          {d.message || "Manual deployment"}
                        </span>
                      </div>
                    </div>
                  </div>

                  {/* Issue Box (if failed) */}
                  {isFailed && (
                    <div className="rounded-lg border border-red-200 bg-red-50/70 p-3 text-xs text-red-900 flex items-start justify-between gap-3">
                      <div className="flex items-start gap-2">
                        <span className="text-base leading-none">⚠️</span>
                        <div>
                          <strong className="font-bold text-red-950 block">Deployment Issue:</strong>
                          <p className="font-mono text-[11px] text-red-800 mt-0.5 break-all">
                            {d.error || "Compilation error or container crashed during rollout."}
                          </p>
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => handleViewLogs(d)}
                        className="shrink-0 text-xs font-bold text-red-700 hover:text-red-950 underline"
                      >
                        Inspect Build Logs →
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Build Logs Modal */}
      {selectedDeployLogs && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs">
          <div className="w-full max-w-3xl rounded-2xl border border-[var(--border)] bg-slate-950 shadow-2xl overflow-hidden flex flex-col max-h-[85vh]">
            <div className="border-b border-slate-800 bg-slate-900 px-6 py-4 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-emerald-400" />
                <h3 className="text-sm font-bold text-white font-mono flex items-center gap-2">
                  <span>Build Logs for Deployment</span>
                  <code className="text-xs text-emerald-400 font-mono">
                    {selectedDeployLogs.deploymentId.slice(0, 8)}
                  </code>
                </h3>
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => copyToClipboard(selectedDeployLogs.logs, "modal-logs")}
                  className="text-xs text-slate-300 hover:text-white font-bold bg-slate-800 px-2.5 py-1 rounded"
                >
                  {copiedKey === "modal-logs" ? "✓ Copied" : "📋 Copy"}
                </button>
                <button
                  type="button"
                  onClick={() => setSelectedDeployLogs(null)}
                  className="text-slate-400 hover:text-white text-lg font-bold p-1 ml-2"
                >
                  ✕
                </button>
              </div>
            </div>

            <div className="p-4 flex-1 overflow-auto font-mono text-xs text-emerald-400 leading-relaxed whitespace-pre-wrap">
              {selectedDeployLogs.logs}
            </div>

            <div className="border-t border-slate-800 bg-slate-900 px-6 py-3 flex items-center justify-between text-xs text-slate-400">
              <span>Status: {selectedDeployLogs.loading ? "Streaming compiler logs..." : "Logs loaded"}</span>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setSelectedDeployLogs(null)}
              >
                Close
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
