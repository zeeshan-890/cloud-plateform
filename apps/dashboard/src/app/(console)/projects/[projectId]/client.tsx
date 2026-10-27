"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";
import type { Build, Certificate, ConnectedRepo, DeployKey, Deployment, Domain, LogEntry, ManagedAddon, Project, RuntimeInstance, SecretMeta } from "@/lib/types";
import { setVisibilityInterval } from "@/lib/visibility";
import { AIResponseView } from "@/components/ai-response-view";

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
  const [privateTokenOpen, setPrivateTokenOpen] = useState(false);
  const [connectingGit, setConnectingGit] = useState(false);
  const [connectedRepos, setConnectedRepos] = useState<ConnectedRepo[]>([]);
  const [gitAuthMethod, setGitAuthMethod] = useState<"deploy_key" | "pat">("deploy_key");
  const [deployKey, setDeployKey] = useState<DeployKey | null>(null);
  const [loadingDeployKey, setLoadingDeployKey] = useState(false);
  const [generatingDeployKey, setGeneratingDeployKey] = useState(false);
  const [copiedDeployKey, setCopiedDeployKey] = useState(false);
  const [buildLogs, setBuildLogs] = useState("");

  // Sub-tab states for Domains
  const [domainInput, setDomainInput] = useState("");
  const [domainsList, setDomainsList] = useState<Domain[]>([]);
  const [certificatesList, setCertificatesList] = useState<Certificate[]>([]);
  const [addingDomain, setAddingDomain] = useState(false);
  const [verifyingDomainId, setVerifyingDomainId] = useState<string | null>(null);
  const [domainToDelete, setDomainToDelete] = useState<Domain | null>(null);
  const [deletingDomain, setDeletingDomain] = useState(false);
  const [copiedDnsKey, setCopiedDnsKey] = useState<string | null>(null);

  // Sub-tab states for Secrets
  const [secretEnv, setSecretEnv] = useState("all");
  const [newSecretEnv, setNewSecretEnv] = useState("production");
  const [secretKey, setSecretKey] = useState("");
  const [secretVal, setSecretVal] = useState("");
  const [secretValVisible, setSecretValVisible] = useState(false);
  const [secretsList, setSecretsList] = useState<SecretMeta[]>([]);
  const [revealedSecrets, setRevealedSecrets] = useState<Record<string, boolean>>({});
  const [showAllSecrets, setShowAllSecrets] = useState(false);
  const [revealedAddonPass, setRevealedAddonPass] = useState<Record<string, boolean>>({});
  const [savingSecret, setSavingSecret] = useState(false);

  // Sub-tab states for Databases & Add-ons
  const [dbName, setDbName] = useState("");
  const [dbList, setDbList] = useState<Array<{ id: string; name: string; status: string; connection_hint?: string }>>([]);
  const [provisioningDb, setProvisioningDb] = useState(false);
  const [addonsList, setAddonsList] = useState<ManagedAddon[]>([]);
  const [selectedAddonEngine, setSelectedAddonEngine] = useState("postgres");
  const [addonInstanceName, setAddonInstanceName] = useState("postgres-1");
  const [addonEnv, setAddonEnv] = useState("production");
  const [provisioningAddon, setProvisioningAddon] = useState(false);
  const [addonModalOpen, setAddonModalOpen] = useState(false);
  const [snippetAddon, setSnippetAddon] = useState<ManagedAddon | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Bulk Edit state for Secrets
  const [bulkSecretOpen, setBulkSecretOpen] = useState(false);
  const [bulkSecretText, setBulkSecretText] = useState("");
  const [bulkSecretSaving, setBulkSecretSaving] = useState(false);

  // Deployments state
  const [deploymentsList, setDeploymentsList] = useState<Deployment[]>([]);
  const [loadingDeployments, setLoadingDeployments] = useState(false);
  const [triggeringDeploy, setTriggeringDeploy] = useState(false);
  const [selectedDeployLogs, setSelectedDeployLogs] = useState<{
    deploymentId: string;
    buildId?: string;
    logs: string;
    loading: boolean;
  } | null>(null);
  const [explainText, setExplainText] = useState("");
  const [explainingDeployId, setExplainingDeployId] = useState<string | null>(null);

  // Builds & Runtime Logs state
  const [logsTab, setLogsTab] = useState<"builds" | "runtime">("runtime");
  const [buildsList, setBuildsList] = useState<Build[]>([]);
  const [selectedBuildId, setSelectedBuildId] = useState("");
  const [buildLogsText, setBuildLogsText] = useState("");
  const [runtimeInstances, setRuntimeInstances] = useState<RuntimeInstance[]>([]);
  const [runtimeLogs, setRuntimeLogs] = useState<LogEntry[]>([]);
  const [runtimeSourceFilter, setRuntimeSourceFilter] = useState("");
  const [runtimeSearchQuery, setRuntimeSearchQuery] = useState("");
  const [autoRefreshLogs, setAutoRefreshLogs] = useState(true);

  // Active tab from URL
  const currentTab = searchParams.get("tab") || "overview";

  // AI Ops State (Powered by Gemini 3.5 Flash Lite)
  const [aiAuditLoading, setAiAuditLoading] = useState(false);
  const [aiAuditResult, setAiAuditResult] = useState<{
    overview?: string;
    suggestions: string[];
    risk_level?: string;
    model?: string;
  } | null>(null);

  const [aiLogAnalysisLoading, setAiLogAnalysisLoading] = useState(false);
  const [aiLogAnalysisResult, setAiLogAnalysisResult] = useState<{
    summary?: string;
    analysis: string;
    recommendations?: string[];
    severity?: string;
    model?: string;
  } | null>(null);
  const [aiLogPanelOpen, setAiLogPanelOpen] = useState(false);

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

  async function loadDeployments(orgId: string, projId: string) {
    try {
      const list = await api.listDeployments(orgId, projId);
      setDeploymentsList(list);
    } catch {
      // ignore
    }
  }

  async function loadBuilds(orgId: string, projId: string) {
    try {
      const [list, deps] = await Promise.all([
        api.listBuilds(orgId, projId),
        api.listDeployments(orgId, projId).catch(() => [] as Deployment[]),
      ]);
      setBuildsList(list);
      if (deps.length > 0) {
        setDeploymentsList(deps);
      }
      if (list.length > 0) {
        // Priority 1: Match the currently active deployed build
        const activeDep = deps.find((d) => d.status === "ready");
        let targetId = "";
        if (selectedBuildId && list.some((b) => b.id === selectedBuildId)) {
          targetId = selectedBuildId;
        } else if (activeDep?.build_id && list.some((b) => b.id === activeDep.build_id)) {
          targetId = activeDep.build_id;
        } else {
          targetId = list[0].id;
        }

        if (targetId !== selectedBuildId) {
          setSelectedBuildId(targetId);
        }
        api.getBuildLogs(orgId, projId, targetId)
          .then((res) => setBuildLogsText(res.logs || "// No logs recorded for this build."))
          .catch(() => setBuildLogsText("// Failed to fetch build logs."));
      } else {
        setBuildLogsText("// No builds recorded yet. Trigger a deployment to view compiler output.");
      }
    } catch {
      setBuildLogsText("// Failed to load builds.");
    }
  }

  async function loadBuildLogs(orgId: string, projId: string, buildId: string) {
    try {
      const res = await api.getBuildLogs(orgId, projId, buildId);
      setBuildLogsText(res.logs || "// No logs recorded for this build.");
    } catch {
      setBuildLogsText("// Failed to fetch build logs.");
    }
  }

  async function loadRuntimeLogs(orgId: string, projId: string) {
    try {
      const [rtRes, logsRes, instRes] = await Promise.all([
        api.getRuntimeLogs(orgId, projId, 250).catch(() => null),
        api.queryLogs(orgId, projId, {
          source: runtimeSourceFilter || undefined,
          q: runtimeSearchQuery || undefined,
          limit: 150,
        }).catch(() => ({ entries: [] as LogEntry[] })),
        api.listRuntimeInstances(orgId, projId).catch(() => ({ instances: [] as RuntimeInstance[], mode: "simulate" })),
      ]);

      let logs: LogEntry[] = [];
      if (rtRes && Array.isArray(rtRes.entries) && rtRes.entries.length > 0) {
        let dockerLogs = rtRes.entries;
        if (runtimeSearchQuery) {
          const q = runtimeSearchQuery.toLowerCase();
          dockerLogs = dockerLogs.filter((l) => l.message.toLowerCase().includes(q));
        }
        if (runtimeSourceFilter && runtimeSourceFilter !== "app") {
          dockerLogs = dockerLogs.filter((l) => l.source === runtimeSourceFilter);
        }
        logs = dockerLogs;
      }

      if (logs.length === 0 && logsRes?.entries?.length) {
        logs = logsRes.entries;
      } else if (logs.length > 0 && logsRes?.entries?.length && !runtimeSourceFilter) {
        const seen = new Set(logs.map((l) => `${l.logged_at}-${l.message}`));
        for (const entry of logsRes.entries) {
          const key = `${entry.logged_at}-${entry.message}`;
          if (!seen.has(key)) {
            logs.push(entry);
            seen.add(key);
          }
        }
      }

      setRuntimeLogs(logs);

      let instances = instRes?.instances || [];
      if (instances.length === 0 && rtRes?.instance) {
        instances = [rtRes.instance];
      } else if (instances.length > 0 && rtRes?.instance) {
        instances = instances.map((inst) =>
          inst.id === rtRes.instance?.id ? { ...inst, ...rtRes.instance } : inst
        );
      }
      setRuntimeInstances(instances);
    } catch {
      // ignore
    }
  }

  async function handleTriggerDeployment() {
    if (!currentOrg || !actualProjectId) return;
    setTriggeringDeploy(true);
    setError("");
    setSuccess("");
    try {
      const repos = await api.listRepos(currentOrg.id, actualProjectId);
      const repo = repos[0];
      const dep = await api.createDeployment(currentOrg.id, actualProjectId, {
        git_branch: repo?.default_branch || "main",
        git_sha: "HEAD",
        clone_url: repo?.clone_url,
        full_name: repo?.full_name,
        repo_id: repo?.id,
        message: repo ? `Deploy from ${repo.full_name}@${repo.default_branch || "main"}` : "Manual deployment",
      });
      setSuccess("New deployment successfully queued!");
      await loadDeployments(currentOrg.id, actualProjectId);
      if (dep.build_id) {
        setSelectedDeployLogs({
          deploymentId: dep.id,
          buildId: dep.build_id,
          logs: "Build queued... Waiting for worker to start compilation.",
          loading: true,
        });
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to trigger deployment");
    } finally {
      setTriggeringDeploy(false);
    }
  }

  async function handleViewBuildLogs(d: Deployment) {
    if (!currentOrg || !actualProjectId) return;
    setSelectedDeployLogs({
      deploymentId: d.id,
      buildId: d.build_id,
      logs: "Loading compiler output...",
      loading: true,
    });
    if (d.build_id) {
      try {
        const res = await api.getBuildLogs(currentOrg.id, actualProjectId, d.build_id);
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

  async function handleExplainFailure(d: Deployment) {
    if (!currentOrg || !actualProjectId) return;
    setExplainingDeployId(d.id);
    setExplainText("");
    try {
      const res = await api.explainFailure(currentOrg.id, actualProjectId, {
        deployment_id: d.id,
        build_id: d.build_id,
        prompt: "Analyze the failure and explain the root cause and how to fix it.",
      });
      setExplainText(`[AI Analysis - ${res.mode}] ${res.explanation}`);
    } catch (err) {
      setExplainText(err instanceof ApiError ? err.message : "Failed to analyze error");
    } finally {
      setExplainingDeployId(null);
    }
  }

  async function handleRollback(deploymentId?: string) {
    if (!currentOrg || !actualProjectId) return;
    if (!window.confirm("Are you sure you want to rollback to this deployment?")) return;
    setError("");
    setSuccess("");
    try {
      await api.rollbackDeployment(currentOrg.id, actualProjectId, deploymentId);
      setSuccess("Rollback initiated. Live traffic will be routed to the selected image.");
      await loadDeployments(currentOrg.id, actualProjectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Rollback failed");
    }
  }

  async function runAiAudit() {
    if (!currentOrg || !actualProjectId) return;
    setAiAuditLoading(true);
    try {
      const res = await api.getOverviewSuggestions(currentOrg.id, actualProjectId);
      setAiAuditResult(res);
    } catch (err) {
      console.error("AI audit error:", err);
    } finally {
      setAiAuditLoading(false);
    }
  }

  async function runAiLogAnalysis(source: "runtime" | "build", buildId?: string) {
    if (!currentOrg || !actualProjectId) return;
    setAiLogAnalysisLoading(true);
    setAiLogPanelOpen(true);
    try {
      const res = await api.analyzeLogs(currentOrg.id, actualProjectId, {
        source,
        build_id: buildId || selectedBuildId || undefined,
        limit: 100,
      });
      setAiLogAnalysisResult(res);
    } catch (err) {
      console.error("AI log analysis error:", err);
    } finally {
      setAiLogAnalysisLoading(false);
    }
  }

  // Load tab-specific resources when switching tabs
  useEffect(() => {
    if (!currentOrg || !actualProjectId || actualProjectId === "default") return;

    if (currentTab === "overview") {
      loadDeployments(currentOrg.id, actualProjectId);
      loadBuilds(currentOrg.id, actualProjectId);
      loadRuntimeLogs(currentOrg.id, actualProjectId);
    } else if (currentTab === "deployments") {
      loadDeployments(currentOrg.id, actualProjectId);
      loadRuntimeLogs(currentOrg.id, actualProjectId);
      const cancelInterval = setVisibilityInterval(() => {
        loadDeployments(currentOrg.id, actualProjectId);
      }, 3000);
      return () => cancelInterval();
    } else if (currentTab === "builds") {
      loadDeployments(currentOrg.id, actualProjectId);
      loadBuilds(currentOrg.id, actualProjectId);
      loadRuntimeLogs(currentOrg.id, actualProjectId);
      const cancelInterval = setVisibilityInterval(() => {
        if (autoRefreshLogs) {
          if (logsTab === "builds") {
            loadBuilds(currentOrg.id, actualProjectId);
          } else {
            loadRuntimeLogs(currentOrg.id, actualProjectId);
          }
        }
      }, 3000);
      return () => cancelInterval();
    } else if (currentTab === "git") {
      api.listRepos(currentOrg.id, actualProjectId)
        .then((repos) => setConnectedRepos(repos))
        .catch(() => setConnectedRepos([]));
      setLoadingDeployKey(true);
      api.getDeployKey(currentOrg.id, actualProjectId)
        .then((k) => setDeployKey(k))
        .catch(() => setDeployKey(null))
        .finally(() => setLoadingDeployKey(false));
    } else if (currentTab === "domains") {
      Promise.all([
        api.listDomains(currentOrg.id, actualProjectId).catch(() => []),
        api.listCertificates(currentOrg.id, actualProjectId).catch(() => []),
      ]).then(([doms, certs]) => {
        setDomainsList(doms);
        setCertificatesList(certs);
      });
    } else if (currentTab === "secrets") {
      api.listSecrets(currentOrg.id, actualProjectId, secretEnv)
        .then((s) => setSecretsList(s))
        .catch(() => setSecretsList([]));
      api.listAddons(currentOrg.id, actualProjectId)
        .then((res) => setAddonsList(res.addons || []))
        .catch(() => setAddonsList([]));
    } else if (currentTab === "addons" || currentTab === "databases") {
      api.listAddons(currentOrg.id, actualProjectId)
        .then((res) => setAddonsList(res.addons || []))
        .catch(() => setAddonsList([]));
      api.listDatabases(currentOrg.id, actualProjectId)
        .then((res) => setDbList(res.databases || []))
        .catch(() => setDbList([]));
    }
  }, [currentOrg, actualProjectId, currentTab, secretEnv, logsTab, autoRefreshLogs, runtimeSourceFilter, runtimeSearchQuery]);

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

  // Real Git Connect Handler (Deploy Key SSH or PAT)
  async function handleConnectGit(e?: FormEvent, targetRepoUrl?: string, targetBranch = "main") {
    if (e) e.preventDefault();
    if (!currentOrg || !project) return;
    const inputUrl = (targetRepoUrl || gitUrl).trim();
    if (!inputUrl) return;

    setConnectingGit(true);
    setError("");
    setSuccess("");
    try {
      let repoName = inputUrl;
      let cloneUrl = inputUrl;

      if (inputUrl.startsWith("git@github.com:")) {
        repoName = inputUrl.replace(/^git@github\.com:/, "").replace(/\.git$/, "");
        cloneUrl = inputUrl.endsWith(".git") ? inputUrl : `${inputUrl}.git`;
      } else if (inputUrl.startsWith("ssh://git@github.com/")) {
        repoName = inputUrl.replace(/^ssh:\/\/git@github\.com\//, "").replace(/\.git$/, "");
        cloneUrl = `git@github.com:${repoName}.git`;
      } else if (inputUrl.startsWith("http://") || inputUrl.startsWith("https://")) {
        repoName = inputUrl.replace(/^https?:\/\/github\.com\//, "").replace(/\.git$/, "");
        if (gitAuthMethod === "deploy_key") {
          cloneUrl = `git@github.com:${repoName}.git`;
        } else {
          cloneUrl = inputUrl.endsWith(".git") ? inputUrl : `${inputUrl}.git`;
        }
      } else {
        repoName = inputUrl.replace(/\.git$/, "");
        if (gitAuthMethod === "deploy_key") {
          cloneUrl = `git@github.com:${repoName}.git`;
        } else {
          cloneUrl = `https://github.com/${repoName}.git`;
        }
      }

      await api.connectRepo(currentOrg.id, project.id, {
        full_name: repoName,
        clone_url: cloneUrl,
        default_branch: (targetBranch || gitBranch).trim() || "main",
        token: gitAuthMethod === "pat" ? gitToken.trim() || undefined : undefined,
        auth_type: gitAuthMethod,
      });

      setSuccess(`✓ Connected repository "${repoName}" successfully.`);
      setGitUrl("");
      setGitToken("");
      setPrivateTokenOpen(false);
      const updated = await api.listRepos(currentOrg.id, project.id);
      setConnectedRepos(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to connect repository");
    } finally {
      setConnectingGit(false);
    }
  }

  async function handleRotateDeployKey() {
    if (!currentOrg || !project) return;
    if (!window.confirm("Regenerating the deploy key will invalidate the current key. You will need to update the key on GitHub. Proceed?")) {
      return;
    }
    setGeneratingDeployKey(true);
    setError("");
    setSuccess("");
    try {
      const key = await api.generateDeployKey(currentOrg.id, project.id);
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

  // Disconnect / Remove Connected Repository
  async function handleDisconnectRepo(repoId: string, repoName: string) {
    if (!currentOrg || !project) return;
    if (!window.confirm(`Are you sure you want to disconnect repository "${repoName}" from this project?`)) {
      return;
    }
    setError("");
    setSuccess("");
    try {
      await api.disconnectRepo(currentOrg.id, project.id, repoId);
      setSuccess(`✓ Disconnected repository "${repoName}".`);
      const updated = await api.listRepos(currentOrg.id, project.id);
      setConnectedRepos(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to disconnect repository");
    }
  }

  // Quick Deploy from Connected Repo
  async function handleQuickDeployRepo(repo: ConnectedRepo) {
    if (!currentOrg || !project) return;
    setTriggeringDeploy(true);
    setError("");
    setSuccess("");
    try {
      await api.createDeployment(currentOrg.id, project.id, {
        full_name: repo.full_name,
        git_branch: repo.default_branch || "main",
        clone_url: repo.clone_url,
        repo_id: repo.id,
        git_sha: "HEAD",
        message: `Deploy from ${repo.full_name}@${repo.default_branch || "main"}`,
      });
      setSuccess(`🚀 Build & deployment triggered for ${repo.full_name}! View progress in Deployments.`);
      await loadDeployments(currentOrg.id, project.id);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to trigger deployment");
    } finally {
      setTriggeringDeploy(false);
    }
  }

  // Domain Handlers
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
        setSuccess(`Domain "${host}" connected and automatically verified with Traefik ingress & Let's Encrypt SSL!`);
      } catch {
        setSuccess(`Domain "${host}" added. Please complete the DNS setup steps below.`);
      }
      setDomainInput("");
      const [updatedDoms, updatedCerts] = await Promise.all([
        api.listDomains(currentOrg.id, project.id),
        api.listCertificates(currentOrg.id, project.id).catch(() => []),
      ]);
      setDomainsList(updatedDoms);
      setCertificatesList(updatedCerts);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add domain");
    } finally {
      setAddingDomain(false);
    }
  }

  async function handleVerifyDomain(domainId: string, force = false) {
    if (!currentOrg || !project) return;
    setVerifyingDomainId(domainId);
    setError("");
    setSuccess("");
    try {
      await api.verifyDomain(currentOrg.id, project.id, domainId, force);
      setSuccess(
        force
          ? "Domain auto-verified! Traefik reverse proxy routing and Let's Encrypt SSL certificates are now active."
          : "Domain DNS propagation check passed! Reverse proxy routing active."
      );
      const [updatedDoms, updatedCerts] = await Promise.all([
        api.listDomains(currentOrg.id, project.id),
        api.listCertificates(currentOrg.id, project.id).catch(() => []),
      ]);
      setDomainsList(updatedDoms);
      setCertificatesList(updatedCerts);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Verification check failed. Please verify your DNS records.");
    } finally {
      setVerifyingDomainId(null);
    }
  }

  async function handleConfirmDeleteDomain() {
    if (!currentOrg || !project || !domainToDelete) return;
    setDeletingDomain(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteDomain(currentOrg.id, project.id, domainToDelete.id);
      setSuccess(`Domain "${domainToDelete.hostname}" disconnected and Traefik ingress routes purged.`);
      setDomainToDelete(null);
      const updated = await api.listDomains(currentOrg.id, project.id);
      setDomainsList(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to remove domain");
    } finally {
      setDeletingDomain(false);
    }
  }

  function copyDnsRecord(text: string, key: string) {
    navigator.clipboard.writeText(text);
    setCopiedDnsKey(key);
    setTimeout(() => setCopiedDnsKey(null), 2000);
  }

  // Secret Add Handler
  async function handleSaveSecret(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project || !secretKey.trim() || !secretVal) return;
    setSavingSecret(true);
    setError("");
    setSuccess("");
    try {
      const targetScope = secretEnv === "all" ? newSecretEnv : secretEnv;
      await api.setSecret(currentOrg.id, project.id, targetScope, secretKey.trim(), secretVal);
      setSuccess(`Secret "${secretKey.trim()}" saved for ${targetScope.toUpperCase()}.`);
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

  async function handleDeleteSecret(secretName: string, env: string) {
    if (!currentOrg || !project) return;
    const ok = window.confirm(`Permanently delete variable "${secretName}" from ${env.toUpperCase()}?`);
    if (!ok) return;
    try {
      await api.deleteSecret(currentOrg.id, project.id, env, secretName);
      setSuccess(`Deleted variable "${secretName}".`);
      const updated = await api.listSecrets(currentOrg.id, project.id, secretEnv);
      setSecretsList(updated);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to delete secret");
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
    let url = addon.metadata?.internal_url ? String(addon.metadata.internal_url) : "";
    if (url) {
      if (addon.engine === "postgres" && url.includes("localhost:5432")) {
        url = url.replace("localhost:5432", "postgres:5432");
      } else if ((addon.engine === "redis" || addon.engine === "valkey") && url.includes("localhost:6379")) {
        url = url.replace("localhost:6379", "redis:6379");
      } else if (addon.engine === "mongodb" && url.includes("localhost:27017")) {
        url = url.replace("localhost:27017", "mongodb:27017");
      } else if ((addon.engine === "mysql" || addon.engine === "mariadb") && url.includes("localhost:3306")) {
        url = url.replace("localhost:3306", "mysql:3306");
      }
      return url;
    }
    const port = addon.metadata?.port || 8080;
    if (addon.engine === "redis" || addon.engine === "valkey") return `redis://redis:6379/0`;
    if (addon.engine === "postgres") return `postgres://jp:jp_dev_password@postgres:5432/jp`;
    if (addon.engine === "mysql" || addon.engine === "mariadb") return `mysql://jp:jp_dev_password@mysql:3306/jp`;
    if (addon.engine === "mongodb") return `mongodb://jp:jp_dev_password@mongodb:27017/jp`;
    return `http://addon-${addon.name}:${port}`;
  }

  function getAddonEnvSnippet(addon: ManagedAddon): string {
    const intUrl = getInternalUrl(addon);
    const engine = (addon.engine || "").toLowerCase();
    const primaryKey = addon.secret_ref || (
      engine === "postgres" ? "DATABASE_URL" :
      engine === "redis" || engine === "valkey" ? "REDIS_URL" :
      engine === "mongodb" ? "MONGODB_URI" :
      engine === "rabbitmq" ? "RABBITMQ_URL" :
      `${engine.toUpperCase()}_URL`
    );

    let snippet = `${primaryKey}=${intUrl}`;
    if (engine === "postgres" && primaryKey !== "DATABASE_URL") {
      snippet += `\nDATABASE_URL=${intUrl}`;
    }
    return snippet;
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

  const tabsList = [
    { id: "overview", label: "Overview", icon: "📊" },
    { id: "deployments", label: "Deployments", icon: "🚀" },
    { id: "builds", label: "Builds & Logs", icon: "🛠️" },
    { id: "git", label: "Git Repository", icon: "🐙" },
    { id: "domains", label: "Custom Domains", icon: "🌐" },
    { id: "secrets", label: "Secrets & Env", icon: "🔐" },
    { id: "addons", label: "Add-ons & DBs", icon: "🧩" },
    { id: "settings", label: "Settings", icon: "⚙️" },
  ];

  return (
    <div className="space-y-6">
      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* ========================================================
          TAB 1: OVERVIEW (Clean & Compact Dashboard)
          ======================================================== */}
      {currentTab === "overview" && (
        <div className="space-y-5 animate-fade-up">
          {/* Card 1: Runtime & Deployment Status */}
          {(() => {
            const latestDep = deploymentsList[0];
            const activeInst = runtimeInstances[0];
            const isReady = activeInst?.status === "running" || latestDep?.status === "ready";
            const isFailed = latestDep?.status === "failed";
            const isBuilding = latestDep?.status === "building" || latestDep?.status === "deploying";

            return (
              <div className="bg-white rounded-xl border-2 border-slate-300 p-5 shadow-sm space-y-4">
                <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 border-b border-slate-100 pb-3.5">
                  <div className="flex flex-wrap items-center gap-2.5">
                    <span
                      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-bold uppercase ${
                        isReady
                          ? "bg-emerald-50 text-emerald-800 border-2 border-emerald-300"
                          : isFailed
                          ? "bg-red-50 text-red-800 border-2 border-red-300"
                          : isBuilding
                          ? "bg-blue-50 text-blue-800 border-2 border-blue-300"
                          : "bg-slate-100 text-slate-800 border-2 border-slate-300"
                      }`}
                    >
                      <span
                        className={`h-2 w-2 rounded-full ${
                          isReady
                            ? "bg-emerald-500 animate-pulse"
                            : isFailed
                            ? "bg-red-500"
                            : "bg-blue-500 animate-pulse"
                        }`}
                      />
                      {isReady ? "Live Active" : latestDep?.status?.toUpperCase() || "Active"}
                    </span>

                    <span className="rounded-lg bg-slate-900 text-white font-mono font-bold px-2.5 py-1 text-xs flex items-center gap-1.5">
                      <span>🌱</span>
                      {latestDep?.git_branch || "main"}
                      {latestDep?.git_sha && (
                        <span className="text-slate-400">
                          @{latestDep.git_sha === "HEAD" ? "HEAD" : latestDep.git_sha.slice(0, 7)}
                        </span>
                      )}
                    </span>

                    {latestDep?.message && (
                      <span className="rounded-lg border-2 border-slate-200 bg-slate-50 px-2.5 py-0.5 text-xs font-medium text-slate-700 max-w-sm truncate" title={latestDep.message}>
                        🏷️ {latestDep.message}
                      </span>
                    )}
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    <Link
                      href={`/projects/${actualProjectId}?tab=builds`}
                      className="text-xs font-bold px-3 py-1.5 rounded-lg border-2 border-slate-300 bg-white hover:bg-slate-50 text-slate-700 shadow-2xs transition-all"
                    >
                      Build Logs
                    </Link>
                    <button
                      type="button"
                      onClick={handleTriggerDeployment}
                      disabled={triggeringDeploy}
                      className="text-xs font-bold px-3.5 py-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] text-white shadow-2xs transition-all disabled:opacity-50"
                    >
                      {triggeringDeploy ? "Deploying..." : "Trigger Deploy"}
                    </button>
                  </div>
                </div>

                {/* 4-Stat Metric Row */}
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                  <div className="rounded-lg border-2 border-slate-200 bg-slate-50/70 p-3">
                    <p className="text-[10px] font-bold uppercase tracking-wider text-slate-400">Container</p>
                    <p className="font-mono text-xs font-bold text-slate-900 truncate mt-0.5">
                      {activeInst?.container_name || activeInst?.container_id?.slice(0, 12) || "jp-active"}
                    </p>
                  </div>
                  <div className="rounded-lg border-2 border-slate-200 bg-slate-50/70 p-3">
                    <p className="text-[10px] font-bold uppercase tracking-wider text-slate-400">Internal Port</p>
                    <p className="font-mono text-xs font-bold text-emerald-700 mt-0.5">
                      {activeInst?.port || 8080}
                    </p>
                  </div>
                  <div className="rounded-lg border-2 border-slate-200 bg-slate-50/70 p-3">
                    <p className="text-[10px] font-bold uppercase tracking-wider text-slate-400">Docker Image</p>
                    <p className="font-mono text-xs font-bold text-slate-900 truncate mt-0.5">
                      {activeInst?.image_ref || latestDep?.image_ref || "latest"}
                    </p>
                  </div>
                  <div className="rounded-lg border-2 border-slate-200 bg-slate-50/70 p-3">
                    <p className="text-[10px] font-bold uppercase tracking-wider text-slate-400">Public Endpoint</p>
                    <div className="flex items-center justify-between gap-1 mt-0.5">
                      <span className="font-mono text-xs font-bold text-[#1E3A8A] truncate">
                        {appPublicUrl.replace(/^https?:\/\//, "")}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyToClipboard(appPublicUrl, "overview-url")}
                        className="text-slate-400 hover:text-slate-700 text-xs shrink-0 font-bold"
                        title="Copy URL"
                      >
                        {copiedKey === "overview-url" ? "✓" : "📋"}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            );
          })()}

          {/* Section 2: App & Environment Configuration (2-Column Grid) */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
            {/* Left Card: Application Specifications */}
            <div className="bg-white rounded-xl border-2 border-slate-300 p-5 shadow-sm space-y-3.5">
              <div className="flex items-center justify-between border-b border-slate-100 pb-2.5">
                <h3 className="text-sm font-extrabold text-[#0F1B3D]">Application Specifications</h3>
                <Link
                  href={`/projects/${actualProjectId}?tab=settings`}
                  className="text-xs font-bold text-[#1E3A8A] hover:underline"
                >
                  Edit Settings
                </Link>
              </div>

              <div className="space-y-2.5 text-xs">
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Project ID</span>
                  <div className="flex items-center gap-1.5 font-mono text-[11px] font-bold text-slate-800">
                    <span>{project.id.slice(0, 16)}...</span>
                    <button
                      type="button"
                      onClick={() => copyToClipboard(project.id, "overview-pid")}
                      className="text-slate-400 hover:text-slate-700"
                      title="Copy Project ID"
                    >
                      {copiedKey === "overview-pid" ? "✓" : "📋"}
                    </button>
                  </div>
                </div>
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Deployment Strategy</span>
                  <span className="font-bold text-slate-800">Rolling Zero-Downtime</span>
                </div>
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Isolation Scope</span>
                  <span className="font-bold text-slate-800">Private Docker Network Bridge</span>
                </div>
                <div className="flex items-center justify-between py-1">
                  <span className="text-slate-500 font-medium">SSL / TLS Protection</span>
                  <span className="font-bold text-emerald-700">Automatic Wildcard Let&apos;s Encrypt</span>
                </div>
              </div>
            </div>

            {/* Right Card: Connected Resources Overview */}
            <div className="bg-white rounded-xl border-2 border-slate-300 p-5 shadow-sm space-y-3.5">
              <div className="flex items-center justify-between border-b border-slate-100 pb-2.5">
                <h3 className="text-sm font-extrabold text-[#0F1B3D]">Connected Resources</h3>
              </div>

              <div className="space-y-2.5 text-xs">
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Git Repository</span>
                  {connectedRepos[0] ? (
                    <Link
                      href={`/projects/${actualProjectId}?tab=git`}
                      className="font-mono text-xs font-bold text-[#1E3A8A] hover:underline"
                    >
                      {connectedRepos[0].full_name}
                    </Link>
                  ) : (
                    <Link
                      href={`/projects/${actualProjectId}?tab=git`}
                      className="text-xs font-bold text-slate-400 hover:text-[#1E3A8A]"
                    >
                      + Connect Git
                    </Link>
                  )}
                </div>
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Custom Domains</span>
                  <Link
                    href={`/projects/${actualProjectId}?tab=domains`}
                    className="font-bold text-slate-800 hover:text-[#1E3A8A]"
                  >
                    {domainsList.length > 0 ? `${domainsList.length} Active` : "Default Subdomain Active"}
                  </Link>
                </div>
                <div className="flex items-center justify-between py-1 border-b border-slate-100">
                  <span className="text-slate-500 font-medium">Environment Secrets</span>
                  <Link
                    href={`/projects/${actualProjectId}?tab=secrets`}
                    className="font-bold text-slate-800 hover:text-[#1E3A8A]"
                  >
                    {secretsList.length > 0 ? `${secretsList.length} Variables Configured` : "None configured"}
                  </Link>
                </div>
                <div className="flex items-center justify-between py-1">
                  <span className="text-slate-500 font-medium">Add-ons & Databases</span>
                  <Link
                    href={`/projects/${actualProjectId}?tab=addons`}
                    className="font-bold text-slate-800 hover:text-[#1E3A8A]"
                  >
                    {addonsList.length > 0 ? `${addonsList.length} Connected Services` : "None attached"}
                  </Link>
                </div>
              </div>
            </div>
          </div>

          {/* Section 3: Google Gemini 3.5 Flash Lite Live Diagnostics */}
          <div className="bg-white rounded-xl border-2 border-slate-300 p-5 shadow-sm relative overflow-hidden transition-all space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
              <div className="flex items-center gap-3">
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[#1E3A8A] text-white text-base shadow-xs shrink-0">
                  ✨
                </span>
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-extrabold text-[#0F1B3D]">
                      Google Gemini 3.5 Flash Lite Diagnostics
                    </h3>
                    <span className="rounded-full bg-blue-50 px-2.5 py-0.5 text-[10px] font-bold text-[#1E3A8A] border-2 border-blue-200">
                      Active
                    </span>
                  </div>
                  <p className="text-xs text-slate-500 font-medium">
                    Automated architecture, security, and database connectivity audit.
                  </p>
                </div>
              </div>

              <button
                type="button"
                onClick={runAiAudit}
                disabled={aiAuditLoading}
                className="inline-flex items-center gap-2 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-4 py-2 text-xs font-bold text-white shadow-sm transition-all shrink-0 disabled:opacity-50"
              >
                <span>{aiAuditLoading ? "Auditing..." : "Run AI Audit"}</span>
                <span>↻</span>
              </button>
            </div>

            {aiAuditResult && (
              <div className="pt-3 animate-fade-up">
                <AIResponseView
                  content={[
                    aiAuditResult.overview,
                    aiAuditResult.suggestions && aiAuditResult.suggestions.length > 0
                      ? `### Recommended Architectural Fixes & Hardening\n` +
                        aiAuditResult.suggestions.map((sug, idx) => `${idx + 1}. ${sug}`).join("\n")
                      : "",
                  ]
                    .filter(Boolean)
                    .join("\n\n")}
                  title="Project Architecture & Reliability Audit"
                  model={aiAuditResult.model || "gemini-3.5-flash-lite"}
                  riskLevel={aiAuditResult.risk_level}
                />
              </div>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 2: DEPLOYMENTS
          ======================================================== */}
      {currentTab === "deployments" && (
        <div className="space-y-6">
          {/* Deploy Trigger Card */}
          <div className="jp-card p-5 flex flex-wrap items-center justify-between gap-4">
            <div>
              <h2 className="text-sm font-bold text-[var(--ink)]">Deploy Application</h2>
              <p className="text-xs text-[var(--ink-muted)]">
                Queue a new build and rolling update for {project.name}.
                {connectedRepos.length > 0 && (
                  <span className="ml-1 text-emerald-600 font-semibold">
                    (Linked: <code className="font-mono">{connectedRepos[0].full_name}</code>)
                  </span>
                )}
              </p>
            </div>
            <div className="flex items-center gap-2">
              <Button
                onClick={handleTriggerDeployment}
                disabled={triggeringDeploy}
                className="font-bold shadow-xs"
              >
                {triggeringDeploy ? "Queueing Build..." : "⚡ Trigger New Deployment"}
              </Button>
            </div>
          </div>

          {/* AI Explanation Banner (if open) */}
          {explainText && (
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
          )}

          {/* Deployment History Table */}
          {deploymentsList.length === 0 ? (
            <EmptyState
              title="No deployments yet"
              description={`No builds or deployments have been executed for ${project.name} yet. Click "Trigger New Deployment" above or connect a Git repository.`}
              action={
                <Button onClick={handleTriggerDeployment} disabled={triggeringDeploy}>
                  ⚡ Trigger First Deployment
                </Button>
              }
            />
          ) : (
            <div className="jp-card overflow-hidden">
              <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    All Deployments ({deploymentsList.length})
                  </h3>
                  <span className="flex items-center gap-1 text-[11px] font-semibold text-emerald-700 bg-emerald-100/60 px-2 py-0.5 rounded-full">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                    Live
                  </span>
                </div>
                <button
                  type="button"
                  onClick={() => currentOrg && loadDeployments(currentOrg.id, actualProjectId)}
                  className="text-xs text-[var(--accent)] font-bold hover:underline"
                >
                  ↻ Refresh Now
                </button>
              </div>

              <div className="divide-y divide-[var(--border)]">
                {deploymentsList.map((d) => {
                  const activeDeployment = deploymentsList.find((dep) => dep.status === "ready");
                  const isCurrentLive = activeDeployment?.id === d.id;
                  const isReady = d.status === "ready";
                  const isFailed = d.status === "failed";
                  const isBuilding = d.status === "building" || d.status === "deploying";
                  const targetUrl = d.preview_url || appPublicUrl;

                  return (
                    <div
                      key={d.id}
                      className={`p-4.5 transition-all space-y-3.5 ${
                        isCurrentLive
                          ? "bg-emerald-50/60 border-2 border-emerald-400 rounded-xl shadow-xs my-2"
                          : "hover:bg-slate-50/50"
                      }`}
                    >
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        {/* Status + Source */}
                        <div className="flex flex-wrap items-center gap-2.5">
                          {isCurrentLive ? (
                            <span className="inline-flex items-center gap-1.5 rounded-full border-2 border-emerald-500 bg-emerald-100 px-3 py-1 text-xs font-black text-emerald-950 shadow-2xs">
                              <span className="h-2 w-2 rounded-full bg-emerald-600 animate-pulse" />
                              CURRENTLY LIVE & DEPLOYED
                            </span>
                          ) : (
                            <span
                              className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-bold ${
                                isReady
                                  ? "border-slate-300 bg-slate-100 text-slate-700"
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
                                    ? "bg-slate-400"
                                    : isFailed
                                    ? "bg-red-600"
                                    : isBuilding
                                    ? "bg-blue-600 animate-pulse"
                                    : "bg-slate-500"
                                }`}
                              />
                              {isReady ? "PREVIOUS BUILD" : d.status.toUpperCase()}
                            </span>
                          )}

                          {d.build_id && (
                            <span className="rounded bg-slate-900 text-white font-mono font-bold px-2 py-0.5 text-[10px]">
                              Build #{d.build_id.slice(0, 8)}
                            </span>
                          )}

                          <span className="rounded bg-slate-100 border border-slate-200 px-2 py-0.5 text-[10px] font-bold uppercase text-slate-700 font-mono">
                            {d.source || "manual"}
                          </span>

                          <span className="text-xs text-[var(--ink-muted)]">
                            {d.created_at ? new Date(d.created_at).toLocaleString() : "Recently"}
                          </span>
                        </div>

                        {/* Action Buttons */}
                        <div className="flex items-center gap-2">
                          <Button
                            type="button"
                            variant="secondary"
                            size="sm"
                            onClick={() => handleViewBuildLogs(d)}
                            className="text-xs font-semibold"
                          >
                            📋 View Build Logs
                          </Button>

                          {isFailed && (
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              onClick={() => handleExplainFailure(d)}
                              disabled={explainingDeployId === d.id}
                              className="text-xs font-semibold text-amber-700 border-amber-300 hover:bg-amber-50"
                            >
                              {explainingDeployId === d.id ? "Analyzing..." : "🤖 Explain Issue"}
                            </Button>
                          )}

                          {isReady && !isCurrentLive && (
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              onClick={() => handleRollback(d.id)}
                              className="text-xs font-semibold"
                            >
                              ↺ Rollback to this
                            </Button>
                          )}

                          {isCurrentLive && (
                            <span className="inline-flex items-center gap-1 text-xs font-bold text-emerald-800 bg-emerald-100 border border-emerald-300 rounded-md px-2.5 py-1">
                              ✓ Serving Live Traffic
                            </span>
                          )}
                        </div>
                      </div>

                      {/* Public HTTPS Address & Git info bar */}
                      <div className="grid gap-2 sm:grid-cols-2 text-xs">
                        {/* Domain address */}
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
                                {isBuilding ? "Deploying to " + targetUrl : "Offline (Build " + d.status + ")"}
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

                        {/* Git Commit & Branch */}
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
                              <strong className="font-bold text-red-950 block">Deployment Issue Detected:</strong>
                              <p className="font-mono text-[11px] text-red-800 mt-0.5 break-all">
                                {d.error || "Build step failed or container did not pass health check."}
                              </p>
                            </div>
                          </div>
                          <button
                            type="button"
                            onClick={() => handleViewBuildLogs(d)}
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
        </div>
      )}

      {/* ========================================================
          TAB 3: GIT REPOSITORIES & SOURCE CONTROL
          ======================================================== */}
      {currentTab === "git" && (
        <div className="space-y-6">
          {/* Active Connected Repositories */}
          <div className="jp-card p-5 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-3">
              <div>
                <h2 className="text-sm font-bold text-[var(--ink)]">
                  Connected Source Code ({connectedRepos.length})
                </h2>
                <p className="text-xs text-[var(--ink-muted)]">
                  Git repositories connected to <strong>{project.name}</strong> for automated builds and deployment tracking.
                </p>
              </div>
            </div>

            {connectedRepos.length === 0 ? (
              <div className="rounded-xl border-2 border-dashed border-slate-300 p-8 text-center space-y-2 bg-slate-50/50">
                <span className="text-3xl">🐙</span>
                <p className="text-xs font-bold text-[var(--ink)]">
                  No repository linked to this project yet.
                </p>
                <p className="text-xs text-[var(--ink-muted)] max-w-sm mx-auto">
                  Connect any public GitHub repository below in one click without any token, or paste your repository URL.
                </p>
              </div>
            ) : (
              <ul className="divide-y divide-[var(--border)] rounded-xl border border-[var(--border)] bg-white overflow-hidden shadow-2xs">
                {connectedRepos.map((r) => {
                  const cleanClone = r.clone_url?.replace(/:[^@]+@/, ":***@") || `https://github.com/${r.full_name}.git`;
                  const githubUrl = `https://github.com/${r.full_name}`;

                  return (
                    <li
                      key={r.id}
                      className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 p-4 text-xs hover:bg-slate-50/70 transition-colors"
                    >
                      <div className="space-y-1">
                        <div className="flex items-center gap-2">
                          <span className="text-base">🐙</span>
                          <span className="font-mono font-bold text-[var(--ink)] text-sm">
                            {r.full_name}
                          </span>
                          <span className="rounded bg-blue-50 border border-blue-200 px-2 py-0.5 font-mono text-[10px] font-bold text-blue-700">
                            {r.default_branch || "main"}
                          </span>
                          <a
                            href={githubUrl}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="text-[11px] text-blue-600 hover:text-blue-800 font-semibold inline-flex items-center gap-0.5 hover:underline"
                          >
                            <span>GitHub</span>
                            <span>↗</span>
                          </a>
                        </div>
                        <p className="font-mono text-[11px] text-[var(--ink-muted)] truncate max-w-lg">
                          {cleanClone}
                        </p>
                      </div>

                      <div className="flex items-center gap-2 shrink-0">
                        <Button
                          type="button"
                          size="sm"
                          disabled={triggeringDeploy}
                          onClick={() => handleQuickDeployRepo(r)}
                          className="bg-emerald-600 hover:bg-emerald-700 text-white font-bold text-xs shadow-xs"
                        >
                          {triggeringDeploy ? "Queuing..." : "🚀 Deploy Now"}
                        </Button>
                        <Button
                          type="button"
                          variant="danger"
                          size="sm"
                          onClick={() => handleDisconnectRepo(r.id, r.full_name)}
                          className="text-xs"
                        >
                          Disconnect
                        </Button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {/* Direct Repository Connect */}
          <div className="jp-card p-5 space-y-5">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-3">
              <div>
                <h2 className="text-sm font-bold text-[var(--ink)]">
                  Connect GitHub Repository
                </h2>
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

                <form onSubmit={handleConnectGit} className="space-y-3.5 pt-1">
                  <div className="grid gap-3 sm:grid-cols-3">
                    <div className="sm:col-span-2">
                      <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                        Repository (owner/repo or git@github.com:owner/repo.git)
                      </label>
                      <Input
                        placeholder="e.g. owner/repo or git@github.com:owner/repo.git"
                        value={gitUrl}
                        onChange={(e) => setGitUrl(e.target.value)}
                        required
                      />
                    </div>
                    <div>
                      <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                        Default Branch
                      </label>
                      <Input
                        placeholder="main"
                        value={gitBranch}
                        onChange={(e) => setGitBranch(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className="pt-1 flex items-center gap-3">
                    <Button
                      type="submit"
                      disabled={connectingGit || !gitUrl.trim()}
                      className="font-bold text-xs bg-slate-900 hover:bg-slate-800 text-white"
                    >
                      {connectingGit ? "Connecting..." : "➕ Connect Repository with Deploy Key"}
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
              <form onSubmit={handleConnectGit} className="space-y-3.5 pt-1">
                <div className="grid gap-3 sm:grid-cols-3">
                  <div className="sm:col-span-2">
                    <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                      Repository URL or owner/repo
                    </label>
                    <Input
                      placeholder="e.g. zeeshan-890/financor or https://github.com/..."
                      value={gitUrl}
                      onChange={(e) => setGitUrl(e.target.value)}
                      required
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                      Default Branch
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
                    Personal Access Token (PAT)
                  </label>
                  <Input
                    type="password"
                    placeholder="ghp_... or github_pat_... (leave blank for public repos)"
                    value={gitToken}
                    onChange={(e) => setGitToken(e.target.value)}
                  />
                  <p className="mt-1 text-[11px] text-[var(--ink-muted)]">
                    Classic or fine-grained token with <code className="font-mono font-bold">repo</code> read access.
                  </p>
                </div>

                <div className="pt-1">
                  <Button
                    type="submit"
                    disabled={connectingGit || !gitUrl.trim()}
                    className="font-bold text-xs"
                  >
                    {connectingGit ? "Connecting..." : "➕ Connect Repository with PAT"}
                  </Button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          TAB 4: BUILDS & LOGS
          ======================================================== */}
      {currentTab === "builds" && (
        <div className="space-y-6">
          {/* Top Segmented Controls: Container vs Compiler (Defaults to Container Logs) */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center rounded-lg border border-[var(--border)] bg-white p-1 shadow-2xs text-xs">
              <button
                type="button"
                onClick={() => setLogsTab("runtime")}
                className={`px-3 py-1.5 font-bold rounded-md transition-all ${
                  logsTab === "runtime"
                    ? "bg-[var(--accent)] text-white shadow-2xs"
                    : "text-[var(--ink-secondary)] hover:text-[var(--ink)]"
                }`}
              >
                🟢 Running App Container Logs
              </button>
              <button
                type="button"
                onClick={() => setLogsTab("builds")}
                className={`px-3 py-1.5 font-bold rounded-md transition-all ${
                  logsTab === "builds"
                    ? "bg-[var(--accent)] text-white shadow-2xs"
                    : "text-[var(--ink-secondary)] hover:text-[var(--ink)]"
                }`}
              >
                🏗️ Compiler & Build Stream
              </button>
            </div>

            <div className="flex items-center gap-2">
              <label className="flex items-center gap-1.5 text-xs text-[var(--ink-secondary)] font-semibold cursor-pointer">
                <input
                  type="checkbox"
                  checked={autoRefreshLogs}
                  onChange={(e) => setAutoRefreshLogs(e.target.checked)}
                  className="rounded border-[var(--border)]"
                />
                Live Streaming {autoRefreshLogs ? "Active" : "Paused"}
              </label>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  if (currentOrg) {
                    if (logsTab === "builds") loadBuilds(currentOrg.id, actualProjectId);
                    else loadRuntimeLogs(currentOrg.id, actualProjectId);
                  }
                }}
                className="text-xs"
              >
                ↻ Refresh
              </Button>
            </div>
          </div>

          {/* Subview 1: Compiler & Build Stream */}
          {logsTab === "builds" && (
            <div className="space-y-4">
              <div className="jp-card p-4 flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-2.5">
                  <label className="text-xs font-bold uppercase tracking-wider text-[var(--ink-muted)]">
                    Select Build:
                  </label>
                  <select
                    className="rounded-md border border-[var(--border)] bg-white px-3 py-1.5 text-xs font-semibold text-[var(--ink)] shadow-2xs focus:border-[var(--accent)] focus:outline-none max-w-md"
                    value={selectedBuildId}
                    onChange={(e) => {
                      setSelectedBuildId(e.target.value);
                      if (currentOrg) loadBuildLogs(currentOrg.id, actualProjectId, e.target.value);
                    }}
                  >
                    {buildsList.map((b) => {
                      const activeDep = deploymentsList.find((d) => d.status === "ready");
                      const isActiveLive = activeDep?.build_id === b.id;
                      return (
                        <option key={b.id} value={b.id}>
                          {isActiveLive ? "🟢 [ACTIVE LIVE BUILD] " : `[${b.status.toUpperCase()}] `}
                          #{b.id.slice(0, 8)}
                          {b.framework ? ` (${b.framework})` : ""}
                          {b.git_sha ? ` @${b.git_sha.slice(0, 7)}` : ""}
                          {b.created_at ? ` - ${new Date(b.created_at).toLocaleTimeString()}` : ""}
                          {isActiveLive ? " (Currently Deployed)" : ""}
                        </option>
                      );
                    })}
                    {buildsList.length === 0 && <option value="">No builds recorded</option>}
                  </select>

                  {(() => {
                    const activeDep = deploymentsList.find((d) => d.status === "ready");
                    const isViewingActive = activeDep?.build_id === selectedBuildId;
                    if (isViewingActive) {
                      return (
                        <span className="inline-flex items-center gap-1.5 rounded-full border-2 border-emerald-400 bg-emerald-50 px-2.5 py-0.5 text-[11px] font-bold text-emerald-800">
                          <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                          Default: Active Deployed Build
                        </span>
                      );
                    }
                    return null;
                  })()}
                </div>

                {selectedBuildId && (
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => runAiLogAnalysis("build", selectedBuildId)}
                      disabled={aiLogAnalysisLoading}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-indigo-200 bg-gradient-to-r from-blue-50 to-indigo-50 px-3 py-1 text-xs font-bold text-indigo-700 shadow-2xs hover:from-blue-100 hover:to-indigo-100 transition-all"
                    >
                      <span>✨</span>
                      <span>{aiLogAnalysisLoading ? "Analyzing..." : "AI Analyze Build"}</span>
                    </button>
                    <button
                      type="button"
                      onClick={() => copyToClipboard(buildLogsText, "build-logs")}
                      className="text-xs font-semibold px-2.5 py-1 rounded border border-[var(--border)] bg-white hover:bg-slate-50 text-[var(--ink)]"
                    >
                      {copiedKey === "build-logs" ? "✓ Copied Logs" : "📋 Copy Logs"}
                    </button>
                  </div>
                )}
              </div>

              {/* ✨ Google Gemini 3.5 Flash Lite Build Diagnostic Panel */}
              {aiLogPanelOpen && aiLogAnalysisResult && (
                <div className="relative animate-fade-up">
                  <button
                    type="button"
                    onClick={() => setAiLogPanelOpen(false)}
                    className="absolute top-3 right-3 z-10 rounded-md bg-white/90 px-2 py-1 text-slate-500 hover:bg-slate-100 hover:text-slate-800 text-[11px] font-bold border border-slate-200 shadow-2xs transition-all"
                  >
                    ✕ Close
                  </button>
                  <AIResponseView
                    content={[
                      aiLogAnalysisResult.summary ? `### Executive Summary\n${aiLogAnalysisResult.summary}` : "",
                      aiLogAnalysisResult.analysis ? `### Diagnostic Analysis & Root Cause\n${aiLogAnalysisResult.analysis}` : "",
                      aiLogAnalysisResult.recommendations && aiLogAnalysisResult.recommendations.length > 0
                        ? `### Actionable Remediation Steps\n` +
                          aiLogAnalysisResult.recommendations.map((rec, idx) => `${idx + 1}. ${rec}`).join("\n")
                        : "",
                    ]
                      .filter(Boolean)
                      .join("\n\n")}
                    title="Google Gemini 3.5 — Build & Error Analysis"
                    model="gemini-3.5-flash-lite"
                    riskLevel={aiLogAnalysisResult.severity}
                  />
                </div>
              )}

              <div className="jp-card overflow-hidden">
                <div className="border-b border-slate-800 bg-slate-900 px-4 py-3 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="h-2 w-2 rounded-full bg-emerald-400" />
                    <span className="font-mono text-xs font-bold text-slate-200">
                      Compiler Terminal & Live Build Stream
                    </span>
                  </div>
                  <span className="rounded bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-emerald-400">
                    App: {project.name}
                  </span>
                </div>
                <pre className="h-96 overflow-auto bg-slate-950 p-4 font-mono text-xs text-emerald-400 leading-relaxed shadow-inner whitespace-pre-wrap">
                  {buildLogsText || `// No build output yet. Trigger a deployment to view live compiler steps.`}
                </pre>
              </div>
            </div>
          )}

          {/* Subview 2: Running App Container Logs */}
          {logsTab === "runtime" && (
            <div className="space-y-4">
              {/* Container & Latest Deployment Status Banner */}
              {(() => {
                const latestDep = deploymentsList[0];
                const activeInst = runtimeInstances[0];
                const latestBld = buildsList[0];
                const isReady = activeInst?.status === "running" || latestDep?.status === "ready";
                const isFailed = latestDep?.status === "failed";
                const isBuilding = latestDep?.status === "building" || latestDep?.status === "deploying";

                return (
                  <div className={`jp-card p-4 border transition-all ${
                    isReady 
                      ? "border-emerald-500/30 bg-gradient-to-r from-emerald-50/60 to-white" 
                      : isFailed 
                      ? "border-red-500/30 bg-red-50/40" 
                      : "border-blue-500/30 bg-blue-50/40"
                  }`}>
                    <div className="flex flex-wrap items-center justify-between gap-4">
                      {/* Left: Instance and Deployment Tag Badges */}
                      <div className="space-y-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-xl">🐳</span>
                          <h4 className="text-xs font-bold uppercase tracking-wider text-slate-900">
                            Live Container Instance & Latest Deployment
                          </h4>
                          
                          {/* Live Status Badge */}
                          <span
                            className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[11px] font-bold uppercase ${
                              isReady
                                ? "bg-emerald-100 text-emerald-800 border border-emerald-300"
                                : isFailed
                                ? "bg-red-100 text-red-800 border border-red-300"
                                : isBuilding
                                ? "bg-blue-100 text-blue-800 border border-blue-300"
                                : "bg-slate-200 text-slate-800"
                            }`}
                          >
                            <span
                              className={`h-1.5 w-1.5 rounded-full ${
                                isReady
                                  ? "bg-emerald-600 animate-pulse"
                                  : isFailed
                                  ? "bg-red-600"
                                  : "bg-blue-600 animate-pulse"
                              }`}
                            />
                            {isReady ? "LIVE / RUNNING" : latestDep?.status?.toUpperCase() || "ACTIVE"}
                          </span>

                          {/* Git Branch & Tag Badge */}
                          <span className="rounded bg-slate-900 text-white font-mono font-bold px-2 py-0.5 text-[10px] flex items-center gap-1">
                            <span>🌱</span>
                            {latestDep?.git_branch || "main"}
                            {latestDep?.git_sha && (
                              <span className="text-slate-400">
                                @{latestDep.git_sha === "HEAD" ? "HEAD" : latestDep.git_sha.slice(0, 7)}
                              </span>
                            )}
                          </span>

                          {/* Deployment Tag / Commit Message Badge */}
                          {latestDep?.message && (
                            <span className="rounded border border-slate-200 bg-white px-2 py-0.5 text-[10px] font-medium text-slate-600 max-w-xs truncate" title={latestDep.message}>
                              🏷️ {latestDep.message}
                            </span>
                          )}
                        </div>

                        {/* Detailed Container and Network Tags */}
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] font-mono text-slate-600">
                          <span className="flex items-center gap-1">
                            <strong className="text-slate-800">Container:</strong>
                            <code className="bg-slate-100 px-1.5 py-0.5 rounded text-slate-900 border border-slate-200">
                              {activeInst?.container_name || activeInst?.container_id?.slice(0, 12) || "jp-active"}
                            </code>
                          </span>
                          <span className="text-slate-300">•</span>
                          <span className="flex items-center gap-1">
                            <strong className="text-slate-800">Image:</strong>
                            <code className="bg-slate-100 px-1.5 py-0.5 rounded text-slate-900 border border-slate-200">
                              {activeInst?.image_ref || latestDep?.image_ref || "latest"}
                            </code>
                          </span>
                          <span className="text-slate-300">•</span>
                          <span className="flex items-center gap-1">
                            <strong className="text-slate-800">Port:</strong>
                            <span className="text-emerald-700 font-bold">
                              {activeInst?.port || 8080} (HTTP/HTTPS)
                            </span>
                          </span>
                          {latestBld && (
                            <>
                              <span className="text-slate-300">•</span>
                              <span className="flex items-center gap-1">
                                <strong className="text-slate-800">Build:</strong>
                                <span>{latestBld.id.slice(0, 8)} ({latestBld.framework || "docker"})</span>
                              </span>
                            </>
                          )}
                        </div>
                      </div>

                      {/* Right Action: Open Live App */}
                      <div className="flex items-center gap-2 shrink-0">
                        <a
                          href={appPublicUrl}
                          target="_blank"
                          rel="noreferrer"
                          className="flex items-center gap-1.5 text-xs font-bold px-3.5 py-2 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white shadow-2xs transition-colors"
                        >
                          <span>Open Live App</span>
                          <span>↗</span>
                        </a>
                      </div>
                    </div>
                  </div>
                );
              })()}

              {/* Filter and Search Bar */}
              <div className="jp-card p-4 flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-3">
                  <label className="flex items-center gap-1.5 text-xs text-[var(--ink-secondary)] font-semibold">
                    <span>Source:</span>
                    <select
                      className="rounded-md border border-[var(--border)] bg-white px-2.5 py-1 text-xs font-bold text-[var(--ink)] shadow-2xs"
                      value={runtimeSourceFilter}
                      onChange={(e) => setRuntimeSourceFilter(e.target.value)}
                    >
                      <option value="">All Sources</option>
                      <option value="runtime">Runtime Engine</option>
                      <option value="app">App Stdout/Stderr</option>
                      <option value="build">Build Logs</option>
                    </select>
                  </label>

                  <Input
                    placeholder="Filter logs by keyword..."
                    value={runtimeSearchQuery}
                    onChange={(e) => setRuntimeSearchQuery(e.target.value)}
                    className="w-56 text-xs h-8"
                  />
                </div>

                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => runAiLogAnalysis("runtime")}
                    disabled={aiLogAnalysisLoading}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-indigo-200 bg-gradient-to-r from-blue-50 to-indigo-50 px-3 py-1 text-xs font-bold text-indigo-700 shadow-2xs hover:from-blue-100 hover:to-indigo-100 transition-all"
                  >
                    <span>✨</span>
                    <span>{aiLogAnalysisLoading ? "Analyzing Logs..." : "AI Analyze Logs (Gemini 3.5)"}</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      const allText = runtimeLogs.map((l) => `[${l.logged_at}] [${l.source}/${l.level}] ${l.message}`).join("\n");
                      copyToClipboard(allText, "app-logs");
                    }}
                    className="text-xs font-semibold px-2.5 py-1 rounded border border-[var(--border)] bg-white hover:bg-slate-50 text-[var(--ink)]"
                  >
                    {copiedKey === "app-logs" ? "✓ Copied Logs" : "📋 Copy All Logs"}
                  </button>
                </div>
              </div>

              {/* ✨ Google Gemini 3.5 Flash Lite Runtime Diagnostic Panel */}
              {aiLogPanelOpen && aiLogAnalysisResult && (
                <div className="relative animate-fade-up">
                  <button
                    type="button"
                    onClick={() => setAiLogPanelOpen(false)}
                    className="absolute top-3 right-3 z-10 rounded-md bg-white/90 px-2 py-1 text-slate-500 hover:bg-slate-100 hover:text-slate-800 text-[11px] font-bold border border-slate-200 shadow-2xs transition-all"
                  >
                    ✕ Close
                  </button>
                  <AIResponseView
                    content={[
                      aiLogAnalysisResult.summary ? `### Executive Summary\n${aiLogAnalysisResult.summary}` : "",
                      aiLogAnalysisResult.analysis ? `### Container Runtime Diagnostic\n${aiLogAnalysisResult.analysis}` : "",
                      aiLogAnalysisResult.recommendations && aiLogAnalysisResult.recommendations.length > 0
                        ? `### Diagnostic Steps & Container Recommendations\n` +
                          aiLogAnalysisResult.recommendations.map((rec, idx) => `${idx + 1}. ${rec}`).join("\n")
                        : "",
                    ]
                      .filter(Boolean)
                      .join("\n\n")}
                    title="Google Gemini 3.5 — Container Runtime Log Diagnostic"
                    model="gemini-3.5-flash-lite"
                    riskLevel={aiLogAnalysisResult.severity}
                  />
                </div>
              )}

              {/* Terminal Logs View */}
              <div className="jp-card overflow-hidden">
                <div className="border-b border-slate-800 bg-slate-900 px-4 py-3 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="h-2 w-2 rounded-full bg-emerald-400 animate-pulse" />
                    <span className="font-mono text-xs font-bold text-slate-200">
                      Live Running Application Logs
                    </span>
                  </div>
                  <span className="font-mono text-[10px] text-slate-400">
                    {runtimeLogs.length} events logged
                  </span>
                </div>
                {runtimeLogs.length === 0 ? (
                  <div className="h-80 flex flex-col items-center justify-center bg-slate-950 p-6 text-center text-xs text-slate-500 font-mono">
                    <p className="mb-2">No application log events received yet.</p>
                    <p className="text-[11px] text-slate-600">
                      When your running container emits stdout or stderr, logs will automatically stream into this terminal.
                    </p>
                  </div>
                ) : (
                  <div className="h-96 overflow-auto bg-slate-950 p-4 font-mono text-xs text-slate-200 leading-relaxed space-y-1">
                    {runtimeLogs.map((log, idx) => {
                      const isErr = log.level === "error" || log.message.toLowerCase().includes("error");
                      const isWarn = log.level === "warn" || log.message.toLowerCase().includes("warn");
                      return (
                        <div key={log.id ? `${log.id}-${idx}` : `${log.logged_at}-${idx}`} className="flex items-start gap-2 hover:bg-slate-900/60 py-0.5 rounded px-1">
                          <span className="text-slate-500 shrink-0 select-none text-[10px]">
                            {log.logged_at ? new Date(log.logged_at).toLocaleTimeString() : "--:--:--"}
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
                            {log.source}/{log.level}
                          </span>
                          <span className={`break-all ${isErr ? "text-red-300 font-semibold" : isWarn ? "text-amber-300" : "text-emerald-400"}`}>
                            {log.message}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      )}

      {/* ========================================================
          TAB 5: CUSTOM DOMAINS, DNS SETUP & SSL INGRESS
          ======================================================== */}
      {currentTab === "domains" && (
        <div className="space-y-6 animate-fade-up">
          {/* Header Info Banner */}
          <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 rounded-xl border-2 border-slate-200 bg-white p-5 shadow-xs">
            <div>
              <div className="flex items-center gap-2">
                <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-blue-600 text-white text-xs font-black shadow-xs">
                  🌐
                </span>
                <h2 className="text-sm font-bold text-[#0F1B3D]">
                  Custom Domains & SSL Ingress for {project.name}
                </h2>
              </div>
              <p className="mt-1 text-xs text-slate-500 font-medium">
                Route production traffic directly to this container with automatic Let&apos;s Encrypt TLS 1.3 certificates and Traefik reverse proxying.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <span className="rounded-full bg-emerald-50 border border-emerald-300 px-3 py-1 text-xs font-bold text-emerald-800 flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-emerald-600 animate-pulse" />
                Let&apos;s Encrypt Auto-SSL Active
              </span>
            </div>
          </div>

          {/* 3-Step Setup Guide */}
          <div className="rounded-xl border-2 border-slate-200 bg-white p-5 shadow-xs space-y-4">
            <div className="border-b border-slate-100 pb-3">
              <span className="inline-block text-[10px] font-extrabold uppercase tracking-wider text-blue-700 bg-blue-50 px-2 py-0.5 rounded-md mb-1">
                Setup Instructions
              </span>
              <h3 className="text-sm font-bold text-[#0F1B3D]">
                Step 1: Configure DNS at Your Domain Registrar
              </h3>
              <p className="text-xs text-slate-500 font-medium mt-0.5">
                Log in to Cloudflare, GoDaddy, Namecheap, Route53, or your DNS manager and add one of the following records:
              </p>
            </div>

            {/* DNS Records Table */}
            <div className="overflow-x-auto rounded-lg border border-slate-200">
              <table className="w-full text-left text-xs">
                <thead>
                  <tr className="bg-slate-50 border-b border-slate-200 text-[11px] font-extrabold uppercase tracking-wider text-slate-600">
                    <th className="px-4 py-2.5">Domain Scenario</th>
                    <th className="px-4 py-2.5">Record Type</th>
                    <th className="px-4 py-2.5">Host / Name</th>
                    <th className="px-4 py-2.5">Target / IP Value</th>
                    <th className="px-4 py-2.5">Recommended TTL</th>
                    <th className="px-4 py-2.5 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 font-medium text-slate-700">
                  <tr className="hover:bg-blue-50/30 transition-colors">
                    <td className="px-4 py-3 font-bold text-slate-900">
                      Subdomain <span className="text-slate-400 font-normal">(e.g. app.{rootDomain})</span>
                    </td>
                    <td className="px-4 py-3">
                      <span className="rounded bg-blue-100/80 px-2 py-0.5 font-mono font-bold text-blue-900 text-[11px]">
                        CNAME
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono font-bold text-slate-900">
                      <div className="flex items-center gap-1.5">
                        <span>subdomain</span>
                        <button
                          type="button"
                          onClick={() => copyDnsRecord("app", "cname-host")}
                          className="text-[10px] text-slate-400 hover:text-blue-600 font-sans"
                        >
                          {copiedDnsKey === "cname-host" ? "✓" : "📋"}
                        </button>
                      </div>
                    </td>
                    <td className="px-4 py-3 font-mono font-bold text-blue-700">
                      161.118.168.6
                    </td>
                    <td className="px-4 py-3 text-slate-500">Auto (300s)</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        type="button"
                        onClick={() => copyDnsRecord("161.118.168.6", "cname-target")}
                        className="px-2.5 py-1 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-[11px] font-bold text-slate-700 shadow-2xs transition-all"
                      >
                        {copiedDnsKey === "cname-target" ? "✓ Copied" : "📋 Copy Value"}
                      </button>
                    </td>
                  </tr>

                  <tr className="hover:bg-blue-50/30 transition-colors">
                    <td className="px-4 py-3 font-bold text-slate-900">
                      Apex / Root Domain <span className="text-slate-400 font-normal">(e.g. {rootDomain})</span>
                    </td>
                    <td className="px-4 py-3">
                      <span className="rounded bg-purple-100/80 px-2 py-0.5 font-mono font-bold text-purple-900 text-[11px]">
                        A Record
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono font-bold text-slate-900">
                      <div className="flex items-center gap-1.5">
                        <span>@</span>
                        <button
                          type="button"
                          onClick={() => copyDnsRecord("@", "a-host")}
                          className="text-[10px] text-slate-400 hover:text-blue-600 font-sans"
                        >
                          {copiedDnsKey === "a-host" ? "✓" : "📋"}
                        </button>
                      </div>
                    </td>
                    <td className="px-4 py-3 font-mono font-bold text-blue-700">
                      161.118.168.6
                    </td>
                    <td className="px-4 py-3 text-slate-500">Auto (300s)</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        type="button"
                        onClick={() => copyDnsRecord("161.118.168.6", "a-ip")}
                        className="px-2.5 py-1 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-[11px] font-bold text-slate-700 shadow-2xs transition-all"
                      >
                        {copiedDnsKey === "a-ip" ? "✓ Copied" : "📋 Copy IP"}
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div className="rounded-lg border border-amber-200 bg-amber-50/70 p-3 text-xs text-amber-900 flex items-start gap-2">
              <span className="text-amber-600 font-bold shrink-0">💡</span>
              <p className="leading-relaxed">
                <span className="font-bold">Cloudflare Users:</span> When setting up DNS on Cloudflare, set proxy status to <span className="font-bold underline">DNS Only (Grey Cloud)</span> during initial certificate issuance so Let&apos;s Encrypt TLS-ALPN-01 / HTTP-01 challenges resolve directly to Traefik.
              </p>
            </div>
          </div>

          {/* Step 2: Add Domain Form */}
          <div className="rounded-xl border-2 border-slate-200 bg-white p-5 shadow-xs space-y-3">
            <div>
              <span className="inline-block text-[10px] font-extrabold uppercase tracking-wider text-blue-700 bg-blue-50 px-2 py-0.5 rounded-md mb-1">
                Attachment
              </span>
              <h3 className="text-sm font-bold text-[#0F1B3D]">
                Step 2: Connect Custom Domain to {project.name}
              </h3>
              <p className="text-xs text-slate-500 font-medium">
                Enter your fully qualified domain name. Traefik will generate ingress routing and trigger Let&apos;s Encrypt automated TLS provisioning.
              </p>
            </div>

            <form onSubmit={handleAddDomain} className="flex flex-col sm:flex-row gap-2.5 pt-1">
              <div className="relative flex-1">
                <Input
                  placeholder="e.g. app.mycompany.com or mycompany.com"
                  value={domainInput}
                  onChange={(e) => setDomainInput(e.target.value)}
                  disabled={addingDomain}
                  required
                  className="font-medium"
                />
              </div>
              <Button type="submit" disabled={addingDomain || !domainInput.trim()} className="font-bold shrink-0">
                {addingDomain ? "Connecting Domain..." : "Connect Domain"}
              </Button>
            </form>
          </div>

          {/* Step 3: Connected Hostnames List & SSL Status */}
          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-5 py-3.5 flex items-center justify-between">
              <div>
                <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)] flex items-center gap-2">
                  <span>🔒 Step 3: Connected Hostnames & SSL Certificates</span>
                  <span className="rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-bold text-blue-800 font-mono">
                    {domainsList.length} Connected
                  </span>
                </h3>
              </div>
              <div className="text-[11px] font-medium text-slate-500">
                Default: <code className="font-bold text-[var(--ink)]">{appPublicUrl}</code>
              </div>
            </div>

            {domainsList.length === 0 ? (
              <div className="p-8 text-center space-y-2">
                <span className="text-3xl">🌐</span>
                <p className="text-xs font-bold text-[var(--ink)]">No custom domains connected yet</p>
                <p className="text-xs text-[var(--ink-muted)] max-w-sm mx-auto">
                  Add your custom domain above to route production traffic to this container with zero manual SSL configuration.
                </p>
              </div>
            ) : (
              <div className="divide-y divide-[var(--border)]">
                {domainsList.map((d) => {
                  const isVerified = d.status === "verified" || d.status === "active";
                  const cert = certificatesList.find((c) => c.hostname === d.hostname);
                  const isCertActive = isVerified || cert?.status === "active" || cert?.status === "issued";
                  const isVerifying = verifyingDomainId === d.id;

                  return (
                    <div key={d.id} className="p-4 hover:bg-slate-50/50 transition-colors flex flex-col md:flex-row md:items-center justify-between gap-4 text-xs">
                      <div className="space-y-1.5">
                        <div className="flex items-center gap-2">
                          <span className="font-bold text-sm text-[var(--ink)] tracking-tight">
                            {d.hostname}
                          </span>
                          <a
                            href={`https://${d.hostname}`}
                            target="_blank"
                            rel="noreferrer"
                            className="rounded p-1 text-[var(--ink-muted)] hover:bg-slate-100 hover:text-[var(--accent)] font-bold text-xs"
                            title="Open https://"
                          >
                            ↗
                          </a>
                        </div>

                        <div className="flex flex-wrap items-center gap-2 text-[11px]">
                          {/* Routing Badge */}
                          {isVerified ? (
                            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 font-bold text-emerald-800">
                              <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                              🟢 Active & Routed
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-300 bg-amber-50 px-2.5 py-0.5 font-bold text-amber-800">
                              <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                              🟡 Pending DNS
                            </span>
                          )}

                          {/* SSL Status Badge */}
                          {isCertActive ? (
                            <span className="inline-flex items-center gap-1 rounded-full border border-blue-300 bg-blue-50 px-2.5 py-0.5 font-bold text-blue-800">
                              <span>🔒</span>
                              <span>Let&apos;s Encrypt (TLS 1.3 Active)</span>
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1 rounded-full border border-slate-300 bg-slate-100 px-2.5 py-0.5 font-bold text-slate-700">
                              <span>⏳</span>
                              <span>Auto-SSL Provisioning...</span>
                            </span>
                          )}

                          <span className="text-slate-400">&bull;</span>
                          <span className="text-slate-500 font-mono text-[10px]">
                            Traefik Ingress: 8080 &rarr; 443
                          </span>
                        </div>
                      </div>

                      {/* Actions */}
                      <div className="flex items-center gap-2 shrink-0">
                        {!isVerified && (
                          <>
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={isVerifying}
                              onClick={() => handleVerifyDomain(d.id, false)}
                              title="Test if DNS records have propagated globally"
                              className="font-bold text-xs"
                            >
                              {isVerifying ? "Checking..." : "🔄 Verify DNS"}
                            </Button>
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={isVerifying}
                              onClick={() => handleVerifyDomain(d.id, true)}
                              title="Bypass public DNS propagation delay and force Traefik ingress route live immediately"
                              className="font-bold text-xs text-blue-700 border-blue-200 hover:bg-blue-50"
                            >
                              {isVerifying ? "Verifying..." : "⚡ Auto-Verify"}
                            </Button>
                          </>
                        )}

                        <Button
                          type="button"
                          variant="danger"
                          size="sm"
                          disabled={deletingDomain}
                          onClick={() => setDomainToDelete(d)}
                          className="font-bold text-xs"
                        >
                          Disconnect
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {/* Confirmation Modal Dialog for Disconnecting Custom Domain */}
          {domainToDelete && (
            <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/60 backdrop-blur-xs p-4 animate-fade-in">
              <div className="w-full max-w-md rounded-2xl border-2 border-slate-200 bg-white p-6 shadow-2xl space-y-4">
                <div className="flex items-center gap-3">
                  <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-rose-100 text-rose-600 font-bold text-xl shrink-0">
                    ⚠️
                  </div>
                  <div>
                    <h3 className="text-sm font-black text-slate-900">
                      Disconnect Custom Domain?
                    </h3>
                    <p className="text-xs text-slate-500 font-medium">
                      This action will remove live ingress traffic.
                    </p>
                  </div>
                </div>

                <div className="rounded-xl border border-rose-200 bg-rose-50/70 p-4 text-xs text-rose-900 space-y-1.5">
                  <p className="font-bold">
                    Are you sure you want to disconnect <code className="font-mono font-black text-rose-950 underline">{domainToDelete.hostname}</code> from {project.name}?
                  </p>
                  <p className="text-[11px] text-rose-700 leading-relaxed">
                    Traefik reverse proxy routes will be purged immediately and Let&apos;s Encrypt TLS certificates will be detached. Incoming user traffic to this domain will stop resolving.
                  </p>
                </div>

                <div className="flex items-center justify-end gap-2.5 pt-2">
                  <button
                    type="button"
                    disabled={deletingDomain}
                    onClick={() => setDomainToDelete(null)}
                    className="h-9 px-4 rounded-lg border border-slate-300 bg-white text-xs font-bold text-slate-700 hover:bg-slate-50 transition-colors"
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    disabled={deletingDomain}
                    onClick={handleConfirmDeleteDomain}
                    className="h-9 px-4 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-bold transition-colors flex items-center gap-1.5 shadow-sm disabled:opacity-50"
                  >
                    {deletingDomain ? "Disconnecting..." : "Yes, Disconnect Domain"}
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* ========================================================
          TAB 6: SECRETS & ENV VARS (Viewable, All Envs & Addons)
          ======================================================== */}
      {currentTab === "secrets" && (
        <div className="space-y-6">
          {/* Add-ons & Database Connection Card */}
          {addonsList.length > 0 && (
            <div className="jp-card overflow-hidden">
              <div className="border-b border-[var(--border)] bg-slate-50 px-5 py-3.5 flex items-center justify-between">
                <div>
                  <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)] flex items-center gap-2">
                    <span>🧩 Connected Add-ons & Database Credentials</span>
                    <span className="rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-bold text-blue-800 font-mono">
                      {addonsList.length} Connected
                    </span>
                  </h3>
                  <p className="text-[11px] text-[var(--ink-muted)] mt-0.5">
                    Live connection strings, credentials, and hostnames auto-injected into {project.name}.
                  </p>
                </div>
                <Link href={`/projects/${actualProjectId}?tab=addons`}>
                  <Button variant="secondary" size="sm" className="text-xs font-bold">
                    + Add More Add-ons
                  </Button>
                </Link>
              </div>

              <div className="divide-y divide-[var(--border)]">
                {addonsList.map((addon) => {
                  const intUrl = getInternalUrl(addon);
                  const meta = (addon.metadata || {}) as Record<string, any>;
                  const user = String(meta.username || meta.user || meta.role || (addon.engine === "redis" ? "default" : "jp"));
                  const pass = String(meta.password || "jp_dev_password");
                  const db = String(meta.database || meta.schema || "jp");
                  const port = meta.port || (addon.engine === "postgres" ? 5432 : addon.engine === "redis" ? 6379 : addon.engine === "mysql" ? 3306 : 8080);
                  const isPassRevealed = revealedAddonPass[addon.id] || showAllSecrets;
                  const injectedKey = addon.engine === "postgres" ? "DATABASE_URL" : addon.engine === "redis" ? "REDIS_URL" : addon.engine === "mysql" ? "MYSQL_URL" : addon.engine === "mongodb" ? "MONGODB_URI" : `${addon.engine.toUpperCase()}_URL`;

                  return (
                    <div key={addon.id} className="p-5 space-y-3 bg-white hover:bg-slate-50/50 transition-colors">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-xs uppercase px-2 py-0.5 rounded bg-slate-100 border border-[var(--border)] font-bold text-[var(--ink)]">
                            {addon.engine}
                          </span>
                          <span className="font-bold text-sm text-[var(--ink)]">{addon.name}</span>
                          <span className="inline-flex items-center gap-1 rounded-full bg-emerald-50 border border-emerald-300 px-2 py-0.5 text-[10px] font-bold text-emerald-800">
                            <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                            Connected to App
                          </span>
                        </div>

                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            onClick={() => copyToClipboard(`${injectedKey}=${intUrl}`, `addon-env-${addon.id}`)}
                            className="px-2.5 py-1 text-xs font-bold rounded border border-[var(--border)] bg-white hover:bg-slate-100 text-[var(--ink-secondary)] transition-all shadow-2xs"
                          >
                            {copiedKey === `addon-env-${addon.id}` ? "✓ Copied .env Line" : `Copy ${injectedKey} for .env`}
                          </button>
                        </div>
                      </div>

                      {/* Credentials Grid */}
                      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-2.5 text-xs">
                        <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                          <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Injected Env Key</span>
                          <code className="font-mono font-bold text-blue-700">{injectedKey}</code>
                        </div>

                        <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                          <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Host & Port (Internal Docker)</span>
                          <code className="font-mono text-[var(--ink)]">{addon.engine}:{port}</code>
                        </div>

                        <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                          <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Username / Role</span>
                          <div className="flex items-center justify-between">
                            <code className="font-mono font-semibold text-[var(--ink)]">{user}</code>
                            <button
                              type="button"
                              onClick={() => copyToClipboard(user, `user-${addon.id}`)}
                              className="text-[10px] text-[var(--accent)] font-bold hover:underline"
                            >
                              {copiedKey === `user-${addon.id}` ? "✓" : "Copy"}
                            </button>
                          </div>
                        </div>

                        <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                          <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Password</span>
                          <div className="flex items-center justify-between">
                            <code className="font-mono text-xs text-[var(--ink)]">
                              {isPassRevealed ? pass : "••••••••••••"}
                            </code>
                            <div className="flex items-center gap-1.5">
                              <button
                                type="button"
                                onClick={() =>
                                  setRevealedAddonPass((prev) => ({
                                    ...prev,
                                    [addon.id]: !prev[addon.id],
                                  }))
                                }
                                className="text-[11px] text-slate-500 hover:text-slate-900 font-bold"
                              >
                                {isPassRevealed ? "🙈" : "👁️"}
                              </button>
                              <button
                                type="button"
                                onClick={() => copyToClipboard(pass, `pass-${addon.id}`)}
                                className="text-[10px] text-[var(--accent)] font-bold hover:underline"
                              >
                                {copiedKey === `pass-${addon.id}` ? "✓" : "Copy"}
                              </button>
                            </div>
                          </div>
                        </div>
                      </div>

                      {/* Connection URL Bar */}
                      <div className="rounded-lg border border-[var(--border)] bg-slate-900 p-3 text-xs flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-2 shadow-inner">
                        <div className="flex items-center gap-2 overflow-hidden">
                          <span className="text-[10px] font-mono uppercase font-bold text-slate-400 shrink-0">Connection URL:</span>
                          <code className="font-mono text-emerald-400 truncate">
                            {isPassRevealed ? intUrl : intUrl.replace(/:([^@]+)@/, ":••••••••@")}
                          </code>
                        </div>

                        <div className="flex items-center gap-2 shrink-0">
                          <button
                            type="button"
                            onClick={() =>
                              setRevealedAddonPass((prev) => ({
                                ...prev,
                                [addon.id]: !prev[addon.id],
                              }))
                            }
                            className="text-xs px-2 py-1 rounded bg-slate-800 text-slate-300 hover:text-white font-medium"
                          >
                            {isPassRevealed ? "🙈 Mask" : "👁️ Reveal"}
                          </button>
                          <button
                            type="button"
                            onClick={() => copyToClipboard(intUrl, `url-${addon.id}`)}
                            className="text-xs px-2.5 py-1 rounded bg-emerald-600 hover:bg-emerald-500 text-white font-bold transition-colors"
                          >
                            {copiedKey === `url-${addon.id}` ? "✓ Copied" : "Copy Full URL"}
                          </button>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* New Secret Addition Form */}
          <div className="jp-card p-5 space-y-4">
            <div className="border-b border-[var(--border)] pb-3 flex items-center justify-between">
              <div>
                <h2 className="text-sm font-bold text-[var(--ink)]">Add Environment Variable</h2>
                <p className="text-xs text-[var(--ink-muted)]">
                  Encrypted secrets injected securely into your container runtime.
                </p>
              </div>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => setBulkSecretOpen(true)}
                className="font-bold text-xs"
              >
                📋 Bulk Edit (.env)
              </Button>
            </div>

            <form onSubmit={handleSaveSecret} autoComplete="off" className="flex flex-wrap items-center gap-2.5">
              <div className="w-full sm:w-44">
                <select
                  className="w-full rounded-lg border-2 border-slate-300 bg-white px-3 py-2 text-xs font-bold text-slate-800 shadow-2xs focus:border-[#1E3A8A] focus:outline-none"
                  value={newSecretEnv}
                  onChange={(e) => setNewSecretEnv(e.target.value)}
                >
                  <option value="production">PRODUCTION</option>
                  <option value="staging">STAGING</option>
                  <option value="development">DEVELOPMENT</option>
                  <option value="preview">PREVIEW</option>
                </select>
              </div>

              <Input
                placeholder="KEY (e.g. DATABASE_URL, STRIPE_KEY)"
                name="app_custom_env_key"
                id="app_custom_env_key"
                autoComplete="off"
                data-lpignore="true"
                data-1p-ignore="true"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value.replace(/[^a-zA-Z0-9_]/g, "_").toUpperCase())}
                className="min-w-[200px] flex-1 font-mono uppercase text-xs"
                required
              />

              <div className="relative min-w-[220px] flex-2">
                <Input
                  placeholder="Value"
                  name="app_custom_env_val"
                  id="app_custom_env_val"
                  type={secretValVisible ? "text" : "password"}
                  autoComplete="new-password"
                  data-lpignore="true"
                  data-1p-ignore="true"
                  value={secretVal}
                  onChange={(e) => setSecretVal(e.target.value)}
                  className="pr-10 font-mono text-xs"
                  required
                />
                <button
                  type="button"
                  onClick={() => setSecretValVisible(!secretValVisible)}
                  className="absolute right-2.5 top-2.5 text-xs text-slate-400 hover:text-slate-700"
                  title={secretValVisible ? "Hide value" : "Show value"}
                >
                  {secretValVisible ? "🙈" : "👁️"}
                </button>
              </div>

              <Button type="submit" disabled={savingSecret || !secretKey.trim() || !secretVal}>
                {savingSecret ? "Saving..." : "Save Secret"}
              </Button>
            </form>
          </div>

          {/* Variables Table with Environment Scope Filter & Reveal All */}
          <div className="jp-card overflow-hidden">
            <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <h3 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                  Environment Variables ({secretsList.length})
                </h3>
              </div>

              <div className="flex items-center gap-2">
                {/* Scope Filter Tabs */}
                <div className="flex items-center rounded-lg border border-[var(--border)] bg-white p-0.5 shadow-2xs text-xs">
                  {[
                    { id: "all", label: "All Envs" },
                    { id: "production", label: "Production" },
                    { id: "staging", label: "Staging" },
                    { id: "development", label: "Development" },
                  ].map((tab) => (
                    <button
                      key={tab.id}
                      type="button"
                      onClick={() => setSecretEnv(tab.id)}
                      className={`px-2.5 py-1 font-semibold rounded-md transition-all ${
                        secretEnv === tab.id
                          ? "bg-[var(--accent)] text-white font-bold shadow-2xs"
                          : "text-[var(--ink-secondary)] hover:text-[var(--ink)]"
                      }`}
                    >
                      {tab.label}
                    </button>
                  ))}
                </div>

                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  onClick={() => setShowAllSecrets(!showAllSecrets)}
                  className="text-xs font-semibold"
                >
                  {showAllSecrets ? "🙈 Hide All Values" : "👁️ Reveal All Values"}
                </Button>
              </div>
            </div>

            {secretsList.length === 0 ? (
              <div className="p-8 text-center text-xs text-[var(--ink-muted)] space-y-2">
                <p className="font-semibold text-[var(--ink)]">
                  No secrets stored in {secretEnv === "all" ? "any environment" : `${secretEnv.toUpperCase()} scope`}.
                </p>
                <p>
                  Add a variable above or click &quot;Bulk Edit (.env)&quot; to paste multiple keys.
                </p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm min-w-[750px]">
                  <thead>
                    <tr className="border-b border-[var(--border)] bg-slate-50/50 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                      <th className="px-4 py-2.5">Key Name</th>
                      <th className="px-4 py-2.5">Scope</th>
                      <th className="px-4 py-2.5">Decrypted Value</th>
                      <th className="px-4 py-2.5 text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {secretsList.map((s) => {
                      const isRevealed = revealedSecrets[s.id] || showAllSecrets;
                      const plainValue = s.value || s.value_hint;

                      return (
                        <tr key={s.id} className="hover:bg-slate-50/70 transition-colors">
                          <td className="px-4 py-3 font-mono font-bold text-[var(--ink)]">
                            {s.name}
                          </td>

                          <td className="px-4 py-3">
                            <span
                              className={`rounded px-2 py-0.5 font-mono text-[10px] font-bold uppercase border ${
                                s.environment === "production"
                                  ? "bg-purple-50 text-purple-800 border-purple-200"
                                  : s.environment === "staging"
                                  ? "bg-amber-50 text-amber-800 border-amber-200"
                                  : "bg-blue-50 text-blue-800 border-blue-200"
                              }`}
                            >
                              {s.environment}
                            </span>
                          </td>

                          <td className="px-4 py-3">
                            <div className="flex items-center gap-2 max-w-md">
                              {isRevealed ? (
                                <code className="font-mono text-xs text-emerald-800 bg-emerald-50 px-2 py-1 rounded border border-emerald-200 break-all select-all">
                                  {plainValue}
                                </code>
                              ) : (
                                <span className="font-mono text-xs text-slate-400 select-none tracking-widest">
                                  ••••••••••••••••
                                </span>
                              )}
                            </div>
                          </td>

                          <td className="px-4 py-3 text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              <button
                                type="button"
                                onClick={() =>
                                  setRevealedSecrets((prev) => ({
                                    ...prev,
                                    [s.id]: !prev[s.id],
                                  }))
                                }
                                className="px-2 py-1 text-xs font-semibold rounded border border-[var(--border)] bg-white hover:bg-slate-100 text-[var(--ink-secondary)] transition-colors"
                                title={isRevealed ? "Mask value" : "Reveal value"}
                              >
                                {isRevealed ? "🙈 Hide" : "👁️ View"}
                              </button>

                              <button
                                type="button"
                                onClick={() => copyToClipboard(plainValue, `sec-${s.id}`)}
                                className="px-2 py-1 text-xs font-semibold rounded border border-[var(--border)] bg-white hover:bg-slate-100 text-[var(--ink-secondary)] transition-colors"
                              >
                                {copiedKey === `sec-${s.id}` ? "✓ Copied" : "Copy"}
                              </button>

                              <Button
                                variant="danger"
                                size="sm"
                                onClick={() => handleDeleteSecret(s.name, s.environment)}
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
${getAddonEnvSnippet(snippetAddon)}`}
              </pre>
              <button
                type="button"
                onClick={() =>
                  copyToClipboard(getAddonEnvSnippet(snippetAddon), "proj-snippet")
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

      {/* ========================================================
          BUILD LOGS MODAL (when clicked from any deployment)
          ======================================================== */}
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
