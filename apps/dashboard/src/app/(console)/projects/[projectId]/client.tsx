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
import type { ManagedAddon, Project } from "@/lib/types";

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
  const [gitToken, setGitToken] = useState("");
  const [connectingGit, setConnectingGit] = useState(false);
  const [connectedRepos, setConnectedRepos] = useState<Array<{ id: string; full_name: string }>>([]);
  const [buildLogs, setBuildLogs] = useState("");

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

  // Sub-tab states for Databases & Add-ons
  const [dbName, setDbName] = useState("");
  const [dbList, setDbList] = useState<Array<{ id: string; name: string; status: string; connection_hint?: string }>>([]);
  const [provisioningDb, setProvisioningDb] = useState(false);
  const [addonsList, setAddonsList] = useState<ManagedAddon[]>([]);
  const [selectedAddonEngine, setSelectedAddonEngine] = useState("postgres");
  const [addonInstanceName, setAddonInstanceName] = useState("postgres-1");
  const [addonEnv, setAddonEnv] = useState("development");
  const [provisioningAddon, setProvisioningAddon] = useState(false);
  const [addonModalOpen, setAddonModalOpen] = useState(false);
  const [snippetAddon, setSnippetAddon] = useState<ManagedAddon | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Bulk Edit state for Secrets
  const [bulkSecretOpen, setBulkSecretOpen] = useState(false);
  const [bulkSecretText, setBulkSecretText] = useState("");
  const [bulkSecretSaving, setBulkSecretSaving] = useState(false);

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
    } else if (currentTab === "addons" || currentTab === "databases") {
      api.listAddons(currentOrg.id, actualProjectId)
        .then((res) => setAddonsList(res.addons || []))
        .catch(() => setAddonsList([]));
      api.listDatabases(currentOrg.id, actualProjectId)
        .then((res) => setDbList(res.databases || []))
        .catch(() => setDbList([]));
    } else if (currentTab === "builds") {
      api.listBuilds(currentOrg.id, actualProjectId)
        .then((builds) => {
          if (builds.length > 0) {
            api.getBuildLogs(currentOrg.id, actualProjectId, builds[0].id)
              .then((res) => setBuildLogs(res.logs || ""))
              .catch(() => setBuildLogs("// No logs recorded"));
          } else {
            setBuildLogs("// No builds recorded yet. Trigger a deployment to view compiler output.");
          }
        })
        .catch(() => setBuildLogs("// Failed to load builds"));
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
        token: gitToken.trim() || undefined,
      });
      setSuccess(`Connected repository "${repoName}".`);
      setGitUrl("");
      setGitToken("");
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

  function copyToClipboard(text: string, key: string) {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(text);
      setCopiedKey(key);
      setTimeout(() => setCopiedKey(null), 2000);
    }
  }

  function getInternalUrl(addon: ManagedAddon): string {
    if (addon.metadata?.internal_url) return String(addon.metadata.internal_url);
    const port = addon.metadata?.port || 8080;
    if (addon.engine === "redis" || addon.engine === "valkey") return `redis://redis:6379/0`;
    if (addon.engine === "postgres") return `postgres://jp:jp_dev_password@postgres:5432/jp`;
    if (addon.engine === "mysql" || addon.engine === "mariadb") return `mysql://jp:jp_dev_password@mysql:3306/jp`;
    if (addon.engine === "mongodb") return `mongodb://jp:jp_dev_password@mongodb:27017/jp`;
    return `http://addon-${addon.name}:${port}`;
  }

  function parseEnvLines(text: string): Array<{ key: string; value: string }> {
    const lines = text.split("\n");
    const result: Array<{ key: string; value: string }> = [];
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const eqIdx = trimmed.indexOf("=");
      if (eqIdx === -1) continue;
      const key = trimmed.slice(0, eqIdx).trim().toUpperCase();
      let val = trimmed.slice(eqIdx + 1).trim();
      if ((val.startsWith('"') && val.endsWith('"')) || (val.startsWith("'") && val.endsWith("'"))) {
        val = val.slice(1, -1);
      }
      if (key) {
        result.push({ key, value: val });
      }
    }
    return result;
  }

  async function handleBulkSaveSecrets() {
    if (!currentOrg || !project) return;
    const parsed = parseEnvLines(bulkSecretText);
    if (parsed.length === 0) {
      setError("No valid KEY=VALUE pairs found.");
      return;
    }
    setBulkSecretSaving(true);
    setError("");
    setSuccess("");
    try {
      for (const item of parsed) {
        await api.setSecret(currentOrg.id, project.id, secretEnv, item.key, item.value);
      }
      setSuccess(`Successfully imported and updated ${parsed.length} variables into ${secretEnv.toUpperCase()}.`);
      setBulkSecretOpen(false);
      setBulkSecretText("");
      const updated = await api.listSecrets(currentOrg.id, project.id, secretEnv);
      setSecretsList(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to bulk save environment variables");
    } finally {
      setBulkSecretSaving(false);
    }
  }

  async function handleProvisionAddon(e?: FormEvent) {
    if (e) e.preventDefault();
    if (!currentOrg || !project || !addonInstanceName.trim()) return;
    setProvisioningAddon(true);
    setError("");
    setSuccess("");
    try {
      const created = await api.createAddon(
        currentOrg.id,
        project.id,
        selectedAddonEngine,
        addonInstanceName.trim(),
        addonEnv,
      );
      setSuccess(`Provisioned ${created.engine} (${created.name}) and injected connection credentials into ${project.name} .env!`);
      setAddonModalOpen(false);
      const [addonsRes, secretsRes] = await Promise.all([
        api.listAddons(currentOrg.id, project.id),
        api.listSecrets(currentOrg.id, project.id, secretEnv),
      ]);
      setAddonsList(addonsRes.addons || []);
      setSecretsList(secretsRes);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to provision add-on");
    } finally {
      setProvisioningAddon(false);
    }
  }

  async function handleDeleteAddon(addon: ManagedAddon) {
    if (!currentOrg || !project) return;
    const ok = window.confirm(`Permanently terminate and delete add-on "${addon.name}" from ${project.name}?`);
    if (!ok) return;
    try {
      await api.deleteAddon(currentOrg.id, project.id, addon.id);
      setSuccess(`Deleted add-on "${addon.name}".`);
      const res = await api.listAddons(currentOrg.id, project.id);
      setAddonsList(res.addons || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
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
        <Link href="/projects" onClick={() => setActiveProject(null)}>
          <Button variant="secondary">← Back to all projects</Button>
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
                  <Link href={`/projects/${actualProjectId}?tab=addons`} className="flex items-center gap-1.5 hover:text-[var(--accent)]">
                    <span>🧩</span> Add-ons & Databases
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
              <div>
                <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                  Personal Access Token (For Private Repositories)
                </label>
                <Input
                  type="password"
                  placeholder="ghp_... or github_pat_... (leave empty for public repos)"
                  value={gitToken}
                  onChange={(e) => setGitToken(e.target.value)}
                />
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
              <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-emerald-400">
                App: {project.name}
              </span>
            </div>
            <pre className="h-80 overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400 leading-relaxed shadow-inner whitespace-pre-wrap">
              {buildLogs || `// Live build stream for ${project.name}. Connect a repository and trigger a deployment to view compilation steps.`}
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
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  onClick={() => setBulkSecretOpen(true)}
                  className="font-bold text-xs"
                >
                  📋 Bulk Edit (.env)
                </Button>
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
          TAB 7: MANAGED ADD-ONS & DATABASES
          ======================================================== */}
      {(currentTab === "addons" || currentTab === "databases") && (
        <div className="space-y-6">
          {/* Quick-Add Popular Templates */}
          <div className="jp-card p-5 space-y-4">
            <div className="flex items-center justify-between border-b border-[var(--border)] pb-3">
              <div>
                <h2 className="text-sm font-bold text-[var(--ink)]">
                  1-Click Add-ons for {project.name}
                </h2>
                <p className="text-xs text-[var(--ink-muted)]">
                  Provision databases, caches, and queues directly attached to this app. Standard credentials (DATABASE_URL, REDIS_URL) are automatically injected into this project.
                </p>
              </div>
              <Link href="/addons">
                <Button variant="secondary" size="sm" className="font-semibold text-xs">
                  Browse All 246+ Add-ons ↗
                </Button>
              </Link>
            </div>

            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2.5">
              {[
                { id: "postgres", name: "PostgreSQL", icon: "🐘", desc: "Relational DB", defaultPort: 5432 },
                { id: "redis", name: "Redis", icon: "⚡", desc: "In-memory Cache", defaultPort: 6379 },
                { id: "mysql", name: "MySQL", icon: "🐬", desc: "Relational DB", defaultPort: 3306 },
                { id: "mongodb", name: "MongoDB", icon: "🍃", desc: "Document DB", defaultPort: 27017 },
                { id: "rabbitmq", name: "RabbitMQ", icon: "🐰", desc: "Message Queue", defaultPort: 5672 },
                { id: "minio", name: "MinIO S3", icon: "📦", desc: "Object Storage", defaultPort: 9000 },
              ].map((template) => (
                <button
                  key={template.id}
                  type="button"
                  onClick={() => {
                    setSelectedAddonEngine(template.id);
                    setAddonInstanceName(`${template.id}-1`);
                    setAddonModalOpen(true);
                  }}
                  className="rounded-xl border border-[var(--border)] bg-white p-3 text-left hover:border-[var(--accent)] hover:shadow-2xs transition-all flex flex-col justify-between"
                >
                  <div>
                    <span className="text-xl">{template.icon}</span>
                    <h4 className="mt-1 font-bold text-xs text-[var(--ink)]">{template.name}</h4>
                    <p className="text-[10px] text-[var(--ink-muted)]">{template.desc}</p>
                  </div>
                  <span className="mt-2 text-[10px] font-bold text-[var(--accent)] font-mono">
                    + Provision
                  </span>
                </button>
              ))}
            </div>
          </div>

          {/* Active Add-ons in this Project */}
          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex items-center justify-between">
              <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                Active Attached Add-ons ({addonsList.length})
              </h3>
              <span className="text-[11px] text-[var(--ink-muted)]">
                Connected over private container network
              </span>
            </div>

            {addonsList.length === 0 ? (
              <div className="p-8 text-center text-xs text-[var(--ink-muted)] space-y-2">
                <p className="font-semibold text-[var(--ink)]">No add-ons attached to this project yet.</p>
                <p>Click any template above (PostgreSQL, Redis, MySQL, etc.) to instantly provision and inject credentials.</p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm min-w-[700px]">
                  <thead>
                    <tr className="border-b border-[var(--border)] bg-slate-50/50 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                      <th className="px-4 py-2.5">Engine / Instance</th>
                      <th className="px-4 py-2.5">Status</th>
                      <th className="px-4 py-2.5">Internal Connection URL</th>
                      <th className="px-4 py-2.5 text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {addonsList.map((addon) => {
                      const intUrl = getInternalUrl(addon);
                      return (
                        <tr key={addon.id} className="hover:bg-slate-50/70 transition-colors">
                          <td className="px-4 py-3 font-medium text-[var(--ink)]">
                            <div className="flex items-center gap-2">
                              <span className="font-mono text-xs uppercase px-1.5 py-0.5 rounded bg-slate-100 border border-[var(--border)] font-bold">
                                {addon.engine}
                              </span>
                              <span className="font-bold">{addon.name}</span>
                            </div>
                            <p className="text-[11px] text-[var(--ink-muted)] font-mono mt-0.5">
                              Secret: <code className="text-blue-700 font-bold">{addon.secret_ref || `${addon.engine.toUpperCase()}_URL`}</code>
                            </p>
                          </td>

                          <td className="px-4 py-3">
                            <span className="inline-flex items-center gap-1 rounded-full bg-emerald-50 border border-emerald-300 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
                              <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                              {addon.status || "ready"}
                            </span>
                          </td>

                          <td className="px-4 py-3">
                            <div className="flex items-center gap-2">
                              <code className="text-xs font-mono text-[var(--ink)] bg-slate-100 px-2 py-1 rounded border border-[var(--border)] max-w-xs truncate">
                                {intUrl}
                              </code>
                              <button
                                type="button"
                                onClick={() => copyToClipboard(intUrl, `proj-int-${addon.id}`)}
                                className="px-2 py-1 text-xs font-bold rounded border border-[var(--border)] bg-white hover:bg-slate-50 text-[var(--ink-secondary)] transition-colors shrink-0"
                              >
                                {copiedKey === `proj-int-${addon.id}` ? "✓ Copied" : "Copy"}
                              </button>
                            </div>
                          </td>

                          <td className="px-4 py-3 text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              <Button
                                type="button"
                                variant="secondary"
                                size="sm"
                                onClick={() => setSnippetAddon(addon)}
                                className="text-xs"
                              >
                                Code Snippet
                              </Button>
                              <Button
                                type="button"
                                variant="danger"
                                size="sm"
                                onClick={() => handleDeleteAddon(addon)}
                                className="text-xs"
                              >
                                Delete
                              </Button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          MODAL: PROVISION ADDON DIRECTLY INTO PROJECT
          ======================================================== */}
      {addonModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs">
          <div className="w-full max-w-lg rounded-2xl border border-[var(--border)] bg-white shadow-2xl overflow-hidden animate-fade-in space-y-4">
            <div className="border-b border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-between">
              <div>
                <h3 className="text-base font-bold text-[var(--ink)]">
                  Provision {selectedAddonEngine.toUpperCase()} for {project.name}
                </h3>
                <p className="text-xs text-[var(--ink-muted)]">
                  Creates an isolated container instance and automatically links credentials into this project.
                </p>
              </div>
              <button
                type="button"
                onClick={() => setAddonModalOpen(false)}
                className="text-slate-400 hover:text-slate-700 text-lg font-bold p-1"
              >
                ✕
              </button>
            </div>

            <form onSubmit={handleProvisionAddon} className="p-6 space-y-4">
              <div>
                <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                  Add-on Instance Name
                </label>
                <Input
                  value={addonInstanceName}
                  onChange={(e) => setAddonInstanceName(e.target.value)}
                  placeholder={`${selectedAddonEngine}-1`}
                  className="font-mono text-xs font-semibold"
                  required
                />
              </div>

              <div>
                <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                  Target Environment Scope
                </label>
                <select
                  className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-xs font-bold text-[var(--ink)] shadow-2xs"
                  value={addonEnv}
                  onChange={(e) => setAddonEnv(e.target.value)}
                >
                  <option value="production">PRODUCTION</option>
                  <option value="staging">STAGING</option>
                  <option value="development">DEVELOPMENT</option>
                </select>
              </div>

              <div className="rounded-lg bg-emerald-50 border border-emerald-200 p-3 text-xs text-emerald-900 space-y-1">
                <p className="font-bold">✓ Automatic Zero-Config Environment Injected:</p>
                <p className="font-mono text-[11px] text-emerald-800">
                  {selectedAddonEngine.toUpperCase()}_URL &bull; DATABASE_URL (for Postgres/MySQL) &bull; REDIS_URL (for Redis)
                </p>
              </div>

              <div className="border-t border-[var(--border)] pt-4 flex items-center justify-end gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  onClick={() => setAddonModalOpen(false)}
                  disabled={provisioningAddon}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={provisioningAddon || !addonInstanceName.trim()}
                  className="font-bold shadow-xs"
                >
                  {provisioningAddon ? "Provisioning..." : `Provision & Link to ${project.name}`}
                </Button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ========================================================
          MODAL: BULK EDIT SECRETS (.env)
          ======================================================== */}
      {bulkSecretOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs">
          <div className="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-white shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
            <div className="border-b border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-between">
              <div>
                <h3 className="text-base font-bold text-[var(--ink)] flex items-center gap-2">
                  <span>Bulk Edit Environment Variables</span>
                  <span className="rounded bg-slate-200 px-2 py-0.5 text-[10px] font-bold uppercase text-slate-700">
                    {project.name} &bull; {secretEnv}
                  </span>
                </h3>
                <p className="text-xs text-[var(--ink-muted)] mt-0.5">
                  Paste raw <code className="font-bold">.env</code> lines. Lines starting with # are ignored.
                </p>
              </div>
              <button
                type="button"
                onClick={() => setBulkSecretOpen(false)}
                className="text-slate-400 hover:text-slate-700 text-lg font-bold p-1"
              >
                ✕
              </button>
            </div>

            <div className="p-6 space-y-4 flex-1 overflow-y-auto">
              <div>
                <div className="flex items-center justify-between mb-1.5 text-xs">
                  <span className="font-bold text-[var(--ink-secondary)]">.env Content</span>
                  <span className="font-mono text-emerald-600 font-bold">
                    {parseEnvLines(bulkSecretText).length} variable{parseEnvLines(bulkSecretText).length === 1 ? "" : "s"} detected
                  </span>
                </div>
                <textarea
                  className="w-full h-72 rounded-xl border border-[var(--border)] bg-slate-900 p-4 font-mono text-xs text-emerald-400 shadow-inner focus:border-[var(--accent)] focus:outline-none leading-relaxed"
                  placeholder={`# Paste your environment variables:
PORT=3000
DATABASE_URL=postgres://user:password@host:5432/dbname
REDIS_URL=redis://localhost:6379
JWT_SECRET=super_secret_key_123`}
                  value={bulkSecretText}
                  onChange={(e) => setBulkSecretText(e.target.value)}
                />
              </div>

              <div className="rounded-lg bg-blue-50 border border-blue-200 p-3 text-xs text-blue-900 flex items-start gap-2">
                <span className="font-bold text-blue-700">ℹ</span>
                <span>
                  All variables will be securely saved into the <strong>{secretEnv.toUpperCase()}</strong> environment scope of <strong>{project.name}</strong>.
                </span>
              </div>
            </div>

            <div className="border-t border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-end gap-3">
              <Button
                type="button"
                variant="secondary"
                onClick={() => setBulkSecretOpen(false)}
                disabled={bulkSecretSaving}
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={handleBulkSaveSecrets}
                disabled={bulkSecretSaving || parseEnvLines(bulkSecretText).length === 0}
                className="font-bold shadow-xs"
              >
                {bulkSecretSaving
                  ? "Saving Variables..."
                  : `Save ${parseEnvLines(bulkSecretText).length} Variable${parseEnvLines(bulkSecretText).length === 1 ? "" : "s"}`}
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Code Snippet Modal */}
      {snippetAddon && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-xl rounded-xl border border-[var(--border)] bg-white p-6 shadow-xl space-y-4">
            <div className="flex items-center justify-between pb-3 border-b border-[var(--border)]">
              <div>
                <h3 className="text-base font-bold text-[var(--ink)]">
                  Integration Snippet for {project.name}
                </h3>
                <p className="text-xs text-[var(--ink-muted)]">
                  {snippetAddon.name} ({snippetAddon.engine})
                </p>
              </div>
              <button
                type="button"
                onClick={() => setSnippetAddon(null)}
                className="text-sm text-[var(--ink-muted)] hover:text-[var(--ink)] font-bold"
              >
                ✕
              </button>
            </div>

            <p className="text-xs text-[var(--ink-muted)] leading-relaxed">
              When {project.name} runs on this server, connect to <strong>{snippetAddon.name}</strong> over the internal Docker network:
            </p>

            <div className="relative">
              <pre className="rounded-lg bg-slate-900 p-4 text-xs text-emerald-400 font-mono overflow-x-auto shadow-inner leading-relaxed">
{`# Injected into ${project.name} environment automatically:
${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}=${getInternalUrl(snippetAddon)}

# Canonical Framework Alias:
DATABASE_URL=${getInternalUrl(snippetAddon)}`}
              </pre>
              <button
                type="button"
                onClick={() =>
                  copyToClipboard(
                    `${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}=${getInternalUrl(snippetAddon)}\nDATABASE_URL=${getInternalUrl(snippetAddon)}`,
                    "proj-snippet"
                  )
                }
                className="absolute top-2.5 right-2.5 px-2.5 py-1 text-xs font-bold rounded bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 transition-colors"
              >
                {copiedKey === "proj-snippet" ? "✓ Copied!" : "Copy"}
              </button>
            </div>

            <div className="flex justify-end pt-2 border-t border-[var(--border)]">
              <Button type="button" onClick={() => setSnippetAddon(null)}>
                Close
              </Button>
            </div>
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
