"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/cn";
import { Button } from "./button";
import { Input } from "./field";
import { Panel } from "./panel";
import { Icon } from "./icons";
import { errorMessage } from "@/lib/api/http";

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: React.ReactNode;
  children?: React.ReactNode;
  footer?: React.ReactNode;
  wide?: boolean;
  /** disable backdrop/Escape dismissal while a request runs */
  locked?: boolean;
}

/** Accessible modal: focus is trapped, Escape closes, focus returns to the opener. */
export function Modal({ open, onClose, title, description, children, footer, wide, locked }: ModalProps) {
  const ref = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const descId = useId();
  const lockedRef = useRef(locked);
  const closeRef = useRef(onClose);
  useEffect(() => {
    lockedRef.current = locked;
    closeRef.current = onClose;
  });

  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement as HTMLElement | null;
    const node = ref.current;
    const first = node?.querySelector<HTMLElement>("[data-autofocus]") ?? node?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? node)?.focus();
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !lockedRef.current) {
        e.stopPropagation();
        closeRef.current();
        return;
      }
      if (e.key !== "Tab" || !node) return;
      const items = Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.offsetParent !== null || el === document.activeElement);
      if (items.length === 0) {
        e.preventDefault();
        node.focus();
        return;
      }
      const firstEl = items[0];
      const lastEl = items[items.length - 1];
      if (e.shiftKey && (document.activeElement === firstEl || document.activeElement === node)) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && document.activeElement === lastEl) {
        e.preventDefault();
        firstEl.focus();
      }
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.body.style.overflow = prevOverflow;
      opener?.focus?.();
    };
  }, [open]);

  if (!open || typeof document === "undefined") return null;
  return createPortal(
    <div
      className="backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !locked) onClose();
      }}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={cn("dialog", wide && "dialog--wide")}
      >
        <Panel as="div" className="!p-0">
          <div className="px-7 pb-5 pt-7">
            <div className="mb-3 flex items-start gap-3">
              <h2 id={titleId} className="text-[22px]">
                {title}
              </h2>
              <button type="button" className="btn btn--ghost btn--sm btn--icon ml-auto" onClick={onClose} disabled={locked} aria-label="Close dialog">
                <Icon name="x" size={18} />
              </button>
            </div>
            {description && (
              <p id={descId} className="muted mb-4 text-[15px]">
                {description}
              </p>
            )}
            {children}
          </div>
          {footer && <div className="flex flex-wrap justify-end gap-3 border-t border-[var(--line)] px-7 py-4">{footer}</div>}
        </Panel>
      </div>
    </div>,
    document.body,
  );
}

interface ConfirmProps {
  open: boolean;
  title: string;
  body: React.ReactNode;
  confirmLabel: string;
  /** if set, the user must type this exact text before the action unlocks */
  requireText?: string;
  danger?: boolean;
  onConfirm: () => Promise<unknown> | unknown;
  onClose: () => void;
}

/** Destructive-action confirmation with optional type-to-confirm gate. */
export function ConfirmDialog({ open, title, body, confirmLabel, requireText, danger = true, onConfirm, onClose }: ConfirmProps) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const unlocked = !requireText || typed === requireText;

  const close = useCallback(() => {
    if (busy) return;
    setTyped("");
    setError("");
    onClose();
  }, [busy, onClose]);

  const run = async () => {
    if (!unlocked || busy) return;
    setBusy(true);
    setError("");
    try {
      await onConfirm();
      setTyped("");
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={close}
      title={title}
      locked={busy}
      footer={
        <>
          <Button variant="ghost" onClick={close} disabled={busy}>
            Cancel
          </Button>
          <Button variant={danger ? "danger" : "primary"} onClick={run} disabled={!unlocked} loading={busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="text-[15px]">{body}</div>
      {requireText && (
        <form
          className="mt-4"
          onSubmit={(e) => {
            e.preventDefault();
            void run();
          }}
        >
          <Input
            data-autofocus
            label={
              <>
                Type <span className="mono normal-case tracking-normal">{requireText}</span> to confirm
              </>
            }
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </form>
      )}
      {error && (
        <p role="alert" className="error-text mt-3">
          {error}
        </p>
      )}
    </Modal>
  );
}
