"use client";

import { useId, useRef } from "react";
import { cn } from "@/lib/cn";

export interface TabItem<T extends string = string> {
  id: T;
  label: string;
  badge?: React.ReactNode;
}

export function Tabs<T extends string>({
  items,
  value,
  onChange,
  label,
  className,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  label: string;
  className?: string;
}) {
  const base = useId();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const move = (i: number) => {
    const n = (i + items.length) % items.length;
    onChange(items[n].id);
    refs.current[n]?.focus();
  };
  return (
    <div role="tablist" aria-label={label} className={cn("tabs", className)}>
      {items.map((t, i) => (
        <button
          key={t.id}
          ref={(el) => {
            refs.current[i] = el;
          }}
          id={`${base}-tab-${t.id}`}
          role="tab"
          type="button"
          className="tab"
          aria-selected={t.id === value}
          tabIndex={t.id === value ? 0 : -1}
          onClick={() => onChange(t.id)}
          onKeyDown={(e) => {
            if (e.key === "ArrowRight") {
              e.preventDefault();
              move(i + 1);
            } else if (e.key === "ArrowLeft") {
              e.preventDefault();
              move(i - 1);
            } else if (e.key === "Home") {
              e.preventDefault();
              move(0);
            } else if (e.key === "End") {
              e.preventDefault();
              move(items.length - 1);
            }
          }}
        >
          {t.label}
          {t.badge !== undefined && <span className="mono ml-2 text-[11px] opacity-80">{t.badge}</span>}
        </button>
      ))}
    </div>
  );
}

export function TabPanel({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div role="tabpanel" tabIndex={0} className={cn("page-enter outline-offset-4", className)}>
      {children}
    </div>
  );
}
