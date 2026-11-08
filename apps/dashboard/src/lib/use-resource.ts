"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api/http";

export interface Resource<T> {
  data: T | undefined;
  error: ApiError | undefined;
  /** first load for this key (no data yet) */
  loading: boolean;
  /** background re-fetch in flight */
  refreshing: boolean;
  reload: () => void;
}

interface State<T> {
  key: string | null;
  data?: T;
  error?: ApiError;
  tick: number;
}

function toApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e;
  return new ApiError(0, "unknown", e instanceof Error ? e.message : "Unexpected error");
}

/**
 * Fetch-on-key hook. `key === null` disables it. Re-fetches when the key changes,
 * when `reload()` is called, or on `interval` while the tab is visible.
 * Stale data stays on screen during a reload so panels never flash empty.
 */
export function useResource<T>(
  key: string | null,
  fetcher: (signal: AbortSignal) => Promise<T>,
  opts: { interval?: number } = {},
): Resource<T> {
  const fetchRef = useRef(fetcher);
  useEffect(() => {
    fetchRef.current = fetcher;
  });
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<State<T>>({ key: null, tick: 0 });
  const interval = opts.interval;

  useEffect(() => {
    if (key === null) return;
    let alive = true;
    const ac = new AbortController();
    fetchRef
      .current(ac.signal)
      .then((data) => {
        if (alive) setState({ key, data, tick });
      })
      .catch((e: unknown) => {
        const err = toApiError(e);
        if (!alive || err.aborted) return;
        setState((s) => ({ key, data: s.key === key ? s.data : undefined, error: err, tick }));
      });
    return () => {
      alive = false;
      ac.abort();
    };
  }, [key, tick]);

  useEffect(() => {
    if (!interval || key === null) return;
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((t) => t + 1);
    }, interval);
    return () => window.clearInterval(id);
  }, [interval, key]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  const current = state.key === key && key !== null;
  return {
    data: current ? state.data : undefined,
    error: current ? state.error : undefined,
    loading: key !== null && !(current && (state.data !== undefined || state.error !== undefined)),
    refreshing: key !== null && current && state.tick !== tick,
    reload,
  };
}
