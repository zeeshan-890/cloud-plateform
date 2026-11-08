"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { AuthStage } from "@/components/auth/auth-stage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { EmptyState, ErrorState } from "@/components/ui/states";
import { auth, errorMessage, orgs } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useToast } from "@/components/ui/toast";
import { useResource } from "@/lib/use-resource";

function RegisterForm() {
  const router = useRouter();
  const params = useSearchParams();
  const toast = useToast();
  const { user, register } = useAuth();
  const status = useResource("bootstrap-status", (signal) => auth.bootstrapStatus({ signal }));

  const [name, setName] = useState("");
  const [email, setEmail] = useState(params.get("email") ?? "");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState(params.get("invite_token") ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (user) router.replace("/");
  }, [user, router]);
  useEffect(() => {
    if (status.data && !status.data.initialized) router.replace("/setup");
  }, [status.data, router]);

  if (status.error && !status.data) return <ErrorState error={status.error} onRetry={status.reload} />;
  const mode = status.data?.registration;
  if (mode === "closed") {
    return (
      <EmptyState
        title="Registration closed"
        body="This installation does not accept new sign-ups. Ask an administrator to invite you."
        action={<Link href="/login">Back to sign in</Link>}
      />
    );
  }
  const tokenRequired = mode === "invite";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await register({ name: name.trim(), email: email.trim(), password, invite_token: token.trim() || undefined });
      if (token.trim()) {
        try {
          await orgs.acceptInvite(token.trim());
        } catch (err) {
          toast.info("Invitation not applied yet", errorMessage(err));
        }
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-5">
      <Input label="Full name" autoComplete="name" required value={name} onChange={(e) => setName(e.target.value)} />
      <Input label="Email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} hint={tokenRequired ? "Must match the email the invitation was sent to." : undefined} />
      <Input label="Password" type="password" autoComplete="new-password" minLength={8} required value={password} onChange={(e) => setPassword(e.target.value)} hint="At least 8 characters." />
      <Input
        label={tokenRequired ? "Invitation token" : "Invitation token (optional)"}
        required={tokenRequired}
        value={token}
        onChange={(e) => setToken(e.target.value)}
        hint={tokenRequired ? "Registration is by invitation. Paste the token from your invite link." : "Have an invite? Paste its token to join the organization."}
        spellCheck={false}
      />
      {error && (
        <p role="alert" className="error-text !mt-0">
          {error}
        </p>
      )}
      <Button type="submit" variant="primary" loading={busy} disabled={!name || !email || password.length < 8 || (tokenRequired && !token.trim())}>
        Create account
      </Button>
      <p className="muted m-0 text-center text-[14px]">
        Already registered? <Link href="/login">Sign in</Link>
      </p>
    </form>
  );
}

export default function RegisterPage() {
  return (
    <AuthStage title="Create account" subtitle="Join your team on JP Cloud.">
      <RegisterForm />
    </AuthStage>
  );
}
