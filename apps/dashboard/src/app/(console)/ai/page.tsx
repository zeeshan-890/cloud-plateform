"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project } from "@/lib/types";

const PROMPT_TEMPLATES = [
  {
    label: "Explain Latest Failure",
    prompt: "Analyze the latest failed deployment or build. Identify the exact root cause, stack trace, and give step-by-step fix instructions.",
    icon: "⚠️",
  },
  {
    label: "Container & Port Audit",
    prompt: "Check our container runtime status, port 8080 binding, and provide best practices for rolling zero-downtime updates.",
    icon: "🐳",
  },
  {
    label: "Add-ons & Database Connection",
    prompt: "Explain how to connect our application to managed PostgreSQL, MongoDB, or Redis add-ons using environment variables.",
    icon: "🧩",
  },
  {
    label: "Security & Wildcard SSL",
    prompt: "Review the production domain setup (*.zeeshanabbaskhan.me) and give recommendations for SSL certificates and CORS headers.",
    icon: "🛡️",
  },
];

export default function AIPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [mode, setMode] = useState("gemini-api");
  const [model, setModel] = useState("gemini-3.5-flash-lite");
  const [prompt, setPrompt] = useState(PROMPT_TEMPLATES[0].prompt);
  const [answer, setAnswer] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState<"explain" | "ask" | "logs" | "">("");

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
        if (status.mode) setMode(status.mode);
        if (status.model) setModel(status.model);
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
      if (res.mode) setMode(res.mode);
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
      if (res.mode) setMode(res.mode);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Ask failed");
    } finally {
      setBusy("");
    }
  }

  async function onAnalyzeLogs() {
    if (!currentOrg || !projectId) return;
    setBusy("logs");
    setError("");
    setAnswer("");
    try {
      const res = await api.analyzeLogs(currentOrg.id, projectId, {
        source: "runtime",
        limit: 100,
      });
      const formatted = `### Live Log Diagnostic Summary
${res.summary || "Container log inspection complete."}

#### Deep Analysis
${res.analysis}

#### Recommended Fixes
${res.recommendations?.map((r, i) => `${i + 1}. ${r}`).join("\n") || "No action required."}`;
      setAnswer(formatted);
      if (res.model) setModel(res.model);
      setMode("gemini-api");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Log analysis failed");
    } finally {
      setBusy("");
    }
  }

  function copyAnswer() {
    if (!answer) return;
    navigator.clipboard.writeText(answer);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="AI operations require an active organization workspace."
      />
    );
  }

  return (
    <div className="space-y-6 animate-fade-up">
      {/* Crezumic-Style Page Header */}
      <div className="bg-white rounded-xl border border-slate-200/90 p-6 shadow-xs">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2.5">
              <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 to-blue-600 text-white text-lg shadow-xs">
                ✨
              </span>
              <div>
                <h1 className="text-2xl md:text-3xl font-extrabold text-slate-900 tracking-tight">
                  AI Ops & Diagnostic Intelligence
                </h1>
                <p className="text-xs text-slate-500">
                  Automated root-cause analysis, container log diagnostics, and architectural optimization.
                </p>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <span className="inline-flex items-center gap-1.5 rounded-full border border-indigo-200 bg-indigo-50/70 px-3 py-1 text-xs font-bold text-indigo-700">
              <span className="h-2 w-2 rounded-full bg-blue-500 animate-pulse" />
              Gemini 3.5 Flash Lite Active
            </span>
          </div>
        </div>
      </div>

      {error ? (
        <Alert tone="error">{error}</Alert>
      ) : null}

      {/* Main Interactive AI Query Console */}
      <div className="bg-white rounded-xl border border-slate-200/90 p-6 shadow-xs space-y-5">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
          <div>
            <h3 className="text-sm font-bold text-slate-900">
              Assistant Query & Failure Diagnosis
            </h3>
            <p className="text-xs text-slate-500">
              Select an application and submit custom prompts or choose pre-built diagnostic templates.
            </p>
          </div>

          <div className="flex items-center gap-2">
            <label className="text-xs font-bold text-slate-600 shrink-0">
              Target App:
            </label>
            <select
              className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-1.5 text-xs font-bold text-slate-900 shadow-2xs focus:border-blue-500 focus:outline-none"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
            >
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
              {projects.length === 0 && <option value="">No projects found</option>}
            </select>
          </div>
        </div>

        {/* Quick Suggestion Chips */}
        <div className="space-y-1.5">
          <p className="text-[11px] font-bold uppercase tracking-wider text-slate-400">
            Quick Diagnostic Templates:
          </p>
          <div className="flex flex-wrap gap-2">
            {PROMPT_TEMPLATES.map((tmpl) => (
              <button
                key={tmpl.label}
                type="button"
                onClick={() => setPrompt(tmpl.prompt)}
                className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-semibold transition-all ${
                  prompt === tmpl.prompt
                    ? "border-blue-500 bg-blue-50 text-blue-700 shadow-2xs"
                    : "border-slate-200 bg-white text-slate-600 hover:border-slate-300 hover:bg-slate-50"
                }`}
              >
                <span>{tmpl.icon}</span>
                <span>{tmpl.label}</span>
              </button>
            ))}
          </div>
        </div>

        {/* Prompt Input Box */}
        <div className="space-y-1.5">
          <label className="text-xs font-bold text-slate-700">
            Prompt / Question:
          </label>
          <textarea
            className="w-full min-h-[110px] rounded-xl border border-slate-200 bg-slate-50/50 p-3.5 text-xs text-slate-900 shadow-inner focus:border-blue-500 focus:bg-white focus:outline-none transition-all leading-relaxed"
            placeholder="Type your question or issue description for Gemini 3.5 Flash Lite..."
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </div>

        {/* Action Buttons */}
        <div className="flex flex-wrap items-center gap-3 pt-2">
          <button
            type="button"
            onClick={onAsk}
            disabled={!!busy || !prompt.trim()}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-xs font-bold text-white shadow-xs hover:bg-blue-700 transition-all disabled:opacity-50"
          >
            <span>✨</span>
            <span>{busy === "ask" ? "Asking Gemini 3.5..." : "Ask Gemini 3.5"}</span>
          </button>

          <button
            type="button"
            onClick={onExplain}
            disabled={!!busy || !projectId}
            className="inline-flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-xs font-bold text-slate-800 shadow-2xs hover:bg-slate-50 transition-all disabled:opacity-50"
          >
            <span>🔍</span>
            <span>{busy === "explain" ? "Diagnosing Failure..." : "Explain Last Failure"}</span>
          </button>

          <button
            type="button"
            onClick={onAnalyzeLogs}
            disabled={!!busy || !projectId}
            className="inline-flex items-center gap-2 rounded-lg border border-indigo-200 bg-gradient-to-r from-blue-50 to-indigo-50 px-4 py-2 text-xs font-bold text-indigo-700 shadow-2xs hover:from-blue-100 hover:to-indigo-100 transition-all disabled:opacity-50"
          >
            <span>📋</span>
            <span>{busy === "logs" ? "Analyzing Live Logs..." : "Analyze Live Logs"}</span>
          </button>
        </div>
      </div>

      {/* Answer / AI Analysis Output Card */}
      {answer ? (
        <div className="bg-white rounded-xl border border-indigo-200 p-6 shadow-sm space-y-4 animate-fade-up">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 pb-3">
            <div className="flex items-center gap-2">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-indigo-500 to-blue-600 text-white text-xs shadow-xs">
                ✨
              </span>
              <h3 className="text-sm font-bold text-slate-900">
                Gemini 3.5 Flash Lite Output
              </h3>
              <span className="rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-bold text-emerald-800 border border-emerald-200">
                Mode: {mode}
              </span>
            </div>

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={copyAnswer}
                className="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs font-bold text-slate-700 shadow-2xs hover:bg-slate-50 transition-all"
              >
                <span>{copied ? "✓ Copied" : "📋 Copy Output"}</span>
              </button>
            </div>
          </div>

          <div className="bg-slate-50/70 border border-slate-200/80 rounded-xl p-4 text-xs font-mono text-slate-800 leading-relaxed whitespace-pre-wrap shadow-inner overflow-x-auto">
            {answer}
          </div>
        </div>
      ) : null}
    </div>
  );
}
