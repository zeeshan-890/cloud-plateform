"use client";

import { useCallback, useSyncExternalStore } from "react";

const listeners = new Map<string, Set<() => void>>();
const memory = new Map<string, string | null>();

function emit(key: string) {
  listeners.get(key)?.forEach((f) => f());
}

export function readStored(key: string): string | null {
  if (memory.has(key)) return memory.get(key) ?? null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStored(key: string, value: string | null) {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
    memory.delete(key);
  } catch {
    // storage blocked: the value lives for this page view only
    memory.set(key, value);
  }
  emit(key);
}

/** localStorage-backed value that is SSR-safe and syncs across components and tabs. */
export function useStoredValue(key: string): [string | null, (v: string | null) => void] {
  const subscribe = useCallback(
    (cb: () => void) => {
      let set = listeners.get(key);
      if (!set) {
        set = new Set();
        listeners.set(key, set);
      }
      const owned = set;
      owned.add(cb);
      const onStorage = (e: StorageEvent) => {
        if (e.key === key) cb();
      };
      window.addEventListener("storage", onStorage);
      return () => {
        owned.delete(cb);
        window.removeEventListener("storage", onStorage);
      };
    },
    [key],
  );
  const value = useSyncExternalStore(
    subscribe,
    () => readStored(key),
    () => null,
  );
  const set = useCallback((v: string | null) => writeStored(key, v), [key]);
  return [value, set];
}
