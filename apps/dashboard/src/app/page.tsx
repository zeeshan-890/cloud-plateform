"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert } from "@/components/ui/page";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError } from "@/lib/api";

export default function RootAuthPage() {
  const { user, loading, login, bootstrap } = useAuth();
  const router = useRouter();

  const [checkingBootstrap, setCheckingBootstrap] = useState(true);
  const [isInitialized, setIsInitialized] = useState(true);

  // Form states
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!loading && user) {
      router.replace("/projects");
    }
  }, [loading, user, router]);

  useEffect(() => {
    let active = true;
    async function checkStatus() {
      try {
        const res = await api.bootstrapStatus();
        if (active) {
          setIsInitialized(Boolean(res.initialized));
        }
      } catch {
        // Fallback: If status endpoint not available or error, assume initialized (regular login)
        if (active) setIsInitialized(true);
      } finally {
        if (active) setCheckingBootstrap(false);
      }
    }
    checkStatus();
    return () => {
      active = false;
    };
  }, []);

  async function handleBootstrap(e: FormEvent) {
    e.preventDefault();
    setError("");

    if (!username.trim()) {
      setError("Please enter a username.");
      return;
    }
    if (password.length < 8) {
      setError("Password must be at least 8 characters long.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords do not match.");
      return;
    }

    setSubmitting(true);
    try {
      await bootstrap(username.trim(), password);
      // Automatically create a default workspace organization
      try {
        await api.createOrg({ name: "Default Workspace", slug: "default-workspace" });
      } catch {
        // Non-critical if auto-created or already exists
      }
      router.replace("/projects");
    } catch (err) {
      // If already initialized, seamlessly fallback to login
      if (err instanceof ApiError && err.status === 403) {
        setIsInitialized(true);
        setError("Platform is already initialized. Please sign in.");
      } else {
        setError(err instanceof ApiError ? err.message : "Failed to set up administrator.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  async function handleLogin(e: FormEvent) {
    e.preventDefault();
    setError("");

    if (!username.trim() || !password) {
      setError("Username and password are required.");
      return;
    }

    setSubmitting(true);
    try {
      await login(username.trim(), password);
      router.replace("/projects");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Invalid username or password.");
    } finally {
      setSubmitting(false);
    }
  }

  if (loading || checkingBootstrap) {
    return (
      <div className="flex min-h-screen items-center justify-center hero-atmosphere">
        <div className="flex flex-col items-center gap-3">
          <BrandMark size="hero" href={null} />
          <p className="text-xs text-[var(--ink-muted)] animate-pulse">Loading console…</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col hero-atmosphere">
      <header className="px-6 py-5 sm:px-10">
        <BrandMark size="md" href="/" />
      </header>

      <main className="flex flex-1 items-start justify-center px-6 py-12 sm:items-center">
        <div className="w-full max-w-md animate-fade-up">
          <div className="mb-6">
            <h1 className="font-[family-name:var(--font-display)] text-3xl font-bold tracking-tight text-[var(--ink)]">
              {!isInitialized ? "Set up Administrator" : "Sign in"}
            </h1>
            <p className="mt-2 text-sm text-[var(--ink-muted)]">
              {!isInitialized
                ? "First-time setup: configure your master administrator credentials."
                : "Enter your username and password to access the platform."}
            </p>
          </div>

          {!isInitialized ? (
            <form onSubmit={handleBootstrap} className="flex flex-col gap-4">
              {error ? <Alert>{error}</Alert> : null}
              <Input
                label="Username"
                type="text"
                autoComplete="username"
                required
                autoFocus
                placeholder="admin"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
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
              <Input
                label="Confirm Password"
                type="password"
                autoComplete="new-password"
                required
                minLength={8}
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
              />
              <Button type="submit" disabled={submitting} className="mt-2 w-full py-2.5">
                {submitting ? "Setting up platform…" : "Set Up Administrator & Launch"}
              </Button>
            </form>
          ) : (
            <form onSubmit={handleLogin} className="flex flex-col gap-4">
              {error ? <Alert>{error}</Alert> : null}
              <Input
                label="Username"
                type="text"
                autoComplete="username"
                required
                autoFocus
                placeholder="admin"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
              <Input
                label="Password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <Button type="submit" disabled={submitting} className="mt-2 w-full py-2.5">
                {submitting ? "Signing in…" : "Sign in"}
              </Button>
            </form>
          )}

          <div className="mt-8 pt-4 border-t border-[var(--ink)]/10 flex items-center justify-between text-xs text-[var(--ink-muted)]">
            <span>JP Cloud Platform — Self-Hosted</span>
            <span>v1.0.0</span>
          </div>
        </div>
      </main>
    </div>
  );
}
