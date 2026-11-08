"use client";

import { useMemo } from "react";
import { encodeQr } from "@/lib/qr";

/** Always black on white with a 4-module quiet zone so phone scanners work in both themes. */
export function QrCode({ text, size = 200, label }: { text: string; size?: number; label: string }) {
  const matrix = useMemo(() => {
    try {
      return encodeQr(text);
    } catch {
      return null;
    }
  }, [text]);
  if (!matrix) return <p className="error-text">This value is too long to draw as a QR code. Use the secret key instead.</p>;
  const n = matrix.length;
  const quiet = 4;
  const total = n + quiet * 2;
  let d = "";
  matrix.forEach((row, y) =>
    row.forEach((v, x) => {
      if (v) d += `M${x + quiet},${y + quiet}h1v1h-1z`;
    }),
  );
  return (
    <svg viewBox={`0 0 ${total} ${total}`} width={size} height={size} role="img" aria-label={label} shapeRendering="crispEdges" style={{ borderRadius: 6, boxShadow: "0 0 0 3px rgba(0,0,0,0.25)" }}>
      <rect width={total} height={total} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  );
}
