"use client";

import React, { useState } from "react";

interface AIResponseViewProps {
  content: string;
  model?: string;
  mode?: string;
  title?: string;
  riskLevel?: string;
  className?: string;
}

export function AIResponseView({
  content,
  model = "gemini-3.5-flash-lite",
  mode,
  title,
  riskLevel,
  className = "",
}: AIResponseViewProps) {
  const [copiedFull, setCopiedFull] = useState(false);
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null);

  if (!content) return null;

  function copyFull() {
    navigator.clipboard.writeText(content);
    setCopiedFull(true);
    setTimeout(() => setCopiedFull(false), 2000);
  }

  function copySnippet(text: string, index: number) {
    navigator.clipboard.writeText(text);
    setCopiedIndex(index);
    setTimeout(() => setCopiedIndex(null), 2000);
  }

  // Parse markdown content into structured blocks (headers, code blocks, bullet lists, step items, paragraphs)
  const blocks = parseMarkdownBlocks(content);

  // Auto-detect risk/severity if not explicitly provided
  const detectedRisk =
    riskLevel ||
    (content.toLowerCase().includes("critical") || content.toLowerCase().includes("fatal")
      ? "critical"
      : content.toLowerCase().includes("warning") || content.toLowerCase().includes("error") || content.toLowerCase().includes("econnrefused")
      ? "warning"
      : "healthy");

  return (
    <div className={`rounded-xl border-2 border-slate-300 bg-white shadow-sm overflow-hidden ${className}`}>
      {/* AI Header Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b-2 border-slate-100 bg-gradient-to-r from-slate-50 to-blue-50/40 px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-blue-600 via-indigo-600 to-slate-900 text-white text-xs shadow-xs">
            ✨
          </span>
          <div>
            <h3 className="text-xs font-black uppercase tracking-wider text-[#0F1B3D] flex items-center gap-2">
              <span>{title || "Gemini 3.5 Operational Intelligence"}</span>
              <span className="rounded-full bg-blue-100/90 border border-blue-300 px-2 py-0.2 text-[9px] font-extrabold text-blue-900 font-mono">
                {model}
              </span>
            </h3>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {detectedRisk === "critical" ? (
            <span className="inline-flex items-center gap-1 rounded-full bg-red-100 border border-red-300 px-2.5 py-0.5 text-[10px] font-bold text-red-800">
              <span className="h-1.5 w-1.5 rounded-full bg-red-600 animate-pulse" />
              Critical Attention Needed
            </span>
          ) : detectedRisk === "warning" ? (
            <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 border border-amber-300 px-2.5 py-0.5 text-[10px] font-bold text-amber-900">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
              Warning / Action Recommended
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 border border-emerald-300 px-2.5 py-0.5 text-[10px] font-bold text-emerald-900">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-600" />
              Diagnostic Complete
            </span>
          )}

          {mode && (
            <span className="text-[10px] font-mono text-slate-500 font-semibold hidden sm:inline">
              Mode: {mode}
            </span>
          )}

          <button
            type="button"
            onClick={copyFull}
            className="h-7 px-2.5 rounded-md border border-slate-300 bg-white hover:bg-slate-50 text-[11px] font-bold text-slate-700 shadow-2xs transition-all flex items-center gap-1"
            title="Copy entire AI response"
          >
            {copiedFull ? "✓ Copied" : "📋 Copy"}
          </button>
        </div>
      </div>

      {/* Structured Content Area */}
      <div className="p-5 space-y-4 text-xs text-slate-800 leading-relaxed">
        {blocks.map((block, idx) => {
          if (block.type === "header") {
            const level = block.level;
            const icon = getHeaderIcon(block.text);
            return (
              <div
                key={idx}
                className={`pt-2 pb-1 border-b border-slate-100 flex items-center gap-2 ${
                  level === 2 ? "mt-4 text-sm font-extrabold text-[#0F1B3D]" : "mt-3 text-xs font-bold text-slate-900"
                }`}
              >
                <span>{icon}</span>
                <span className="tracking-tight">{formatInline(block.text)}</span>
              </div>
            );
          }

          if (block.type === "code") {
            const isSnippetCopied = copiedIndex === idx;
            return (
              <div key={idx} className="rounded-xl border border-slate-800 bg-slate-900 text-slate-100 overflow-hidden shadow-xs my-2">
                <div className="flex items-center justify-between px-3.5 py-1.5 bg-slate-950/80 border-b border-slate-800 text-[10px] font-mono text-slate-400">
                  <span className="uppercase font-bold text-emerald-400">
                    {block.lang || "bash"}
                  </span>
                  <button
                    type="button"
                    onClick={() => copySnippet(block.text, idx)}
                    className="hover:text-white transition-colors flex items-center gap-1 font-sans font-bold"
                  >
                    {isSnippetCopied ? "✓ Copied" : "📋 Copy Snippet"}
                  </button>
                </div>
                <pre className="p-3.5 text-[11px] font-mono overflow-x-auto leading-relaxed select-all">
                  <code>{block.text}</code>
                </pre>
              </div>
            );
          }

          if (block.type === "step") {
            return (
              <div
                key={idx}
                className="flex items-start gap-3 rounded-lg border-2 border-slate-200 bg-slate-50/70 p-3 shadow-2xs hover:border-blue-300 transition-colors"
              >
                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[#1E3A8A] text-white font-mono text-[10px] font-bold">
                  {block.stepNumber}
                </span>
                <div className="leading-snug text-slate-800 font-medium">
                  {formatInline(block.text)}
                </div>
              </div>
            );
          }

          if (block.type === "bullet") {
            return (
              <div key={idx} className="flex items-start gap-2.5 pl-1 my-1">
                <span className="text-blue-600 font-bold shrink-0 text-xs">▸</span>
                <div className="leading-relaxed text-slate-800 font-normal">
                  {formatInline(block.text)}
                </div>
              </div>
            );
          }

          // Regular paragraph
          return (
            <p key={idx} className="text-slate-800 leading-relaxed font-normal">
              {formatInline(block.text)}
            </p>
          );
        })}
      </div>
    </div>
  );
}

