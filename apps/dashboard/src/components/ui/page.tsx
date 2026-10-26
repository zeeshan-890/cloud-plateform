import React from "react";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between pb-4 border-b border-[var(--border)]">
      <div>
        <h1 className="font-[family-name:var(--font-display)] text-2xl font-bold tracking-tight text-[var(--ink)] sm:text-3xl">
          {title}
        </h1>
        {description ? (
          <p className="mt-1 text-sm text-[var(--ink-secondary)] font-medium">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function Card({
  className = "",
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={`jp-card p-4 sm:p-5 ${className}`}
      {...props}
    >
      {children}
    </div>
  );
}

export function CardHeader({
  className = "",
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={`mb-3 flex flex-col gap-1 ${className}`} {...props}>
      {children}
    </div>
  );
}

export function CardTitle({
  className = "",
  children,
  ...props
}: React.HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3
      className={`font-[family-name:var(--font-display)] text-base font-bold text-[var(--ink)] tracking-tight ${className}`}
      {...props}
    >
      {children}
    </h3>
  );
}

export function CardDescription({
  className = "",
  children,
  ...props
}: React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p className={`text-xs text-[var(--ink-secondary)] font-medium ${className}`} {...props}>
      {children}
    </p>
  );
}

export function CardContent({
  className = "",
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={`${className}`} {...props}>
      {children}
    </div>
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="jp-card px-6 py-12 text-center border-dashed">
      <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-full bg-slate-100 text-slate-500 mb-3">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
        </svg>
      </div>
      <h2 className="font-[family-name:var(--font-display)] text-lg font-bold text-[var(--ink)]">
        {title}
      </h2>
      <p className="mx-auto mt-1 max-w-md text-xs text-[var(--ink-secondary)] font-medium">
        {description}
      </p>
      {action ? <div className="mt-5 flex justify-center">{action}</div> : null}
    </div>
  );
}

export function Alert({
  children,
  tone = "error",
}: {
  children: React.ReactNode;
  tone?: "error" | "success" | "info" | "warning";
}) {
  const tones = {
    error: "border-[var(--danger-border)] bg-[var(--danger-soft)] text-[var(--danger)] font-medium",
    success: "border-[var(--ok-border)] bg-[var(--ok-soft)] text-[var(--ok)] font-medium",
    info: "border-[var(--accent-border)] bg-[var(--accent-soft)] text-[var(--accent-deep)] font-medium",
    warning: "border-[var(--warning-border)] bg-[var(--warning-soft)] text-[var(--warning)] font-medium",
  };
  return (
    <div className={`rounded-md border px-3 py-2 text-xs shadow-xs ${tones[tone]}`}>
      {children}
    </div>
  );
}
