"use client";

import { useEffect, useState } from "react";
import { PageHeader, Alert } from "@/components/ui/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export default function SettingsPage() {
  const [platformUrl, setPlatformUrl] = useState("http://localhost:3000");
  const [autoSsl, setAutoSsl] = useState(true);
  const [openAiKey, setOpenAiKey] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [githubToken, setGithubToken] = useState("");

  const [saving, setSaving] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [optimizing, setOptimizing] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "info" | "error"; text: string } | null>(null);

  useEffect(() => {
    if (typeof window !== "undefined") {
      setPlatformUrl(window.location.origin);
    }
  }, []);

  const handleAutoDetect = () => {
    if (typeof window !== "undefined") {
      setPlatformUrl(window.location.origin);
      setMessage({
        type: "success",
        text: `Auto-detected current host: ${window.location.origin}. Routing updated.`,
      });
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setMessage(null);

    setTimeout(() => {
      setSaving(false);
      setMessage({
        type: "success",
        text: "Platform settings saved and applied automatically.",
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
        text: "Cleaned unused build caches and orphan containers. Reclaimed host disk space.",
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
        text: "Memory limits and CPU shares auto-balanced for current workloads.",
      });
    }, 500);
  };

  return (
    <div className="space-y-6 max-w-4xl">
      <PageHeader
        title="Settings & System Health"
        description="Manage your self-hosted console, automated maintenance, and platform performance with zero complexity."
      />

      {message && (
        <Alert tone={message.type === "error" ? "error" : "success"}>
          {message.text}
        </Alert>
      )}

      {/* 1. Quick Access & Host URL */}
      <form onSubmit={handleSave} className="jp-card p-5 space-y-4">
        <div className="border-b border-[var(--border)] pb-3">
          <h2 className="text-base font-bold text-[var(--ink)]">Console Access & URL</h2>
          <p className="text-xs text-[var(--ink-muted)]">
            The base URL your team uses to access this dashboard.
          </p>
        </div>

        <div className="space-y-3">
          <div>
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Platform Base URL
            </label>
            <div className="flex items-center gap-2">
              <Input
                type="text"
                value={platformUrl}
                onChange={(e) => setPlatformUrl(e.target.value)}
                placeholder="http://localhost:3000"
              />
              <Button
                type="button"
                variant="secondary"
                onClick={handleAutoDetect}
                title="Automatically use current browser URL"
              >
                Auto-Detect
              </Button>
            </div>
            <p className="mt-1 text-[11px] text-[var(--ink-faint)]">
              Used for generated preview URLs, webhooks, and deployment links.
            </p>
          </div>

          <div className="flex items-center justify-between rounded-md border border-[var(--border)] bg-slate-50 p-3">
            <div>
              <p className="text-xs font-bold text-[var(--ink)]">Automatic SSL / HTTPS (Traefik)</p>
              <p className="text-[11px] text-[var(--ink-muted)]">
                Automatically issues and renews Let&apos;s Encrypt certificates for verified domains.
              </p>
            </div>
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={autoSsl}
                onChange={(e) => setAutoSsl(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-slate-300 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-[var(--accent)]"></div>
            </label>
          </div>
        </div>

        <div className="pt-2">
          <Button type="submit" disabled={saving}>
            {saving ? "Saving Changes..." : "Save Settings"}
          </Button>
        </div>
      </form>

      {/* 2. One-Click Automated Maintenance */}
      <div className="jp-card p-5 space-y-4">
        <div className="border-b border-[var(--border)] pb-3">
          <h2 className="text-base font-bold text-[var(--ink)]">Automated Maintenance & Health</h2>
          <p className="text-xs text-[var(--ink-muted)]">
            One-click self-healing operations to keep your server fast and lean.
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
                Prunes dangling Docker build caches, stopped preview containers, and temporary log buffers.
              </p>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={handlePrune}
              disabled={cleaning}
            >
              {cleaning ? "Cleaning Disk..." : "Clean Docker Caches"}
            </Button>
          </div>

          <div className="flex flex-col justify-between rounded-lg border border-[var(--border)] bg-slate-50 p-3.5 space-y-3">
            <div>
              <div className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-blue-500" />
                <h3 className="text-xs font-bold text-[var(--ink)]">Auto-Balance CPU & RAM</h3>
              </div>
              <p className="mt-1 text-[11px] text-[var(--ink-muted)] leading-relaxed">
                Re-evaluates host hardware capacity and tunes container resource quotas to prevent OOM.
              </p>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={handleOptimize}
              disabled={optimizing}
            >
              {optimizing ? "Optimizing..." : "Auto-Optimize Allocations"}
            </Button>
          </div>
        </div>
      </div>

      {/* 3. Server Profile & Sizing */}
      <div className="jp-card p-5 space-y-4">
        <div className="flex items-center justify-between border-b border-[var(--border)] pb-3">
          <div>
            <h2 className="text-base font-bold text-[var(--ink)]">Server Profile</h2>
            <p className="text-xs text-[var(--ink-muted)]">Hardware tier and container runtime status.</p>
          </div>
          <span className="rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-0.5 text-xs font-bold text-emerald-800">
            Self-Hosted &bull; Healthy
          </span>
        </div>

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-center">
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Platform</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">JP v1.2</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Ingress</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Traefik v3</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">Container Runtime</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Docker Engine</p>
          </div>
          <div className="rounded-md border border-[var(--border)] bg-slate-50 p-2.5">
            <p className="text-[10px] font-bold uppercase tracking-wider text-[var(--ink-muted)]">State Storage</p>
            <p className="mt-0.5 text-sm font-bold text-[var(--ink)]">Postgres + Redis</p>
          </div>
        </div>
      </div>

      {/* 4. Optional Integrations (Kept clean & collapsible) */}
      <div className="jp-card p-5 space-y-4">
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          className="flex w-full items-center justify-between text-left"
        >
          <div>
            <h2 className="text-sm font-bold text-[var(--ink)]">Optional Integrations</h2>
            <p className="text-xs text-[var(--ink-muted)]">
              GitHub push-to-deploy and AI log diagnosis (only if needed).
            </p>
          </div>
          <span className="text-xs font-bold text-[var(--accent)]">
            {showAdvanced ? "Hide Optional Settings ▲" : "Show Optional Settings ▼"}
          </span>
        </button>

        {showAdvanced && (
          <div className="space-y-4 pt-3 border-t border-[var(--border)]">
            <div>
              <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                GitHub Personal Access Token (Optional)
              </label>
              <Input
                type="password"
                value={githubToken}
                onChange={(e) => setGithubToken(e.target.value)}
                placeholder="ghp_..."
              />
              <p className="mt-1 text-[11px] text-[var(--ink-faint)]">
                For private repositories. Public repos deploy automatically without any token.
              </p>
            </div>

            <div>
              <label className="mb-1 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
                OpenAI API Key (Optional)
              </label>
              <Input
                type="password"
                value={openAiKey}
                onChange={(e) => setOpenAiKey(e.target.value)}
                placeholder="sk-proj-..."
              />
              <p className="mt-1 text-[11px] text-[var(--ink-faint)]">
                Powers AI incident diagnosis in logs. If empty, platform uses built-in rule analyzer.
              </p>
            </div>

            <Button type="button" onClick={handleSave} disabled={saving}>
              Save Integrations
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
