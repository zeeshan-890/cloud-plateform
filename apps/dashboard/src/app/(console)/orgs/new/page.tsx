"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";

export default function NewOrgPage() {
  const { createOrg } = useOrg();
  const router = useRouter();
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      await createOrg(name);
      router.push("/projects");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to create organization",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg animate-fade-up">
      <PageHeader
        title="New organization"
        description="Organizations hold projects, members, and billing."
      />
      <form onSubmit={onSubmit} className="flex flex-col gap-4">
        {error ? <Alert>{error}</Alert> : null}
        <Input
          label="Organization name"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Acme Labs"
        />
        <Button type="submit" disabled={submitting} className="w-fit">
          {submitting ? "Creating…" : "Create organization"}
        </Button>
      </form>
    </div>
  );
}
