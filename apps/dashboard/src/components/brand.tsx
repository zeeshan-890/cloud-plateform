import { cn } from "@/lib/cn";

/** JP Cloud mark: a 1U rack unit with a status lamp. */
export function BrandMark({ size = 34 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 40 40" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id="bm-plate" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="var(--plate-1)" />
          <stop offset="1" stopColor="var(--plate-3)" />
        </linearGradient>
        <radialGradient id="bm-led" cx="35%" cy="30%" r="80%">
          <stop offset="0" stopColor="#fff" stopOpacity="0.9" />
          <stop offset="0.35" stopColor="var(--accent-hi)" />
          <stop offset="1" stopColor="var(--accent-lo)" />
        </radialGradient>
      </defs>
      <rect x="2" y="4" width="36" height="32" rx="6" fill="url(#bm-plate)" stroke="var(--plate-edge)" />
      <rect x="2.5" y="4.5" width="35" height="31" rx="5.5" fill="none" stroke="var(--plate-hi)" />
      <circle cx="7" cy="9" r="1.4" fill="var(--screw-2)" />
      <circle cx="33" cy="9" r="1.4" fill="var(--screw-2)" />
      <circle cx="7" cy="31" r="1.4" fill="var(--screw-2)" />
      <circle cx="33" cy="31" r="1.4" fill="var(--screw-2)" />
      <path d="M12 14h16M12 18h16M12 22h10" stroke="var(--ink)" strokeOpacity="0.55" strokeWidth="1.6" strokeLinecap="round" />
      <circle cx="27" cy="26" r="3.6" fill="url(#bm-led)" />
    </svg>
  );
}

export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2.5", className)}>
      <BrandMark />
      <span className="wordmark-text flex flex-col leading-none">
        <span className="text-[22px] font-bold uppercase tracking-[0.12em]" style={{ fontFamily: "var(--font-display)" }}>
          JP Cloud
        </span>
        <span className="label !text-[10.5px] !tracking-[0.3em]">Control room</span>
      </span>
    </span>
  );
}
