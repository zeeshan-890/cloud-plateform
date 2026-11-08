"use client";

import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/api/http";
import type { Resource } from "@/lib/use-resource";
import { Button } from "./button";
import { Lamp } from "./status";

export function Spinner({ size = 18, className }: { size?: number; className?: string }) {
  return (
    <svg className={cn("spin", className)} width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <div className={cn("skeleton", className)} style={style} aria-hidden="true" />;
}

export function SkeletonRows({ rows = 4, label = "Loading" }: { rows?: number; label?: string }) {
  return (
    <div className="flex flex-col gap-3" role="status" aria-label={label}>
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-10" style={{ width: `${100 - ((i * 13) % 35)}%` }} />
      ))}
      <span className="sr-only">{label}...</span>
    </div>
  );
}

/** A blanking plate: the empty slot of a rack unit. */
export function EmptyState({
  title,
  body,
  action,
  className,
}: {
  title: string;
  body?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("well flex flex-col items-center gap-3 px-6 py-10 text-center", className)}>
      <div aria-hidden="true" className="flex flex-col gap-[5px]">
        {[0, 1, 2].map((i) => (
          <span key={i} className="block h-[3px] w-14 rounded-full" style={{ background: "rgba(0,0,0,0.45)", boxShadow: "0 1px 0 var(--plate-hi)" }} />
        ))}
      </div>
      <h3 className="text-[20px]">{title}</h3>
      {body && <p className="m-0 max-w-md text-[15px] opacity-85">{body}</p>}
      {action && <div className="mt-1">{action}</div>}
    </div>
  );
}

/** Shows a clear "service unavailable" state for 502/503/504/network and a retry button. */
export function ErrorState({ error, onRetry, className }: { error: ApiError | Error; onRetry?: () => void; className?: string }) {
  const api = error instanceof ApiError ? error : undefined;
  const unavailable = api?.unavailable ?? false;
  const forbidden = api?.forbidden ?? false;
  const title = unavailable ? "Service unavailable" : forbidden ? "Not permitted" : "Could not load this";
  const body = unavailable
    ? "The backend service behind this panel is not responding. Nothing was changed. Try again in a moment."
    : error.message;
  return (
    <div className={cn("well flex flex-col items-center gap-3 px-6 py-9 text-center", className)} role="alert">
      <Lamp state={forbidden ? "warn" : "fail"} large />
      <h3 className="text-[20px]">{title}</h3>
      <p className="m-0 max-w-md text-[15px] opacity-90">{body}</p>
      {api && api.status > 0 && !forbidden && (
        <p className="mono m-0 text-[12px] opacity-60">
          HTTP {api.status} {api.code}
        </p>
      )}
      {onRetry && !forbidden && (
        <Button onClick={onRetry} className="mt-1">
          Retry
        </Button>
      )}
    </div>
  );
}

/**
 * Standard async panel body: skeleton while loading, error state with retry,
 * empty state, otherwise the render prop with the data.
 */
export function Loaded<T>({
  res,
  children,
  isEmpty,
  empty,
  rows = 3,
  label = "Loading",
}: {
  res: Resource<T>;
  children: (data: T) => React.ReactNode;
  isEmpty?: (data: T) => boolean;
  empty?: React.ReactNode;
  rows?: number;
  label?: string;
}) {
  if (res.error && res.data === undefined) return <ErrorState error={res.error} onRetry={res.reload} />;
  if (res.loading || res.data === undefined) return <SkeletonRows rows={rows} label={label} />;
  if (isEmpty && isEmpty(res.data) && empty) return <>{empty}</>;
  return <>{children(res.data)}</>;
}
