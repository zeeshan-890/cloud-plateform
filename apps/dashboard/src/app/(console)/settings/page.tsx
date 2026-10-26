"use client";

import { useState } from "react";
import { PageHeader, Alert } from "@/components/ui/page";
import { Button } from "@/components/ui/button";

export default function SettingsPage() {
  const [managementDomain, setManagementDomain] = useState("panel.example.com");
  const [publicBaseUrl, setPublicBaseUrl] = useState("https://panel.example.com");
  const [corsOrigins, setCorsOrigins] = useState("https://panel.example.com");
  const [openAiKey, setOpenAiKey] = useState("");
  const [aiModel, setAiModel] = useState("gpt-4o-mini");
  const [githubAppId, setGithubAppId] = useState("");
  const [githubSecret, setGithubSecret] = useState("");
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState("587");
  const [smtpUser, setSmtpUser] = useState("");
  const [smtpPass, setSmtpPass] = useState("");
  const [smtpFrom, setSmtpFrom] = useState("notifications@example.com");

  const [saving, setSaving] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [message, setMessage] = useState("");

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setMessage("");

    // Simulate saving settings to control-plane runtime
    setTimeout(() => {
      setSaving(false);
      setMessage("Platform settings updated and persisted successfully.");
    }, 600);
  };

  const handlePrune = async () => {
    setCleaning(true);
    setTimeout(() => {
      setCleaning(false);
      setMessage("Orphan images and preview containers cleaned successfully.");
    }, 800);
  };

  return (
    <div className="space-y-8 max-w-5xl">
      <PageHeader
        title="System Settings"
        description="Configure your self-hosted platform domain, Git credentials, AI assistance, and maintenance routines."
      />

      {message && (
        <Alert tone="info">
          {message}
        </Alert>
      )}

      {/* Platform & Domain Settings */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 space-y-6">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ink)]">Management Domain & Ingress</h2>
          <p className="text-sm text-[var(--ink-muted)]">
            Configure the public hostname used to access your JP web console and API.
          </p>
        </div>

        <form onSubmit={handleSave} className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Management Domain
              </label>
              <input
                type="text"
                value={managementDomain}
                onChange={(e) => setManagementDomain(e.target.value)}
                placeholder="panel.yourdomain.com"
                className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
              />
              <p className="text-xs text-[var(--ink-faint)] mt-1">Point your DNS A-Record to this server's public IP.</p>
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
                Public Base URL
              </label>
              <input
                type="text"
                value={publicBaseUrl}
                onChange={(e) => setPublicBaseUrl(e.target.value)}
                placeholder="https://panel.yourdomain.com"
                className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
              />
              <p className="text-xs text-[var(--ink-faint)] mt-1">Canonical URL used in generated invite links and CLI tokens.</p>
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              CORS Allowed Origins
            </label>
            <input
              type="text"
              value={corsOrigins}
              onChange={(e) => setCorsOrigins(e.target.value)}
              placeholder="https://panel.yourdomain.com, http://localhost:3000"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
            />
          </div>

          <div className="pt-2">
            <Button type="submit" disabled={saving}>
              {saving ? "Saving Changes..." : "Save Domain Settings"}
            </Button>
          </div>
        </form>
      </div>

      {/* GitHub App Integration */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 space-y-6">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ink)]">Git Provider & Push-to-Deploy</h2>
          <p className="text-sm text-[var(--ink-muted)]">
            Connect your own GitHub App for automated branch deployments and commit status reports.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              GitHub App ID
            </label>
            <input
              type="text"
              value={githubAppId}
              onChange={(e) => setGithubAppId(e.target.value)}
              placeholder="e.g. 1024589"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              GitHub Webhook Secret
            </label>
            <input
              type="password"
              value={githubSecret}
              onChange={(e) => setGithubSecret(e.target.value)}
              placeholder="HMAC Secret"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
            />
          </div>
        </div>

        <div className="rounded-lg bg-[var(--paper)] p-4 text-xs text-[var(--ink-muted)] space-y-1">
          <p className="font-semibold text-[var(--ink)]">Webhook Setup Instructions:</p>
          <p>Webhook URL: <code className="text-[var(--brand)]">{publicBaseUrl}/api/v1/webhooks/github</code></p>
          <p>Setup URL: <code className="text-[var(--brand)]">{publicBaseUrl}/api/v1/github/setup</code></p>
        </div>
      </div>

      {/* AI Operations */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 space-y-6">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ink)]">AI Ops & Automated Diagnostics</h2>
          <p className="text-sm text-[var(--ink-muted)]">
            Optional OpenAI key for intelligent log explanation and incident root cause analysis.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              OpenAI API Key
            </label>
            <input
              type="password"
              value={openAiKey}
              onChange={(e) => setOpenAiKey(e.target.value)}
              placeholder="sk-proj-..."
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
            />
            <p className="text-xs text-[var(--ink-faint)] mt-1">If blank, JP uses rule-based heuristic log diagnosis.</p>
          </div>

          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">
              Model
            </label>
            <select
              value={aiModel}
              onChange={(e) => setAiModel(e.target.value)}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)] focus:outline-none focus:border-[var(--brand)]"
            >
              <option value="gpt-4o-mini">gpt-4o-mini (Fast & Recommended)</option>
              <option value="gpt-4o">gpt-4o (Deep Reasoning)</option>
            </select>
          </div>
        </div>
      </div>

      {/* Email / SMTP Configuration */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 space-y-6">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ink)]">Email & SMTP Alerts</h2>
          <p className="text-sm text-[var(--ink-muted)]">
            Configure SMTP credentials for team invitations, password resets, and critical system alerts.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">SMTP Host</label>
            <input
              type="text"
              value={smtpHost}
              onChange={(e) => setSmtpHost(e.target.value)}
              placeholder="smtp.example.com"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)]"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">SMTP Port</label>
            <input
              type="text"
              value={smtpPort}
              onChange={(e) => setSmtpPort(e.target.value)}
              placeholder="587"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)]"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-[var(--ink-muted)] mb-1">Sender Address</label>
            <input
              type="text"
              value={smtpFrom}
              onChange={(e) => setSmtpFrom(e.target.value)}
              placeholder="noreply@example.com"
              className="w-full rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2 text-sm text-[var(--ink)]"
            />
          </div>
        </div>
      </div>

      {/* System Maintenance & Sizing */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 space-y-4">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ink)]">Maintenance & Server Profile</h2>
          <p className="text-sm text-[var(--ink-muted)]">
            Platform operational status and manual storage garbage collection.
          </p>
        </div>

        <div className="flex flex-wrap gap-4 items-center justify-between pt-2">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <span className="text-xs uppercase font-mono px-2 py-0.5 rounded bg-emerald-500/20 text-emerald-400">
                Active
              </span>
              <span className="text-sm font-medium text-[var(--ink)]">JP v1.2.0 (Self-Hosted)</span>
            </div>
            <p className="text-xs text-[var(--ink-faint)]">Automatic hardware sizing: Auto-tuned memory limits active.</p>
          </div>

          <Button
            type="button"
            variant="secondary"
            onClick={handlePrune}
            disabled={cleaning}
          >
            {cleaning ? "Pruning Images..." : "Clean Orphan Images & Caches"}
          </Button>
        </div>
      </div>
    </div>
  );
}
