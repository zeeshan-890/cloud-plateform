"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project } from "@/lib/types";

export default function AIPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [mode, setMode] = useState("simulate");
  const [prompt, setPrompt] = useState("Explain the latest failed deployment or build.");
  const [answer, setAnswer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        const [list, status] = await Promise.all([
          api.listProjects(currentOrg.id),
          api.aiStatus(currentOrg.id),
        ]);
        if (!mounted) return;
        setProjects(list);
        if (list[0]) setProjectId(list[0].id);
        setMode(status.mode || "simulate");
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load AI status");
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  async function onExplain() {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setAnswer("");
    try {
      const res = await api.explainFailure(currentOrg.id, projectId, { prompt });
      setAnswer(res.explanation);
      setMode(res.mode);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Explain failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="AI ops requires an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="AI ops"
        description={`Explain failed deploys/builds from logs. Mode: ${mode} (set OPENAI_API_KEY for hosted LLM).`}
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

      <label className="mb-4 block max-w-2xl text-sm text-[var(--ink-muted)]">
        Prompt
        <textarea
          className="mt-1 min-h-[88px] w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[var(--ink)]"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
        />
      </label>

      <Button type="button" onClick={onExplain} disabled={busy || !projectId}>
        {busy ? "Analyzing…" : "Explain failure"}
      </Button>

      {answer ? (
        <pre className="mt-8 max-w-3xl whitespace-pre-wrap rounded-md border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)]">
          {answer}
        </pre>
      ) : null}
    </div>
  );
}
