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

      <div className="mb-6 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Project</span>
          <select
            className="rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2"
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
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Environment</span>
          <select
            className="rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2"
            value={env}
            onChange={(e) => setEnv(e.target.value)}
          >
            {ENVS.map((e) => (
              <option key={e} value={e}>
                {e}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="mb-8 flex flex-wrap items-end gap-3">
        <Input
          placeholder="KEY"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="max-w-[180px]"
        />
        <Input
          placeholder="value"
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="max-w-[240px]"
        />
        <Button onClick={onSet} disabled={busy || !name.trim() || !value}>
          Set secret
        </Button>
      </div>

      {secrets.length === 0 ? (
        <EmptyState title="No secrets" description={`Nothing stored in ${env} yet.`} />
      ) : (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-[var(--ink-muted)]">
              <th className="py-2 font-medium">Name</th>
              <th className="py-2 font-medium">Version</th>
              <th className="py-2 font-medium">Hint</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {secrets.map((s) => (
              <tr key={s.id} className="border-b border-[var(--border)]">
                <td className="py-2 font-mono">{s.name}</td>
                <td className="py-2">{s.current_version}</td>
                <td className="py-2 font-mono text-[var(--ink-muted)]">{s.value_hint}</td>
                <td className="py-2 text-right">
                  <Button variant="ghost" disabled={busy} onClick={() => onDelete(s.name)}>
                    Unset
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
