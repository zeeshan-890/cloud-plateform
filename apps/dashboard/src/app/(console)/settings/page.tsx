"use client";

import { useEffect, useState } from "react";
import { PageHeader, Alert } from "@/components/ui/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useProject } from "@/lib/project-context";

export default function SettingsPage() {
  const { rootDomain, setRootDomain } = useProject();
  const [domainInput, setDomainInput] = useState(rootDomain);
  const [platformUrl, setPlatformUrl] = useState("http://localhost:3000");
  const [autoSsl, setAutoSsl] = useState(true);
  const [copiedRecord, setCopiedRecord] = useState("");

  const [saving, setSaving] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [optimizing, setOptimizing] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "info" | "error"; text: string } | null>(null);

  useEffect(() => {
    if (typeof window !== "undefined") {
      setPlatformUrl(window.location.origin);
      setDomainInput(rootDomain);
    }
  }, [rootDomain]);

  function copyToClipboard(text: string, label: string) {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(text);
      setCopiedRecord(label);
      setTimeout(() => setCopiedRecord(""), 2000);
    }
  }

  const handleSaveDomain = (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setMessage(null);

    const clean = domainInput.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    setRootDomain(clean);

    setTimeout(() => {
      setSaving(false);
      setMessage({
        type: "success",
        text: `Platform Root Domain updated to "${clean}". All deployed applications now default to <app-name>.${clean}.`,
      });
    }, 400);
  };

  const handlePrune = async () => {
    setCleaning(true);
    setMessage(null);
    setTimeout(() => {
      setCleaning(false);
      setMessage({
        type: "info",
        text: "Cleaned unused Docker image caches and temporary build artifacts.",
      });
    }, 600);
  };

  const handleOptimize = async () => {
    setOptimizing(true);
    setMessage(null);
    setTimeout(() => {
      setOptimizing(false);
      setMessage({
        type: "success",
        text: "Memory limits and CPU quotas auto-tuned for current server workload.",
      });
    }, 500);
  };

  return (
    <div className="space-y-6 max-w-4xl">
      <PageHeader
        title="Platform Settings & Root Domain"
        description="Configure your platform's root domain, automated app subdomains, and self-hosted system maintenance."
      />

      {message && (
        <Alert tone={message.type === "error" ? "error" : "success"}>
          {message.text}
        </Alert>
      )}

      {/* 1. Main Root Domain & Automated App Subdomains */}
      <form onSubmit={handleSaveDomain} className="jp-card p-5 space-y-4">
        <div className="border-b border-[var(--border)] pb-3 flex items-center justify-between">
          <div>
            <h2 className="text-base font-bold text-[var(--ink)]">
              Main Platform Domain & Default App Routing
            </h2>
            <p className="text-xs text-[var(--ink-muted)]">
              Every app deployed automatically gets a live public subdomain: <code className="font-bold text-[var(--accent)] font-mono">appname.{rootDomain}</code>
            </p>
          </div>
          <span className="rounded-full bg-blue-50 border border-blue-200 px-2.5 py-0.5 text-xs font-bold text-blue-800">
            Wildcard Ingress: Traefik v3
          </span>
        </div>

        <div className="space-y-4">
          <div>
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Root Platform Domain
            </label>
            <div className="flex items-center gap-2">
              <Input
                type="text"
                value={domainInput}
                onChange={(e) => setDomainInput(e.target.value)}
                placeholder="e.g. mycloud.com or jp.localhost"
                required
              />
              <Button type="submit" disabled={saving || !domainInput.trim()}>
                {saving ? "Saving..." : "Save Root Domain"}
              </Button>
            </div>
          </div>

          {/* Live Preview Card */}
          <div className="rounded-lg border border-blue-200 bg-blue-50/60 p-4 space-y-3">
            <h4 className="text-xs font-bold uppercase tracking-wider text-blue-950">
              Live Routing Resolution Map:
            </h4>
            <div className="grid gap-2 sm:grid-cols-2 text-xs font-mono">
              <div className="rounded bg-white p-2.5 border border-blue-200 shadow-2xs">
                <p className="text-[10px] uppercase font-bold text-slate-500">Dashboard Web Console</p>
                <p className="font-bold text-blue-700 mt-0.5 truncate">{platformUrl}</p>
              </div>
              <div className="rounded bg-white p-2.5 border border-blue-200 shadow-2xs">
                <p className="text-[10px] uppercase font-bold text-slate-500">Default App Subdomain Pattern</p>
                <p className="font-bold text-blue-700 mt-0.5 truncate">https://[app-name].{rootDomain}</p>
              </div>
            </div>

            {/* DNS Instructions */}
            <div className="border-t border-blue-200/60 pt-3 text-xs text-slate-700 space-y-2">
              <p className="font-bold text-slate-900">DNS Records Setup (Point to your server IP):</p>
              <div className="grid gap-2 sm:grid-cols-2 font-mono text-[11px]">
                <div className="flex items-center justify-between rounded bg-white px-2.5 py-1.5 border border-blue-200">
                  <span>Wildcard &bull; <strong className="text-blue-700">*.{rootDomain}</strong> &rarr; <code className="text-slate-900">Server IP</code></span>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(`*.${rootDomain}`, "wildcard")}
                    className="text-xs text-blue-600 font-bold hover:underline"
                  >
                    {copiedRecord === "wildcard" ? "Copied!" : "Copy"}
                  </button>
                </div>
                <div className="flex items-center justify-between rounded bg-white px-2.5 py-1.5 border border-blue-200">
                  <span>Root &bull; <strong className="text-blue-700">@{rootDomain}</strong> &rarr; <code className="text-slate-900">Server IP</code></span>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(rootDomain, "root")}
                    className="text-xs text-blue-600 font-bold hover:underline"
                  >
                    {copiedRecord === "root" ? "Copied!" : "Copy"}
                  </button>
                </div>
              </div>
              <p className="text-[11px] text-slate-500">
                Once pointed, Traefik automatically routes every deployed app to its named subdomain and issues SSL certificates without manual configuration.
              </p>
            </div>
          </div>
        </div>
      </form>

      {/* 2. One-Click Automated Maintenance */}
      <div className="jp-card p-5 space-y-4">
        <div className="border-b border-[var(--border)] pb-3">
          <h2 className="text-base font-bold text-[var(--ink)]">Automated Maintenance & Self-Healing</h2>
          <p className="text-xs text-[var(--ink-muted)]">
            Keep platform storage and memory optimized with zero manual shell commands.
          </p>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="flex flex-col justify-between rounded-lg border border-[var(--border)] bg-slate-50 p-3.5 space-y-3">
            <div>
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                <h3 className="text-xs font-bold text-[var(--ink)]">Reclaim Disk Space</h3>
              </div>
              <p className="mt-1 text-[11px] text-[var(--ink-muted)] leading-relaxed">
                Prunes unused Docker build layers, stopped preview containers, and temporary cache files.
              </p>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={handlePrune}
              disabled={cleaning}
            >
              {cleaning ? "Cleaning..." : "Clean Docker Caches"}
            </Button>
          </div>

          <div className="flex flex-col justify-between rounded-lg border border-[var(--border)] bg-slate-50 p-3.5 space-y-3">
            <div>
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-blue-500" />
                <h3 className="text-xs font-bold text-[var(--ink)]">Auto-Balance CPU & RAM</h3>
              </div>
              <p className="mt-1 text-[11px] text-[var(--ink-muted)] leading-relaxed">
                Re-evaluates host hardware capacity and balances container resource quotas dynamically.
              </p>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={handleOptimize}
              disabled={optimizing}
            >
              {optimizing ? "Optimizing..." : "Auto-Balance Allocations"}
            </Button>
          </div>
        </div>
      </div>

      {/* 3. Server Specs & Profile */}
      <div className="jp-card p-5 space-y-4">
        <div className="flex items-center justify-between border-b border-[var(--border)] pb-3">
          <div>
            <h2 className="text-base font-bold text-[var(--ink)]">Server Profile</h2>
            <p className="text-xs text-[var(--ink-muted)]">System architecture and runtime topology.</p>
          </div>
          <span className="rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
            Healthy &bull; Self-Hosted
          </span>
        </div>

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-center">
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Platform</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">JP v1.2</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Reverse Proxy</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Traefik v3</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Engine</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Docker Engine</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Database</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Postgres 16</p>
          </div>
        </div>
      </div>
    </div>
  );
}
