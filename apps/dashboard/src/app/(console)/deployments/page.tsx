"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Deployment, Project } from "@/lib/types";

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
    const t = setInterval(() => {
      loadDeploys(projectId).catch(() => undefined);
    }, 4000);
    return () => {
      mounted = false;
      clearInterval(t);
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

      {(configHash || drift !== null) && (
        <p className="mb-4 text-sm text-[var(--ink-muted)]">
          jp.yaml last-applied hash:{" "}
          <span className="font-[family-name:var(--font-mono)] text-xs">
            {configHash ? configHash.slice(0, 12) : "none"}
          </span>
          {drift !== null ? (
            <span className="ml-3">
              Drift stub: {drift ? "possible drift" : "in sync"}
            </span>
          ) : null}
        </p>
      )}

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

      {deployments.length === 0 ? (
        <EmptyState
          title="No deployments yet"
          description="Connect a repo, then click Deploy to queue a build."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
                <th className="py-2 pr-3 font-medium">Status</th>
                <th className="py-2 pr-3 font-medium">Strategy</th>
                <th className="py-2 pr-3 font-medium">Source</th>
                <th className="py-2 pr-3 font-medium">SHA</th>
                <th className="py-2 pr-3 font-medium">Image</th>
                <th className="py-2 pr-3 font-medium">Created</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {deployments.map((d) => (
                <tr key={d.id} className="border-b border-[var(--border)]">
                  <td className="py-3 pr-3 font-medium text-[var(--ink)]">
                    {d.status}
                  </td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">
                    {d.strategy || "rolling"}
                  </td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{d.source}</td>
                  <td className="py-3 pr-3 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {(d.git_sha || "").slice(0, 12)}
                  </td>
                  <td className="py-3 pr-3 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {d.image_ref || "—"}
                  </td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">
                    {d.created_at ? new Date(d.created_at).toLocaleString() : "—"}
                  </td>
                  <td className="py-3 text-right">
                    <div className="flex justify-end gap-2">
                      {d.status === "failed" ? (
                        <Button
                          type="button"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => onExplain(d)}
                        >
                          Explain failure
                        </Button>
                      ) : null}
                      {d.status === "ready" && d.image_ref ? (
                        <Button
                          type="button"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => onRollback(d.id)}
                        >
                          Roll back to
                        </Button>
                      ) : null}
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
