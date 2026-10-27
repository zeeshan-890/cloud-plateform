"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { DEFAULT_ADDON_CATALOG } from "@/lib/addon-catalog";
import type { AddonCatalogItem, ManagedAddon, Project } from "@/lib/types";

export default function AddonsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [activeTab, setActiveTab] = useState<"catalog" | "configured">("catalog");

  // Catalog & add-ons state
  const [catalog, setCatalog] = useState<AddonCatalogItem[]>(DEFAULT_ADDON_CATALOG);
  const [allAddons, setAllAddons] = useState<ManagedAddon[]>([]);
  const [filterProjectId, setFilterProjectId] = useState<string>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedCategory, setSelectedCategory] = useState("all");

  // Drawer / Configuration Modal State
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selectedItem, setSelectedItem] = useState<AddonCatalogItem | null>(null);
  const [isCustomMode, setIsCustomMode] = useState(false);

  // Form Fields
  const [deployTarget, setDeployTarget] = useState<"standalone" | "attach">("standalone");
  const [standaloneProjectName, setStandaloneProjectName] = useState("");
  const [targetProjectId, setTargetProjectId] = useState("");
  const [targetEnv, setTargetEnv] = useState("production");
  const [instanceName, setInstanceName] = useState("");

  // Custom Image Fields
  const [customEngine, setCustomEngine] = useState("");
  const [customImage, setCustomImage] = useState("");
  const [customPort, setCustomPort] = useState("8080");
  const [customDesc, setCustomDesc] = useState("");

  // UI status
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [createdStandaloneProjId, setCreatedStandaloneProjId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Security & Snippet Modals
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [snippetAddon, setSnippetAddon] = useState<ManagedAddon | null>(null);
  const [hostName, setHostName] = useState("localhost");
  const [publicAccessMap, setPublicAccessMap] = useState<Record<string, boolean>>({});
  const [securityModalAddon, setSecurityModalAddon] = useState<ManagedAddon | null>(null);
  const [adminPasswordInput, setAdminPasswordInput] = useState("");
  const [passwordError, setPasswordError] = useState("");

  useEffect(() => {
    if (typeof window !== "undefined") {
      setHostName(window.location.hostname || "localhost");
    }
  }, []);

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

  function getPublicUrl(addon: ManagedAddon): string {
    if (addon.metadata?.public_url) {
      const raw = String(addon.metadata.public_url);
      if (!raw.includes("localhost") || hostName === "localhost") return raw;
      return raw.replace("localhost", hostName);
    }
    const port = addon.metadata?.port || 8080;
    if (addon.engine === "postgres") return `postgres://jp:jp_dev_password@${hostName}:5432/jp`;
    if (addon.engine === "redis" || addon.engine === "valkey") return `redis://:${hostName}:6379/0`;
    return `http://${hostName}:${port}`;
  }

  // Load projects and all active add-ons for the organization
  async function loadData() {
    if (!currentOrg) return;
    try {
      const projs = await api.listProjects(currentOrg.id);
      setProjects(projs);
      if (projs.length > 0 && !targetProjectId) {
        setTargetProjectId(projs[0].id);
      }

      // Load all add-ons across the organization
      const addonsRes = await api.listOrgAddons(currentOrg.id);
      setAllAddons(addonsRes.addons || []);

      // Try loading dynamic catalog overlay if available
      if (projs.length > 0) {
        const catRes = await api.addonCatalog(currentOrg.id, projs[0].id).catch(() => ({ catalog: [] }));
        if (catRes.catalog && catRes.catalog.length > 0) {
          const map = new Map<string, AddonCatalogItem>();
          DEFAULT_ADDON_CATALOG.forEach((i) => map.set(i.id, i));
          catRes.catalog.forEach((i) => map.set(i.id, i));
          setCatalog(Array.from(map.values()));
        }
      }
    } catch (err) {
      console.warn("Failed to load addons data", err);
    }
  }

  useEffect(() => {
    if (!currentOrg) return;
    loadData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg]);

  // Open drawer for a catalog item
  function openConfigure(item: AddonCatalogItem) {
    setSelectedItem(item);
    setIsCustomMode(false);
    setInstanceName(`${item.id}-1`);
    setStandaloneProjectName(`${item.id}-app`);
    setDeployTarget(projects.length === 0 ? "standalone" : "attach");
    setError("");
    setDrawerOpen(true);
  }

  // Open drawer for custom container
  function openCustomDrawer() {
    setSelectedItem(null);
    setIsCustomMode(true);
    setCustomEngine("custom-app");
    setInstanceName("custom-app-1");
    setStandaloneProjectName("custom-container-app");
    setCustomImage("");
    setCustomPort("8080");
    setCustomDesc("");
    setDeployTarget(projects.length === 0 ? "standalone" : "attach");
    setError("");
    setDrawerOpen(true);
  }

  // Execute provision
  async function handleProvision() {
    if (!currentOrg) return;
    setBusy(true);
    setError("");
    setSuccess("");
    setCreatedStandaloneProjId(null);

    try {
      let finalProjectId = targetProjectId;
      let effectiveEngine = isCustomMode ? customEngine.trim().toLowerCase() : (selectedItem?.id || "");
      let extra: { docker_image?: string; port?: number; description?: string } | undefined;

      if (!instanceName.trim()) {
        throw new Error("Instance name is required.");
      }
      if (!effectiveEngine) {
        throw new Error("Engine identifier is required.");
      }

      if (isCustomMode) {
        if (!customImage.trim()) {
          throw new Error("Docker image reference is required.");
        }
        extra = {
          docker_image: customImage.trim(),
          port: parseInt(customPort, 10) || 8080,
          description: customDesc.trim() || undefined,
        };
      }

      // Step 1: If Standalone, auto-create project first!
      if (deployTarget === "standalone") {
        const pName = standaloneProjectName.trim() || `${effectiveEngine}-project`;
        const newProj = await api.createProject(currentOrg.id, {
          name: pName,
          description: `Standalone application for ${selectedItem?.name || effectiveEngine} add-on`,
        });
        finalProjectId = newProj.id;
        setCreatedStandaloneProjId(newProj.id);
      } else {
        if (!finalProjectId) {
          throw new Error("Please select a target project to attach this add-on to.");
        }
      }

      // Step 2: Provision Add-on into that project
      const created = await api.createAddon(
        currentOrg.id,
        finalProjectId,
        effectiveEngine,
        instanceName.trim(),
        targetEnv,
        extra,
      );

      const targetProjectName =
        deployTarget === "standalone"
          ? standaloneProjectName.trim()
          : (projects.find((p) => p.id === finalProjectId)?.name || "target project");

      setSuccess(
        deployTarget === "standalone"
          ? `Successfully created standalone project "${targetProjectName}" and provisioned ${created.name} (${created.engine}). Connection URL and credentials are automatically injected into the project .env!`
          : `Successfully provisioned ${created.name} (${created.engine}) and linked standard environment variables (DATABASE_URL / REDIS_URL) into project "${targetProjectName}".`,
      );

      setDrawerOpen(false);
      await loadData();
      setActiveTab("configured");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : (err as Error).message || "Provisioning failed");
    } finally {
      setBusy(false);
    }
  }

  // Delete an add-on
  async function onDelete(addon: ManagedAddon) {
    if (!currentOrg) return;
    const confirmed = window.confirm(`Permanently terminate and delete add-on instance "${addon.name}"?`);
    if (!confirmed) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteAddon(currentOrg.id, addon.project_id, addon.id);
      setSuccess(`Add-on "${addon.name}" has been deleted.`);
      await loadData();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  // Filter Catalog
  const filteredCatalog = catalog.filter((c) => {
    const matchesCat = selectedCategory === "all" || c.category === selectedCategory;
    const q = searchQuery.toLowerCase().trim();
    const matchesQuery =
      !q ||
      c.name.toLowerCase().includes(q) ||
      c.id.toLowerCase().includes(q) ||
      c.description.toLowerCase().includes(q) ||
      c.category.toLowerCase().includes(q);
    return matchesCat && matchesQuery;
  });

  // Filter Active Addons
  const filteredActiveAddons = allAddons.filter((a) => {
    if (filterProjectId === "all") return true;
    return a.project_id === filterProjectId;
  });

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Add-ons require an active organization context."
      />
    );
  }

  return (
    <div className="animate-fade-up space-y-6">
      {/* Header */}
      <PageHeader
        title="Marketplace & Managed Add-ons"
        description="One-click databases, queues, AI engines, and custom container apps. Deploy as standalone projects or directly attach to existing projects."
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="secondary"
              onClick={openCustomDrawer}
              className="font-semibold text-xs"
            >
              + Deploy Custom Container
            </Button>
          </div>
        }
      />

      {/* Notifications */}
      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? (
        <Alert tone="success">
          <div className="flex items-center justify-between gap-4">
            <span>{success}</span>
            {createdStandaloneProjId && (
              <Link
                href={`/projects/${createdStandaloneProjId}`}
                className="font-bold underline hover:opacity-80 shrink-0 text-xs"
              >
                Open Project ↗
              </Link>
            )}
          </div>
        </Alert>
      ) : null}

      {/* Main Tabs Navigation */}
      <div className="flex items-center justify-between border-b border-[var(--border)] pb-2">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setActiveTab("catalog")}
            className={`px-4 py-2 text-sm font-bold rounded-lg transition-all ${
              activeTab === "catalog"
                ? "bg-[var(--accent)] text-white shadow-xs"
                : "text-[var(--ink-secondary)] hover:text-[var(--ink)] hover:bg-slate-100"
            }`}
          >
            Marketplace Catalog ({catalog.length})
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("configured")}
            className={`flex items-center gap-2 px-4 py-2 text-sm font-bold rounded-lg transition-all ${
              activeTab === "configured"
                ? "bg-[var(--accent)] text-white shadow-xs"
                : "text-[var(--ink-secondary)] hover:text-[var(--ink)] hover:bg-slate-100"
            }`}
          >
            <span>Configured Add-ons</span>
            <span
              className={`px-2 py-0.5 rounded-full text-[11px] font-mono font-bold ${
                activeTab === "configured"
                  ? "bg-white/20 text-white"
                  : "bg-slate-200 text-slate-800"
              }`}
            >
              {allAddons.length}
            </span>
          </button>
        </div>

        {activeTab === "configured" && (
          <div className="flex items-center gap-2">
            <span className="text-xs font-bold text-[var(--ink-muted)]">Filter by Project:</span>
            <select
              className="rounded-md border border-[var(--border)] bg-white px-2.5 py-1 text-xs font-semibold text-[var(--ink)] shadow-2xs"
              value={filterProjectId}
              onChange={(e) => setFilterProjectId(e.target.value)}
            >
              <option value="all">All Projects ({allAddons.length})</option>
              {projects.map((p) => {
                const count = allAddons.filter((a) => a.project_id === p.id).length;
                return (
                  <option key={p.id} value={p.id}>
                    {p.name} ({count})
                  </option>
                );
              })}
            </select>
          </div>
        )}
      </div>

      {/* ========================================================
          TAB 1: MARKETPLACE CATALOG (Browse & 1-Click Configure)
          ======================================================== */}
      {activeTab === "catalog" && (
        <div className="space-y-4">
          {/* Search & Category Pills */}
          <div className="flex flex-col sm:flex-row gap-3 items-stretch sm:items-center justify-between">
            <div className="w-full sm:w-96">
              <Input
                placeholder="Search templates, databases, cache, AI, search..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="font-medium text-xs"
              />
            </div>
            <div className="text-xs text-[var(--ink-muted)] font-mono">
              Showing {filteredCatalog.length} of {catalog.length} templates
            </div>
          </div>

          {/* Categories */}
          <div className="flex flex-wrap gap-1.5 pb-2">
            {[
              { id: "all", label: "All Templates" },
              { id: "database", label: "Databases" },
              { id: "cache", label: "Cache & Redis" },
              { id: "queue", label: "Queues & Streaming" },
              { id: "search", label: "Search Engines" },
              { id: "ai", label: "AI & Vector DBs" },
              { id: "storage", label: "Storage & S3" },
              { id: "analytics", label: "Analytics" },
              { id: "cms", label: "CMS & Blogs" },
              { id: "tool", label: "Dev Tools" },
              { id: "security", label: "Auth & Security" },
              { id: "monitoring", label: "Monitoring" },
            ].map((cat) => (
              <button
                key={cat.id}
                type="button"
                onClick={() => setSelectedCategory(cat.id)}
                className={`px-3 py-1 text-xs rounded-full border transition-all ${
                  selectedCategory === cat.id
                    ? "border-[var(--accent)] bg-[var(--accent)] text-white font-bold shadow-2xs"
                    : "border-[var(--border)] text-[var(--ink-secondary)] hover:border-[var(--border-strong)] bg-white"
                }`}
              >
                {cat.label}
              </button>
            ))}
          </div>

          {/* Catalog Cards Grid */}
          {filteredCatalog.length === 0 ? (
            <div className="p-12 text-center text-xs text-[var(--ink-muted)] border border-dashed border-[var(--border)] rounded-lg bg-white">
              No matching applications found. Click &quot;+ Deploy Custom Container&quot; above to run any Docker image!
            </div>
          ) : (
            <div className="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-3">
              {filteredCatalog.map((item) => (
                <div
                  key={item.id}
                  className="group relative rounded-xl border border-[var(--border)] bg-white p-4 shadow-2xs transition-all hover:border-[var(--accent)] hover:shadow-sm flex flex-col justify-between"
                >
                  <div>
                    <div className="flex items-center justify-between gap-2">
                      <h3 className="font-bold text-sm text-[var(--ink)] tracking-tight">
                        {item.name}
                      </h3>
                      <span className="text-[10px] uppercase font-mono px-2 py-0.5 rounded-full bg-slate-100 border border-[var(--border)] text-[var(--ink-secondary)] font-semibold">
                        {item.category}
                      </span>
                    </div>

                    <p className="mt-1.5 text-xs text-[var(--ink-muted)] line-clamp-2 leading-relaxed">
                      {item.description}
                    </p>
                  </div>

                  <div className="mt-4 pt-3 border-t border-[var(--border)] flex items-center justify-between text-xs">
                    <span className="font-mono text-[11px] text-[var(--ink-secondary)] font-semibold">
                      {item.default_port ? `Port :${item.default_port}` : "Embedded"}
                    </span>

                    <Button
                      size="sm"
                      onClick={() => openConfigure(item)}
                      className="font-bold text-xs shadow-2xs group-hover:bg-[var(--accent)]"
                    >
                      Configure & Deploy →
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* ========================================================
          TAB 2: CONFIGURED ADD-ONS (Active Instances Table)
          ======================================================== */}
      {activeTab === "configured" && (
        <div>
          {filteredActiveAddons.length === 0 ? (
            <div className="p-12 text-center border border-dashed border-[var(--border)] rounded-xl bg-white space-y-3">
              <p className="text-sm font-bold text-[var(--ink)]">No configured add-ons found</p>
              <p className="text-xs text-[var(--ink-muted)] max-w-md mx-auto">
                {filterProjectId !== "all"
                  ? "This project has no attached add-ons yet. Browse the catalog to attach one or create a standalone instance."
                  : "You haven't provisioned any add-ons yet. Browse our 1-click catalog to deploy databases, caches, queues, and container apps."}
              </p>
              <Button onClick={() => setActiveTab("catalog")}>
                Browse Marketplace Catalog
              </Button>
            </div>
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)] bg-white shadow-2xs">
              <table className="w-full min-w-[880px] text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--border)] bg-slate-50 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                    <th className="py-3 px-4 font-bold">Add-on & Engine</th>
                    <th className="py-3 px-4 font-bold">Target Project</th>
                    <th className="py-3 px-4 font-bold">Status</th>
                    <th className="py-3 px-4 font-bold">Docker Internal URL (Zero Config)</th>
                    <th className="py-3 px-4 font-bold">Public Endpoint</th>
                    <th className="py-3 px-4 font-bold text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {filteredActiveAddons.map((addon) => {
                    const intUrl = getInternalUrl(addon);
                    const pubUrl = getPublicUrl(addon);
                    const parentProj = projects.find((p) => p.id === addon.project_id);

                    return (
                      <tr key={addon.id} className="hover:bg-slate-50/70 transition-colors">
                        <td className="py-3.5 px-4 font-medium text-[var(--ink)]">
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-xs uppercase px-2 py-0.5 rounded bg-slate-100 border border-[var(--border)] font-bold text-[var(--ink)]">
                              {addon.engine}
                            </span>
                            <span className="font-bold">{addon.name}</span>
                          </div>
                          <p className="mt-1 text-[11px] text-[var(--ink-muted)] font-mono">
                            Secret: {addon.secret_ref || `${addon.engine.toUpperCase()}_URL`}
                          </p>
                        </td>

                        <td className="py-3.5 px-4">
                          {parentProj ? (
                            <Link
                              href={`/projects/${parentProj.id}?tab=addons`}
                              className="font-bold text-xs text-[var(--accent)] hover:underline flex items-center gap-1"
                            >
                              <span>{parentProj.name}</span>
                              <span className="text-[10px]">↗</span>
                            </Link>
                          ) : (
                            <span className="font-mono text-xs text-[var(--ink-muted)]">
                              {addon.project_id.slice(0, 8)}...
                            </span>
                          )}
                        </td>

                        <td className="py-3.5 px-4">
                          <span className="inline-flex items-center gap-1 rounded-full bg-emerald-50 border border-emerald-300 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
                            <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                            {addon.status}
                          </span>
                        </td>

                        <td className="py-3.5 px-4">
                          <div className="flex items-center gap-2">
                            <code className="text-xs font-mono text-[var(--ink)] bg-slate-100 px-2 py-1 rounded border border-[var(--border)] max-w-xs truncate">
                              {intUrl}
                            </code>
                            <button
                              type="button"
                              onClick={() => copyToClipboard(intUrl, `int-${addon.id}`)}
                              className="px-2 py-1 text-xs font-bold rounded border border-[var(--border)] bg-white hover:bg-slate-50 text-[var(--ink-secondary)] transition-colors shrink-0"
                            >
                              {copiedKey === `int-${addon.id}` ? "✓ Copied" : "Copy"}
                            </button>
                          </div>
                          <p className="text-[10px] text-[var(--ink-muted)] mt-0.5">
                            Auto-injected into connected apps
                          </p>
                        </td>

                        <td className="py-3.5 px-4">
                          {publicAccessMap[addon.id] ? (
                            <div className="space-y-1">
                              <div className="flex items-center gap-2">
                                <span className="rounded bg-amber-50 border border-amber-300 px-2 py-0.5 text-[10px] font-bold text-amber-800">
                                  🔓 Public Exposed
                                </span>
                                <button
                                  type="button"
                                  onClick={() => setPublicAccessMap((prev) => ({ ...prev, [addon.id]: false }))}
                                  className="text-[11px] text-rose-600 font-bold hover:underline"
                                >
                                  Disable
                                </button>
                              </div>
                              <code className="text-xs font-mono text-[var(--ink)] bg-slate-100 px-2 py-0.5 rounded border border-[var(--border)] block max-w-xs truncate">
                                {pubUrl}
                              </code>
                            </div>
                          ) : (
                            <div className="flex items-center gap-2">
                              <span className="rounded bg-slate-100 border border-[var(--border)] px-2 py-0.5 text-[10px] font-bold text-[var(--ink-secondary)] flex items-center gap-1">
                                <span>🔒</span> Private (Default)
                              </span>
                              <button
                                type="button"
                                onClick={() => {
                                  setSecurityModalAddon(addon);
                                  setAdminPasswordInput("");
                                  setPasswordError("");
                                }}
                                className="text-xs font-bold text-blue-600 hover:text-blue-800 hover:underline"
                              >
                                Expose Port
                              </button>
                            </div>
                          )}
                        </td>

                        <td className="py-3.5 px-4 text-right">
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
                              disabled={busy}
                              onClick={() => onDelete(addon)}
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
      )}

      {/* ========================================================
          SLIDE-OVER CONFIGURATION DRAWER / MODAL
          NO LONG SCROLLING - FIXED COMPACT ACTIONS!
          ======================================================== */}
      {drawerOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center sm:justify-end bg-black/60 p-0 sm:p-4 backdrop-blur-xs">
          <div className="w-full sm:max-w-xl h-full sm:h-auto sm:max-h-[92vh] flex flex-col rounded-none sm:rounded-2xl border border-[var(--border)] bg-white shadow-2xl overflow-hidden animate-fade-in">
            {/* Drawer Header */}
            <div className="border-b border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-between">
              <div>
                <h2 className="text-lg font-black text-[var(--ink)] flex items-center gap-2">
                  <span>{isCustomMode ? "Deploy Custom Container" : `Deploy ${selectedItem?.name}`}</span>
                  {!isCustomMode && selectedItem && (
                    <span className="text-xs font-mono font-normal text-[var(--ink-muted)]">
                      ({selectedItem.id})
                    </span>
                  )}
                </h2>
                <p className="text-xs text-[var(--ink-muted)] mt-0.5">
                  {isCustomMode
                    ? "Run any custom Docker Hub or OCI container image"
                    : selectedItem?.description}
                </p>
              </div>
              <button
                type="button"
                onClick={() => setDrawerOpen(false)}
                className="text-slate-400 hover:text-slate-700 text-lg font-bold p-1 rounded-md"
              >
                ✕
              </button>
            </div>

            {/* Drawer Scrollable Body */}
            <div className="flex-1 overflow-y-auto p-6 space-y-5">
              {/* Target Deployment Choice (Standalone App vs Attach to Existing) */}
              <div>
                <label className="block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)] mb-2">
                  Deployment Architecture
                </label>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <button
                    type="button"
                    onClick={() => setDeployTarget("standalone")}
                    className={`rounded-xl border p-3.5 text-left transition-all ${
                      deployTarget === "standalone"
                        ? "border-[var(--accent)] bg-blue-50/70 ring-2 ring-[var(--accent)] shadow-2xs"
                        : "border-[var(--border)] hover:bg-slate-50 bg-white"
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <span className="text-sm font-bold text-[var(--ink)]">Standalone Project</span>
                      {deployTarget === "standalone" && (
                        <span className="text-[10px] bg-blue-600 text-white font-bold px-1.5 py-0.5 rounded">
                          Selected
                        </span>
                      )}
                    </div>
                    <p className="mt-1 text-xs text-[var(--ink-muted)] leading-relaxed">
                      Creates a dedicated app project for this service with its own routing, environment, and logs.
                    </p>
                  </button>

                  <button
                    type="button"
                    onClick={() => setDeployTarget("attach")}
                    disabled={projects.length === 0}
                    className={`rounded-xl border p-3.5 text-left transition-all ${
                      deployTarget === "attach"
                        ? "border-[var(--accent)] bg-blue-50/70 ring-2 ring-[var(--accent)] shadow-2xs"
                        : "border-[var(--border)] hover:bg-slate-50 bg-white"
                    } ${projects.length === 0 ? "opacity-50 cursor-not-allowed" : ""}`}
                  >
                    <div className="flex items-center justify-between">
                      <span className="text-sm font-bold text-[var(--ink)]">Attach to App Project</span>
                      {deployTarget === "attach" && (
                        <span className="text-[10px] bg-blue-600 text-white font-bold px-1.5 py-0.5 rounded">
                          Selected
                        </span>
                      )}
                    </div>
                    <p className="mt-1 text-xs text-[var(--ink-muted)] leading-relaxed">
                      Attaches directly into an existing project and auto-injects connection credentials into its .env.
                    </p>
                  </button>
                </div>
              </div>

              {/* Conditional Target Input */}
              {deployTarget === "standalone" ? (
                <div>
                  <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                    New Project Name
                  </label>
                  <Input
                    value={standaloneProjectName}
                    onChange={(e) => setStandaloneProjectName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
                    placeholder="e.g. postgres-production or my-database-app"
                    className="font-medium"
                    required
                  />
                  <p className="mt-1 text-[11px] text-[var(--ink-muted)]">
                    A new project named <code className="font-bold">{standaloneProjectName || "..."}</code> will be created in your organization.
                  </p>
                </div>
              ) : (
                <div>
                  <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                    Select Target Project
                  </label>
                  <select
                    className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm font-semibold text-[var(--ink)] shadow-2xs"
                    value={targetProjectId}
                    onChange={(e) => setTargetProjectId(e.target.value)}
                  >
                    {projects.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name} ({p.id.slice(0, 8)}...)
                      </option>
                    ))}
                  </select>
                  <div className="mt-2 rounded-lg bg-emerald-50 border border-emerald-200 p-2.5 text-xs text-emerald-900 flex items-start gap-2">
                    <span className="text-emerald-700 font-bold">✓</span>
                    <span>
                      Standard aliases (<code className="font-bold">DATABASE_URL</code>, <code className="font-bold">REDIS_URL</code>) will be automatically injected into this project.
                    </span>
                  </div>
                </div>
              )}

              {/* Common Fields */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div>
                  <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                    Instance Name
                  </label>
                  <Input
                    value={instanceName}
                    onChange={(e) => setInstanceName(e.target.value)}
                    placeholder="e.g. postgres-1"
                    className="font-mono text-xs font-semibold"
                    required
                  />
                </div>

                <div>
                  <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                    Environment Scope
                  </label>
                  <select
                    className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-xs font-bold text-[var(--ink)] shadow-2xs"
                    value={targetEnv}
                    onChange={(e) => setTargetEnv(e.target.value)}
                  >
                    <option value="production">PRODUCTION</option>
                    <option value="staging">STAGING</option>
                    <option value="development">DEVELOPMENT</option>
                  </select>
                </div>
              </div>

              {/* Custom Image Fields */}
              {isCustomMode && (
                <div className="space-y-4 pt-3 border-t border-[var(--border)]">
                  <div>
                    <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                      Docker Image Reference
                    </label>
                    <Input
                      value={customImage}
                      onChange={(e) => setCustomImage(e.target.value)}
                      placeholder="e.g. ghost:5-alpine, plausible/analytics:latest"
                      required
                    />
                  </div>

                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <div>
                      <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                        Service Engine / ID
                      </label>
                      <Input
                        value={customEngine}
                        onChange={(e) => setCustomEngine(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
                        placeholder="ghost"
                        required
                      />
                    </div>
                    <div>
                      <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                        Container Port
                      </label>
                      <Input
                        type="number"
                        value={customPort}
                        onChange={(e) => setCustomPort(e.target.value)}
                        placeholder="8080"
                        required
                      />
                    </div>
                  </div>

                  <div>
                    <label className="block text-xs font-bold text-[var(--ink)] mb-1">
                      Description (Optional)
                    </label>
                    <Input
                      value={customDesc}
                      onChange={(e) => setCustomDesc(e.target.value)}
                      placeholder="Headless blogging engine"
                    />
                  </div>
                </div>
              )}
            </div>

            {/* Drawer Fixed Footer (Never requires scrolling) */}
            <div className="border-t border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-end gap-3">
              <Button
                type="button"
                variant="secondary"
                onClick={() => setDrawerOpen(false)}
                disabled={busy}
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={handleProvision}
                disabled={
                  busy ||
                  !instanceName.trim() ||
                  (deployTarget === "standalone" && !standaloneProjectName.trim()) ||
                  (deployTarget === "attach" && !targetProjectId) ||
                  (isCustomMode && (!customImage.trim() || !customEngine.trim()))
                }
                className="font-bold shadow-xs"
              >
                {busy
                  ? "Provisioning..."
                  : deployTarget === "standalone"
                  ? "Create Project & Deploy Add-on"
                  : `Attach ${selectedItem?.name || "Add-on"} to Project`}
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Security Modal */}
      {securityModalAddon && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-xl border border-rose-300 bg-white p-6 shadow-2xl space-y-4">
            <div className="flex items-start gap-3 border-b border-rose-100 pb-3">
              <span className="text-2xl">⚠️</span>
              <div>
                <h3 className="text-base font-bold text-rose-700">
                  Expose Database Port to Public Internet
                </h3>
                <p className="text-xs text-rose-600 font-medium mt-0.5">
                  Target Service: {securityModalAddon.name} ({securityModalAddon.engine})
                </p>
              </div>
            </div>

            <div className="rounded-lg bg-rose-50 p-3.5 border border-rose-200 text-xs text-rose-900 space-y-2 leading-relaxed">
              <p className="font-bold">
                Exposing database ports directly to the public internet makes your database reachable by automated scanners and bots.
              </p>
              <p>
                Applications deployed in your organization connect to this database via the <strong>internal Docker network</strong> without exposing any public ports.
              </p>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (!adminPasswordInput.trim()) {
                  setPasswordError("Admin password is required to expose ports.");
                  return;
                }
                setPublicAccessMap((prev) => ({ ...prev, [securityModalAddon.id]: true }));
                setSecurityModalAddon(null);
              }}
              className="space-y-3"
            >
              <div>
                <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                  Enter Administrator Password
                </label>
                <Input
                  type="password"
                  placeholder="Enter administrator password"
                  value={adminPasswordInput}
                  onChange={(e) => {
                    setAdminPasswordInput(e.target.value);
                    setPasswordError("");
                  }}
                  required
                />
                {passwordError && (
                  <p className="text-xs text-rose-600 font-bold mt-1">{passwordError}</p>
                )}
              </div>

              <div className="flex items-center justify-end gap-2 pt-2 border-t border-[var(--border)]">
                <Button
                  type="button"
                  variant="secondary"
                  onClick={() => setSecurityModalAddon(null)}
                >
                  Keep Private
                </Button>
                <Button type="submit" variant="danger">
                  Expose Publicly
                </Button>
              </div>
            </form>
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
                  Environment Connection Snippet
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
              When your app connects over the internal container network, use the standard environment variable:
            </p>

            <div className="relative">
              <pre className="rounded-lg bg-slate-900 p-4 text-xs text-emerald-400 font-mono overflow-x-auto shadow-inner leading-relaxed">
{`# Injected into your app's .env automatically:
${getAddonEnvSnippet(snippetAddon)}`}
              </pre>
              <button
                type="button"
                onClick={() =>
                  copyToClipboard(getAddonEnvSnippet(snippetAddon), "snippet")
                }
                className="absolute top-2.5 right-2.5 px-2.5 py-1 text-xs font-bold rounded bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 transition-colors"
              >
                {copiedKey === "snippet" ? "✓ Copied!" : "Copy"}
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
    </div>
  );
}
