"use client";

import { FormEvent, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type {
  AvailableRepo,
  ConnectedRepo,
  GitInstallation,
  Project,
} from "@/lib/types";

export default function ConnectGitPage() {
  const { currentOrg } = useOrg();
  const searchParams = useSearchParams();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [installations, setInstallations] = useState<GitInstallation[]>([]);
  const [available, setAvailable] = useState<AvailableRepo[]>([]);
  const [connected, setConnected] = useState<ConnectedRepo[]>([]);
  
  // Direct connect form
  const [fullName, setFullName] = useState("");
  const [branch, setBranch] = useState("main");
  const [patToken, setPatToken] = useState("");
  const [filterSearch, setFilterSearch] = useState("");

  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);
  const [fetchingRepos, setFetchingRepos] = useState(false);

  // Initialize stored token if present in localStorage
  useEffect(() => {
    try {
      const stored = localStorage.getItem("jp_gh_token");
      if (stored) {
        setPatToken(stored);
      }
    } catch {}
  }, []);

  async function loadReposForOrg(token?: string) {
    if (!currentOrg) return;
    setFetchingRepos(true);
    try {
      const avail = await api.listAvailableRepos(currentOrg.id, token);
      setAvailable(avail.repos);
      if (avail.repos.length > 0) {
        setSuccess(`Loaded ${avail.repos.length} repositories from GitHub.`);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load GitHub repos");
    } finally {
      setFetchingRepos(false);
    }
  }

  async function refresh() {
    if (!currentOrg) return;
    const [inst, projs] = await Promise.all([
      api.listGitInstallations(currentOrg.id),
      api.listProjects(currentOrg.id),
    ]);
    setInstallations(inst);
    setProjects(projs);
    if (!projectId && projs[0]) setProjectId(projs[0].id);
    await loadReposForOrg(patToken || undefined);
  }

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        await refresh();
        if (searchParams.get("installed") === "1" && mounted) {
          setSuccess("GitHub App installation linked successfully.");
        }
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load Git settings",
          );
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg]);

  useEffect(() => {
    if (!currentOrg || !projectId) {
      setConnected([]);
      return;
    }
    let mounted = true;
    (async () => {
      try {
        const repos = await api.listRepos(currentOrg.id, projectId);
        if (mounted) setConnected(repos);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to list connected repos",
          );
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg, projectId]);

  function handleSaveTokenAndFetch() {
    if (!patToken.trim()) {
      setError("Please enter a GitHub Personal Access Token first.");
      return;
    }
    try {
      localStorage.setItem("jp_gh_token", patToken.trim());
    } catch {}
    setError("");
    loadReposForOrg(patToken.trim());
  }

  async function onConnectRepo(repoFullName: string, defaultBranch = "main", isPrivate = false) {
    if (!currentOrg || !projectId || !repoFullName.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.connectRepo(currentOrg.id, projectId, {
        full_name: repoFullName.trim(),
        default_branch: defaultBranch || "main",
        token: isPrivate || patToken.trim() ? patToken.trim() : undefined,
        installation_id: installations[0]?.installation_id,
      });
      setSuccess(`Connected ${repoFullName.trim()} to selected project.`);
      setFullName("");
      const repos = await api.listRepos(currentOrg.id, projectId);
      setConnected(repos);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Connect failed");
    } finally {
      setBusy(false);
    }
  }

  async function onManualSubmit(e: FormEvent) {
    e.preventDefault();
    await onConnectRepo(fullName, branch, Boolean(patToken.trim()));
  }

  async function onDisconnect(repoId: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.disconnectRepo(currentOrg.id, projectId, repoId);
      setConnected(await api.listRepos(currentOrg.id, projectId));
      setSuccess("Repository disconnected.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Disconnect failed");
    } finally {
      setBusy(false);
    }
  }

  async function onQuickDeploy(r: ConnectedRepo) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.createDeployment(currentOrg.id, projectId, {
        full_name: r.full_name,
        git_branch: r.default_branch || "main",
        clone_url: r.clone_url,
        repo_id: r.id,
        git_sha: "HEAD",
        message: `Deploy from ${r.full_name}@${r.default_branch || "main"}`,
      });
      setSuccess(`Deployment triggered for ${r.full_name}! View status in Deployments or Builds.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Deploy failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Connect Git requires an active organization."
      />
    );
  }

  const filteredRepos = available.filter((r) =>
    r.full_name.toLowerCase().includes(filterSearch.toLowerCase()),
  );

  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        title="GitHub & Git Repositories"
        description="Connect public and private GitHub repositories to enable automated container builds and deployments."
      />

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* 1. Target Project Selection & Active Connected Repositories */}
      <div className="jp-card p-5 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-3">
          <div>
            <h2 className="text-sm font-bold text-[var(--ink)]">Target Project</h2>
            <p className="text-xs text-[var(--ink-muted)]">
              Choose the project to manage connected source code and deployments.
            </p>
          </div>
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
        </div>

        <div>
          <h3 className="mb-2 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
            Connected Repositories ({connected.length})
          </h3>
          {connected.length === 0 ? (
            <div className="rounded-md border border-dashed border-[var(--border)] p-4 text-center">
              <p className="text-xs text-[var(--ink-muted)]">
                No repositories linked to this project yet. Connect a repository below to enable builds.
              </p>
            </div>
          ) : (
            <ul className="divide-y divide-[var(--border)] rounded-md border border-[var(--border)] bg-white">
              {connected.map((r) => (
                <li
                  key={r.id}
                  className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 p-3.5 text-xs"
                >
                  <div className="space-y-0.5">
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-bold text-[var(--ink)]">
                        {r.full_name}
                      </span>
                      <span className="rounded bg-slate-100 px-1.5 py-0.2 font-mono text-[10px] text-slate-700">
                        {r.default_branch || "main"}
                      </span>
                    </div>
                    <p className="font-mono text-[11px] text-[var(--ink-muted)] truncate max-w-md">
                      {r.clone_url.replace(/:[^@]+@/, ":***@")}
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <Button
                      type="button"
                      size="sm"
                      disabled={busy}
                      onClick={() => onQuickDeploy(r)}
                      className="bg-emerald-600 hover:bg-emerald-700 text-white font-bold"
                    >
                      🚀 Deploy Now
                    </Button>
                    <Button
                      type="button"
                      variant="danger"
                      size="sm"
                      disabled={busy}
                      onClick={() => onDisconnect(r.id)}
                    >
                      Disconnect
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {/* 2. Direct Repository Connect (URL or owner/repo) */}
      <div className="jp-card p-5 space-y-4">
        <div className="border-b border-[var(--border)] pb-3">
          <h2 className="text-sm font-bold text-[var(--ink)]">Connect Any Repository (Public or Private)</h2>
          <p className="text-xs text-[var(--ink-muted)]">
            Paste any GitHub repository URL or name. If the repository is private, provide your Personal Access Token.
          </p>
        </div>

        <form onSubmit={onManualSubmit} className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Repository URL or owner/repo
              </label>
              <Input
                placeholder="e.g. zeeshan-890/my-app or https://github.com/..."
                value={fullName}
                onChange={(e) => setFullName(e.target.value)}
                required
              />
            </div>
            <div>
              <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Default Branch
              </label>
              <Input
                placeholder="main"
                value={branch}
                onChange={(e) => setBranch(e.target.value)}
              />
            </div>
          </div>

          <div>
            <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Personal Access Token (Required for Private Repositories)
            </label>
            <div className="flex items-center gap-2">
              <Input
                type="password"
                placeholder="ghp_... or github_pat_..."
                value={patToken}
                onChange={(e) => setPatToken(e.target.value)}
              />
              <Button
                type="button"
                variant="secondary"
                onClick={handleSaveTokenAndFetch}
                disabled={fetchingRepos || !patToken.trim()}
              >
                {fetchingRepos ? "Loading..." : "Load Repos"}
              </Button>
            </div>
            <p className="mt-1 text-[11px] text-[var(--ink-muted)]">
              Generate a classic or fine-grained token with <code className="font-mono font-bold">repo</code> read access.
            </p>
          </div>

          <div className="pt-2">
            <Button type="submit" disabled={busy || !projectId || !fullName.trim()}>
              {busy ? "Connecting..." : "Connect Repository"}
            </Button>
          </div>
        </form>
      </div>

      {/* 3. Browse Available GitHub Repositories */}
      {available.length > 0 && (
        <div className="jp-card p-5 space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-3">
            <div>
              <h2 className="text-sm font-bold text-[var(--ink)]">Your GitHub Repositories ({available.length})</h2>
              <p className="text-xs text-[var(--ink-muted)]">Click connect to link any repository to your project in one click.</p>
            </div>
            <Input
              placeholder="Search repositories..."
              value={filterSearch}
              onChange={(e) => setFilterSearch(e.target.value)}
              className="w-56"
            />
          </div>

          <div className="max-h-80 overflow-y-auto divide-y divide-[var(--border)] rounded-md border border-[var(--border)] bg-white">
            {filteredRepos.length === 0 ? (
              <p className="p-4 text-xs text-[var(--ink-muted)] text-center">No matching repositories found.</p>
            ) : (
              filteredRepos.map((r) => {
                const isAlreadyConnected = connected.some((c) => c.full_name === r.full_name);
                return (
                  <div
                    key={r.full_name}
                    className="flex items-center justify-between p-3 text-xs hover:bg-slate-50 transition-colors"
                  >
                    <div className="flex items-center gap-2.5">
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-bold ${
                        r.private ? "bg-amber-100 text-amber-800" : "bg-blue-100 text-blue-800"
                      }`}>
                        {r.private ? "🔒 Private" : "🌐 Public"}
                      </span>
                      <span className="font-mono font-bold text-[var(--ink)]">{r.full_name}</span>
                      <span className="text-[11px] text-[var(--ink-muted)] font-mono">({r.default_branch || "main"})</span>
                    </div>

                    <div>
                      {isAlreadyConnected ? (
                        <span className="text-xs font-bold text-emerald-600">✓ Connected</span>
                      ) : (
                        <Button
                          type="button"
                          size="sm"
                          disabled={busy}
                          onClick={() => onConnectRepo(r.full_name, r.default_branch, r.private)}
                        >
                          Connect
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}
