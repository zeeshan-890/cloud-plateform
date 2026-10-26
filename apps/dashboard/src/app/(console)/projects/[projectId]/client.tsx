"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";
import type { Project } from "@/lib/types";

export default function ProjectDetailClient() {
  const params = useParams<{ projectId: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();
  const { currentOrg } = useOrg();
  const { setActiveProject, rootDomain, getAppPublicUrl } = useProject();

  const [project, setProject] = useState<Project | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  // Sub-tab states for Git connect
  const [gitUrl, setGitUrl] = useState("");
  const [gitBranch, setGitBranch] = useState("main");
  const [connectingGit, setConnectingGit] = useState(false);
  const [connectedRepos, setConnectedRepos] = useState<Array<{ id: string; full_name: string }>>([]);

  // Sub-tab states for Domains
  const [domainInput, setDomainInput] = useState("");
  const [domainsList, setDomainsList] = useState<Array<{ id: string; hostname: string; status: string }>>([]);
  const [addingDomain, setAddingDomain] = useState(false);

  // Sub-tab states for Secrets
  const [secretEnv, setSecretEnv] = useState("production");
  const [secretKey, setSecretKey] = useState("");
  const [secretVal, setSecretVal] = useState("");
  const [secretsList, setSecretsList] = useState<Array<{ id: string; name: string; current_version: number; value_hint: string }>>([]);
  const [savingSecret, setSavingSecret] = useState(false);

  // Sub-tab states for Databases
  const [dbName, setDbName] = useState("");
  const [dbList, setDbList] = useState<Array<{ id: string; name: string; status: string; connection_hint?: string }>>([]);
  const [provisioningDb, setProvisioningDb] = useState(false);

  // Active tab from URL
  const currentTab = searchParams.get("tab") || "overview";

  // Resolve actual projectId
  const rawId = params?.projectId;
  const actualProjectId =
    typeof window !== "undefined" && (!rawId || rawId === "default")
      ? window.location.pathname.split("/projects/")[1]?.split("/")[0]?.split("?")[0] || rawId
      : rawId;

  // Load project details
  useEffect(() => {
    if (!currentOrg || !actualProjectId || actualProjectId === "default") return;
    let mounted = true;
    (async () => {
      setLoading(true);
      setError("");
      try {
        const p = await api.getProject(currentOrg.id, actualProjectId);
        if (mounted) {
          setProject(p);
          setName(p.name);
          setDescription(p.description || "");
          setActiveProject(p);
        }
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load project");
        }
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg, actualProjectId, setActiveProject]);

  // Load tab-specific resources when switching tabs
  useEffect(() => {
    if (!currentOrg || !actualProjectId || actualProjectId === "default") return;

    if (currentTab === "git") {
      api.listRepos(currentOrg.id, actualProjectId)
        .then((repos) => setConnectedRepos(repos))
        .catch(() => setConnectedRepos([]));
    } else if (currentTab === "domains") {
      api.listDomains(currentOrg.id, actualProjectId)
        .then((doms) => setDomainsList(doms))
        .catch(() => setDomainsList([]));
    } else if (currentTab === "secrets") {
      api.listSecrets(currentOrg.id, actualProjectId, secretEnv)
        .then((s) => setSecretsList(s))
        .catch(() => setSecretsList([]));
    } else if (currentTab === "databases") {
      api.listDatabases(currentOrg.id, actualProjectId)
        .then((res) => setDbList(res.databases || []))
        .catch(() => setDbList([]));
    }
  }, [currentOrg, actualProjectId, currentTab, secretEnv]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project) return;
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      const updated = await api.updateProject(currentOrg.id, project.id, {
        name: name.trim(),
        description: description.trim(),
      });
      setProject(updated);
      setActiveProject(updated);
      setSuccess("Project settings saved.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to update project");
    } finally {
      setSaving(false);
    }
  }

  async function onDelete() {
    if (!currentOrg || !project) return;
    const confirmed = window.confirm(
      `Are you sure you want to delete "${project.name}"? This will terminate all running instances permanently.`
    );
    if (!confirmed) return;
    setDeleting(true);
    setError("");
    try {
      await api.deleteProject(currentOrg.id, project.id);
      setActiveProject(null);
      router.push("/projects");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to delete project");
      setDeleting(false);
    }
  }

  // Real Git Connect Handler (NO MOCK!)
  async function handleConnectGit(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project || !gitUrl.trim()) return;
    setConnectingGit(true);
    setError("");
    setSuccess("");
    try {
      const cleanUrl = gitUrl.trim();
      const repoName = cleanUrl.replace(/^https?:\/\/github\.com\//, "").replace(/\.git$/, "");
      await api.connectRepo(currentOrg.id, project.id, {
        full_name: repoName,
        clone_url: cleanUrl,
        default_branch: gitBranch.trim() || "main",
      });
      setSuccess(`Connected repository "${repoName}".`);
      setGitUrl("");
      const updated = await api.listRepos(currentOrg.id, project.id);
      setConnectedRepos(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to connect repository");
    } finally {
      setConnectingGit(false);
    }
  }

  // Domain Add Handler
  async function handleAddDomain(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project || !domainInput.trim()) return;
    setAddingDomain(true);
    setError("");
    setSuccess("");
    const host = domainInput.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    try {
      const dom = await api.addDomain(currentOrg.id, project.id, { hostname: host });
      try {
        await api.verifyDomain(currentOrg.id, project.id, dom.id, true);
        setSuccess(`Domain "${host}" added and verified with Traefik ingress!`);
      } catch {
        setSuccess(`Domain "${host}" added.`);
      }
      setDomainInput("");
      const updated = await api.listDomains(currentOrg.id, project.id);
      setDomainsList(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add domain");
    } finally {
      setAddingDomain(false);
    }
  }

  // Secret Add Handler
  async function handleSaveSecret(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project || !secretKey.trim() || !secretVal) return;
    setSavingSecret(true);
    setError("");
    setSuccess("");
    try {
      await api.setSecret(currentOrg.id, project.id, secretEnv, secretKey.trim(), secretVal);
      setSuccess(`Secret "${secretKey.trim()}" saved for ${secretEnv.toUpperCase()}.`);
      setSecretKey("");
      setSecretVal("");
      const updated = await api.listSecrets(currentOrg.id, project.id, secretEnv);
      setSecretsList(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to set secret");
    } finally {
      setSavingSecret(false);
    }
  }

  // Database Provision Handler
  async function handleProvisionDb(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project || !dbName.trim()) return;
    setProvisioningDb(true);
    setError("");
    setSuccess("");
    try {
      await api.createDatabase(currentOrg.id, project.id, dbName.trim());
      setSuccess(`Postgres database "${dbName.trim()}" provisioned.`);
      setDbName("");
      const res = await api.listDatabases(currentOrg.id, project.id);
      setDbList(res.databases || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to provision database");
    } finally {
      setProvisioningDb(false);
    }
  }

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center text-sm font-semibold text-[var(--ink-muted)]">
        Loading project application...
      </div>
    );
  }

  if (!project) {
    return (
      <div className="space-y-4">
        <Alert tone="error">
          {error || "Project not found or you do not have permission to view it."}
        </Alert>
        <Link href="/projects">
          <Button variant="secondary">Back to all projects</Button>
        </Link>
      </div>
    );
  }

  const appPublicUrl = getAppPublicUrl(project.name);

  return (
    <div className="space-y-6">
      {/* Top Breadcrumb & Quick Actions Header */}
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-[var(--border)] pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-black text-[var(--ink)] tracking-tight">
              {project.name}
            </h1>
            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
              Active
            </span>
          </div>
          <p className="mt-1 text-xs text-[var(--ink-muted)]">
            Default App Domain:{" "}
            <a
              href={appPublicUrl}
              target="_blank"
              rel="noreferrer"
              className="font-mono font-bold text-[var(--accent)] hover:underline"
            >
              {appPublicUrl} ↗
            </a>
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Link href={`/projects/${actualProjectId}?tab=deployments`}>
            <Button size="sm">Deploy App</Button>
          </Link>
        </div>
      </div>

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* ========================================================
          TAB 1: OVERVIEW (Default Dashboard)
          ======================================================== */}
      {currentTab === "overview" && (
        <div className="space-y-6">
          <div className="grid gap-4 md:grid-cols-3">
            <div className="jp-card p-4 space-y-1">
              <p className="text-[11px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">
                Default Public URL
              </p>
              <a
                href={appPublicUrl}
                target="_blank"
                rel="noreferrer"
                className="block truncate font-mono text-sm font-bold text-[var(--accent)] hover:underline"
              >
                {appPublicUrl} ↗
              </a>
              <p className="text-[10px] text-[var(--ink-faint)]">
                Auto-assigned via wildcard: *.{rootDomain}
              </p>
            </div>

            <div className="jp-card p-4 space-y-1">
              <p className="text-[11px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">
                Deployment Strategy
              </p>
              <p className="text-sm font-bold text-[var(--ink)]">Rolling Zero-Downtime</p>
              <p className="text-[10px] text-[var(--ink-faint)]">Automatic container health checking</p>
            </div>

            <div className="jp-card p-4 space-y-1">
              <p className="text-[11px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">
                Environment Scope
              </p>
              <p className="text-sm font-bold text-[var(--ink)]">Production Isolated</p>
              <p className="text-[10px] text-[var(--ink-faint)]">Private Docker network bridge</p>
            </div>
          </div>

          <div className="grid gap-6 lg:grid-cols-3">
            <div className="jp-card p-5 lg:col-span-2 space-y-4">
              <div className="border-b border-[var(--border)] pb-3 flex items-center justify-between">
                <div>
                  <h3 className="text-sm font-bold text-[var(--ink)]">Application Details</h3>
                  <p className="text-xs text-[var(--ink-muted)]">Core metadata and runtime identity.</p>
                </div>
                <Link href={`/projects/${actualProjectId}?tab=settings`}>
                  <Button variant="secondary" size="sm">Edit</Button>
                </Link>
              </div>

              <dl className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-xs">
                <div>
                  <dt className="font-bold text-[var(--ink-muted)] uppercase text-[10px]">App Name</dt>
                  <dd className="mt-0.5 font-bold text-[var(--ink)] text-sm">{project.name}</dd>
                </div>
                <div>
                  <dt className="font-bold text-[var(--ink-muted)] uppercase text-[10px]">Project ID</dt>
                  <dd className="mt-0.5 font-mono text-[var(--ink-secondary)]">{project.id}</dd>
                </div>
                <div className="sm:col-span-2">
                  <dt className="font-bold text-[var(--ink-muted)] uppercase text-[10px]">Description</dt>
                  <dd className="mt-0.5 text-[var(--ink-secondary)]">
                    {project.description || "No description provided."}
                  </dd>
                </div>
              </dl>
            </div>

            <div className="jp-card p-5 space-y-3">
              <h3 className="text-sm font-bold text-[var(--ink)]">Quick Navigation</h3>
              <ul className="space-y-2 text-xs font-semibold text-[var(--ink-secondary)]">
                <li>
                  <Link href={`/projects/${actualProjectId}?tab=git`} className="flex items-center gap-1.5 hover:text-[var(--accent)]">
                    <span>🐙</span> Git Repository Source
                  </Link>
                </li>
                <li>
                  <Link href={`/projects/${actualProjectId}?tab=domains`} className="flex items-center gap-1.5 hover:text-[var(--accent)]">
                    <span>🌐</span> Custom Domains & TLS
                  </Link>
                </li>
                <li>
                  <Link href={`/projects/${actualProjectId}?tab=secrets`} className="flex items-center gap-1.5 hover:text-[var(--accent)]">
                    <span>🔐</span> Environment Variables & Secrets
                  </Link>
                </li>
                <li>
                  <Link href={`/projects/${actualProjectId}?tab=databases`} className="flex items-center gap-1.5 hover:text-[var(--accent)]">
                    <span>🗄️</span> Managed Postgres Database
                  </Link>
                </li>
              </ul>
            </div>
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 2: GIT REPOSITORY (Real Git URL - NO MOCK)
          ======================================================== */}
      {currentTab === "git" && (
        <div className="space-y-6">
          <div className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3">
              <h2 className="text-sm font-bold text-[var(--ink)]">
                Connect Git Repository (Public or Private)
              </h2>
              <p className="text-xs text-[var(--ink-muted)]">
                Enter any real Git clone URL (GitHub, GitLab, Bitbucket, self-hosted Gitea).
              </p>
            </div>

            <form onSubmit={handleConnectGit} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    Repository Clone URL or owner/repo
                  </label>
                  <Input
                    placeholder="https://github.com/myaccount/my-app.git or myaccount/my-app"
                    value={gitUrl}
                    onChange={(e) => setGitUrl(e.target.value)}
                    required
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    Deploy Branch
                  </label>
                  <Input
                    placeholder="main"
                    value={gitBranch}
                    onChange={(e) => setGitBranch(e.target.value)}
                  />
                </div>
              </div>
              <Button type="submit" disabled={connectingGit || !gitUrl.trim()}>
                {connectingGit ? "Connecting..." : "Connect Git Repository"}
              </Button>
            </form>
          </div>

          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
              <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Connected Repositories ({connectedRepos.length})
              </h3>
            </div>
            {connectedRepos.length === 0 ? (
              <div className="p-6 text-center text-xs text-[var(--ink-muted)]">
                No Git repository linked yet. Connect a repository above to trigger automatic builds.
              </div>
            ) : (
              <ul className="divide-y divide-[var(--border)]">
                {connectedRepos.map((r) => (
                  <li key={r.id} className="flex items-center justify-between p-3.5 text-xs">
                    <span className="font-mono font-bold text-[var(--ink)]">{r.full_name}</span>
                    <span className="rounded bg-emerald-50 border border-emerald-200 px-2 py-0.5 font-bold text-emerald-800">
                      Auto-Deploy Active
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 3: DEPLOYMENTS
          ======================================================== */}
      {currentTab === "deployments" && (
        <div className="space-y-6">
          <div className="jp-card p-5 flex items-center justify-between">
            <div>
              <h2 className="text-sm font-bold text-[var(--ink)]">Deploy Application</h2>
              <p className="text-xs text-[var(--ink-muted)]">
                Queue a new build and rolling update for {project.name}.
              </p>
            </div>
            <Button
              onClick={async () => {
                try {
                  await api.createDeployment(currentOrg!.id, actualProjectId, { message: "Manual deployment" });
                  setSuccess("Deployment queued successfully!");
                } catch (err) {
                  setError(err instanceof ApiError ? err.message : "Deployment failed to start");
                }
              }}
            >
              Trigger New Deployment
            </Button>
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 4: BUILDS & LOGS
          ======================================================== */}
      {currentTab === "builds" && (
        <div className="space-y-6">
          <div className="jp-card overflow-hidden">
            <div className="border-b border-slate-700 bg-slate-900 px-4 py-3 flex items-center justify-between">
              <span className="font-mono text-xs font-bold text-slate-200">
                Compiler Terminal & Live Build Stream
              </span>
              <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-slate-400">
                App: {project.name}
              </span>
            </div>
            <pre className="h-80 overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400 leading-relaxed shadow-inner">
              // Live build stream for {project.name}. Connect a repository and trigger a deployment to view compilation steps.
            </pre>
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 5: CUSTOM DOMAINS & ROUTING
          ======================================================== */}
      {currentTab === "domains" && (
        <div className="space-y-6">
          <div className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3">
              <h2 className="text-sm font-bold text-[var(--ink)]">
                Add Custom Domain to {project.name}
              </h2>
              <p className="text-xs text-[var(--ink-muted)]">
                Point a CNAME or A-Record to this server. Traefik automatically wires the proxy route.
              </p>
            </div>

            <form onSubmit={handleAddDomain} className="flex gap-2">
              <Input
                placeholder="e.g. app.mycompany.com or mydomain.com"
                value={domainInput}
                onChange={(e) => setDomainInput(e.target.value)}
                required
              />
              <Button type="submit" disabled={addingDomain || !domainInput.trim()}>
                {addingDomain ? "Connecting..." : "Add Domain"}
              </Button>
            </form>
          </div>

          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
              <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Connected Hostnames ({domainsList.length})
              </h3>
            </div>
            {domainsList.length === 0 ? (
              <div className="p-6 text-center text-xs text-[var(--ink-muted)]">
                No custom domains connected yet. Default URL: <code className="font-bold text-[var(--ink)]">{appPublicUrl}</code>
              </div>
            ) : (
              <ul className="divide-y divide-[var(--border)]">
                {domainsList.map((d) => (
                  <li key={d.id} className="flex items-center justify-between p-3.5 text-xs">
                    <span className="font-bold text-[var(--ink)]">{d.hostname}</span>
                    <span className="rounded bg-emerald-50 border border-emerald-200 px-2 py-0.5 font-bold text-emerald-800">
                      Active &bull; Traefik Ingress
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 6: SECRETS & ENV VARS
          ======================================================== */}
      {currentTab === "secrets" && (
        <div className="space-y-6">
          <div className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3 flex items-center justify-between">
              <div>
                <h2 className="text-sm font-bold text-[var(--ink)]">Environment Variables</h2>
                <p className="text-xs text-[var(--ink-muted)]">
                  Encrypted secrets injected securely into container runtime.
                </p>
              </div>
              <select
                className="rounded-md border border-[var(--border)] bg-white px-2.5 py-1 text-xs font-bold text-[var(--ink)]"
                value={secretEnv}
                onChange={(e) => setSecretEnv(e.target.value)}
              >
                <option value="production">PRODUCTION</option>
                <option value="staging">STAGING</option>
                <option value="development">DEVELOPMENT</option>
              </select>
            </div>

            <form onSubmit={handleSaveSecret} className="flex flex-wrap gap-2">
              <Input
                placeholder="VARIABLE_NAME"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value)}
                className="min-w-[180px] font-mono uppercase"
                required
              />
              <Input
                placeholder="Value"
                type="password"
                value={secretVal}
                onChange={(e) => setSecretVal(e.target.value)}
                className="min-w-[220px] flex-1"
                required
              />
              <Button type="submit" disabled={savingSecret || !secretKey.trim() || !secretVal}>
                {savingSecret ? "Saving..." : "Save"}
              </Button>
            </form>
          </div>

          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
              <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Variables in {secretEnv.toUpperCase()} ({secretsList.length})
              </h3>
            </div>
            {secretsList.length === 0 ? (
              <div className="p-6 text-center text-xs text-[var(--ink-muted)]">
                No secrets stored in {secretEnv} scope.
              </div>
            ) : (
              <ul className="divide-y divide-[var(--border)]">
                {secretsList.map((s) => (
                  <li key={s.id} className="flex items-center justify-between p-3.5 text-xs">
                    <span className="font-mono font-bold text-[var(--ink)]">{s.name}</span>
                    <span className="font-mono text-xs text-[var(--ink-muted)]">{s.value_hint}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 7: MANAGED DATABASES
          ======================================================== */}
      {currentTab === "databases" && (
        <div className="space-y-6">
          <div className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3">
              <h2 className="text-sm font-bold text-[var(--ink)]">Provision PostgreSQL Schema</h2>
              <p className="text-xs text-[var(--ink-muted)]">
                Dedicated database schema and connection credentials for {project.name}.
              </p>
            </div>

            <form onSubmit={handleProvisionDb} className="flex gap-2">
              <Input
                placeholder="e.g. app_production"
                value={dbName}
                onChange={(e) => setDbName(e.target.value)}
                required
              />
              <Button type="submit" disabled={provisioningDb || !dbName.trim()}>
                {provisioningDb ? "Provisioning..." : "Provision Postgres"}
              </Button>
            </form>
          </div>

          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
              <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Active Databases ({dbList.length})
              </h3>
            </div>
            {dbList.length === 0 ? (
              <div className="p-6 text-center text-xs text-[var(--ink-muted)]">
                No databases attached yet. Provision a database above to generate DATABASE_URL.
              </div>
            ) : (
              <ul className="divide-y divide-[var(--border)]">
                {dbList.map((d) => (
                  <li key={d.id} className="flex items-center justify-between p-3.5 text-xs">
                    <div>
                      <p className="font-bold text-[var(--ink)]">{d.name}</p>
                      <p className="font-mono text-[11px] text-[var(--ink-muted)] truncate max-w-md">
                        {d.connection_hint}
                      </p>
                    </div>
                    <span className="rounded bg-emerald-50 border border-emerald-200 px-2 py-0.5 font-bold text-emerald-800">
                      {d.status || "Ready"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 8: PROJECT SETTINGS & DANGER ZONE
          ======================================================== */}
      {currentTab === "settings" && (
        <div className="space-y-6">
          <form onSubmit={onSave} className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3">
              <h2 className="text-base font-bold text-[var(--ink)]">General Settings</h2>
              <p className="text-xs text-[var(--ink-muted)]">Update app metadata and identity.</p>
            </div>

            <div>
              <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                App Name
              </label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                disabled={saving}
              />
            </div>

            <div>
              <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Description
              </label>
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                disabled={saving}
                rows={3}
                className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm text-[var(--ink)] placeholder:text-[var(--ink-faint)] focus:border-[var(--accent)] focus:outline-none shadow-2xs"
                placeholder="What does this application do?"
              />
            </div>

            <Button type="submit" disabled={saving}>
              {saving ? "Saving..." : "Save Changes"}
            </Button>
          </form>

          <div className="jp-card border-red-300 bg-red-50/50 p-5 space-y-3">
            <h3 className="text-sm font-bold text-red-700">Danger Zone</h3>
            <p className="text-xs text-red-800/80 leading-relaxed">
              Deleting this project terminates all container replicas, drops routing records, and deletes secrets permanently.
            </p>
            <Button
              variant="danger"
              onClick={onDelete}
              disabled={deleting}
            >
              {deleting ? "Deleting..." : "Delete Project Permanently"}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
