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

function AppNotDeployedPage({ hostname }: { hostname: string }) {
  const parts = hostname.split(".");
  const appSlug = parts[0] || "application";
  const appTitle = appSlug.replace(/-/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
  const rootDomain = parts.length > 2 ? parts.slice(1).join(".") : hostname;
  const consoleUrl = `https://panel.${rootDomain}`;

  return (
    <div className="flex min-h-screen flex-col bg-[#D6DEEB] font-[family-name:var(--font-body)]">
      <header className="flex h-16 items-center justify-between border-b-2 border-slate-300 bg-white/95 px-6 lg:px-12 backdrop-blur-md">
        <BrandMark size="md" href={consoleUrl} />
        <a
          href={consoleUrl}
          className="inline-flex items-center gap-1.5 rounded-lg bg-[#1E3A8A] hover:bg-[#162456] px-4 py-2 text-xs font-bold text-white shadow-sm transition-all"
        >
          <span>Cloud Console</span>
          <span>↗</span>
        </a>
      </header>

      <main className="flex flex-1 items-center justify-center p-6">
        <div className="w-full max-w-lg animate-fade-up">
          <div className="bg-white rounded-2xl border-2 border-slate-300 p-8 shadow-md space-y-6 text-center">
            {/* Status Icon */}
            <div className="mx-auto flex h-16 w-16 items-center justify-center rounded-2xl bg-amber-50 border-2 border-amber-300 text-3xl shadow-xs">
              🚀
            </div>

            <div className="space-y-2">
              <div className="inline-flex items-center gap-1.5 rounded-full border-2 border-amber-300 bg-amber-50 px-3 py-1 text-xs font-bold text-amber-900">
                <span className="h-2 w-2 rounded-full bg-amber-500 animate-pulse" />
                Application Offline / Not Yet Deployed
              </div>
              <h1 className="text-2xl font-black text-[#0F1B3D] tracking-tight">
                {appTitle}
              </h1>
              <p className="text-xs text-slate-500 font-medium leading-relaxed max-w-sm mx-auto">
                This domain is active and registered on JP Cloud, but no container instance is currently serving traffic.
              </p>
            </div>

            {/* Diagnostic Information */}
            <div className="rounded-xl border-2 border-slate-200 bg-slate-50 p-4 text-left space-y-2 text-xs">
              <div className="flex items-center justify-between py-1 border-b border-slate-200/60">
                <span className="text-slate-500 font-medium">Domain:</span>
                <span className="font-mono font-bold text-slate-800">{hostname}</span>
              </div>
              <div className="flex items-center justify-between py-1 border-b border-slate-200/60">
                <span className="text-slate-500 font-medium">SSL / TLS Certificate:</span>
                <span className="font-bold text-emerald-700">✓ Let&apos;s Encrypt Active</span>
              </div>
              <div className="flex items-center justify-between py-1 border-b border-slate-200/60">
                <span className="text-slate-500 font-medium">Routing Ingress:</span>
                <span className="font-bold text-slate-800">Traefik Edge Gateway</span>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-slate-500 font-medium">Container State:</span>
                <span className="font-bold text-amber-700">Awaiting Build / Deployment</span>
              </div>
            </div>

            {/* Next Steps for Developer */}
            <div className="pt-2 space-y-3">
              <a
                href={consoleUrl}
                className="w-full inline-flex items-center justify-center gap-2 rounded-xl bg-[#1E3A8A] hover:bg-[#162456] px-5 py-3 text-xs font-extrabold text-white shadow-sm transition-all"
              >
                <span>Sign in to Deploy this Application</span>
                <span>&rarr;</span>
              </a>
              <p className="text-[11px] text-slate-400">
                Are you the project owner? Sign in to the console to configure environment variables and trigger a build.
              </p>
            </div>
          </div>
        </div>
      </main>

      <footer className="py-4 text-center text-xs font-semibold text-slate-500 border-t border-slate-300 bg-white/60">
        Powered by JP Cloud Platform • Enterprise Self-Hosted Infrastructure
      </footer>
    </div>
  );
}

export default function RootAuthPage() {
  const [appDomain, setAppDomain] = useState<string | null>(null);

  useEffect(() => {
    if (typeof window !== "undefined") {
      const h = window.location.hostname.toLowerCase();
      // If hostname is NOT panel.* and NOT localhost/127.0.0.1, it's an app domain!
      const isPanel = h.startsWith("panel.") || h === "localhost" || h === "127.0.0.1" || h === "panel.localhost";
      if (!isPanel) {
        setAppDomain(window.location.hostname);
      }
    }
  }, []);

  if (appDomain) {
    return <AppNotDeployedPage hostname={appDomain} />;
  }

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
