"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { Button } from "./button";
import { Icon } from "./icons";
import { useToast } from "./toast";

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    try {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand("copy");
      document.body.removeChild(ta);
      return ok;
    } catch {
      return false;
    }
  }
}

export function CopyButton({ text, label = "Copy", size = "sm", className }: { text: string; label?: string; size?: "sm" | "md"; className?: string }) {
  const [done, setDone] = useState(false);
  const toast = useToast();
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  return (
    <Button
      size={size}
      className={className}
      aria-label={done ? "Copied" : `${label}`}
      onClick={async () => {
        const ok = await copyText(text);
        if (!ok) {
          toast.error("Copy failed", new Error("Clipboard access is blocked. Select the text and copy it manually."));
          return;
        }
        setDone(true);
        window.clearTimeout(timer.current);
        timer.current = window.setTimeout(() => setDone(false), 1600);
      }}
    >
      <Icon name={done ? "check" : "copy"} size={15} />
      {done ? "Copied" : label}
    </Button>
  );
}

/** Dark terminal-style block for commands, keys and URLs. Content is always rendered as text. */
export function CodeBlock({ text, copy = true, className, label }: { text: string; copy?: boolean; className?: string; label?: string }) {
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {label && <span className="label">{label}</span>}
      <pre className="code-block m-0" tabIndex={0} aria-label={label}>
        {text}
      </pre>
      {copy && (
        <div>
          <CopyButton text={text} />
        </div>
      )}
    </div>
  );
}

const ANSI = new RegExp(String.fromCharCode(27) + "\\[[0-9;?]*[ -/]*[@-~]", "g");
export function stripAnsi(s: string): string {
  return s.replace(ANSI, "");
}

export interface TapeLine {
  text: string;
  level?: string;
  at?: string;
}

/** Paper-roll log viewer. Follows the tail unless the reader scrolled up. */
export function LogTape({
  lines,
  follow = true,
  empty = "Nothing printed yet.",
  label,
  maxHeight = 440,
}: {
  lines: TapeLine[];
  follow?: boolean;
  empty?: string;
  label: string;
  maxHeight?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (el && follow && pinned.current) el.scrollTop = el.scrollHeight;
  }, [lines, follow]);
  return (
    <div className="tape-roll">
      <div
        ref={ref}
        className="tape-scroll scroll-thin"
        style={{ maxHeight }}
        tabIndex={0}
        role="log"
        aria-label={label}
        aria-live="off"
        onScroll={(e) => {
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
        }}
      >
        {lines.length === 0 ? (
          <p className="muted m-0 py-3 text-center" style={{ color: "#62563f" }}>
            {empty}
          </p>
        ) : (
          lines.map((l, i) => (
            <div key={i} className="tape-line" data-level={(l.level || "").toLowerCase() || undefined}>
              <span className="n" aria-hidden="true">
                {i + 1}
              </span>
              <span>
                {l.at && <span style={{ opacity: 0.8 }}>{l.at} </span>}
                {stripAnsi(l.text)}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

export function textToLines(text: string): TapeLine[] {
  if (!text) return [];
  return text.replace(/\r\n/g, "\n").split("\n").map((t) => {
    const lower = t.toLowerCase();
    const level = /\b(error|fatal|panic|failed)\b/.test(lower) ? "error" : /\bwarn(ing)?\b/.test(lower) ? "warn" : undefined;
    return { text: t, level };
  });
}
