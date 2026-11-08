"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { AuthStage } from "@/components/auth/auth-stage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { Lamp } from "@/components/ui/status";
import { auth, errorMessage, localTokenStore } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";

function ChangeForm() {
  const router = useRouter();
  const { user, loading, login, logout } = useAuth();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!loading && !user) router.replace("/login");
    else if (!loading && user && !user.must_change_password) router.replace("/");
  }, [loading, user, router]);

  const mismatch = again !== "" && again !== next;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!user || mismatch) return;
    setBusy(true);
    setError("");
    try {
      await auth.changePassword({ current_password: current, new_password: next });
      // The limited token is spent: sign in again with the new password for a normal session.
      localTokenStore.clear();
      const who = user.username || user.email || "";
      const out = await login(who, next);
      if (out.kind === "mfa") router.replace("/login");
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
        <p className="m-0 text-[14.5px]">An administrator set a temporary password for <strong>{user?.username || user?.email}</strong>. Choose your own to continue.</p>
      </div>
      <Input label="Temporary password" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} data-testid="pw-current" />
      <Input label="New password (8+ characters)" type="password" autoComplete="new-password" minLength={8} required value={next} onChange={(e) => setNext(e.target.value)} data-testid="pw-new" />
      <Input label="Repeat new password" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} error={mismatch ? "The two passwords do not match." : undefined} data-testid="pw-again" />
      {error && (
        <p role="alert" className="error-text !mt-0">
          {error}
        </p>
      )}
      <Button type="submit" variant="primary" loading={busy} disabled={!current || next.length < 8 || next !== again} data-testid="pw-submit">
        Set password and continue
      </Button>
      <Button variant="ghost" onClick={() => void logout().then(() => router.replace("/login"))}>
        Sign out
      </Button>
    </form>
  );
}

export default function ChangePasswordPage() {
  return (
    <AuthStage title="Choose a new password" subtitle="Required before you can use the console.">
      <ChangeForm />
    </AuthStage>
  );
}
