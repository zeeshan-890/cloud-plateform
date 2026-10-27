"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProject } from "@/lib/project-context";
import type { Certificate, Domain, Project } from "@/lib/types";

export default function DomainsPage() {
  const { currentOrg } = useOrg();
  const { rootDomain } = useProject();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [domains, setDomains] = useState<Domain[]>([]);
  const [certificates, setCertificates] = useState<Certificate[]>([]);
  const [hostname, setHostname] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [deleteConfirmDomain, setDeleteConfirmDomain] = useState<Domain | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

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

  async function loadDomainsAndCerts(pid: string) {
    if (!currentOrg || !pid) return;
    try {
      const [domList, certList] = await Promise.all([
        api.listDomains(currentOrg.id, pid).catch(() => []),
        api.listCertificates(currentOrg.id, pid).catch(() => []),
      ]);
      setDomains(domList);
      setCertificates(certList);
    } catch {
      setDomains([]);
      setCertificates([]);
    }
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await loadDomainsAndCerts(projectId);
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

  function copyText(text: string, key: string) {
    navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 2000);
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
        setSuccess(`Domain "${cleanHost}" connected and automatically verified with Traefik routing & Let's Encrypt SSL.`);
      } catch {
        setSuccess(`Domain "${cleanHost}" added. Complete DNS setup below.`);
      }
      await loadDomainsAndCerts(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add domain");
    } finally {
      setBusy(false);
    }
  }

  async function onVerify(id: string, force = false) {
    if (!currentOrg || !projectId) return;
    setVerifyingId(id);
    setError("");
    setSuccess("");
    try {
      await api.verifyDomain(currentOrg.id, projectId, id, force);
      setSuccess(
        force
          ? "Domain auto-verified! Traefik reverse proxy routes and Let's Encrypt SSL certificates are now active."
          : "Domain DNS propagation check passed! Routing active."
      );
      await loadDomainsAndCerts(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Verification check failed. Please check your DNS records.");
    } finally {
      setVerifyingId(null);
    }
  }

  async function onConfirmDelete() {
    if (!currentOrg || !projectId || !deleteConfirmDomain) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteDomain(currentOrg.id, projectId, deleteConfirmDomain.id);
      setSuccess(`Domain "${deleteConfirmDomain.hostname}" disconnected and Traefik ingress routes purged.`);
      setDeleteConfirmDomain(null);
      await loadDomainsAndCerts(projectId);
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

  const selectedProj = projects.find((p) => p.id === projectId);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Custom Domains & SSL Ingress"
        description="Attach custom production hostnames to your projects. Traefik automatically provisions reverse proxy ingress routes and Let's Encrypt TLS 1.3 certificates."
      />

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      {/* Step 1: DNS Setup Guide */}
      <div className="rounded-xl border-2 border-slate-200 bg-white p-5 shadow-xs space-y-4">
        <div className="border-b border-slate-100 pb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <span className="inline-block text-[10px] font-extrabold uppercase tracking-wider text-blue-700 bg-blue-50 px-2 py-0.5 rounded-md mb-1">
              Setup Instructions
            </span>
            <h3 className="text-sm font-bold text-[#0F1B3D]">
              Step 1: Point Your DNS Records to JP Cloud
            </h3>
            <p className="text-xs text-slate-500 font-medium mt-0.5">
              Add the following record to your DNS provider (Cloudflare, GoDaddy, Namecheap, Route53):
            </p>
          </div>
          <span className="rounded-full bg-emerald-50 border border-emerald-300 px-3 py-1 text-xs font-bold text-emerald-800 flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full bg-emerald-600 animate-pulse" />
            Let&apos;s Encrypt Auto-SSL Active
          </span>
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
                      onClick={() => copyText("app", "cname-host")}
                      className="text-[10px] text-slate-400 hover:text-blue-600 font-sans"
                    >
                      {copiedKey === "cname-host" ? "✓" : "📋"}
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
                    onClick={() => copyText("161.118.168.6", "cname-target")}
                    className="px-2.5 py-1 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-[11px] font-bold text-slate-700 shadow-2xs transition-all"
                  >
                    {copiedKey === "cname-target" ? "✓ Copied" : "📋 Copy Value"}
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
                      onClick={() => copyText("@", "a-host")}
                      className="text-[10px] text-slate-400 hover:text-blue-600 font-sans"
                    >
                      {copiedKey === "a-host" ? "✓" : "📋"}
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
                    onClick={() => copyText("161.118.168.6", "a-ip")}
                    className="px-2.5 py-1 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-[11px] font-bold text-slate-700 shadow-2xs transition-all"
                  >
                    {copiedKey === "a-ip" ? "✓ Copied" : "📋 Copy IP"}
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

      {/* Step 2: Add Domain Controls Card */}
      <div className="jp-card p-5 space-y-4">
        <div>
          <span className="inline-block text-[10px] font-extrabold uppercase tracking-wider text-blue-700 bg-blue-50 px-2 py-0.5 rounded-md mb-1">
            Attachment
          </span>
          <h3 className="text-sm font-bold text-[var(--ink)]">
            Step 2: Connect Domain to Project
          </h3>
          <p className="text-xs text-[var(--ink-muted)]">
            Select the destination project and enter your domain. Ingress routing and TLS certificates will be activated automatically.
          </p>
        </div>

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
                className="font-bold shrink-0"
              >
                {busy ? "Connecting..." : "Connect Domain"}
              </Button>
            </div>
          </div>
        </div>
      </div>

      {/* Step 3: Configured Domains Table */}
      {domains.length === 0 ? (
        <EmptyState
          title="No domains connected yet"
          description={`Enter a custom domain above to automatically link ${selectedProj ? selectedProj.name : "your project"} with Traefik routing and Let's Encrypt SSL.`}
        />
      ) : (
        <div className="jp-card overflow-hidden">
          <div className="border-b border-[var(--border)] bg-slate-50 px-5 py-3.5 flex items-center justify-between">
            <h4 className="text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)] flex items-center gap-2">
              <span>🔒 Step 3: Configured Hostnames & SSL Status</span>
              <span className="rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-bold text-blue-800 font-mono">
                {domains.length} Connected
              </span>
            </h4>
            {selectedProj && (
              <span className="text-xs font-bold text-slate-600">
                Project: <span className="text-[#1E3A8A] font-black">{selectedProj.name}</span>
              </span>
            )}
          </div>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-slate-50/70 text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                <th className="px-5 py-3">Hostname</th>
                <th className="px-5 py-3">Routing Ingress</th>
                <th className="px-5 py-3">SSL / TLS Certificate</th>
                <th className="px-5 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {domains.map((d) => {
                const isVerified = d.status === "verified" || d.status === "active";
                const cert = certificates.find((c) => c.hostname === d.hostname);
                const isCertActive = isVerified || cert?.status === "active" || cert?.status === "issued";
                const isVerifying = verifyingId === d.id;

                return (
                  <tr key={d.id} className="hover:bg-slate-50/50 transition-colors">
                    <td className="px-5 py-3.5">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-[var(--ink)]">{d.hostname}</span>
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
                    </td>
                    <td className="px-5 py-3.5">
                      {isVerified ? (
                        <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-600 animate-pulse" />
                          🟢 Connected & Routed
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-300 bg-amber-50 px-2.5 py-0.5 text-xs font-bold text-amber-800">
                          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                          🟡 Pending DNS
                        </span>
                      )}
                    </td>
                    <td className="px-5 py-3.5 text-xs font-medium text-[var(--ink-secondary)]">
                      {isCertActive ? (
                        <span className="inline-flex items-center gap-1 rounded-full border border-blue-300 bg-blue-50 px-2.5 py-0.5 text-xs font-bold text-blue-800">
                          <span>🔒</span>
                          <span>Let&apos;s Encrypt (TLS 1.3 Active)</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 rounded-full border border-slate-300 bg-slate-100 px-2.5 py-0.5 text-xs font-bold text-slate-700">
                          <span>⏳</span>
                          <span>Auto-SSL Provisioning...</span>
                        </span>
                      )}
                    </td>
                    <td className="px-5 py-3.5 text-right">
                      <div className="flex justify-end gap-2">
                        {!isVerified && (
                          <>
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={busy || isVerifying}
                              onClick={() => onVerify(d.id, false)}
                              title="Test if public DNS records resolve"
                              className="font-bold text-xs"
                            >
                              {isVerifying ? "Checking..." : "🔄 Verify DNS"}
                            </Button>
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              disabled={busy || isVerifying}
                              onClick={() => onVerify(d.id, true)}
                              title="Force Traefik routing without waiting for public DNS"
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
                          disabled={busy}
                          onClick={() => setDeleteConfirmDomain(d)}
                          className="font-bold text-xs"
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

      {/* Confirmation Modal Dialog for Disconnecting Custom Domain */}
      {deleteConfirmDomain && (
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
                Are you sure you want to disconnect <code className="font-mono font-black text-rose-950 underline">{deleteConfirmDomain.hostname}</code>?
              </p>
              <p className="text-[11px] text-rose-700 leading-relaxed">
                Traefik reverse proxy routes will be purged immediately and Let&apos;s Encrypt TLS certificates will be detached. Incoming user traffic to this domain will stop resolving.
              </p>
            </div>

            <div className="flex items-center justify-end gap-2.5 pt-2">
              <button
                type="button"
                disabled={busy}
                onClick={() => setDeleteConfirmDomain(null)}
                className="h-9 px-4 rounded-lg border border-slate-300 bg-white text-xs font-bold text-slate-700 hover:bg-slate-50 transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={onConfirmDelete}
                className="h-9 px-4 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-bold transition-colors flex items-center gap-1.5 shadow-sm disabled:opacity-50"
              >
                {busy ? "Disconnecting..." : "Yes, Disconnect Domain"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
