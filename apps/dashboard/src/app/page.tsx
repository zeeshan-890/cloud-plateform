"use client";

import { FormEvent, Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert } from "@/components/ui/page";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError } from "@/lib/api";

function AuthForm() {
  const { user, loading, login, bootstrap } = useAuth();
  const router = useRouter();
  const searchParams = useSearchParams();
  const expired = searchParams.get("expired") === "1";

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
      try {
        await api.createOrg({ name: "Default Workspace", slug: "default-workspace" });
      } catch {
        // Non-critical if auto-created
      }
      router.replace("/projects");
    } catch (err) {
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
      <div className="flex min-h-screen items-center justify-center bg-[#D6DEEB]">
        <div className="flex flex-col items-center gap-3">
          <BrandMark size="hero" href={null} />
          <p className="text-xs font-bold text-slate-500 animate-pulse">Loading console…</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-[#D6DEEB]">
      <header className="px-6 py-5 sm:px-10 flex items-center justify-between">
        <BrandMark size="md" href="/" />
        <span className="text-xs font-bold text-slate-600 bg-white/80 border-2 border-slate-300 rounded-lg px-2.5 py-1 shadow-2xs">
          Cloud Console
        </span>
      </header>

      <main className="flex flex-1 items-center justify-center px-4 py-8">
        <div className="w-full max-w-md animate-fade-up">
          <div className="bg-white border-2 border-slate-300 rounded-2xl shadow-sm p-6 sm:p-8">
            <div className="mb-6">
              <h1 className="text-2xl font-extrabold tracking-tight text-[#0F1B3D]">
                {!isInitialized ? "Set up Administrator" : "Sign in to Platform"}
              </h1>
              <p className="mt-1.5 text-xs font-medium text-slate-500">
                {!isInitialized
                  ? "First-time setup: configure your master administrator credentials."
                  : "Enter your master credentials to access the deployment console."}
              </p>
            </div>

            {expired ? (
              <div className="mb-4">
                <Alert tone="warning">
                  Your session has expired. Please sign in again.
                </Alert>
              </div>
            ) : null}

            {!isInitialized ? (
              <form onSubmit={handleBootstrap} className="flex flex-col gap-4">
                {error ? <Alert tone="error">{error}</Alert> : null}
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
                {error ? <Alert tone="error">{error}</Alert> : null}
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
                  {submitting ? "Signing in…" : "Sign in to Console"}
                </Button>
              </form>
            )}

            <div className="mt-6 pt-4 border-t-2 border-slate-100 flex items-center justify-between text-[11px] font-semibold text-slate-500">
              <span>Cloud Platform</span>
              <span>Self-Hosted • v1.0.0</span>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

export default function RootAuthPage() {
  return (
    <Suspense fallback={
      <div className="flex min-h-screen items-center justify-center bg-[#D6DEEB]">
        <div className="flex flex-col items-center gap-3">
          <BrandMark size="hero" href={null} />
          <p className="text-xs font-bold text-slate-500 animate-pulse">Loading console…</p>
        </div>
      </div>
    }>
      <AuthForm />
    </Suspense>
  );
}
