import React from "react";

export function PageHeader({
  actions,
}: {
  title?: string;
  description?: string;
  actions?: React.ReactNode;
}) {
  if (!actions) return null;
  return (
    <div className="flex items-center justify-end gap-2.5 mb-4 shrink-0">
      {actions}
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
      className={`bg-white border-2 border-slate-300 rounded-xl shadow-sm p-5 sm:p-6 transition-all ${className}`}
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
    <div className={`mb-3 flex flex-col gap-1 border-b border-slate-100 pb-3 ${className}`} {...props}>
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
      className={`text-base font-extrabold text-slate-900 tracking-tight ${className}`}
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
    <p className={`text-xs text-slate-500 font-medium ${className}`} {...props}>
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
    <div className="bg-white/70 border-2 border-dashed border-slate-300 rounded-xl px-6 py-14 text-center shadow-xs">
      <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-blue-50 text-[#1E3A8A] border-2 border-blue-200 mb-3.5">
        <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.75} d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
        </svg>
      </div>
      <h2 className="text-lg font-extrabold text-[#0F1B3D]">
        {title}
      </h2>
      <p className="mx-auto mt-1 max-w-md text-xs text-slate-600 font-medium leading-relaxed">
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
    error: "border-2 border-red-300 bg-red-50 text-red-900 font-medium",
    success: "border-2 border-emerald-300 bg-emerald-50 text-emerald-950 font-medium",
    info: "border-2 border-blue-300 bg-blue-50 text-blue-950 font-medium",
    warning: "border-2 border-amber-300 bg-amber-50 text-amber-950 font-medium",
  };
  return (
    <div className={`rounded-xl px-4 py-3 text-xs shadow-xs flex items-center justify-between gap-3 ${tones[tone]}`}>
      <div className="flex items-center gap-2">{children}</div>
    </div>
  );
}
