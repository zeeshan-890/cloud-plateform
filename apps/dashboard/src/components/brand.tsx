import Link from "next/link";

export function BrandMark({
  size = "md",
  href = "/projects",
  showBadge = true,
}: {
  size?: "sm" | "md" | "lg" | "hero";
  href?: string | null;
  showBadge?: boolean;
}) {
  const iconSizes = {
    sm: "w-5 h-5",
    md: "w-6 h-6",
    lg: "w-9 h-9",
    hero: "w-14 h-14",
  };

  const textSizes = {
    sm: "text-base tracking-tight",
    md: "text-lg tracking-tight",
    lg: "text-2xl tracking-tight",
    hero: "text-4xl tracking-tight",
  };

  const logoIcon = (
    <div
      className={`${iconSizes[size]} relative flex items-center justify-center rounded-lg bg-gradient-to-br from-slate-900 to-slate-950 p-1 shadow-sm ring-1 ring-slate-800 shrink-0`}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        className="w-full h-full"
      >
        <path
          d="M6.5 17h11a3.5 3.5 0 0 0 3.2-2.3 4 4 0 0 0-2.8-5.1 5.5 5.5 0 0 0-10.4 1A4 4 0 0 0 6.5 17z"
          fill="url(#brandCloudGrad)"
        />
        <circle cx="12" cy="14" r="1.8" fill="#ffffff" />
        <circle cx="12" cy="14" r="0.9" fill="#0284c7" />
        <defs>
          <linearGradient id="brandCloudGrad" x1="4" y1="6" x2="20" y2="18" gradientUnits="userSpaceOnUse">
            <stop stopColor="#38bdf8" />
            <stop offset="1" stopColor="#2563eb" />
          </linearGradient>
        </defs>
      </svg>
      <span className="absolute -top-0.5 -right-0.5 flex h-2 w-2">
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
        <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
      </span>
    </div>
  );

  const content = (
    <div className="flex items-center gap-2.5">
      {logoIcon}
      <div className="flex items-baseline gap-1.5">
        <span
          className={`font-[family-name:var(--font-display)] font-extrabold text-[var(--ink)] ${textSizes[size]}`}
        >
          JP<span className="text-[var(--accent)]">.</span>CLOUD
        </span>
        {showBadge && size !== "sm" ? (
          <span className="hidden sm:inline-block rounded-full bg-slate-100 px-2 py-0.5 text-[9px] font-semibold uppercase tracking-wider text-slate-600 ring-1 ring-slate-200">
            Self-Hosted
          </span>
        ) : null}
      </div>
    </div>
  );

  if (href === null) return content;
  return (
    <Link href={href} className="inline-flex items-center group transition-opacity hover:opacity-95">
      {content}
    </Link>
  );
}
