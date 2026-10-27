"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
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
  const router = useRouter();
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [mode, setMode] = useState("gemini-api");
  const [model, setModel] = useState("gemini-3.5-flash-lite");
  const [prompt, setPrompt] = useState(PROMPT_TEMPLATES[0].prompt);
  const [answer, setAnswer] = useState("");
  const [error, setError] = useState("");
  const [sessionExpired, setSessionExpired] = useState(false);
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
          if (err instanceof ApiError && (err.status === 401 || err.message.toLowerCase().includes("token") || err.message.toLowerCase().includes("unauthorized"))) {
            setSessionExpired(true);
            return;
          }
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
      if (err instanceof ApiError && (err.status === 401 || err.message.toLowerCase().includes("token"))) {
        setSessionExpired(true);
        return;
      }
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
      if (err instanceof ApiError && (err.status === 401 || err.message.toLowerCase().includes("token"))) {
        setSessionExpired(true);
        return;
      }
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
      if (err instanceof ApiError && (err.status === 401 || err.message.toLowerCase().includes("token"))) {
        setSessionExpired(true);
        return;
      }
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
      <div className="bg-white rounded-xl border-2 border-slate-300 p-6 shadow-sm">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-3">
              <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-gradient-to-br from-blue-700 to-indigo-800 text-white text-lg shadow-sm">
                ✨
              </span>
              <div>
                <h1 className="text-xl md:text-2xl font-extrabold text-[#0F1B3D] tracking-tight">
                  AI Ops & Diagnostic Intelligence
                </h1>
                <p className="text-xs text-slate-500 font-medium">
                  Automated root-cause analysis, container log diagnostics, and architectural optimization.
                </p>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <span className="inline-flex items-center gap-1.5 rounded-full border-2 border-blue-200 bg-blue-50 px-3 py-1 text-xs font-bold text-[#1E3A8A] shadow-2xs">
              <span className="h-2 w-2 rounded-full bg-blue-600 animate-pulse" />
              Gemini 3.5 Flash Lite Active
            </span>
          </div>
        </div>
      </div>

      {sessionExpired ? (
        <div className="bg-amber-50 border-2 border-amber-300 rounded-xl p-5 shadow-sm flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
          <div className="flex items-center gap-3">
            <span className="text-2xl">🔐</span>
            <div>
              <p className="text-sm font-extrabold text-amber-950">Your console session has expired</p>
              <p className="text-xs text-amber-800">Please sign in again to refresh security credentials and access your projects.</p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => router.push("/login")}
            className="h-9 px-4 rounded-lg bg-[#1E3A8A] text-white font-bold text-xs inline-flex items-center justify-center shadow-sm hover:bg-[#162456] transition-colors shrink-0"
          >
            Log In Again &rarr;
          </button>
        </div>
      ) : null}

      {error ? (
        <Alert tone="error">{error}</Alert>
      ) : null}

      {/* Main Interactive AI Query Console */}
      <div className="bg-white rounded-xl border-2 border-slate-300 p-6 shadow-sm space-y-5">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-100 pb-4">
          <div>
            <h3 className="text-base font-extrabold text-[#0F1B3D]">
              Assistant Query & Failure Diagnosis
            </h3>
            <p className="text-xs text-slate-500 font-medium">
              Select an application and submit custom prompts or choose pre-built diagnostic templates.
            </p>
          </div>

          <div className="flex items-center gap-2.5">
            <label className="text-xs font-bold text-slate-700 shrink-0 uppercase tracking-wider text-[11px]">
              Target App:
            </label>
            <select
              className="rounded-lg border-2 border-slate-300 bg-white px-3 py-1.5 text-xs font-bold text-slate-900 shadow-2xs focus:border-[#1E3A8A] focus:outline-none"
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
        <div className="space-y-2">
          <p className="text-[11px] font-bold uppercase tracking-wider text-slate-500">
            Quick Diagnostic Templates:
          </p>
          <div className="flex flex-wrap gap-2">
            {PROMPT_TEMPLATES.map((tmpl) => (
              <button
                key={tmpl.label}
                type="button"
                onClick={() => setPrompt(tmpl.prompt)}
                className={`inline-flex items-center gap-1.5 rounded-lg border-2 px-3.5 py-1.5 text-xs font-bold transition-all shadow-2xs ${
                  prompt === tmpl.prompt
                    ? "border-[#1E3A8A] bg-blue-50 text-[#1E3A8A] ring-1 ring-[#1E3A8A]/30"
                    : "border-slate-300 bg-white text-slate-700 hover:border-slate-400 hover:bg-slate-50"
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
          <label className="text-xs font-bold uppercase tracking-wider text-slate-700 text-[11px]">
            Prompt / Question:
          </label>
          <textarea
            className="w-full min-h-[120px] rounded-xl border-2 border-slate-300 bg-white p-4 text-xs font-medium text-slate-900 shadow-2xs focus:border-[#1E3A8A] focus:outline-none focus:ring-2 focus:ring-blue-500/20 transition-all leading-relaxed"
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
            className="h-10 px-5 rounded-lg bg-[#1E3A8A] text-white font-bold text-xs inline-flex items-center gap-2 shadow-sm hover:bg-[#162456] transition-colors disabled:opacity-50"
          >
            <span>✨</span>
            <span>{busy === "ask" ? "Asking Gemini 3.5..." : "Ask Gemini 3.5"}</span>
          </button>

          <button
            type="button"
            onClick={onExplain}
            disabled={!!busy || !projectId}
            className="h-10 px-4 rounded-lg bg-white border-2 border-slate-300 text-slate-800 font-bold text-xs inline-flex items-center gap-2 shadow-sm hover:bg-slate-50 hover:border-slate-400 transition-colors disabled:opacity-50"
          >
            <span>🔍</span>
            <span>{busy === "explain" ? "Diagnosing Failure..." : "Explain Last Failure"}</span>
          </button>

          <button
            type="button"
            onClick={onAnalyzeLogs}
            disabled={!!busy || !projectId}
            className="h-10 px-4 rounded-lg bg-white border-2 border-blue-200 text-[#1E3A8A] font-bold text-xs inline-flex items-center gap-2 shadow-sm hover:bg-blue-50 transition-colors disabled:opacity-50"
          >
            <span>📋</span>
            <span>{busy === "logs" ? "Analyzing Live Logs..." : "Analyze Live Logs"}</span>
          </button>
        </div>
      </div>

      {/* Answer / AI Analysis Output Card */}
      {answer ? (
        <div className="bg-white rounded-xl border-2 border-slate-300 p-6 shadow-sm space-y-4 animate-fade-up">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 pb-3">
            <div className="flex items-center gap-2">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-blue-700 to-indigo-800 text-white text-xs shadow-xs">
                ✨
              </span>
              <h3 className="text-sm font-extrabold text-[#0F1B3D]">
                Gemini 3.5 Flash Lite Output
              </h3>
              <span className="rounded-full bg-emerald-100 px-2.5 py-0.5 text-[10px] font-bold text-emerald-800 border border-emerald-300">
                Mode: {mode}
              </span>
            </div>

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={copyAnswer}
                className="h-8 px-3 rounded-md bg-white border-2 border-slate-300 text-slate-700 font-bold text-xs inline-flex items-center gap-1.5 hover:border-[#1E3A8A] hover:text-[#1E3A8A] transition-colors shadow-sm"
              >
                <span>{copied ? "✓ Copied" : "📋 Copy Output"}</span>
              </button>
            </div>
          </div>

          <div className="bg-slate-50 border-2 border-slate-200 rounded-xl p-4 text-xs font-mono text-slate-800 leading-relaxed whitespace-pre-wrap shadow-inner overflow-x-auto">
            {answer}
          </div>
        </div>
      ) : null}
    </div>
  );
}
