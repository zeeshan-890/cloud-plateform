"use client";

import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import { Lamp } from "./status";
import { errorMessage } from "@/lib/api/http";

type Kind = "success" | "error" | "info";
interface ToastItem {
  id: number;
  kind: Kind;
  title: string;
  message?: string;
  leaving?: boolean;
}

interface ToastApi {
  success: (title: string, message?: string) => void;
  error: (title: string, err?: unknown) => void;
  info: (title: string, message?: string) => void;
}

const Ctx = createContext<ToastApi | null>(null);

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);

  const dismiss = useCallback((id: number) => {
    setItems((l) => l.map((t) => (t.id === id ? { ...t, leaving: true } : t)));
    window.setTimeout(() => setItems((l) => l.filter((t) => t.id !== id)), 240);
  }, []);

  const push = useCallback(
    (kind: Kind, title: string, message?: string) => {
      const id = ++seq.current;
      setItems((l) => [...l.slice(-4), { id, kind, title, message }]);
      window.setTimeout(() => dismiss(id), kind === "error" ? 9000 : 5000);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (t, m) => push("success", t, m),
      error: (t, e) => push("error", t, e === undefined ? undefined : errorMessage(e)),
      info: (t, m) => push("info", t, m),
    }),
    [push],
  );

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="toast-stack" role="region" aria-label="Notifications">
        {items.map((t) => (
          <div key={t.id} className="toast" data-leaving={t.leaving ? "true" : undefined} role={t.kind === "error" ? "alert" : "status"}>
            <Lamp state={t.kind === "success" ? "ok" : t.kind === "error" ? "fail" : "info"} className="mt-[5px]" />
            <div className="min-w-0 flex-1">
              <p className="m-0 text-[15px] font-semibold leading-tight">{t.title}</p>
              {t.message && <p className="muted m-0 mt-1 break-words text-[13.5px]">{t.message}</p>}
            </div>
            <button type="button" className="btn btn--ghost btn--sm btn--icon -mr-2 -mt-1" aria-label="Dismiss notification" onClick={() => dismiss(t.id)}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                <path d="M6 6l12 12M18 6 6 18" />
              </svg>
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): ToastApi {
  const v = useContext(Ctx);
  if (!v) throw new Error("useToast must be used inside ToastProvider");
  return v;
}
