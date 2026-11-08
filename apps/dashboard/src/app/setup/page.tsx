"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { AuthStage } from "@/components/auth/auth-stage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { ErrorState } from "@/components/ui/states";
import { Lamp } from "@/components/ui/status";
import { auth, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useResource } from "@/lib/use-resource";

function SetupForm() {
  const router = useRouter();
  const { user, bootstrap } = useAuth();
  const status = useResource("bootstrap-status", (signal) => auth.bootstrapStatus({ signal }));
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (user) router.replace("/orgs/new");
  }, [user, router]);
  useEffect(() => {
    if (status.data?.initialized) router.replace("/login");
  }, [status.data, router]);

  if (status.error && !status.data) return <ErrorState error={status.error} onRetry={status.reload} />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await bootstrap({ name: name.trim(), email: email.trim(), password });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-5">
      <div className="well flex items-start gap-3 !py-3">
        <Lamp state="warn" pulse className="mt-1" />
        <p className="m-0 text-[14.5px]">
          This installation has no users yet. The account you create here becomes the <strong>platform administrator</strong> and can manage cluster, security and system settings.
        </p>
      </div>
      <Input label="Your name" autoComplete="name" required value={name} onChange={(e) => setName(e.target.value)} data-testid="setup-name" />
      <Input label="Email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} data-testid="setup-email" />
      <Input label="Password" type="password" autoComplete="new-password" minLength={8} required value={password} onChange={(e) => setPassword(e.target.value)} hint="At least 8 characters. Use a password manager." data-testid="setup-password" />
      {error && (
        <p role="alert" className="error-text !mt-0">
          {error}
        </p>
      )}
      <Button type="submit" variant="primary" loading={busy} disabled={!name || !email || password.length < 8} data-testid="setup-submit">
        Create administrator
      </Button>
    </form>
  );
}

export default function SetupPage() {
  return (
    <AuthStage title="First-run setup" subtitle="Create the administrator account for this installation.">
      <SetupForm />
    </AuthStage>
  );
}
