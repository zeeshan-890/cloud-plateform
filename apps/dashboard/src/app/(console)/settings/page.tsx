"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input } from "@/components/ui/field";
import { Modal } from "@/components/ui/modal";
import { PageHeader, Panel } from "@/components/ui/panel";
import { QrCode } from "@/components/ui/qr-code";
import { Lamp } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { auth, errorMessage, type MfaSetup } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";

function Profile() {
  const { user, refreshUser } = useAuth();
  const toast = useToast();
  const [name, setName] = useState(user?.name ?? "");
  const [busy, setBusy] = useState(false);
  return (
    <Panel title="Profile">
      <form
        className="grid items-end gap-4 sm:grid-cols-[1fr_1fr_auto]"
        onSubmit={async (e: FormEvent) => {
          e.preventDefault();
          setBusy(true);
          try {
            await auth.updateProfile({ name: name.trim() });
            await refreshUser();
            toast.success("Profile saved");
          } catch (err) {
            toast.error("Could not save profile", err);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <Input label="Email" value={user?.email ?? ""} readOnly disabled />
        <Button type="submit" variant="primary" loading={busy} disabled={!name.trim() || name === user?.name}>Save</Button>
      </form>
    </Panel>
  );
}

function Password() {
  const toast = useToast();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  return (
    <Panel title="Password">
      <form
        className="grid items-end gap-4 sm:grid-cols-[1fr_1fr_auto]"
        onSubmit={async (e: FormEvent) => {
          e.preventDefault();
          setBusy(true);
          setErr("");
          try {
            await auth.changePassword({ current_password: cur, new_password: next });
            setCur("");
            setNext("");
            toast.success("Password changed");
          } catch (e2) {
            setErr(errorMessage(e2));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Input label="Current password" type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} />
        <Input label="New password (8+ characters)" type="password" autoComplete="new-password" minLength={8} value={next} onChange={(e) => setNext(e.target.value)} error={err || undefined} />
        <Button type="submit" variant="primary" loading={busy} disabled={!cur || next.length < 8}>Change</Button>
      </form>
    </Panel>
  );
}

function Mfa() {
  const { user, refreshUser } = useAuth();
  const toast = useToast();
  const enabled = !!user?.totp_enabled;
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const [dcode, setDcode] = useState("");

  const begin = async () => {
    setBusy(true);
    setErr("");
    try {
      setSetup(await auth.mfaSetup());
      setCode("");
    } catch (e) {
      toast.error("Could not start setup", e);
    } finally {
      setBusy(false);
    }
  };
  const enable = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const r = await auth.mfaEnable(code.trim());
      setCodes(r.backup_codes);
      setSetup(null);
      await refreshUser();
      toast.success("Two-step sign-in is on");
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };
  const disable = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      await auth.mfaDisable(dcode.trim());
      setDisableOpen(false);
      setDcode("");
      await refreshUser();
      toast.success("Two-step sign-in is off");
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel title="Two-step sign-in">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Lamp state={enabled ? "ok" : "idle"} large label={enabled ? "enabled" : "disabled"} />
        <strong>{enabled ? "Authenticator app is required at sign-in" : "Not enabled"}</strong>
        <div className="ml-auto">
          {enabled ? <Button variant="danger" onClick={() => { setErr(""); setDisableOpen(true); }}>Turn off</Button> : !setup && <Button variant="primary" onClick={() => void begin()} loading={busy} data-testid="mfa-begin">Set up authenticator</Button>}
        </div>
      </div>
      {setup && (
        <form onSubmit={enable} className="grid gap-6 md:grid-cols-[auto_1fr]" data-testid="mfa-setup">
          <div className="flex flex-col items-center gap-2">
            <QrCode text={setup.otpauth_uri} label="Authenticator QR code" />
            <span className="hint !mt-0">Scan with your authenticator app</span>
          </div>
          <div className="flex flex-col gap-4">
            <div>
              <div className="label mb-1">Or enter this key manually</div>
              <code className="mono well block break-all !py-2 text-[14px]" data-testid="mfa-secret">{setup.secret}</code>
              <div className="mt-2 flex flex-wrap gap-2">
                <CopyButton text={setup.secret} label="Copy key" />
                <CopyButton text={setup.otpauth_uri} label="Copy otpauth link" />
              </div>
            </div>
            <Input label="6-digit code from the app" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} error={err || undefined} data-testid="mfa-code" />
            <div className="flex gap-3">
              <Button type="submit" variant="primary" loading={busy} disabled={code.trim().length < 6} data-testid="mfa-enable">Verify and turn on</Button>
              <Button variant="ghost" onClick={() => setSetup(null)}>Cancel</Button>
            </div>
          </div>
        </form>
      )}
      <Modal open={!!codes} onClose={() => setCodes(null)} title="Save your backup codes" description="Each code works once if you lose your phone. They are shown only now.">
        <div className="well grid grid-cols-2 gap-2" data-testid="backup-codes">
          {codes?.map((c) => <code key={c} className="mono text-[14px]">{c}</code>)}
        </div>
        <div className="mt-4 flex gap-2">
          <CopyButton text={(codes ?? []).join("\n")} label="Copy all" />
          <Button onClick={() => setCodes(null)} variant="primary">I have stored them</Button>
        </div>
      </Modal>
      <Modal open={disableOpen} onClose={() => setDisableOpen(false)} locked={busy} title="Turn off two-step sign-in" description="Enter a current code or an unused backup code to confirm.">
        <form onSubmit={disable} className="flex flex-col gap-4">
          <Input data-autofocus label="Code" value={dcode} onChange={(e) => setDcode(e.target.value)} error={err || undefined} />
          <Button type="submit" variant="danger" loading={busy} disabled={!dcode.trim()}>Turn off</Button>
        </form>
      </Modal>
    </Panel>
  );
}

export default function SettingsPage() {
  return (
    <>
      <PageHeader title="Account settings" description="Your profile, password and two-step sign-in." />
      <div className="flex flex-col gap-6">
        <Profile />
        <Password />
        <Mfa />
      </div>
    </>
  );
}
