"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project, SecretMeta } from "@/lib/types";

const ENVS = ["development", "preview", "staging", "production"] as const;

export default function SecretsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [env, setEnv] = useState<string>("development");
  const [secrets, setSecrets] = useState<SecretMeta[]>([]);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);

  // Bulk Edit state
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkText, setBulkText] = useState("");
  const [bulkSaving, setBulkSaving] = useState(false);

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
    const list = await api.listSecrets(currentOrg.id, pid, environment);
    setSecrets(list);
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadSecrets(projectId, env);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load secrets");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId, env]);

  async function onSet() {
    if (!currentOrg || !projectId || !name.trim() || !value) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.setSecret(currentOrg.id, projectId, env, name.trim(), value);
      setName("");
      setValue("");
      setSuccess(`Secret "${name.trim()}" saved.`);
      await loadSecrets(projectId, env);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to set secret");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(secretName: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteSecret(currentOrg.id, projectId, env, secretName);
      setSuccess(`Secret "${secretName}" removed.`);
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
    try {
      for (const item of parsed) {
        await api.setSecret(currentOrg.id, projectId, env, item.key, item.value);
      }
      setSuccess(`Successfully imported and updated ${parsed.length} variables into ${env.toUpperCase()}.`);
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

  return (
    <div className="animate-fade-up space-y-6">
      <PageHeader
        title="Secrets & Environment Variables"
        description="Encrypted env vars per development / preview / staging / production. Injected securely into runtime containers."
        actions={
          <Button
            variant="secondary"
            onClick={() => {
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
              Environment Scope
            </label>
            <select
              className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm font-semibold text-[var(--ink)] shadow-2xs focus:border-[var(--accent)] focus:outline-none"
              value={env}
              onChange={(e) => setEnv(e.target.value)}
            >
              {ENVS.map((e) => (
                <option key={e} value={e}>
                  {e.toUpperCase()}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="border-t border-[var(--border)] pt-4">
          <div className="flex items-center justify-between mb-2">
            <label className="block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Add or Update Single Variable
            </label>
            <button
              type="button"
              onClick={() => setBulkOpen(true)}
              className="text-xs font-bold text-[var(--accent)] hover:underline"
            >
              Need to add multiple? Switch to Bulk Edit →
            </button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              placeholder="VARIABLE_NAME"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="min-w-[180px] flex-1 font-mono uppercase"
            />
            <Input
              placeholder="Secret Value"
              type="password"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className="min-w-[220px] flex-2"
            />
            <Button onClick={onSet} disabled={busy || !name.trim() || !value}>
              Save Secret
            </Button>
          </div>
        </div>
      </div>

      {secrets.length === 0 ? (
        <EmptyState
          title="No secrets configured"
          description={`No environment variables defined in ${env.toUpperCase()} scope yet. Click "Bulk Edit (.env)" to paste your config.`}
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3 flex items-center justify-between">
            <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Encrypted Variables ({secrets.length})
            </h4>
            <span className="rounded bg-slate-200/70 px-2 py-0.5 text-[10px] font-bold text-slate-700 uppercase">
              {env}
            </span>
          </div>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-slate-50/70 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                <th className="px-4 py-3">Key Name</th>
                <th className="px-4 py-3">Version</th>
                <th className="px-4 py-3">Value Fingerprint</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {secrets.map((s) => (
                <tr key={s.id} className="hover:bg-slate-50/50 transition-colors">
                  <td className="px-4 py-3 font-mono font-bold text-[var(--ink)]">{s.name}</td>
                  <td className="px-4 py-3">
                    <span className="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs font-semibold text-slate-700">
                      v{s.current_version}
                    </span>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-[var(--ink-muted)]">{s.value_hint}</td>
                  <td className="px-4 py-3 text-right">
                    <Button variant="danger" size="sm" disabled={busy} onClick={() => onDelete(s.name)}>
                      Delete
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* ========================================================
          BULK EDIT (.env) MODAL
          ======================================================== */}
      {bulkOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs">
          <div className="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-white shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
            <div className="border-b border-[var(--border)] bg-slate-50 px-6 py-4 flex items-center justify-between">
              <div>
                <h3 className="text-base font-bold text-[var(--ink)] flex items-center gap-2">
                  <span>Bulk Edit Environment Variables</span>
                  <span className="rounded bg-slate-200 px-2 py-0.5 text-[10px] font-bold uppercase text-slate-700">
                    {env}
                  </span>
                </h3>
                <p className="text-xs text-[var(--ink-muted)] mt-0.5">
                  Paste or write raw <code className="font-bold">.env</code> lines. Lines starting with # are ignored.
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
                  All detected variables will be securely encrypted and saved to the <strong>{env.toUpperCase()}</strong> environment of project <strong>{projects.find((p) => p.id === projectId)?.name}</strong>.
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
