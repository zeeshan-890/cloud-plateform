import { cn } from "@/lib/cn";

export interface Point {
  t: number; // epoch ms
  v: number;
}

const W = 640;
const H = 200;
const PAD = { l: 48, r: 16, t: 14, b: 26 };

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const n = v / p;
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return step * p;
}

/** Strip-chart recorder: graph paper, ink trace and a pen at the latest sample. All data is real input. */
export function StripChart({
  points,
  label,
  unit,
  className,
  format = (n: number) => String(Math.round(n * 100) / 100),
  max,
}: {
  points: Point[];
  label: string;
  unit?: string;
  className?: string;
  format?: (n: number) => string;
  max?: number;
}) {
  const sorted = [...points].sort((a, b) => a.t - b.t);
  const innerW = W - PAD.l - PAD.r;
  const innerH = H - PAD.t - PAD.b;
  const hasData = sorted.length > 0;
  const yMax = max ?? niceMax(Math.max(...sorted.map((p) => p.v), 0));
  const t0 = hasData ? sorted[0].t : 0;
  const t1 = hasData ? sorted[sorted.length - 1].t : 1;
  const span = Math.max(1, t1 - t0);
  const x = (t: number) => PAD.l + (sorted.length === 1 ? innerW / 2 : ((t - t0) / span) * innerW);
  const y = (v: number) => PAD.t + innerH - (v / yMax) * innerH;
  const path = sorted.map((p, i) => `${i === 0 ? "M" : "L"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(" ");
  const last = sorted[sorted.length - 1];
  const hh = (t: number) => new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

  return (
    <figure className={cn("recorder-paper m-0 p-2", className)} style={{ color: "#2a2216" }} >
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" role="img" aria-label={`${label} chart${hasData ? `, latest ${format(last.v)}${unit ? " " + unit : ""}` : ", no data"}`}>
        {Array.from({ length: Math.floor(innerW / 10) + 1 }, (_, i) => (
          <line key={`v${i}`} x1={PAD.l + i * 10} x2={PAD.l + i * 10} y1={PAD.t} y2={PAD.t + innerH} stroke="rgba(150,60,40,0.16)" strokeWidth={i % 5 === 0 ? 0.9 : 0.4} />
        ))}
        {Array.from({ length: Math.floor(innerH / 10) + 1 }, (_, i) => (
          <line key={`h${i}`} x1={PAD.l} x2={PAD.l + innerW} y1={PAD.t + i * 10} y2={PAD.t + i * 10} stroke="rgba(150,60,40,0.16)" strokeWidth={i % 5 === 0 ? 0.9 : 0.4} />
        ))}
        {[0, 0.5, 1].map((f) => (
          <text key={f} x={PAD.l - 6} y={y(yMax * f) + 3.5} textAnchor="end" fontSize="10.5" fontFamily="var(--font-mono)" fill="currentColor">
            {format(yMax * f)}
          </text>
        ))}
        {hasData && (
          <>
            <text x={PAD.l} y={H - 8} fontSize="10.5" fontFamily="var(--font-mono)" fill="currentColor">
              {hh(t0)}
            </text>
            <text x={PAD.l + innerW} y={H - 8} textAnchor="end" fontSize="10.5" fontFamily="var(--font-mono)" fill="currentColor">
              {hh(t1)}
            </text>
          </>
        )}
        {hasData ? (
          <>
            <path d={path} fill="none" stroke="#1d3557" strokeWidth="1.8" strokeLinejoin="round" strokeLinecap="round" className="recorder-trace" />
            {sorted.length > 1 && (
              <path d={`${path} L${x(last.t).toFixed(1)},${PAD.t + innerH} L${x(sorted[0].t).toFixed(1)},${PAD.t + innerH} Z`} fill="rgba(29,53,87,0.09)" />
            )}
            <polygon points={`${x(last.t) + 2},${y(last.v)} ${x(last.t) + 11},${y(last.v) - 5} ${x(last.t) + 11},${y(last.v) + 5}`} fill="#b3361f" />
            <circle cx={x(last.t)} cy={y(last.v)} r="3" fill="#b3361f" />
          </>
        ) : (
          <text x={W / 2} y={H / 2} textAnchor="middle" fontSize="13" fontFamily="var(--font-mono)" fill="currentColor" opacity="0.7">
            no samples recorded yet
          </text>
        )}
      </svg>
      <figcaption className="mono flex justify-between px-1 text-[12px]">
        <span>{label}</span>
        <span>{hasData ? `${format(last.v)}${unit ? " " + unit : ""} - ${sorted.length} samples` : "-"}</span>
      </figcaption>
    </figure>
  );
}
