"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { PageHeader, Panel } from "@/components/ui/panel";
import { useToast } from "@/components/ui/toast";
import { errorMessage, orgs, slugify } from "@/lib/api";
import { useOrg } from "@/lib/org-context";

export default function NewOrgPage() {
  const router = useRouter();
  const toast = useToast();
  const { orgs: existing, setCurrentOrgId, reload } = useOrg();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const o = await orgs.create({ name: name.trim(), slug: slug || slugify(name) });
      setCurrentOrgId(o.id);
      reload();
      toast.success("Organization created", o.name);
      router.push("/projects/new");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        title="New organization"
        eyebrow={existing.length === 0 ? "Step 1 of 2" : "Workspace"}
        description="An organization owns projects and the people who work on them. You become its owner."
      />
      <Panel className="max-w-xl">
        <form onSubmit={submit} className="flex flex-col gap-5">
          <Input
            label="Organization name"
            required
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (!touched) setSlug(slugify(e.target.value));
            }}
            data-testid="org-name"
          />
          <Input
            label="Slug"
            value={slug}
            onChange={(e) => {
              setTouched(true);
              setSlug(e.target.value.toLowerCase());
            }}
            hint="Lowercase letters, numbers and dashes. Part of every project's system URL."
            data-testid="org-slug"
          />
          {error && (
            <p role="alert" className="error-text !mt-0">
              {error}
            </p>
          )}
          <div className="flex gap-3">
            <Button type="submit" variant="primary" loading={busy} disabled={!name.trim()} data-testid="org-submit">
              Create organization
            </Button>
            {existing.length > 0 && (
              <Button variant="ghost" onClick={() => router.back()}>
                Cancel
              </Button>
            )}
          </div>
        </form>
      </Panel>
    </>
  );
}
