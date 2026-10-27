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
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("Explain the latest failed deployment or build.");
  const [answer, setAnswer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"explain" | "ask" | "">("");

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
        setModel(status.model || "");
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
    setBusy("explain");
    setError("");
    setAnswer("");
    try {
      const res = await api.explainFailure(currentOrg.id, projectId, { prompt });
      setAnswer(res.explanation);
      setMode(res.mode);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Explain failed");
    } finally {
      setBusy("");
    }
  }

  async function onAsk() {
    if (!currentOrg || !prompt.trim()) return;
    setBusy("ask");
    setError("");
    setAnswer("");
    try {
      const res = await api.aiAsk(
        currentOrg.id,
        prompt,
        projectId || undefined,
      );
      setAnswer(res.answer);
      setMode(res.mode);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Ask failed");
    } finally {
      setBusy("");
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
        title="AI Ops & Diagnostics"
        description="Automated failure analysis and root-cause diagnostic assistant."
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

      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          onClick={onExplain}
          disabled={!!busy || !projectId}
        >
          {busy === "explain" ? "Analyzing…" : "Explain failure"}
        </Button>
        <Button
          type="button"
          variant="secondary"
          onClick={onAsk}
          disabled={!!busy || !prompt.trim()}
        >
          {busy === "ask" ? "Asking…" : "Ask"}
        </Button>
      </div>

      {answer ? (
        <pre className="mt-8 max-w-3xl whitespace-pre-wrap rounded-md border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)]">
          {answer}
        </pre>
      ) : null}
    </div>
  );
}
