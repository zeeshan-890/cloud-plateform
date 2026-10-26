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
    try {
      await api.setSecret(currentOrg.id, projectId, env, name.trim(), value);
      setName("");
      setValue("");
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
    try {
      await api.deleteSecret(currentOrg.id, projectId, env, secretName);
      await loadSecrets(projectId, env);
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
        description="Secrets require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Secrets & environments"
        description="Encrypted env vars per development / preview / staging / production. Values are never shown after save."
      />
      {error ? <Alert>{error}</Alert> : null}

      <div className="jp-card p-5 mb-6 space-y-4">
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
          <label className="mb-2 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
            Add or Update Environment Variable
          </label>
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
        <EmptyState title="No secrets configured" description={`No environment variables defined in ${env.toUpperCase()} scope yet.`} />
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
    </div>
  );
}
