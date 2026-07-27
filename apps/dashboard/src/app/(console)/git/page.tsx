"use client";

import { FormEvent, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type {
  AvailableRepo,
  ConnectedRepo,
  GitInstallation,
  Project,
} from "@/lib/types";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL?.replace(/\/$/, "") ||
  "http://localhost:8000/api/v1";

export default function ConnectGitPage() {
  const { currentOrg } = useOrg();
  const searchParams = useSearchParams();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [installations, setInstallations] = useState<GitInstallation[]>([]);
  const [available, setAvailable] = useState<AvailableRepo[]>([]);
  const [mode, setMode] = useState("mock");
  const [installMode, setInstallMode] = useState("");
  const [connected, setConnected] = useState<ConnectedRepo[]>([]);
  const [fullName, setFullName] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);

  async function refresh() {
    if (!currentOrg) return;
    const [inst, avail, projs] = await Promise.all([
      api.listGitInstallations(currentOrg.id),
      api.listAvailableRepos(currentOrg.id),
      api.listProjects(currentOrg.id),
    ]);
    setInstallations(inst);
    setAvailable(avail.repos);
    setMode(avail.mode);
    setProjects(projs);
    if (!projectId && projs[0]) setProjectId(projs[0].id);
  }

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        await refresh();
        if (searchParams.get("installed") === "1" && mounted) {
          setSuccess("GitHub App installation saved.");
        }
        const errParam = searchParams.get("error");
        if (errParam && mounted) {
          setError(`GitHub setup: ${errParam}`);
        }
        const pendingInstall = searchParams.get("installation_id");
        if (pendingInstall && mounted) {
          try {
            await api.completeGitInstall(currentOrg.id, {
              installation_id: pendingInstall,
            });
            setSuccess("GitHub installation linked to this organization.");
            await refresh();
          } catch (err) {
            setError(
              err instanceof ApiError
                ? err.message
                : "Failed to link installation",
            );
          }
        }
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load Git data",
          );
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg]);

  useEffect(() => {
    if (!currentOrg || !projectId) {
      setConnected([]);
      return;
    }
    let mounted = true;
    (async () => {
      try {
        const repos = await api.listRepos(currentOrg.id, projectId);
        if (mounted) setConnected(repos);
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to list repos",
          );
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg, projectId]);

  async function onInstall() {
    if (!currentOrg) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const start = await api.startGitInstall(currentOrg.id);
      setInstallMode(start.mode);
      if (start.mode === "github_app" && start.install_url) {
        window.open(start.install_url, "_blank", "noopener,noreferrer");
        setSuccess(
          "GitHub App install opened in a new tab. After approving, return here and refresh.",
        );
        return;
      }
      // Stub path
      await api.completeGitInstall(currentOrg.id, {
        account_login: "stub-github-org",
      });
      setSuccess("GitHub install stub completed (no App credentials configured).");
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Install failed");
    } finally {
      setBusy(false);
    }
  }

  async function onConnect(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !projectId || !fullName.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const match = available.find((r) => r.full_name === fullName.trim());
      await api.connectRepo(currentOrg.id, projectId, {
        full_name: fullName.trim(),
        clone_url: match?.clone_url,
        default_branch: match?.default_branch || "main",
        installation_id: installations[0]?.installation_id,
      });
      setSuccess(`Connected ${fullName.trim()}.`);
      setFullName("");
      const repos = await api.listRepos(currentOrg.id, projectId);
      setConnected(repos);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Connect failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDisconnect(repoId: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    try {
      await api.disconnectRepo(currentOrg.id, projectId, repoId);
      setConnected(await api.listRepos(currentOrg.id, projectId));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Disconnect failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Connect Git requires an active organization."
      />
    );
  }

  return (
    <div className="mx-auto max-w-2xl animate-fade-up">
      <PageHeader
        title="Connect Git"
        description="Install the GitHub App (or stub locally), then connect repositories to a project."
      />

      {error ? <Alert>{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      <section className="mt-6 space-y-3">
        <h2 className="text-sm font-semibold text-[var(--ink)]">GitHub App</h2>
        <p className="text-sm text-[var(--ink-muted)]">
          Installations: {installations.length || "none"} · Repo list mode:{" "}
          {mode}
          {installMode ? ` · last start: ${installMode}` : ""}
        </p>
        <p className="text-xs text-[var(--ink-faint)] font-[family-name:var(--font-mono)]">
          Webhook URL: {API_BASE}/webhooks/github
        </p>
        <p className="text-xs text-[var(--ink-faint)] font-[family-name:var(--font-mono)]">
          Setup URL: {API_BASE}/github/setup
        </p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" onClick={onInstall} disabled={busy}>
            {busy ? "Working…" : "Install GitHub App"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={busy}
            onClick={() => refresh().catch(() => undefined)}
          >
            Refresh
          </Button>
        </div>
        {installations.length > 0 ? (
          <ul className="text-sm text-[var(--ink-muted)]">
            {installations.map((i) => (
              <li key={i.id}>
                {i.account_login} ({i.installation_id})
              </li>
            ))}
          </ul>
        ) : null}
      </section>

      <section className="mt-10 space-y-4">
        <h2 className="text-sm font-semibold text-[var(--ink)]">
          Connect repository
        </h2>
        <label className="block text-sm text-[var(--ink-muted)]">
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

        <form onSubmit={onConnect} className="flex flex-col gap-3">
          <Input
            label="owner/repo"
            placeholder="acme/web-app"
            value={fullName}
            onChange={(e) => setFullName(e.target.value)}
            list="available-repos"
            required
          />
          <datalist id="available-repos">
            {available.map((r) => (
              <option key={r.full_name} value={r.full_name} />
            ))}
          </datalist>
          <Button type="submit" disabled={busy || !projectId}>
            Connect repo
          </Button>
        </form>

        <div className="pt-2">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-[var(--ink-faint)]">
            Connected
          </h3>
          {connected.length === 0 ? (
            <p className="text-sm text-[var(--ink-muted)]">No repos yet.</p>
          ) : (
            <ul className="divide-y divide-[var(--border)]">
              {connected.map((r) => (
                <li
                  key={r.id}
                  className="flex items-center justify-between py-3 text-sm"
                >
                  <span className="font-[family-name:var(--font-mono)] text-[var(--ink)]">
                    {r.full_name}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => onDisconnect(r.id)}
                  >
                    Disconnect
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </div>
  );
}
