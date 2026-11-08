export function bytes(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}

export function relTime(iso: string | undefined | null, now: number = Date.now()): string {
  if (!iso) return "-";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "-";
  const s = Math.round((now - t) / 1000);
  const abs = Math.abs(s);
  const suffix = s >= 0 ? "ago" : "from now";
  if (abs < 10) return "just now";
  if (abs < 60) return `${abs}s ${suffix}`;
  if (abs < 3600) return `${Math.round(abs / 60)}m ${suffix}`;
  if (abs < 86400) return `${Math.round(abs / 3600)}h ${suffix}`;
  if (abs < 86400 * 45) return `${Math.round(abs / 86400)}d ${suffix}`;
  return new Date(iso).toLocaleDateString();
}

export function dateTime(iso: string | undefined | null): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function duration(seconds: number | undefined | null): string {
  if (seconds === undefined || seconds === null) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function shortSha(sha: string | undefined): string {
  return sha ? sha.slice(0, 7) : "-";
}

export function pct(used: number | undefined, total: number | undefined): number {
  if (!used || !total) return 0;
  return Math.min(100, Math.max(0, (used / total) * 100));
}

export type LampState = "ok" | "warn" | "fail" | "info" | "idle";

const OK = ["running", "ready", "active", "verified", "succeeded", "success", "healthy", "issued", "pass", "enabled", "live", "routed"];
const BUSY = ["pending", "building", "queued", "provisioning", "starting", "deploying", "creating", "verifying", "unverified", "issuing", "restarting", "stopping"];
const BAD = ["failed", "error", "unhealthy", "dead", "exited", "rejected", "fail", "expired", "timeout", "missing", "canceled", "cancelled"];
const OFF = ["stopped", "inactive", "paused", "created", "none", "unknown", "disabled", "superseded", "rolled_back"];

/** Map free-form backend statuses to a lamp state. */
export function statusLamp(status: string | undefined): { state: LampState; pulse: boolean } {
  const s = (status || "").toLowerCase();
  if (OK.includes(s)) return { state: "ok", pulse: false };
  if (BUSY.includes(s)) return { state: "warn", pulse: true };
  if (BAD.includes(s)) return { state: "fail", pulse: false };
  if (OFF.includes(s)) return { state: "idle", pulse: false };
  return { state: "info", pulse: false };
}

export function isTransient(status: string | undefined): boolean {
  return statusLamp(status).pulse;
}
