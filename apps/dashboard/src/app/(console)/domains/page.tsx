"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Domain, Project } from "@/lib/types";

export default function DomainsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [domains, setDomains] = useState<Domain[]>([]);
  const [hostname, setHostname] = useState("");
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

  async function loadDomains(pid: string) {
    if (!currentOrg || !pid) return;
    const list = await api.listDomains(currentOrg.id, pid);
    setDomains(list);
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadDomains(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load domains");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onAdd() {
    if (!currentOrg || !projectId || !hostname.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.addDomain(currentOrg.id, projectId, { hostname: hostname.trim() });
      setHostname("");
      await loadDomains(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add domain");
    } finally {
      setBusy(false);
    }
  }

  async function onVerify(id: string, force?: boolean) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.verifyDomain(currentOrg.id, projectId, id, force);
      await loadDomains(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Verify failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteDomain(currentOrg.id, projectId, id);
      await loadDomains(projectId);
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
        description="Domains require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Domains"
        description="Attach custom hostnames, verify DNS, and wire Traefik routers."
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      <label className="mb-4 block max-w-sm text-sm text-[var(--ink-muted)]">
        Project
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

      <div className="mb-8 flex max-w-xl flex-wrap items-end gap-2">
        <label className="min-w-[200px] flex-1 text-sm text-[var(--ink-muted)]">
          Hostname
          <Input
            className="mt-1"
            placeholder="app.example.com"
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
          />
        </label>
        <Button type="button" onClick={onAdd} disabled={busy || !projectId}>
          Add domain
        </Button>
      </div>

      {domains.length === 0 ? (
        <EmptyState
          title="No domains yet"
          description="Add a hostname, then verify (force works in local stub mode)."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
                <th className="py-2 pr-3 font-medium">Hostname</th>
                <th className="py-2 pr-3 font-medium">Status</th>
                <th className="py-2 pr-3 font-medium">Verify</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {domains.map((d) => (
                <tr key={d.id} className="border-b border-[var(--border)]">
                  <td className="py-3 pr-3 font-medium text-[var(--ink)]">{d.hostname}</td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{d.status}</td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{d.verification_type}</td>
                  <td className="py-3 text-right">
                    <div className="flex justify-end gap-2">
                      {d.status === "pending" ? (
                        <>
                          <Button
                            type="button"
                            variant="secondary"
                            disabled={busy}
                            onClick={() => onVerify(d.id)}
                          >
                            Verify
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            disabled={busy}
                            onClick={() => onVerify(d.id, true)}
                          >
                            Force
                          </Button>
                        </>
                      ) : null}
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => onDelete(d.id)}
                      >
                        Delete
                      </Button>
                    </div>
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
