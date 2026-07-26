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
    <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <h1 className="font-[family-name:var(--font-display)] text-3xl font-bold tracking-tight text-[var(--ink)] sm:text-4xl">
          {title}
        </h1>
        {description ? (
          <p className="mt-2 max-w-xl text-sm text-[var(--ink-muted)]">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
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
    <div className="rounded-lg border border-dashed border-[var(--border)] bg-[var(--surface)] px-6 py-16 text-center">
      <h2 className="font-[family-name:var(--font-display)] text-xl font-semibold text-[var(--ink)]">
        {title}
      </h2>
      <p className="mx-auto mt-2 max-w-md text-sm text-[var(--ink-muted)]">
        {description}
      </p>
      {action ? <div className="mt-6 flex justify-center">{action}</div> : null}
    </div>
  );
}

export function Alert({
  children,
  tone = "error",
}: {
  children: React.ReactNode;
  tone?: "error" | "success" | "info";
}) {
  const tones = {
    error: "border-[var(--danger-border)] bg-[var(--danger-soft)] text-[var(--danger)]",
    success: "border-[var(--ok-border)] bg-[var(--ok-soft)] text-[var(--ok)]",
    info: "border-[var(--border)] bg-[var(--surface)] text-[var(--ink-muted)]",
  };
  return (
    <div className={`rounded-md border px-3 py-2.5 text-sm ${tones[tone]}`}>
      {children}
    </div>
  );
}
