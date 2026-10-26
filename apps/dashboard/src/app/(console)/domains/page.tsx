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
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);
  const [copiedText, setCopiedText] = useState("");

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
  }, [currentOrg, projectId]);

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text);
    setCopiedText(text);
    setTimeout(() => setCopiedText(""), 2000);
  }

  async function onAdd() {
    if (!currentOrg || !projectId || !hostname.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    const cleanHost = hostname.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    try {
      const added = await api.addDomain(currentOrg.id, projectId, { hostname: cleanHost });
      setHostname("");
      // Automatically attempt auto-verification so the user doesn't have to click twice
      try {
        await api.verifyDomain(currentOrg.id, projectId, added.id, true);
        setSuccess(`Domain "${cleanHost}" connected and automatically verified with Traefik routing.`);
      } catch {
        setSuccess(`Domain "${cleanHost}" added. Complete DNS setup below.`);
      }
      await loadDomains(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add domain");
    } finally {
      setBusy(false);
    }
  }

  async function onAutoVerify(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      // Force verify to immediately register routing in Traefik without waiting for public DNS propagation delays
      await api.verifyDomain(currentOrg.id, projectId, id, true);
      setSuccess("Domain verified! Traefik router and load-balancer are now live.");
      await loadDomains(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Verification failed");
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
      await api.deleteDomain(currentOrg.id, projectId, id);
      setSuccess("Domain removed and Traefik route deregistered.");
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
    <div className="space-y-6">
      <PageHeader
        title="Custom Domains & Routing"
        description="Attach custom hostnames to your projects. Traefik automatically provisions reverse proxy routes and SSL certificates."
      />

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* 2-Step DNS & Routing Guide Card */}
      <div className="jp-card p-5 bg-gradient-to-r from-blue-50/70 via-indigo-50/40 to-slate-50 border-blue-200 space-y-4">
        <div className="flex items-center justify-between border-b border-blue-200/60 pb-3">
          <div className="flex items-center gap-2">
            <span className="flex h-6 w-6 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">
              ℹ
            </span>
            <h3 className="text-sm font-bold text-blue-950">How Domain Routing Works (Automatic)</h3>
          </div>
          <span className="rounded-full bg-blue-100 px-2.5 py-0.5 text-[11px] font-bold text-blue-800">
            Self-Hosted Ingress: Traefik v3
          </span>
        </div>

        <div className="grid gap-4 md:grid-cols-2 text-xs">
          <div className="space-y-1.5 rounded-lg border border-blue-200/80 bg-white/80 p-3 shadow-2xs">
            <p className="font-bold text-slate-900">Step 1: Point your DNS</p>
            <p className="text-slate-600">Add either record to your DNS provider (Cloudflare, GoDaddy, Namecheap):</p>
            <div className="mt-2 space-y-1 font-mono text-[11px]">
              <div className="flex items-center justify-between rounded bg-slate-100 px-2 py-1 text-slate-800">
                <span>CNAME &bull; <strong className="text-blue-700">app</strong> &rarr; <code className="text-slate-900">cname.jp.localhost</code></span>
                <button
                  type="button"
                  onClick={() => copyToClipboard("cname.jp.localhost")}
                  className="text-xs text-blue-600 font-bold hover:underline"
                >
                  {copiedText === "cname.jp.localhost" ? "Copied!" : "Copy"}
                </button>
              </div>
              <div className="flex items-center justify-between rounded bg-slate-100 px-2 py-1 text-slate-800">
                <span>A Record &bull; <strong className="text-blue-700">@</strong> &rarr; <code className="text-slate-900">Your Server IP</code></span>
              </div>
            </div>
          </div>

          <div className="space-y-1.5 rounded-lg border border-blue-200/80 bg-white/80 p-3 shadow-2xs">
            <p className="font-bold text-slate-900">Step 2: Connect & Auto-Route</p>
            <p className="text-slate-600">Enter your domain below and click &quot;Connect Domain&quot;.</p>
            <div className="mt-2 rounded bg-amber-50 border border-amber-200 px-2.5 py-1.5 text-[11px] text-amber-900 leading-relaxed">
              <strong>Local / Development testing:</strong> Add <code className="font-mono bg-white px-1 py-0.5 rounded border border-amber-300">127.0.0.1 yourdomain.local</code> to your system <code className="font-mono">hosts</code> file to test immediately!
            </div>
          </div>
        </div>
      </div>

      {/* Add Domain Controls Card */}
      <div className="jp-card p-5 space-y-4">
        <h3 className="text-sm font-bold text-[var(--ink)]">Add Domain to Project</h3>
        <div className="grid gap-4 md:grid-cols-3">
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

          <div className="md:col-span-2">
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Domain or Subdomain
            </label>
            <div className="flex items-center gap-2">
              <Input
                placeholder="e.g. app.mydomain.com or mydomain.com"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    onAdd();
                  }
                }}
              />
              <Button
                type="button"
                onClick={onAdd}
                disabled={busy || !projectId || !hostname.trim()}
              >
                {busy ? "Connecting..." : "Connect Domain"}
              </Button>
            </div>
          </div>
        </div>
      </div>

      {/* Configured Domains Table */}
      {domains.length === 0 ? (
        <EmptyState
          title="No domains connected yet"
          description="Enter a domain name above to automatically link your project with Traefik routing."
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-4 py-3">
            <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Configured Hostnames ({domains.length})
            </h4>
          </div>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-slate-50/70 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                <th className="px-4 py-3">Hostname</th>
                <th className="px-4 py-3">Routing Status</th>
                <th className="px-4 py-3">SSL / TLS</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {domains.map((d) => {
                const isVerified = d.status === "verified" || d.status === "active";
                return (
                  <tr key={d.id} className="hover:bg-slate-50/50 transition-colors">
                    <td className="px-4 py-3.5">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-[var(--ink)]">{d.hostname}</span>
                        <a
                          href={`http://${d.hostname}`}
                          target="_blank"
                          rel="noreferrer"
                          className="rounded p-1 text-[var(--ink-muted)] hover:bg-slate-100 hover:text-[var(--accent)]"
                          title="Open domain in new tab"
                        >
                          ↗
                        </a>
                      </div>
                    </td>
                    <td className="px-4 py-3.5">
                      {isVerified ? (
                        <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                          Connected & Routed
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-300 bg-amber-50 px-2.5 py-0.5 text-xs font-bold text-amber-800">
                          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                          Pending DNS
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3.5 text-xs font-medium text-[var(--ink-secondary)]">
                      {isVerified ? "Automatic (Traefik)" : "Pending Verification"}
                    </td>
                    <td className="px-4 py-3.5 text-right">
                      <div className="flex justify-end gap-2">
                        {!isVerified && (
                          <Button
                            type="button"
                            variant="secondary"
                            size="sm"
                            disabled={busy}
                            onClick={() => onAutoVerify(d.id)}
                            title="Force Traefik routing without waiting for public DNS"
                          >
                            Auto-Verify Now
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="danger"
                          size="sm"
                          disabled={busy}
                          onClick={() => onDelete(d.id)}
                        >
                          Remove
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
  );
}
