"use client";

import { cloneElement, isValidElement, useId, useState } from "react";
import { createPortal } from "react-dom";

interface Pos {
  x: number;
  y: number;
}

/** Hover/focus tooltip. Child gets aria-describedby; works with keyboard focus. */
export function Tooltip({ label, children }: { label: string; children: React.ReactElement<Record<string, unknown>> }) {
  const id = useId();
  const [pos, setPos] = useState<Pos | null>(null);
  if (!isValidElement(children)) return children;

  const show = (el: Element) => {
    const r = el.getBoundingClientRect();
    setPos({ x: Math.min(Math.max(r.left + r.width / 2, 130), window.innerWidth - 130), y: r.bottom + 8 });
  };
  const hide = () => setPos(null);
  const orig = children.props as {
    onMouseEnter?: (e: React.MouseEvent) => void;
    onMouseLeave?: (e: React.MouseEvent) => void;
    onFocus?: (e: React.FocusEvent) => void;
    onBlur?: (e: React.FocusEvent) => void;
    onKeyDown?: (e: React.KeyboardEvent) => void;
  };
  return (
    <>
      {cloneElement(children, {
        "aria-describedby": pos ? id : undefined,
        onMouseEnter: (e: React.MouseEvent) => {
          orig.onMouseEnter?.(e);
          show(e.currentTarget);
        },
        onMouseLeave: (e: React.MouseEvent) => {
          orig.onMouseLeave?.(e);
          hide();
        },
        onFocus: (e: React.FocusEvent) => {
          orig.onFocus?.(e);
          show(e.currentTarget);
        },
        onBlur: (e: React.FocusEvent) => {
          orig.onBlur?.(e);
          hide();
        },
        onKeyDown: (e: React.KeyboardEvent) => {
          orig.onKeyDown?.(e);
          if (e.key === "Escape") hide();
        },
      })}
      {pos &&
        createPortal(
          <div id={id} role="tooltip" className="tip" style={{ left: pos.x, top: pos.y, transform: "translateX(-50%)" }}>
            {label}
          </div>,
          document.body,
        )}
    </>
  );
}
