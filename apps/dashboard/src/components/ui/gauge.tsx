import { cn } from "@/lib/cn";

const CX = 100;
const CY = 100;

function polar(r: number, deg: number): [number, number] {
  const a = ((deg - 90) * Math.PI) / 180;
  return [CX + r * Math.cos(a), CY + r * Math.sin(a)];
}

function arc(r: number, from: number, to: number): string {
  const [x1, y1] = polar(r, from);
  const [x2, y2] = polar(r, to);
  const large = to - from > 180 ? 1 : 0;
  return `M${x1.toFixed(2)},${y1.toFixed(2)} A${r},${r} 0 ${large} 1 ${x2.toFixed(2)},${y2.toFixed(2)}`;
}

/** Analogue panel meter: 240 degree sweep, red zone in the last 20 %. */
export function Gauge({
  value,
  label,
  readout,
  className,
  size = 190,
}: {
  /** 0..100 */
  value: number;
  label: string;
  readout: string;
  className?: string;
  size?: number;
}) {
  const v = Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
  const sweep = -120 + (v / 100) * 240;
  const ticks = Array.from({ length: 51 }, (_, i) => i);
  return (
    <figure className={cn("m-0 inline-flex flex-col items-center gap-2", className)} aria-label={`${label}: ${readout}`}>
      <svg viewBox="0 0 200 200" width={size} height={size} role="img" aria-label={`${label} gauge at ${Math.round(v)} percent`}>
        <defs>
          <radialGradient id={`g-bezel-${label}`} cx="35%" cy="25%" r="90%">
            <stop offset="0" stopColor="#f4f1ea" />
            <stop offset="0.5" stopColor="#8d877a" />
            <stop offset="1" stopColor="#3b362e" />
          </radialGradient>
          <linearGradient id={`g-glass-${label}`} x1="0" y1="0" x2="0.6" y2="1">
            <stop offset="0" stopColor="#fff" stopOpacity="0.55" />
            <stop offset="0.45" stopColor="#fff" stopOpacity="0.06" />
            <stop offset="1" stopColor="#fff" stopOpacity="0" />
          </linearGradient>
        </defs>
        <circle cx={CX} cy={CY} r="96" fill={`url(#g-bezel-${label})`} />
        <circle cx={CX} cy={CY} r="86" className="gauge-face" stroke="rgba(0,0,0,0.5)" strokeWidth="1.5" />
        <path d={arc(74, 96, 120)} fill="none" stroke="#b3361f" strokeWidth="6" strokeLinecap="butt" opacity="0.85" />
        <path d={arc(74, -120, 96)} fill="none" stroke="#2c2518" strokeWidth="1" opacity="0.45" />
        {ticks.map((i) => {
          const deg = -120 + (i / 50) * 240;
          const major = i % 10 === 0;
          const [x1, y1] = polar(major ? 62 : 67, deg);
          const [x2, y2] = polar(74, deg);
          return <line key={i} x1={x1} y1={y1} x2={x2} y2={y2} stroke="#2c2518" strokeWidth={major ? 2 : 0.9} />;
        })}
        {[0, 25, 50, 75, 100].map((n) => {
          const [x, y] = polar(50, -120 + (n / 100) * 240);
          return (
            <text key={n} x={x} y={y + 3} textAnchor="middle" fontSize="9" fontFamily="var(--font-mono)" fill="#2c2518">
              {n}
            </text>
          );
        })}
        <text x={CX} y={CY + 38} textAnchor="middle" fontSize="11" fontFamily="var(--font-display)" fontWeight="700" letterSpacing="2" fill="#2c2518">
          {label.toUpperCase()}
        </text>
        <g className="gauge-needle" style={{ transform: `rotate(${sweep}deg)` }}>
          <polygon points="98.2,100 101.8,100 100.9,28 99.1,28" fill="#1b1712" />
          <polygon points="98.8,100 101.2,100 100.4,34 99.6,34" fill="#c4361c" />
          <polygon points="98.5,100 101.5,100 101,116 99,116" fill="#1b1712" />
        </g>
        <circle cx={CX} cy={CY} r="7" fill="#2d2923" stroke="#000" strokeWidth="0.8" />
        <circle cx={CX - 2} cy={CY - 2} r="2.2" fill="#8c867a" />
        <path d="M18 88A86 86 0 0 1 100 14 86 86 0 0 1 182 88Q100 60 18 88z" fill={`url(#g-glass-${label})`} opacity="0.8" />
      </svg>
      <figcaption className="mono text-[13px] font-medium">{readout}</figcaption>
    </figure>
  );
}