// Helpers for block parsing
interface Block {
  type: "header" | "code" | "step" | "bullet" | "paragraph";
  text: string;
  level?: number;
  lang?: string;
  stepNumber?: number;
}

function parseMarkdownBlocks(raw: string): Block[] {
  const lines = raw.split("\n");
  const blocks: Block[] = [];
  let inCode = false;
  let codeLang = "";
  let codeLines: string[] = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    if (line.trim().startsWith("```")) {
      if (inCode) {
        blocks.push({
          type: "code",
          text: codeLines.join("\n"),
          lang: codeLang,
        });
        inCode = false;
        codeLines = [];
        codeLang = "";
      } else {
        inCode = true;
        codeLang = line.trim().replace(/^```/, "").trim();
        codeLines = [];
      }
      continue;
    }

    if (inCode) {
      codeLines.push(line);
      continue;
    }

    const trimmed = line.trim();
    if (!trimmed) continue;

    // Headers
    if (trimmed.startsWith("### ")) {
      blocks.push({ type: "header", text: trimmed.replace(/^###\s+/, ""), level: 3 });
      continue;
    }
    if (trimmed.startsWith("## ")) {
      blocks.push({ type: "header", text: trimmed.replace(/^##\s+/, ""), level: 2 });
      continue;
    }
    if (trimmed.startsWith("# ")) {
      blocks.push({ type: "header", text: trimmed.replace(/^#\s+/, ""), level: 1 });
      continue;
    }

    // Numbered steps e.g. "1. " or "1) "
    const stepMatch = trimmed.match(/^(\d+)[\.\)]\s+(.*)/);
    if (stepMatch) {
      blocks.push({
        type: "step",
        stepNumber: parseInt(stepMatch[1], 10),
        text: stepMatch[2],
      });
      continue;
    }

    // Bullets e.g. "- ", "* ", "• "
    if (trimmed.startsWith("- ") || trimmed.startsWith("* ") || trimmed.startsWith("• ")) {
      blocks.push({
        type: "bullet",
        text: trimmed.replace(/^[-*•]\s+/, ""),
      });
      continue;
    }

    blocks.push({
      type: "paragraph",
      text: trimmed,
    });
  }

  if (inCode && codeLines.length > 0) {
    blocks.push({
      type: "code",
      text: codeLines.join("\n"),
      lang: codeLang,
    });
  }

  return blocks;
}

function getHeaderIcon(text: string): string {
  const low = text.toLowerCase();
  if (low.includes("cause") || low.includes("root") || low.includes("error") || low.includes("failure")) return "⚠️";
  if (low.includes("fix") || low.includes("step") || low.includes("solution") || low.includes("resolve") || low.includes("remediation")) return "🛠️";
  if (low.includes("overview") || low.includes("summary") || low.includes("diagnosis")) return "🔍";
  if (low.includes("ssl") || low.includes("security") || low.includes("cert")) return "🔒";
  if (low.includes("db") || low.includes("database") || low.includes("redis") || low.includes("postgres")) return "🧩";
  if (low.includes("recommend") || low.includes("tip") || low.includes("best practice")) return "💡";
  return "⚡";
}

function formatInline(text: string): React.ReactNode[] {
  // Regex to split inline code `...` and bold **...**
  const parts: React.ReactNode[] = [];
  const regex = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let lastIdx = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIdx) {
      parts.push(text.slice(lastIdx, match.index));
    }
    const token = match[0];
    if (token.startsWith("`") && token.endsWith("`")) {
      parts.push(
        <code
          key={match.index}
          className="rounded border border-slate-300 bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-bold text-slate-900 select-all"
        >
          {token.slice(1, -1)}
        </code>
      );
    } else if (token.startsWith("**") && token.endsWith("**")) {
      parts.push(
        <strong key={match.index} className="font-extrabold text-[#0F1B3D]">
          {token.slice(2, -2)}
        </strong>
      );
    }
    lastIdx = regex.lastIndex;
  }

  if (lastIdx < text.length) {
    parts.push(text.slice(lastIdx));
  }

  return parts.length > 0 ? parts : [text];
}
