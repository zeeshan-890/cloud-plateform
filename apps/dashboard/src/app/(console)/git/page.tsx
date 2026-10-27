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
  DeployKey,
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
  const [gitAuthMethod, setGitAuthMethod] = useState<"deploy_key" | "pat">("deploy_key");
  const [deployKey, setDeployKey] = useState<DeployKey | null>(null);
  const [loadingDeployKey, setLoadingDeployKey] = useState(false);
  const [generatingDeployKey, setGeneratingDeployKey] = useState(false);
  const [copiedDeployKey, setCopiedDeployKey] = useState(false);
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
        const [repos, key] = await Promise.all([
          api.listRepos(currentOrg.id, projectId),
          api.getDeployKey(currentOrg.id, projectId).catch(() => null),
        ]);
        if (mounted) {
          setConnected(repos);
          setDeployKey(key);
        }
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

  async function handleRotateDeployKey() {
    if (!currentOrg || !projectId) return;
    if (!window.confirm("Regenerating the deploy key will invalidate the current key. You will need to update the key on GitHub. Proceed?")) {
      return;
    }
    setGeneratingDeployKey(true);
    setError("");
    setSuccess("");
    try {
      const key = await api.generateDeployKey(currentOrg.id, projectId);
      setDeployKey(key);
      setSuccess("✓ Generated new SSH Deploy Key. Please add it to your GitHub repository Deploy keys.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to rotate deploy key");
    } finally {
      setGeneratingDeployKey(false);
    }
  }

  function handleCopyDeployKey() {
    if (!deployKey?.public_key) return;
    navigator.clipboard.writeText(deployKey.public_key);
    setCopiedDeployKey(true);
    setTimeout(() => setCopiedDeployKey(false), 2500);
  }

  async function onConnectRepo(repoFullName: string, defaultBranch = "main", isPrivate = false) {
    if (!currentOrg || !projectId || !repoFullName.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      let cleanName = repoFullName.trim();
      let cloneUrl = "";

      if (cleanName.startsWith("git@github.com:")) {
        cleanName = cleanName.replace(/^git@github\.com:/, "").replace(/\.git$/, "");
        cloneUrl = `git@github.com:${cleanName}.git`;
      } else if (cleanName.startsWith("ssh://git@github.com/")) {
        cleanName = cleanName.replace(/^ssh:\/\/git@github\.com\//, "").replace(/\.git$/, "");
        cloneUrl = `git@github.com:${cleanName}.git`;
      } else if (cleanName.startsWith("http://") || cleanName.startsWith("https://")) {
        cleanName = cleanName.replace(/^https?:\/\/github\.com\//, "").replace(/\.git$/, "");
        if (gitAuthMethod === "deploy_key") {
          cloneUrl = `git@github.com:${cleanName}.git`;
        }
      } else if (gitAuthMethod === "deploy_key") {
        cloneUrl = `git@github.com:${cleanName}.git`;
      }

      await api.connectRepo(currentOrg.id, projectId, {
        full_name: cleanName,
        clone_url: cloneUrl || undefined,
        default_branch: defaultBranch || "main",
        token: gitAuthMethod === "pat" && (isPrivate || patToken.trim()) ? patToken.trim() : undefined,
        auth_type: gitAuthMethod,
        installation_id: installations[0]?.installation_id,
      });
      setSuccess(`Connected ${cleanName} to selected project.`);
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

      {/* 2. Direct Repository Connect */}
      <div className="jp-card p-5 space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-3">
          <div>
            <h2 className="text-sm font-bold text-[var(--ink)]">Connect GitHub Repository</h2>
            <p className="text-xs text-[var(--ink-muted)]">
              Connect private or public repositories using SSH Deploy Keys (recommended) or a GitHub Personal Access Token (PAT).
            </p>
          </div>

          {/* Segmented Auth Method Toggle */}
          <div className="flex items-center rounded-lg border border-slate-300 bg-slate-100 p-1 text-xs font-bold">
            <button
              type="button"
              onClick={() => setGitAuthMethod("deploy_key")}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md transition-all ${
                gitAuthMethod === "deploy_key"
                  ? "bg-white text-slate-900 shadow-2xs border border-slate-200"
                  : "text-slate-600 hover:text-slate-900"
              }`}
            >
              <span>🔑</span>
              <span>SSH Deploy Key</span>
              <span className="rounded bg-emerald-100 px-1.5 py-0.2 text-[9px] font-bold text-emerald-800">
                Recommended
              </span>
            </button>
            <button
              type="button"
              onClick={() => setGitAuthMethod("pat")}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md transition-all ${
                gitAuthMethod === "pat"
                  ? "bg-white text-slate-900 shadow-2xs border border-slate-200"
                  : "text-slate-600 hover:text-slate-900"
              }`}
            >
              <span>🔒</span>
              <span>Personal Access Token</span>
            </button>
          </div>
        </div>

        {/* Deploy Key Panel */}
        {gitAuthMethod === "deploy_key" && (
          <div className="space-y-4">
            <div className="rounded-xl border border-blue-200 bg-blue-50/50 p-4 space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <span className="text-base">🔑</span>
                  <span className="text-xs font-bold text-slate-900 uppercase tracking-wider">
                    Project SSH Public Deploy Key
                  </span>
                  <span className="rounded bg-emerald-100 border border-emerald-300 px-2 py-0.5 font-mono text-[10px] font-bold text-emerald-800">
                    {deployKey?.key_type?.toUpperCase() || "ED25519"}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    onClick={handleCopyDeployKey}
                    disabled={!deployKey?.public_key}
                    className="bg-slate-900 hover:bg-slate-800 text-white font-bold text-xs shadow-xs"
                  >
                    {copiedDeployKey ? "✓ Copied to Clipboard!" : "📋 Copy Public Key"}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="secondary"
                    disabled={generatingDeployKey}
                    onClick={handleRotateDeployKey}
                    className="text-xs font-semibold"
                  >
                    {generatingDeployKey ? "Rotating..." : "↺ Rotate Key"}
                  </Button>
                </div>
              </div>

              {loadingDeployKey ? (
                <div className="p-3 bg-white rounded-lg border border-slate-200 text-xs text-slate-400 font-mono animate-pulse">
                  Generating or retrieving project deploy key...
                </div>
              ) : (
                <div className="relative">
                  <pre className="p-3 bg-slate-900 text-emerald-400 rounded-lg text-[11px] font-mono break-all whitespace-pre-wrap select-all max-h-24 overflow-y-auto border border-slate-800">
                    {deployKey?.public_key || "Loading deploy key..."}
                  </pre>
                </div>
              )}

              {/* 3-Step Setup Instructions */}
              <div className="rounded-lg bg-white/95 border border-blue-200 p-3 space-y-2 text-xs text-slate-700">
                <p className="font-bold text-slate-900 flex items-center gap-1.5">
                  <span>📖</span> How to add this Deploy Key in GitHub:
                </p>
                <ol className="list-decimal list-inside space-y-1 text-[11px] text-slate-600 font-medium">
                  <li>Go to your GitHub repository and open <strong>Settings → Deploy keys → Add deploy key</strong>.</li>
                  <li>Paste the copied public key into the <strong>Key</strong> box and name it (e.g. <code className="font-mono text-slate-800 font-bold">Crezumic Platform</code>).</li>
                  <li>Leave <em>&ldquo;Allow write access&rdquo;</em> <strong>unchecked</strong> (read-only is safer and all that is needed) and click <strong>Add key</strong>.</li>
                </ol>
              </div>
            </div>

            <form onSubmit={onManualSubmit} className="space-y-3.5 pt-1">
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    Repository (owner/repo or git@github.com:owner/repo.git)
                  </label>
                  <Input
                    placeholder="e.g. owner/repo or git@github.com:owner/repo.git"
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

              <div className="pt-1 flex items-center gap-3">
                <Button
                  type="submit"
                  disabled={busy || !projectId || !fullName.trim()}
                  className="font-bold text-xs bg-slate-900 hover:bg-slate-800 text-white"
                >
                  {busy ? "Connecting..." : "➕ Connect Repository with Deploy Key"}
                </Button>
                <p className="text-[11px] text-[var(--ink-muted)]">
                  Worker will clone via SSH using this project&apos;s deploy key.
                </p>
              </div>
            </form>
          </div>
        )}

        {/* PAT Panel */}
        {gitAuthMethod === "pat" && (
          <form onSubmit={onManualSubmit} className="space-y-3.5 pt-1">
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="sm:col-span-2">
                <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                  Repository URL or owner/repo
                </label>
                <Input
                  placeholder="e.g. zeeshan-890/financor or https://github.com/..."
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
                Personal Access Token (PAT)
              </label>
              <div className="flex items-center gap-2">
                <Input
                  type="password"
                  placeholder="ghp_... or github_pat_... (leave blank for public repos)"
                  value={patToken}
                  onChange={(e) => setPatToken(e.target.value)}
                />
                <Button
                  type="button"
                  variant="secondary"
                  onClick={handleSaveTokenAndFetch}
                  disabled={fetchingRepos || !patToken.trim()}
                  className="text-xs"
                >
                  {fetchingRepos ? "Loading..." : "Load My Repos"}
                </Button>
              </div>
              <p className="mt-1 text-[11px] text-[var(--ink-muted)]">
                Classic or fine-grained token with <code className="font-mono font-bold">repo</code> read access.
              </p>
            </div>

            <div className="pt-1">
              <Button type="submit" disabled={busy || !projectId || !fullName.trim()} className="font-bold text-xs">
                {busy ? "Connecting..." : "➕ Connect Repository with PAT"}
              </Button>
            </div>
          </form>
        )}
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
