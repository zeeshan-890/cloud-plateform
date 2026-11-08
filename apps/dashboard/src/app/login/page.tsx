"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { AuthStage } from "@/components/auth/auth-stage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { ErrorState } from "@/components/ui/states";
import { Lamp } from "@/components/ui/status";
import { auth, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useResource } from "@/lib/use-resource";

function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return "/";
  return raw;
}

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));
  const inviteToken = params.get("invite_token");
  const { user, login, completeMfa } = useAuth();
  const status = useResource("bootstrap-status", (signal) => auth.bootstrapStatus({ signal }));

  const [email, setEmail] = useState(params.get("email") ?? "");
  const [password, setPassword] = useState("");
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!user) return;
    if (user.must_change_password) router.replace("/change-password");
    else router.replace(inviteToken ? `/invite/accept?token=${encodeURIComponent(inviteToken)}` : next);
  }, [user, router, next, inviteToken]);

  useEffect(() => {
    if (status.data && !status.data.initialized) router.replace("/setup");
  }, [status.data, router]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (mfaToken) {
        await completeMfa(email.trim(), password, mfaToken, code);
      } else {
        const out = await login(email.trim(), password);
        if (out.kind === "mfa") {
          setMfaToken(out.mfaToken);
          setCode("");
        }
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (status.error && !status.data) {
    return <ErrorState error={status.error} onRetry={status.reload} />;
  }

  const reg = status.data?.registration;
  const canRegister = reg === "open" || (reg === "invite" && !!inviteToken);

  return (
    <form onSubmit={submit} className="flex flex-col gap-5" noValidate>
      {!mfaToken ? (
        <>
          <Input
            label="Email or username"
            type="text"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@company.com or jdoe"
            data-testid="login-email"
          />
          <Input
            label="Password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            data-testid="login-password"
          />
        </>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="well flex items-center gap-3 !py-3">
            <Lamp state="warn" pulse />
            <p className="m-0 text-[14.5px]">Two-step check. Enter the 6-digit code from your authenticator app, or a backup code.</p>
          </div>
          <Input
            label="Verification code"
            inputMode="numeric"
            autoComplete="one-time-code"
            autoFocus
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="123456"
            data-testid="login-mfa-code"
          />
        </div>
      )}
      {error && (
        <p role="alert" className="error-text !mt-0">
          {error}
        </p>
      )}
      <Button type="submit" variant="primary" loading={busy} disabled={!email || (!mfaToken && !password) || (!!mfaToken && !code.trim())} data-testid="login-submit">
        {mfaToken ? "Verify and sign in" : "Sign in"}
      </Button>
      {mfaToken && (
        <Button
          variant="ghost"
          onClick={() => {
            setMfaToken(null);
            setCode("");
            setError("");
          }}
        >
          Back
        </Button>
      )}
      <p className="muted m-0 text-center text-[14px]">
        {canRegister ? (
          <>
            Need an account?{" "}
            <Link href={`/register${inviteToken ? `?invite_token=${encodeURIComponent(inviteToken)}${email ? `&email=${encodeURIComponent(email)}` : ""}` : ""}`}>Create one</Link>
          </>
        ) : reg === "invite" ? (
          "Accounts are created from an invitation link sent by an organization admin."
        ) : reg === "closed" ? (
          "Registration is closed on this installation."
        ) : null}
      </p>
    </form>
  );
}

export default function LoginPage() {
  return (
    <AuthStage title="Sign in" subtitle="Welcome back to the control room.">
      <LoginForm />
    </AuthStage>
  );
}
