/**
 * Single fetch wrapper for the JP Cloud gateway.
 *  - bearer auth from the token store
 *  - 401 -> single-flight refresh -> retry once
 *  - AbortController support (pass `signal`)
 *  - typed ApiError {status, code, message}
 */

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** parsed JSON body of the failed response, when there was one */
  readonly body?: unknown;

  constructor(status: number, code: string, message: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }

  /** Upstream service is down / unreachable (gateway 502/503/504 or network failure). */
  get unavailable(): boolean {
    return this.status === 0 || this.status === 502 || this.status === 503 || this.status === 504;
  }
  get forbidden(): boolean {
    return this.status === 403;
  }
  get notFound(): boolean {
    return this.status === 404;
  }
  get aborted(): boolean {
    return this.code === "aborted";
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return "Something went wrong";
}

export interface TokenStore {
  getAccess(): string | null;
  getRefresh(): string | null;
  set(access: string, refresh: string): void;
  clear(): void;
}

const ACCESS_KEY = "jp_access_token";
const REFRESH_KEY = "jp_refresh_token";

function safeStorage(): Storage | null {
  try {
    return typeof window !== "undefined" ? window.localStorage : null;
  } catch {
    return null;
  }
}

export const localTokenStore: TokenStore = {
  getAccess: () => {
    try {
      return safeStorage()?.getItem(ACCESS_KEY) ?? null;
    } catch {
      return null;
    }
  },
  getRefresh: () => {
    try {
      return safeStorage()?.getItem(REFRESH_KEY) ?? null;
    } catch {
      return null;
    }
  },
  set: (a, r) => {
    try {
      const s = safeStorage();
      s?.setItem(ACCESS_KEY, a);
      s?.setItem(REFRESH_KEY, r);
    } catch {
      /* storage unavailable (private mode) — session lasts until reload */
    }
  },
  clear: () => {
    try {
      const s = safeStorage();
      s?.removeItem(ACCESS_KEY);
      s?.removeItem(REFRESH_KEY);
    } catch {
      /* ignore */
    }
  },
};

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  /** raw body (streaming uploads); bypasses JSON encoding */
  rawBody?: BodyInit;
  headers?: Record<string, string>;
  query?: Record<string, string | number | boolean | undefined | null>;
  signal?: AbortSignal;
  /** send without bearer token (login, bootstrap...) */
  anon?: boolean;
}

export interface ClientConfig {
  baseUrl: string;
  tokens: TokenStore;
  fetchImpl?: typeof fetch;
  /** called when the session can no longer be refreshed */
  onSessionExpired?: () => void;
}

interface ErrorEnvelope {
  error?: string | { code?: string; message?: string };
  message?: string;
  detail?: string;
}

export function parseError(status: number, data: unknown): ApiError {
  const env = (data ?? {}) as ErrorEnvelope;
  if (env.error && typeof env.error === "object") {
    return new ApiError(status, env.error.code || codeFor(status), env.error.message || `Request failed (${status})`, data);
  }
  if (typeof env.error === "string" && env.error) return new ApiError(status, codeFor(status), env.error, data);
  return new ApiError(status, codeFor(status), env.message || env.detail || defaultMessage(status), data);
}

function codeFor(status: number): string {
  if (status === 401) return "unauthorized";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status === 429) return "rate_limited";
  if (status >= 502 && status <= 504) return "unavailable";
  return status >= 500 ? "internal" : "request_failed";
}

function defaultMessage(status: number): string {
  if (status >= 502 && status <= 504) return "A backend service is unavailable right now.";
  if (status === 403) return "You do not have permission to do that.";
  if (status === 404) return "Not found.";
  if (status === 429) return "Too many requests. Try again in a moment.";
  return `Request failed (${status})`;
}

