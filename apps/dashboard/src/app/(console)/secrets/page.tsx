"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project, SecretMeta, ManagedAddon } from "@/lib/types";

const ENVS = [
  { id: "all", label: "All Environments" },
  { id: "production", label: "Production" },
  { id: "staging", label: "Staging" },
  { id: "development", label: "Development" },
  { id: "preview", label: "Preview" },
] as const;

export default function SecretsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [env, setEnv] = useState<string>("all");
  const [newSecretEnv, setNewSecretEnv] = useState<string>("production");
  const [secrets, setSecrets] = useState<SecretMeta[]>([]);
  const [addons, setAddons] = useState<ManagedAddon[]>([]);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [inputValVisible, setInputValVisible] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);

  // Reveal states
  const [showAllSecrets, setShowAllSecrets] = useState(false);
  const [revealedSecrets, setRevealedSecrets] = useState<Record<string, boolean>>({});
  const [revealedAddonPass, setRevealedAddonPass] = useState<Record<string, boolean>>({});
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Bulk Edit state
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkText, setBulkText] = useState("");
  const [bulkTargetEnv, setBulkTargetEnv] = useState("production");
  const [bulkSaving, setBulkSaving] = useState(false);

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
          setError(err instanceof ApiError ? err.message : "Failed to load projects");
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  async function loadSecrets(pid: string, environment: string) {
    if (!currentOrg || !pid) return;
    try {
      const list = await api.listSecrets(currentOrg.id, pid, environment);
      setSecrets(list || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load secrets");
    }
  }

  async function loadAddons(pid: string) {
    if (!currentOrg || !pid) return;
    try {
      const res = await api.listAddons(currentOrg.id, pid);
      setAddons(res.addons || []);
    } catch {
      setAddons([]);
    }
  }

  useEffect(() => {
    if (!projectId) return;
    loadSecrets(projectId, env);
    loadAddons(projectId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId, env]);

  async function onSet() {
    if (!currentOrg || !projectId || !name.trim() || !value) return;
    setBusy(true);
    setError("");
    setSuccess("");
    const targetEnv = env === "all" ? newSecretEnv : env;
    try {
      await api.setSecret(currentOrg.id, projectId, targetEnv, name.trim(), value);
      setName("");
      setValue("");
      setSuccess(`Secret "${name.trim()}" saved to ${targetEnv.toUpperCase()}.`);
      await loadSecrets(projectId, env);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to set secret");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(secretEnvScope: string, secretName: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteSecret(currentOrg.id, projectId, secretEnvScope, secretName);
      setSuccess(`Secret "${secretName}" removed from ${secretEnvScope.toUpperCase()}.`);
      await loadSecrets(projectId, env);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
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

  async function handleBulkSave() {
    if (!currentOrg || !projectId) return;
    const parsed = parseEnvLines(bulkText);
    if (parsed.length === 0) {
      setError("No valid KEY=VALUE pairs found in pasted text.");
      return;
    }
    setBulkSaving(true);
    setError("");
    setSuccess("");
    const targetScope = env === "all" ? bulkTargetEnv : env;
    try {
      for (const item of parsed) {
        await api.setSecret(currentOrg.id, projectId, targetScope, item.key, item.value);
      }
      setSuccess(`Successfully imported ${parsed.length} variables into ${targetScope.toUpperCase()}.`);
      setBulkOpen(false);
      setBulkText("");
      await loadSecrets(projectId, env);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to bulk save environment variables");
    } finally {
      setBulkSaving(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Secrets require an active organization."
      />
    );
  }

  const parsedCount = parseEnvLines(bulkText).length;
  const currentProjectName = projects.find((p) => p.id === projectId)?.name || "Project";

  return (
    <div className="animate-fade-up space-y-6">
      <PageHeader
        title="Secrets & Environment Variables"
        description="Plaintext & encrypted runtime environment variables with connected database add-on credentials."
        actions={
          <Button
            variant="secondary"
            onClick={() => {
              setBulkTargetEnv(env === "all" ? "production" : env);
              setBulkOpen(true);
              setError("");
            }}
            className="font-bold text-xs"
          >
            📋 Bulk Edit (.env)
          </Button>
        }
      />

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* Target Project and Scope Navigation */}
      <div className="jp-card p-5 space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Target Project
            </label>
            <select
              className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm font-semibold text-[var(--ink)] shadow-2xs focus:border-[var(--accent)] focus:outline-none"
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
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Environment Scope View
            </label>
            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
              {ENVS.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  onClick={() => setEnv(e.id)}
                  className={`px-3 py-1.5 text-xs font-bold rounded-md transition-all ${
                    env === e.id
                      ? "bg-[var(--accent)] text-white shadow-2xs"
                      : "bg-slate-100 text-slate-700 hover:bg-slate-200"
                  }`}
                >
                  {e.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* Add Variable Form */}
        <div className="border-t border-[var(--border)] pt-4">
          <div className="flex items-center justify-between mb-2">
            <label className="block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Add or Update Environment Variable
            </label>
            <button
              type="button"
              onClick={() => {
                setBulkTargetEnv(env === "all" ? "production" : env);
                setBulkOpen(true);
              }}
              className="text-xs font-bold text-[var(--accent)] hover:underline"
            >
              Need to add multiple? Switch to Bulk Edit →
            </button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {env === "all" && (
              <select
                className="w-36 rounded-md border border-[var(--border)] bg-white px-2.5 py-2 text-xs font-bold text-[var(--ink)] shadow-2xs"
                value={newSecretEnv}
                onChange={(e) => setNewSecretEnv(e.target.value)}
              >
                <option value="production">PRODUCTION</option>
                <option value="staging">STAGING</option>
                <option value="development">DEVELOPMENT</option>
                <option value="preview">PREVIEW</option>
              </select>
            )}
            <Input
              placeholder="VARIABLE_NAME"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="min-w-[180px] flex-1 font-mono uppercase"
            />
            <div className="relative min-w-[220px] flex-2">
              <Input
                placeholder="Secret Value"
                type={inputValVisible ? "text" : "password"}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                className="pr-10"
              />
              <button
                type="button"
                onClick={() => setInputValVisible(!inputValVisible)}
                className="absolute right-2.5 top-2.5 text-xs text-slate-400 hover:text-slate-700"
                title={inputValVisible ? "Hide value" : "Show value"}
              >
                {inputValVisible ? "🙈" : "👁️"}
              </button>
            </div>
            <Button onClick={onSet} disabled={busy || !name.trim() || !value}>
              Save Variable
            </Button>
          </div>
        </div>
      </div>

      {/* Connected Add-ons & Service Credentials Card */}
      {addons.length > 0 && (
        <div className="jp-card overflow-hidden border-emerald-500/30">
          <div className="border-b border-emerald-500/20 bg-emerald-50/60 px-4 py-3 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="text-base">🔌</span>
              <div>
                <h3 className="text-xs font-bold uppercase tracking-wider text-emerald-950">
                  Connected Add-ons & Database Credentials ({addons.length})
                </h3>
                <p className="text-[11px] text-emerald-700">
                  Variables automatically injected into this app. Full credentials visible and copyable below.
                </p>
              </div>
            </div>
            <span className="rounded bg-emerald-200/60 px-2 py-0.5 text-[10px] font-bold text-emerald-800 uppercase">
              Auto-Injected
            </span>
          </div>

          <div className="divide-y divide-[var(--border)] p-4 space-y-4">
            {addons.map((addon) => {
              const meta = addon.metadata || {};
              const host = meta.internal_host || "127.0.0.1";
              const port = meta.internal_port || meta.port || "5432";
              const user = meta.username || meta.user || "admin";
              const pass = meta.password || meta.pass || "••••••••";
              const db = meta.database || meta.dbname || addon.name;
              const intUrl = meta.connection_url || addon.connection_hint || `${addon.engine}://${user}:${pass}@${host}:${port}/${db}`;
              const injectedKey = meta.injected_key || (addon.engine.toLowerCase().includes("redis") ? "REDIS_URL" : "DATABASE_URL");
              const isPassRevealed = revealedAddonPass[addon.id];

              return (
                <div key={addon.id} className="pt-3 first:pt-0 space-y-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="rounded bg-emerald-100 text-emerald-800 px-2 py-0.5 text-xs font-bold uppercase">
                        {addon.engine}
                      </span>
                      <span className="font-bold text-sm text-[var(--ink)]">{addon.name}</span>
                      <span className="text-xs text-[var(--ink-muted)]">
                        (Injected as <code className="font-mono font-bold text-emerald-700">{injectedKey}</code>)
                      </span>
                    </div>

                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => copyToClipboard(`${injectedKey}=${intUrl}`, `env-${addon.id}`)}
                        className="text-xs font-semibold px-2 py-1 rounded border border-[var(--border)] hover:bg-slate-50 text-[var(--ink-secondary)]"
                      >
                        {copiedKey === `env-${addon.id}` ? "✓ Copied .env Line" : "📋 Copy .env Line"}
                      </button>
                    </div>
                  </div>

                  <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs">
                    <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                      <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Host & Port</span>
                      <code className="font-mono font-semibold text-[var(--ink)]">{host}:{port}</code>
                    </div>

                    <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                      <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Database Name</span>
                      <code className="font-mono font-semibold text-[var(--ink)]">{db}</code>
                    </div>

                    <div className="rounded-lg bg-slate-50 border border-[var(--border)] p-2.5">
                      <span className="text-[10px] uppercase font-bold text-[var(--ink-muted)] block">Username</span>
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

      {/* Variables Table */}
      {secrets.length === 0 ? (
        <EmptyState
          title="No secrets configured"
          description={`No environment variables defined in ${env === "all" ? "any scope" : env.toUpperCase()} yet. Click "Bulk Edit (.env)" to paste your config.`}
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex items-center justify-between">
            <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Variables in {env === "all" ? "All Scopes" : env.toUpperCase()} ({secrets.length})
            </h4>
            <div className="flex items-center gap-2">
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
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-slate-50/70 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                <th className="px-4 py-3">Key Name</th>
                <th className="px-4 py-3">Environment</th>
                <th className="px-4 py-3">Value</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {secrets.map((s) => {
                const isRevealed = showAllSecrets || revealedSecrets[s.id];
                const displayVal = s.value || s.value_hint || "";
                return (
                  <tr key={s.id} className="hover:bg-slate-50/50 transition-colors">
                    <td className="px-4 py-3 font-mono font-bold text-[var(--ink)]">{s.name}</td>
                    <td className="px-4 py-3">
                      <span className="rounded bg-slate-200/80 px-2 py-0.5 font-mono text-[10px] font-bold text-slate-800 uppercase">
                        {s.environment}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2 max-w-md">
                        <code className="font-mono text-xs text-slate-700 truncate bg-slate-100 rounded px-2 py-1 flex-1">
                          {isRevealed ? displayVal : "••••••••••••"}
                        </code>
                        <button
                          type="button"
                          onClick={() =>
                            setRevealedSecrets((prev) => ({
                              ...prev,
                              [s.id]: !prev[s.id],
                            }))
                          }
                          className="text-xs text-slate-500 hover:text-slate-900 p-1"
                          title={isRevealed ? "Hide value" : "Reveal value"}
                        >
                          {isRevealed ? "🙈" : "👁️"}
                        </button>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(displayVal, s.id)}
                          className="text-xs text-[var(--accent)] font-bold hover:underline px-1"
                        >
                          {copiedKey === s.id ? "✓ Copied" : "Copy"}
                        </button>
                      </div>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <Button
                        variant="danger"
                        size="sm"
                        disabled={busy}
                        onClick={() => onDelete(s.environment, s.name)}
                      >
                        Delete
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Bulk Edit (.env) Modal */}
      {bulkOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs">
          <div className="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-white shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
            <div className="border-b border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-between">
              <div>
                <h3 className="text-base font-bold text-[var(--ink)] flex items-center gap-2">
                  <span>Bulk Edit Environment Variables</span>
                  <span className="rounded bg-slate-200 px-2 py-0.5 text-[10px] font-bold uppercase text-slate-700">
                    {env === "all" ? bulkTargetEnv : env}
                  </span>
                </h3>
                <p className="text-xs text-[var(--ink-muted)] mt-0.5">
                  Paste raw <code className="font-bold">.env</code> lines. Lines starting with # are ignored.
                </p>
              </div>
              <button
                type="button"
                onClick={() => setBulkOpen(false)}
                className="text-slate-400 hover:text-slate-700 text-lg font-bold p-1"
              >
                ✕
              </button>
            </div>

            <div className="p-6 space-y-4 flex-1 overflow-y-auto">
              {env === "all" && (
                <div>
                  <label className="block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)] mb-1">
                    Select Target Environment for Imported Variables
                  </label>
                  <select
                    className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm font-semibold text-[var(--ink)] shadow-2xs"
                    value={bulkTargetEnv}
                    onChange={(e) => setBulkTargetEnv(e.target.value)}
                  >
                    <option value="production">PRODUCTION</option>
                    <option value="staging">STAGING</option>
                    <option value="development">DEVELOPMENT</option>
                    <option value="preview">PREVIEW</option>
                  </select>
                </div>
              )}

              <div>
                <div className="flex items-center justify-between mb-1.5 text-xs">
                  <span className="font-bold text-[var(--ink-secondary)]">.env Content</span>
                  <span className="font-mono text-emerald-600 font-bold">
                    {parsedCount} variable{parsedCount === 1 ? "" : "s"} detected
                  </span>
                </div>
                <textarea
                  className="w-full h-72 rounded-xl border border-[var(--border)] bg-slate-900 p-4 font-mono text-xs text-emerald-400 shadow-inner focus:border-[var(--accent)] focus:outline-none leading-relaxed"
                  placeholder={`# Paste your environment variables:
PORT=3000
DATABASE_URL=postgres://user:password@host:5432/dbname
REDIS_URL=redis://localhost:6379
JWT_SECRET=super_secret_key_123
NEXT_PUBLIC_API_URL=https://api.domain.com`}
                  value={bulkText}
                  onChange={(e) => setBulkText(e.target.value)}
                />
              </div>

              <div className="rounded-lg bg-blue-50 border border-blue-200 p-3 text-xs text-blue-900 flex items-start gap-2">
                <span className="font-bold text-blue-700">ℹ</span>
                <span>
                  All detected variables will be securely saved to <strong>{(env === "all" ? bulkTargetEnv : env).toUpperCase()}</strong> for project <strong>{currentProjectName}</strong>.
                </span>
              </div>
            </div>

            <div className="border-t border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-end gap-3">
              <Button
                type="button"
                variant="secondary"
                onClick={() => setBulkOpen(false)}
                disabled={bulkSaving}
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={handleBulkSave}
                disabled={bulkSaving || parsedCount === 0}
                className="font-bold shadow-xs"
              >
                {bulkSaving ? "Saving Variables..." : `Save ${parsedCount} Variable${parsedCount === 1 ? "" : "s"}`}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
