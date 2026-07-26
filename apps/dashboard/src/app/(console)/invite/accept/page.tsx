"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { setCurrentOrgId } from "@/lib/storage";

export default function AcceptInvitePage() {
  const { refreshOrgs } = useOrg();
  const router = useRouter();
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const org = await api.acceptInvite(token.trim());
      setCurrentOrgId(org.id);
      await refreshOrgs();
      router.push("/projects");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to accept invite",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg animate-fade-up">
      <PageHeader
        title="Accept invite"
        description="Paste the invite token you received to join an organization."
      />
      <form onSubmit={onSubmit} className="flex flex-col gap-4">
        {error ? <Alert>{error}</Alert> : null}
        <Input
          label="Invite token"
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
          placeholder="Paste token"
        />
        <Button type="submit" disabled={submitting} className="w-fit">
          {submitting ? "Joining…" : "Join organization"}
        </Button>
      </form>
    </div>
  );
}
