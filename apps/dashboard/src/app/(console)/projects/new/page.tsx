"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/field";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState } from "@/components/ui/states";
import { useToast } from "@/components/ui/toast";
import { errorMessage, projects, slugify } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";

export default function NewProjectPage() {
  const router = useRouter();
  const toast = useToast();
  const { currentOrg, can } = useOrg();
  const { reload, setActiveProjectId } = useProjects();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  if (!can("admin")) {
    return (
      <>
        <PageHeader title="New project" />
        <EmptyState title="Administrators only" body="Projects are created by organization admins. Ask one to create it and give you access." />
      </>
    );
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!currentOrg) return;
    setBusy(true);
    setError("");
    try {
      const p = await projects.create(currentOrg.id, { name: name.trim(), slug: slug || slugify(name), description: description.trim() || undefined });
      toast.success("Project created", p.name);
      setActiveProjectId(p.id);
      reload();
      router.push(`/project?id=${encodeURIComponent(p.id)}`);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title="New project" eyebrow={currentOrg?.name} description="Name it, then connect a repository and deploy." />
      <Panel className="max-w-xl">
        <form onSubmit={submit} className="flex flex-col gap-5">
          <Input
            label="Project name"
            required
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (!slugTouched) setSlug(slugify(e.target.value));
            }}
            data-testid="project-name"
          />
          <Input
            label="Slug"
            value={slug}
            pattern="[a-z0-9]([a-z0-9-]*[a-z0-9])?"
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value.toLowerCase());
            }}
            hint={`Used in the system URL: ${slug || "slug"}-${currentOrg?.slug ?? "org"}.<root domain>`}
            data-testid="project-slug"
          />
          <Textarea label="Description (optional)" value={description} onChange={(e) => setDescription(e.target.value)} />
          {error && (
            <p role="alert" className="error-text !mt-0">
              {error}
            </p>
          )}
          <div className="flex gap-3">
            <Button type="submit" variant="primary" loading={busy} disabled={!name.trim()} data-testid="project-submit">
              Create project
            </Button>
            <Button variant="ghost" onClick={() => router.back()}>
              Cancel
            </Button>
          </div>
        </form>
      </Panel>
    </>
  );
}
