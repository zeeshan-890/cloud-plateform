"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";

export default function AppsPage() {
  const { currentOrg } = useOrg();
  const [projectName, setProjectName] = useState("go-http");
  const [createdId, setCreatedId] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setCreatedId("");
  }, [currentOrg]);

  async function onCreateGoProject() {
    if (!currentOrg || !projectName.trim()) return;
    setBusy(true);
    setError("");
    setSuccess("");
    setCreatedId("");
    try {
      const p = await api.createProject(currentOrg.id, {
        name: projectName.trim(),
        description: "Go HTTP starter (jp one-click app)",
      });
      setCreatedId(p.id);
      setSuccess(`Project created: ${p.name} (${p.id}).`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create project failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="One-click apps require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Apps"
        description="Starter templates and marketplace shortcuts. Scaffold locally with the jp CLI, then deploy."
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}
      {success ? (
        <div className="mb-4">
          <Alert tone="success">{success}</Alert>
        </div>
      ) : null}

      <section className="mb-8 rounded-md border border-[var(--border)] bg-[var(--surface)] p-5">
        <h2 className="text-lg font-medium text-[var(--ink)]">Go HTTP starter</h2>
        <p className="mt-2 text-sm text-[var(--ink-muted)]">
          A clean one-click starter for Go apps. It includes a health endpoint, Go module, and
          deploy-ready <code className="font-[family-name:var(--font-mono)] text-xs">jp.yaml</code>.
        </p>
        <div className="mt-3 flex flex-wrap gap-2 text-xs text-[var(--ink-faint)]">
          <span className="rounded border border-[var(--border)] px-2 py-1">runtime: go</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">healthz ready</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">rolling deploy</span>
        </div>
        <pre className="mt-4 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--paper)] p-4 text-sm text-[var(--ink)]">
{`jp init --runtime go
go run .
jp apply --project <project-id>
jp deploy --project <project-id>`}
        </pre>
        <div className="mt-4 flex flex-wrap items-end gap-3">
          <label className="block text-sm text-[var(--ink-muted)]">
            New project name
            <Input
              className="mt-1 w-56"
              value={projectName}
              onChange={(e) => setProjectName(e.target.value)}
            />
          </label>
          <Button type="button" onClick={onCreateGoProject} disabled={busy || !projectName.trim()}>
            Create project
          </Button>
        </div>
        {createdId ? (
          <p className="mt-3 text-sm text-[var(--ink-muted)]">
            Created project <span className="font-[family-name:var(--font-mono)] text-xs">{createdId}</span>.
            Scaffold with <code className="font-[family-name:var(--font-mono)] text-xs">jp init --runtime go</code>,
            then deploy to this project id.
          </p>
        ) : null}
      </section>

      <section className="rounded-md border border-[var(--border)] bg-[var(--surface)] p-5">
        <h2 className="text-lg font-medium text-[var(--ink)]">Managed add-ons marketplace</h2>
        <p className="mt-2 text-sm text-[var(--ink-muted)]">
          Add data services and queues to any project in one click. Each provision action stores a
          connection value in the secrets service.
        </p>
        <div className="mt-3 flex flex-wrap gap-2 text-xs text-[var(--ink-faint)]">
          <span className="rounded border border-[var(--border)] px-2 py-1">redis</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">postgres</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">mysql</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">mongodb</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">rabbitmq</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">kafka</span>
          <span className="rounded border border-[var(--border)] px-2 py-1">sqlite</span>
        </div>
        <div className="mt-4">
          <Link
            href="/addons"
            className="inline-flex items-center justify-center rounded-md border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-sm font-medium text-[var(--ink)] hover:bg-[var(--surface-hover)]"
          >
            Open Add-ons
          </Link>
        </div>
      </section>
    </div>
  );
}