export function createClient(cfg: ClientConfig) {
  const doFetch: typeof fetch = (...a) => (cfg.fetchImpl ?? fetch)(...a);
  let refreshing: Promise<boolean> | null = null;

  function url(path: string, query?: RequestOptions["query"]): string {
    let u = `${cfg.baseUrl}${path}`;
    if (query) {
      const qs = new URLSearchParams();
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined && v !== null && v !== "") qs.set(k, String(v));
      }
      const s = qs.toString();
      if (s) u += `?${s}`;
    }
    return u;
  }

  async function refreshOnce(): Promise<boolean> {
    const refresh = cfg.tokens.getRefresh();
    if (!refresh) return false;
    try {
      const res = await doFetch(url("/auth/refresh"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refresh }),
      });
      if (!res.ok) {
        // Only a definitive rejection ends the session; a flaky gateway must not log the user out.
        if (res.status === 401 || res.status === 400 || res.status === 403) cfg.tokens.clear();
        return false;
      }
      const data = (await res.json()) as { access_token?: string; refresh_token?: string };
      if (!data.access_token) return false;
      cfg.tokens.set(data.access_token, data.refresh_token || refresh);
      return true;
    } catch {
      return false;
    }
  }

  /** Concurrent callers share one in-flight refresh. */
  function refreshSingleFlight(): Promise<boolean> {
    if (!refreshing) {
      refreshing = refreshOnce().finally(() => {
        refreshing = null;
      });
    }
    return refreshing;
  }

  async function raw(path: string, opts: RequestOptions, retried: boolean): Promise<Response> {
    const headers = new Headers(opts.headers);
    let body: BodyInit | undefined;
    if (opts.rawBody !== undefined) body = opts.rawBody;
    else if (opts.body !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(opts.body);
    }
    if (!opts.anon) {
      const t = cfg.tokens.getAccess();
      if (t) headers.set("Authorization", `Bearer ${t}`);
    }
    let res: Response;
    try {
      res = await doFetch(url(path, opts.query), {
        method: opts.method ?? "GET",
        headers,
        body,
        signal: opts.signal,
        ...(opts.rawBody !== undefined ? ({ duplex: "half" } as object) : {}),
      });
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") throw new ApiError(0, "aborted", "Request cancelled");
      throw new ApiError(0, "network", "Cannot reach the JP Cloud gateway. Check your connection.");
    }
    if (res.status === 401 && !opts.anon && !retried) {
      const ok = await refreshSingleFlight();
      if (ok) return raw(path, opts, true);
      cfg.onSessionExpired?.();
    }
    return res;
  }

  async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    const res = await raw(path, opts, false);
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    let data: unknown = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { message: res.ok ? text : text.slice(0, 200) };
      }
    }
    if (!res.ok) throw parseError(res.status, data);
    return data as T;
  }

  /** Authenticated fetch returning the raw Response (downloads). Throws ApiError on non-2xx. */
  async function blob(path: string, opts: RequestOptions = {}): Promise<Response> {
    const res = await raw(path, opts, false);
    if (!res.ok) {
      let data: unknown = null;
      try {
        data = await res.json();
      } catch {
        /* not json */
      }
      throw parseError(res.status, data);
    }
    return res;
  }

  return { request, blob, baseUrl: cfg.baseUrl, refreshSingleFlight };
}

export type Client = ReturnType<typeof createClient>;

type SessionExpiredListener = () => void;
const expiredListeners = new Set<SessionExpiredListener>();
export function onSessionExpired(fn: SessionExpiredListener): () => void {
  expiredListeners.add(fn);
  return () => {
    expiredListeners.delete(fn);
  };
}

const API_BASE = (process.env.NEXT_PUBLIC_API_URL || "/api/v1").replace(/\/$/, "");

export const client = createClient({
  baseUrl: API_BASE,
  tokens: localTokenStore,
  onSessionExpired: () => expiredListeners.forEach((f) => f()),
});

/** Pick a list out of `[...]` or `{ key: [...] }` responses. */
export function listOf<T>(data: unknown, key: string): T[] {
  if (Array.isArray(data)) return data as T[];
  if (data && typeof data === "object") {
    const v = (data as Record<string, unknown>)[key];
    if (Array.isArray(v)) return v as T[];
  }
  return [];
}

/** Pick an entity out of `{ key: {...} }` or the bare object. */
export function entityOf<T>(data: unknown, key: string): T {
  if (data && typeof data === "object" && key in (data as object)) {
    return (data as Record<string, T>)[key];
  }
  return data as T;
}

export function slugify(input: string): string {
  const base = input
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  return base || `item-${Date.now().toString(36)}`;
}
