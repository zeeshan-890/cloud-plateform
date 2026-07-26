"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert } from "@/components/ui/page";
import { useAuth } from "@/lib/auth-context";
import { ApiError } from "@/lib/api";

export default function RegisterPage() {
  const { register, user, loading } = useAuth();
  const router = useRouter();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!loading && user) router.replace("/projects");
  }, [loading, user, router]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      await register(name, email, password);
      router.replace("/orgs/new");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Unable to create account",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-screen flex-col hero-atmosphere">
      <header className="px-6 py-5 sm:px-10">
        <BrandMark size="md" />
      </header>
      <main className="flex flex-1 items-start justify-center px-6 py-12 sm:items-center">
        <div className="w-full max-w-md animate-fade-up">
          <h1 className="font-[family-name:var(--font-display)] text-3xl font-bold text-[var(--ink)]">
            Create account
          </h1>
          <p className="mt-2 text-sm text-[var(--ink-muted)]">
            Start with a free jp workspace.
          </p>
          <form onSubmit={onSubmit} className="mt-8 flex flex-col gap-4">
            {error ? <Alert>{error}</Alert> : null}
            <Input
              label="Name"
              type="text"
              autoComplete="name"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <Input
              label="Email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
            <Input
              label="Password"
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              hint="At least 8 characters"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button type="submit" disabled={submitting} className="mt-2 w-full">
              {submitting ? "Creating…" : "Create account"}
            </Button>
          </form>
          <p className="mt-6 text-sm text-[var(--ink-muted)]">
            Already have an account?{" "}
            <Link
              href="/login"
              className="font-medium text-[var(--accent-deep)] hover:underline"
            >
              Sign in
            </Link>
          </p>
        </div>
      </main>
    </div>
  );
}
