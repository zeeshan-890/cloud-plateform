"use client";

import Link from "next/link";
import { Fragment } from "react";
import { CopyButton } from "@/components/ui/code";
import { ErrorState } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { ApiError, type AiAnswer } from "@/lib/api";

/* Tiny markdown subset renderer. Output is React elements only: model text can
   never become HTML, and links are restricted to http(s). */

function safeHref(raw: string): string | null {
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.toString() : null;
  } catch {
    return null;
  }
}

const INLINE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|(\[[^\]\n]+\]\([^)\s]+\))/g;

export function renderInline(text: string): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  INLINE.lastIndex = 0;
  let k = 0;
  while ((m = INLINE.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("`")) {
      out.push(
        <code key={k++} className="mono rounded px-1 text-[0.92em]" style={{ background: "rgba(0,0,0,0.12)" }}>
          {tok.slice(1, -1)}
        </code>,
      );
    } else if (tok.startsWith("**")) {
      out.push(<strong key={k++}>{tok.slice(2, -2)}</strong>);
    } else {
      const lm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok);
      const href = lm ? safeHref(lm[2]) : null;
      out.push(
        href && lm ? (
          <a key={k++} href={href} target="_blank" rel="noopener noreferrer nofollow">
            {lm[1]}
          </a>
        ) : (
          <Fragment key={k++}>{lm ? lm[1] : tok}</Fragment>
        ),
      );
    }
    last = m.index + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

type Block =
  | { kind: "h"; level: number; text: string }
  | { kind: "p"; text: string }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: string[] }
  | { kind: "code"; lang: string; text: string };

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      const lang = line.slice(3).trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(lines[i++]);
      i++;
      blocks.push({ kind: "code", lang, text: buf.join("\n") });
    } else if (/^#{1,4}\s/.test(line)) {
      const m = /^(#{1,4})\s+(.*)$/.exec(line)!;
      blocks.push({ kind: "h", level: m[1].length, text: m[2] });
      i++;
    } else if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*[-*]\s+/, ""));
      blocks.push({ kind: "ul", items });
    } else if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*\d+[.)]\s+/, ""));
      blocks.push({ kind: "ol", items });
    } else if (line.trim() === "") {
      i++;
    } else {
      const buf: string[] = [];
      while (i < lines.length && lines[i].trim() !== "" && !/^(```|#{1,4}\s|\s*[-*]\s+|\s*\d+[.)]\s+)/.test(lines[i])) buf.push(lines[i++]);
      blocks.push({ kind: "p", text: buf.join(" ") });
    }
  }
  return blocks;
}

export function Markdownish({ text }: { text: string }) {
  const blocks = parseBlocks(text);
  return (
    <div className="flex flex-col gap-3 text-[15px] leading-relaxed">
      {blocks.map((b, i) => {
        switch (b.kind) {
          case "h":
            return (
              <h4 key={i} className="text-[17px]">
                {renderInline(b.text)}
              </h4>
            );
          case "p":
            return (
              <p key={i} className="m-0">
                {renderInline(b.text)}
              </p>
            );
          case "ul":
            return (
              <ul key={i} className="m-0 list-disc pl-6">
                {b.items.map((t, j) => (
                  <li key={j}>{renderInline(t)}</li>
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={i} className="m-0 list-decimal pl-6">
                {b.items.map((t, j) => (
                  <li key={j}>{renderInline(t)}</li>
                ))}
              </ol>
            );
          case "code":
            return (
              <div key={i} className="flex flex-col gap-2">
                <pre className="code-block m-0" tabIndex={0}>
                  {b.text}
                </pre>
                <div>
                  <CopyButton text={b.text} />
                </div>
              </div>
            );
        }
      })}
    </div>
  );
}

export function AiResponseView({ answer, error }: { answer?: AiAnswer; error?: unknown }) {
  if (error !== undefined) {
    const err = error instanceof Error ? error : new Error(String(error));
    if (err instanceof ApiError && (err.code === "ai_not_configured" || err.status === 412 || /not configured|api key/i.test(err.message))) {
      return (
        <div className="well flex flex-col gap-2">
          <p className="m-0 font-semibold">AI is not configured for this organization.</p>
          <p className="m-0 text-[14.5px]">{err.message}</p>
          <Link href="/ai">Configure AI provider</Link>
        </div>
      );
    }
    return <ErrorState error={err} />;
  }
  if (!answer) return null;
  const body = answer.answer ?? answer.explanation ?? answer.analysis ?? "";
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {answer.severity && <Tag tone={/crit|high|error/i.test(answer.severity) ? "fail" : /med|warn/i.test(answer.severity) ? "warn" : "ok"}>severity: {answer.severity}</Tag>}
        {answer.model && <Tag tone="idle">{answer.model}</Tag>}
        {answer.mode && answer.mode !== "live" && <Tag tone="warn">mode: {answer.mode}</Tag>}
      </div>
      {answer.summary && <p className="m-0 text-[16px] font-semibold">{answer.summary}</p>}
      {body && <Markdownish text={body} />}
      {answer.recommendations && answer.recommendations.length > 0 && (
        <div>
          <h4 className="label mb-2">Recommendations</h4>
          <ul className="m-0 list-disc pl-6">
            {answer.recommendations.map((r, i) => (
              <li key={i}>{renderInline(r)}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
