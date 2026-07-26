import Link from "next/link";

export function BrandMark({
  size = "md",
  href = "/",
}: {
  size?: "sm" | "md" | "lg" | "hero";
  href?: string | null;
}) {
  const sizes = {
    sm: "text-xl tracking-tight",
    md: "text-2xl tracking-tight",
    lg: "text-4xl tracking-tighter",
    hero: "text-7xl sm:text-8xl md:text-9xl tracking-tighter",
  };

  const content = (
    <span
      className={`font-[family-name:var(--font-display)] font-bold text-[var(--ink)] ${sizes[size]}`}
    >
      jp
    </span>
  );

  if (href === null) return content;
  return (
    <Link href={href} className="inline-flex items-baseline gap-0.5 group">
      {content}
      <span
        className="ml-0.5 inline-block h-1.5 w-1.5 translate-y-[-0.35em] rounded-full bg-[var(--accent)] transition-transform duration-300 group-hover:scale-125"
        aria-hidden
      />
    </Link>
  );
}
