import { cn } from "@/lib/cn";

/* 7-segment geometry in a 16x28 cell. Segments a..g. */
const H = (cy: number) => `3,${cy} 5,${cy - 2} 11,${cy - 2} 13,${cy} 11,${cy + 2} 5,${cy + 2}`;
const V = (cx: number, y1: number, y2: number) =>
  `${cx},${y1} ${cx + 2},${y1 + 2} ${cx + 2},${y2 - 2} ${cx},${y2} ${cx - 2},${y2 - 2} ${cx - 2},${y1 + 2}`;

const SEG: Record<string, string> = {
  a: H(2),
  b: V(14, 3, 13),
  c: V(14, 15, 25),
  d: H(26),
  e: V(2, 15, 25),
  f: V(2, 3, 13),
  g: H(14),
};

const MAP: Record<string, string> = {
  "0": "abcdef",
  "1": "bc",
  "2": "abged",
  "3": "abgcd",
  "4": "fgbc",
  "5": "afgcd",
  "6": "afgedc",
  "7": "abc",
  "8": "abcdefg",
  "9": "abcdfg",
  "-": "g",
  " ": "",
};

export function LcdDigit({ ch }: { ch: string }) {
  const on = MAP[ch] ?? "";
  return (
    <svg className="lcd-seg" viewBox="0 0 16 28" width="16" height="28" aria-hidden="true">
      {Object.entries(SEG).map(([k, pts]) => (
        <polygon key={k} points={pts} className={on.includes(k) ? "on" : "off"} />
      ))}
    </svg>
  );
}

function LcdPunct({ ch }: { ch: string }) {
  if (ch === ".") {
    return (
      <svg className="lcd-seg" viewBox="0 0 6 28" width="6" height="28" aria-hidden="true">
        <circle cx="3" cy="25" r="2" className="on" />
      </svg>
    );
  }
  return (
    <svg className="lcd-seg" viewBox="0 0 6 28" width="6" height="28" aria-hidden="true">
      <circle cx="3" cy="9" r="1.8" className="on" />
      <circle cx="3" cy="19" r="1.8" className="on" />
    </svg>
  );
}

/**
 * Segmented LCD readout. `value` may contain digits, '-', '.', ':' and spaces;
 * anything else (e.g. 'n/a') falls back to a mono label inside the same well.
 */
export function LcdCounter({
  value,
  unit,
  label,
  width,
  className,
}: {
  value: string | number;
  unit?: string;
  label: string;
  /** pad with unlit cells to this many digits */
  width?: number;
  className?: string;
}) {
  const text = String(value);
  const renderable = /^[0-9\-. :]+$/.test(text);
  const chars = renderable ? text.split("") : [];
  const digitCount = chars.filter((c) => c !== "." && c !== ":").length;
  const pad = renderable && width ? Math.max(0, width - digitCount) : 0;
  return (
    <div className={cn("well well--lcd inline-flex min-w-0 flex-col gap-1", className)} role="group" aria-label={`${label}: ${text}${unit ? " " + unit : ""}`}>
      <span className="lcd-unit" aria-hidden="true">
        {label}
      </span>
      <div className="flex items-end gap-[3px] leading-none" aria-hidden="true">
        {renderable ? (
          <>
            {Array.from({ length: pad }).map((_, i) => (
              <LcdDigit key={`p${i}`} ch=" " />
            ))}
            {chars.map((c, i) => (c === "." || c === ":" ? <LcdPunct key={i} ch={c} /> : <LcdDigit key={i} ch={c} />))}
          </>
        ) : (
          <span className="mono text-[22px] font-medium leading-[28px]">{text}</span>
        )}
        {unit && <span className="lcd-unit ml-2 pb-[2px]">{unit}</span>}
      </div>
    </div>
  );
}
