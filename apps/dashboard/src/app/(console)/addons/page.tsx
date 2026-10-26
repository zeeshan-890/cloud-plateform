"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { AddonCatalogItem, ManagedAddon, Project } from "@/lib/types";

export default function AddonsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [catalog, setCatalog] = useState<AddonCatalogItem[]>([]);
  const [addons, setAddons] = useState<ManagedAddon[]>([]);
  const [mode, setMode] = useState("");
  const [engine, setEngine] = useState("redis");
  const [name, setName] = useState("");

  // Custom application state
  const [tab, setTab] = useState<"catalog" | "custom">("catalog");
  const [customEngine, setCustomEngine] = useState("");
  const [customImage, setCustomImage] = useState("");
  const [customPort, setCustomPort] = useState("8080");
  const [customDesc, setCustomDesc] = useState("");

  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedCategory, setSelectedCategory] = useState("all");

  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [snippetAddon, setSnippetAddon] = useState<ManagedAddon | null>(null);
  const [hostName, setHostName] = useState("localhost");

  // Public access is strictly OFF by default for security
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
    if (addon.metadata?.internal_url) return String(addon.metadata.internal_url);
    const port = addon.metadata?.port || 8080;
    if (addon.engine === "redis" || addon.engine === "valkey") return `redis://redis:6379/0`;
    if (addon.engine === "postgres") return `postgres://jp:jp_dev_password@postgres:5432/jp`;
    if (addon.engine === "mysql" || addon.engine === "mariadb") return `mysql://jp:jp_dev_password@mysql:3306/jp`;
    if (addon.engine === "mongodb") return `mongodb://jp:jp_dev_password@mongodb:27017/jp`;
    return `http://addon-${addon.name}:${port}`;
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

  const selected = catalog.find((c) => c.id === engine);

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
    const [cat, list] = await Promise.all([
      api.addonCatalog(currentOrg.id, pid),
      api.listAddons(currentOrg.id, pid),
    ]);
    setCatalog(cat.catalog);
    setMode(cat.mode || list.mode || "");
    setAddons(list.addons);
    if (cat.catalog[0] && !cat.catalog.find((c) => c.id === engine)) {
      setEngine(cat.catalog[0].id);
    }
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await load(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load add-ons");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onCreate() {
    if (!currentOrg || !projectId || !name.trim() || !engine) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const created = await api.createAddon(currentOrg.id, projectId, engine, name.trim());
      setName("");
      setSuccess(
        `Provisioned ${created.engine}/${created.name}. Secret ref: ${created.secret_ref || "created"}.`,
      );
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateCustom() {
    if (!currentOrg || !projectId || !name.trim() || !customEngine.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const portNum = parseInt(customPort, 10) || 8080;
      const created = await api.createAddon(
        currentOrg.id,
        projectId,
        customEngine.trim(),
        name.trim(),
        "development",
        {
          docker_image: customImage.trim(),
          port: portNum,
          description: customDesc.trim() || undefined,
        },
      );
      setName("");
      setCustomEngine("");
      setCustomImage("");
      setCustomDesc("");
      setSuccess(`Provisioned custom app: ${created.engine}/${created.name}.`);
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create custom app failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteAddon(currentOrg.id, projectId, id);
      setSuccess("Add-on deleted.");
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Add-ons require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Add-ons & Marketplace"
        description="Provision one-click databases or deploy any custom Docker application on demand."
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}
      {success ? (
        <div className="mb-4">
          <Alert tone="success">{success}</Alert>
        </div>
      ) : null}

      <div className="mb-5 rounded-lg border border-blue-200 bg-blue-50/80 p-3.5 text-xs text-blue-950 flex items-start gap-3 shadow-2xs">
        <span className="text-base font-bold text-blue-600 mt-0.5">💡</span>
        <div className="flex-1">
          <p className="font-bold text-blue-900 text-sm">Zero-Domain Architecture & Internal Express Linking</p>
          <p className="mt-1 text-blue-800 leading-relaxed font-medium">
            All services communicate over the internal Docker network (<code className="bg-blue-100 text-blue-900 px-1.5 py-0.5 rounded font-mono font-bold">jp</code>). Connect your Express, Node.js, Python, or Go servers using the <strong>Docker Internal URLs</strong> below. For public outside browser access without any domain name, open apps directly via your <strong>Server IP and Port</strong> (<code className="bg-blue-100 text-blue-900 px-1.5 py-0.5 rounded font-mono font-bold">http://{hostName}:PORT</code>).
          </p>
        </div>
      </div>

      <div className="mb-6 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-sm text-[var(--ink-muted)]">
            Provision mode:{" "}
            <span className="font-medium text-[var(--ink)]">{mode || "unknown"}</span>
          </p>
          <p className="mt-1 text-xs text-[var(--ink-faint)]">
            Services are only created when you explicitly request them. Zero background bloat.
          </p>
        </div>

        <div className="flex rounded-md border border-[var(--border)] bg-[var(--paper)] p-1">
          <button
            type="button"
            onClick={() => setTab("catalog")}
            className={`px-3 py-1 text-xs font-medium rounded ${
              tab === "catalog"
                ? "bg-[var(--brand)] text-[var(--paper)]"
                : "text-[var(--ink-muted)] hover:text-[var(--ink)]"
            }`}
          >
            One-Click Catalog ({catalog.length})
          </button>
          <button
            type="button"
            onClick={() => setTab("custom")}
            className={`px-3 py-1 text-xs font-medium rounded ${
              tab === "custom"
                ? "bg-[var(--brand)] text-[var(--paper)]"
                : "text-[var(--ink-muted)] hover:text-[var(--ink)]"
            }`}
          >
            + Deploy Any Custom Container
          </button>
        </div>
      </div>

      <div className="mb-4 flex flex-col sm:flex-row gap-3 items-stretch sm:items-center justify-between">
        <label className="block max-w-sm text-sm text-[var(--ink-muted)]">
          Target Project
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

        {tab === "catalog" && (
          <div className="w-full sm:w-72">
            <label className="block text-sm text-[var(--ink-muted)]">
              Search Catalog
              <Input
                className="mt-1"
                placeholder="Search 240+ apps (e.g. jellyfin, postgres, wireguard, n8n, ollama)..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
            </label>
          </div>
        )}
      </div>

      {tab === "catalog" ? (
        <>
          {/* Category Filter Pills */}
          <div className="mb-4 flex flex-wrap gap-1.5">
            {[
              { id: "all", label: "All Apps" },
              { id: "database", label: "Databases" },
              { id: "cache", label: "Cache & Key-Value" },
              { id: "queue", label: "Queues & Streaming" },
              { id: "search", label: "Search" },
              { id: "ai", label: "AI & Vector DBs" },
              { id: "analytics", label: "Analytics & BI" },
              { id: "media", label: "Media & Streaming" },
              { id: "networking", label: "Networking & VPN" },
              { id: "productivity", label: "Productivity & Office" },
              { id: "communication", label: "Chat & Social" },
              { id: "finance", label: "Finance & Accounting" },
              { id: "tool", label: "Dev Tools & Automation" },
              { id: "cms", label: "CMS & Publishing" },
              { id: "storage", label: "Storage & Sync" },
              { id: "security", label: "Security & Auth" },
              { id: "monitoring", label: "Monitoring" },
              { id: "gaming", label: "Game Servers" },
            ].map((cat) => (
              <button
                key={cat.id}
                type="button"
                onClick={() => setSelectedCategory(cat.id)}
                className={`px-2.5 py-1 text-xs rounded-full border transition-colors ${
                  selectedCategory === cat.id
                    ? "border-[var(--brand)] bg-[var(--brand)] text-[var(--paper)] font-medium"
                    : "border-[var(--border)] text-[var(--ink-muted)] hover:border-[var(--ink-muted)] bg-[var(--surface)]"
                }`}
              >
                {cat.label}
              </button>
            ))}
          </div>

          {filteredCatalog.length > 0 ? (
            <div className="mb-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 max-h-[520px] overflow-y-auto p-1 border border-[var(--border)] rounded-lg bg-[var(--paper)]">
              {filteredCatalog.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  onClick={() => setEngine(c.id)}
                  className={`rounded-md border p-3.5 text-left transition-all ${
                    engine === c.id
                      ? "border-[var(--brand)] bg-[var(--surface)] shadow-sm ring-1 ring-[var(--brand)]"
                      : "border-[var(--border)] hover:bg-[var(--surface-hover)] bg-[var(--surface)]"
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-semibold text-[var(--ink)]">{c.name}</span>
                    <span className="text-[10px] uppercase font-mono px-1.5 py-0.5 rounded bg-[var(--paper)] border border-[var(--border)] text-[var(--ink-faint)]">
                      {c.category}
                    </span>
                  </div>
                  <p className="mt-1.5 text-xs text-[var(--ink-muted)] line-clamp-2 leading-relaxed">
                    {c.description}
                  </p>
                  <div className="mt-2 text-[11px] font-mono text-[var(--brand)]">
                    Default Port: {c.default_port ? `:${c.default_port}` : "File/Embedded"}
                  </div>
                </button>
              ))}
            </div>
          ) : (
            <div className="p-8 text-center text-xs text-[var(--ink-muted)] border border-dashed border-[var(--border)] rounded-md mb-5">
              No matching applications found. You can deploy it using the "+ Deploy Any Custom Container" tab above!
            </div>
          )}

          {selected ? (
            <div className="mb-6 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
              <h2 className="text-sm font-semibold text-[var(--ink)]">
                Selected: {selected.name} ({selected.id})
              </h2>
              <p className="mt-1 text-xs text-[var(--ink-muted)]">{selected.description}</p>
              {selected.secret_keys?.length ? (
                <p className="mt-2 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-faint)]">
                  Secret key pattern: {selected.secret_keys.join(", ")}
                </p>
              ) : null}
            </div>
          ) : null}

          <div className="mb-8 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
            <h2 className="mb-3 text-sm font-semibold text-[var(--ink)]">Create add-on instance</h2>
            <div className="flex max-w-xl flex-wrap items-end gap-3">
              <label className="block flex-1 text-sm text-[var(--ink-muted)]">
                Instance Name
                <Input
                  className="mt-1"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="cache"
                />
              </label>
              <Button type="button" onClick={onCreate} disabled={busy || !projectId || !name.trim()}>
                Provision {engine}
              </Button>
            </div>
          </div>
        </>
      ) : (
        /* Custom App Form */
        <div className="mb-8 rounded-xl border border-[var(--brand)] bg-[var(--surface)] p-5 space-y-4">
          <div>
            <h2 className="text-base font-semibold text-[var(--ink)]">Deploy Any Custom Container App</h2>
            <p className="text-xs text-[var(--ink-muted)]">
              Run any Docker Hub or OCI image. Automatically attaches internal networking and injects credentials.
            </p>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Instance Name
              </label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-ghost-blog"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Engine / Identifier
              </label>
              <Input
                value={customEngine}
                onChange={(e) => setCustomEngine(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
                placeholder="ghost"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Docker Image Ref
              </label>
              <Input
                value={customImage}
                onChange={(e) => setCustomImage(e.target.value)}
                placeholder="ghost:5-alpine or plausible/analytics:latest"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Exposed Port
              </label>
              <Input
                type="number"
                value={customPort}
                onChange={(e) => setCustomPort(e.target.value)}
                placeholder="2368"
              />
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              Description (Optional)
            </label>
            <Input
              value={customDesc}
              onChange={(e) => setCustomDesc(e.target.value)}
              placeholder="Headless blogging engine"
            />
          </div>

          <div className="pt-2">
            <Button
              type="button"
              onClick={onCreateCustom}
              disabled={busy || !projectId || !name.trim() || !customEngine.trim()}
            >
              {busy ? "Deploying Custom App..." : "Deploy Custom Container"}
            </Button>
          </div>
        </div>
      )}

      {/* Active Instances Table */}
      {addons.length === 0 ? (
        <EmptyState
          title="No active add-ons or custom apps"
          description="Provision a database above or deploy any custom container app when you need one."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[850px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
                <th className="py-2 pr-3 font-medium">Engine / App</th>
                <th className="py-2 pr-3 font-medium">Status</th>
                <th className="py-2 pr-3 font-medium">Docker Internal URL (For Express)</th>
                <th className="py-2 pr-3 font-medium">Public Access (Server IP)</th>
                <th className="py-2 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {addons.map((a) => {
                const intUrl = getInternalUrl(a);
                const pubUrl = getPublicUrl(a);
                return (
                  <tr key={a.id} className="border-b border-[var(--border)] hover:bg-[var(--surface)] transition-colors">
                    <td className="py-3 pr-3 font-medium text-[var(--ink)]">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs uppercase px-1.5 py-0.5 rounded bg-[var(--paper)] border border-[var(--border)]">
                          {a.engine}
                        </span>
                        <span className="font-semibold">{a.name}</span>
                      </div>
                      <p className="mt-0.5 text-xs text-[var(--ink-muted)] font-mono">
                        Key: {a.secret_ref || `${a.engine.toUpperCase()}_URL`}
                      </p>
                    </td>

                    <td className="py-3 pr-3">
                      <span className="inline-block px-2 py-0.5 text-xs rounded bg-emerald-500/20 text-emerald-400 font-medium">
                        {a.status}
                      </span>
                      <p className="text-[10px] text-[var(--ink-faint)] mt-0.5 capitalize">{a.mode}</p>
                    </td>

                    <td className="py-3 pr-3">
                      <div className="flex items-center gap-2">
                        <code className="text-xs font-mono text-[var(--ink)] bg-[var(--paper)] px-2 py-1 rounded border border-[var(--border)] max-w-xs truncate">
                          {intUrl}
                        </code>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(intUrl, `int-${a.id}`)}
                          className="px-2 py-1 text-xs font-medium rounded border border-[var(--border)] bg-[var(--surface)] hover:bg-[var(--paper)] text-[var(--ink-muted)] hover:text-[var(--ink)] transition-colors shrink-0"
                          title="Copy for Express.js / Node.js"
                        >
                          {copiedKey === `int-${a.id}` ? "✓ Copied!" : "Copy"}
                        </button>
                      </div>
                      <p className="text-[10px] text-[var(--ink-muted)] mt-1">Inter-container Docker network</p>
                    </td>

                    <td className="py-3 pr-3">
                      {publicAccessMap[a.id] ? (
                        <div className="space-y-1">
                          <div className="flex items-center gap-2">
                            <span className="rounded bg-amber-50 border border-amber-300 px-2 py-0.5 text-[10px] font-bold text-amber-800">
                              🔓 Public Exposed
                            </span>
                            <button
                              type="button"
                              onClick={() => setPublicAccessMap((prev) => ({ ...prev, [a.id]: false }))}
                              className="text-[11px] text-red-600 font-bold hover:underline"
                            >
                              Disable
                            </button>
                          </div>
                          <code className="text-xs font-mono text-[var(--ink)] bg-[var(--paper)] px-2 py-0.5 rounded border border-[var(--border)] block max-w-xs truncate">
                            {pubUrl}
                          </code>
                        </div>
                      ) : (
                        <div className="flex items-center gap-2">
                          <span className="rounded bg-emerald-50 border border-emerald-300 px-2 py-0.5 text-[10px] font-bold text-emerald-800 flex items-center gap-1">
                            <span>🔒</span> Private (Default Off)
                          </span>
                          <button
                            type="button"
                            onClick={() => {
                              setSecurityModalAddon(a);
                              setAdminPasswordInput("");
                              setPasswordError("");
                            }}
                            className="text-xs font-bold text-blue-600 hover:text-blue-800 hover:underline"
                          >
                            Expose Publicly
                          </button>
                        </div>
                      )}
                      <p className="text-[10px] text-[var(--ink-muted)] mt-1">
                        {publicAccessMap[a.id] ? "Accessible over internet" : "Isolated inside container network"}
                      </p>
                    </td>

                    <td className="py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          type="button"
                          variant="ghost"
                          onClick={() => setSnippetAddon(a)}
                          className="text-xs"
                        >
                          Express .env
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => onDelete(a.id)}
                          className="text-xs text-rose-400 hover:text-rose-300 hover:bg-rose-500/10"
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

      {/* Security Warning & Admin Password Confirmation Modal */}
      {securityModalAddon && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-xl border border-red-300 bg-white p-6 shadow-2xl space-y-4">
            <div className="flex items-start gap-3 border-b border-red-100 pb-3">
              <span className="text-2xl">⚠️</span>
              <div>
                <h3 className="text-base font-bold text-red-700">
                  Critical Security Warning: Expose Database Publicly
                </h3>
                <p className="text-xs text-red-600 font-medium mt-0.5">
                  Target Service: {securityModalAddon.name} ({securityModalAddon.engine})
                </p>
              </div>
            </div>

            <div className="rounded-lg bg-red-50 p-3.5 border border-red-200 text-xs text-red-900 space-y-2 leading-relaxed">
              <p className="font-bold">
                Exposing database ports directly to the public internet makes your database reachable by automated botnets, credential brute-forcing, and ransomware scanners.
              </p>
              <p>
                Applications deployed on JP Cloud Platform (Express.js, Next.js, FastAPI, Go) can securely connect to this database through the <strong>internal Docker network</strong> without exposing any public ports.
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
                  Enter Administrator Password to Confirm Exposure
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
                  <p className="text-xs text-red-600 font-bold mt-1">{passwordError}</p>
                )}
              </div>

              <div className="flex items-center justify-end gap-2 pt-2 border-t border-[var(--border)]">
                <Button
                  type="button"
                  variant="secondary"
                  onClick={() => setSecurityModalAddon(null)}
                >
                  Cancel (Keep Secure)
                </Button>
                <Button type="submit" variant="danger">
                  Confirm Public Exposure
                </Button>
              </div>
            </form>
          </div>
        </div>
      )}

      {snippetAddon && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-xl rounded-lg border border-[var(--border)] bg-[var(--paper)] p-6 shadow-xl">
            <div className="flex items-center justify-between pb-3 border-b border-[var(--border)]">
              <h3 className="text-base font-semibold text-[var(--ink)]">
                Express / Node.js Environment Integration
              </h3>
              <button
                type="button"
                onClick={() => setSnippetAddon(null)}
                className="text-sm text-[var(--ink-muted)] hover:text-[var(--ink)]"
              >
                ✕
              </button>
            </div>

            <p className="mt-3 text-xs text-[var(--ink-muted)] leading-relaxed">
              When your Express server or backend runs in JP or Docker, connect to <strong>{snippetAddon.name}</strong> over the internal network without needing a domain name:
            </p>

            <div className="mt-3 relative">
              <pre className="rounded bg-black/70 p-3 text-xs text-emerald-400 font-mono overflow-x-auto border border-zinc-800">
{`# Add to your Express server .env file:
${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}=${getInternalUrl(snippetAddon)}

# Optional: Public access via Server IP from outside
${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}_PUBLIC=${getPublicUrl(snippetAddon)}`}
              </pre>
              <button
                type="button"
                onClick={() => copyToClipboard(
`${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}=${getInternalUrl(snippetAddon)}
${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`}_PUBLIC=${getPublicUrl(snippetAddon)}`,
                  "snippet"
                )}
                className="absolute top-2 right-2 px-2.5 py-1 text-xs font-medium rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200 border border-zinc-700 transition-colors"
              >
                {copiedKey === "snippet" ? "✓ Copied!" : "Copy Snippet"}
              </button>
            </div>

            <p className="mt-4 text-xs text-[var(--ink-muted)]">
              In your Express code (<code className="text-zinc-300 font-mono">server.js</code>):
            </p>
            <pre className="mt-1 rounded bg-black/70 p-3 text-xs text-zinc-300 font-mono overflow-x-auto border border-zinc-800">
{`// Connect using the internal Docker URL:
const url = process.env.${snippetAddon.secret_ref || `${snippetAddon.engine.toUpperCase()}_URL`};
console.log('Connected to ${snippetAddon.engine} at:', url);`}
            </pre>

            <div className="mt-5 flex justify-end">
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
