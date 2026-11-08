import { cn } from "@/lib/cn";
import { statusLamp, type LampState } from "@/lib/format";

export function Lamp({
  state,
  pulse,
  label,
  large,
  className,
}: {
  state: LampState;
  pulse?: boolean;
  /** accessible name; omit when the lamp sits next to visible text */
  label?: string;
  large?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn("lamp", large && "lamp--lg", className)}
      data-state={state}
      data-pulse={pulse ? "true" : undefined}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    />
  );
}

type TagTone = "default" | "ok" | "warn" | "fail" | "info" | "accent" | "idle";

/** Label-maker tape tag. */
export function Tag({ tone = "default", children, className, title }: { tone?: TagTone; children: React.ReactNode; className?: string; title?: string }) {
  return (
    <span className={cn("tag", tone !== "default" && `tag--${tone}`, className)} title={title}>
      {children}
    </span>
  );
}

/** Lamp + words: color is never the only signal. */
export function StatusBadge({ status, label }: { status: string | undefined; label?: string }) {
  const { state, pulse } = statusLamp(status);
  const text = label ?? (status || "unknown");
  return (
    <span className="inline-flex items-center gap-2">
      <Lamp state={state} pulse={pulse} />
      <span className="mono text-[12.5px] font-medium uppercase tracking-[0.08em]">{text}</span>
    </span>
  );
}
