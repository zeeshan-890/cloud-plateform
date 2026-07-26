"use client";

import Link from "next/link";
import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";

export default function NewProjectPage() {
  const { currentOrg } = useOrg();
  const router = useRouter();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg) {
      setError("Select or create an organization first.");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      const project = await api.createProject(currentOrg.id, {
        name,
        description: description || undefined,
      });
      router.push(`/projects/${project.id}`);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to create project",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg animate-fade-up">
      <PageHeader
        title="New project"
        description="A project scopes apps and resources within your organization."
      />
      <form onSubmit={onSubmit} className="flex flex-col gap-4">
        {error ? <Alert>{error}</Alert> : null}
        <Input
          label="Name"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="api-service"
        />
        <Input
          label="Description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Optional"
        />
        <div className="mt-2 flex gap-2">
          <Button type="submit" disabled={submitting || !currentOrg}>
            {submitting ? "Creating…" : "Create project"}
          </Button>
          <Link href="/projects">
            <Button type="button" variant="secondary">
              Cancel
            </Button>
          </Link>
        </div>
      </form>
    </div>
  );
}
