"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { PageHeader, Panel } from "@/components/ui/panel";
import { useToast } from "@/components/ui/toast";
import { errorMessage, orgs } from "@/lib/api";
import { useOrg } from "@/lib/org-context";

function AcceptForm() {
  const params = useSearchParams();
  const router = useRouter();
  const toast = useToast();
  const { setCurrentOrgId, reload } = useOrg();
  const [token, setToken] = useState(params.get("token") ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const accept = async () => {
    setBusy(true);
    setError("");
    try {
      const o = await orgs.acceptInvite(token.trim());
      if (o?.id) setCurrentOrgId(o.id);
      reload();
      toast.success("Invitation accepted", o?.name ? `You joined ${o.name}` : undefined);
      router.push("/");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel className="max-w-xl">
      <form
        className="flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault();
          void accept();
        }}
      >
        <Input label="Invitation token" required value={token} onChange={(e) => setToken(e.target.value)} spellCheck={false} data-testid="invite-token" />
        {error && (
          <p role="alert" className="error-text !mt-0">
            {error}
          </p>
        )}
        <Button type="submit" variant="primary" loading={busy} disabled={!token.trim()} data-testid="invite-accept">
          Accept invitation
        </Button>
      </form>
    </Panel>
  );
}

export default function AcceptInvitePage() {
  return (
    <>
      <PageHeader title="Accept invitation" description="Join an organization using the token from your invite link. The invitation must have been sent to your account email." />
      <Suspense>
        <AcceptForm />
      </Suspense>
    </>
  );
}
