import Link from "next/link";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";

export default function LandingPage() {
  return (
    <div className="relative flex min-h-screen flex-col overflow-hidden hero-atmosphere">
      <header className="relative z-10 flex items-center justify-between px-6 py-5 sm:px-10">
        <BrandMark size="md" href="/" />
        <nav className="flex items-center gap-2">
          <Link
            href="/login"
            className="rounded-md px-3 py-2 text-sm text-[var(--ink-muted)] transition-colors hover:text-[var(--ink)]"
          >
            Sign in
          </Link>
          <Link href="/register">
            <Button>Get started</Button>
          </Link>
        </nav>
      </header>

      <main className="relative z-10 flex flex-1 flex-col justify-center px-6 pb-24 pt-10 sm:px-10 lg:px-16">
        <BrandMark size="hero" href={null} />
        <h1 className="animate-fade-up mt-6 max-w-2xl font-[family-name:var(--font-display)] text-3xl font-bold tracking-tight text-[var(--ink)] sm:text-4xl lg:text-5xl">
          Ship infrastructure without the ceremony.
        </h1>
        <p className="animate-fade-up-delay mt-4 max-w-lg text-base text-[var(--ink-muted)] sm:text-lg">
          Projects, teams, and keys in one sharp console — builds and deploys
          next.
        </p>
        <div className="animate-fade-up-delay-2 mt-8 flex flex-wrap gap-3">
          <Link href="/register">
            <Button className="px-6 py-3">Create account</Button>
          </Link>
          <Link href="/login">
            <Button variant="secondary" className="px-6 py-3">
              Sign in
            </Button>
          </Link>
        </div>
      </main>

      <div
        className="pointer-events-none absolute inset-y-0 right-0 w-1/2 max-w-xl opacity-40"
        aria-hidden
      >
        <div className="absolute inset-0 bg-gradient-to-l from-[var(--accent)]/20 via-transparent to-transparent" />
        <div className="absolute bottom-16 right-8 h-64 w-64 rounded-full border border-[var(--ink)]/10" />
        <div className="absolute bottom-28 right-24 h-40 w-40 rounded-full border border-[var(--accent)]/40" />
      </div>
    </div>
  );
}
