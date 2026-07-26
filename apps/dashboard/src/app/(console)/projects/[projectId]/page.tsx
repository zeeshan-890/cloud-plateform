"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project } from "@/lib/types";

export default function ProjectDetailPage() {
  const params = useParams<{ projectId: string }>();
  const { currentOrg } = useOrg();
  const router = useRouter();
  const [project, setProject] = useState<Project | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  useEffect(() => {
    if (!currentOrg || !params.projectId) return;
    let mounted = true;
    (async () => {
      setLoading(true);
      setError("");
      try {
        const p = await api.getProject(currentOrg.id, params.projectId);
        if (mounted) {
          setProject(p);
          setName(p.name);
          setDescription(p.description || "");
        }
      } catch (err) {
        if (mounted) {
          setError(
            err instanceof ApiError ? err.message : "Failed to load project",
          );
        }
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg, params.projectId]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg || !project) return;
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      const updated = await api.updateProject(currentOrg.id, project.id, {
        name,
        description,
      });
      setProject(updated);
      setSuccess("Project updated.");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to update project",
      );
    } finally {
      setSaving(false);
    }
  }

  async function onDelete() {
    if (!currentOrg || !project) return;
    if (!confirm(`Delete project “${project.name}”? This cannot be undone.`)) {
      return;
    }
    setDeleting(true);
    setError("");
    try {
      await api.deleteProject(currentOrg.id, project.id);
      router.push("/projects");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to delete project",
      );
      setDeleting(false);
    }
  }

  if (loading) {
    return <p className="text-sm text-[var(--ink-muted)]">Loading project…</p>;
  }

  if (!project) {
    return (
      <div>
        <Alert>{error || "Project not found."}</Alert>
        <Link href="/projects" className="mt-4 inline-block text-sm text-[var(--accent-deep)]">
          Back to projects
        </Link>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-xl animate-fade-up">
      <PageHeader
        title={project.name}
        description={project.description || "Project settings"}
        actions={
          <Link href="/projects">
            <Button variant="secondary">All projects</Button>
          </Link>
        }
      />

      <dl className="mb-8 grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-[var(--ink-faint)]">ID</dt>
          <dd className="mt-0.5 font-[family-name:var(--font-mono)] text-[var(--ink)]">
            {project.id}
          </dd>
        </div>
        {project.slug ? (
          <div>
            <dt className="text-[var(--ink-faint)]">Slug</dt>
            <dd className="mt-0.5 font-[family-name:var(--font-mono)] text-[var(--ink)]">
              {project.slug}
            </dd>
          </div>
        ) : null}
        {project.created_at ? (
          <div>
            <dt className="text-[var(--ink-faint)]">Created</dt>
            <dd className="mt-0.5 text-[var(--ink)]">
              {new Date(project.created_at).toLocaleString()}
            </dd>
          </div>
        ) : null}
      </dl>

      <form onSubmit={onSave} className="flex flex-col gap-4">
        {error ? <Alert>{error}</Alert> : null}
        {success ? <Alert tone="success">{success}</Alert> : null}
        <Input
          label="Name"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Input
          label="Description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={saving}>
            {saving ? "Saving…" : "Save changes"}
          </Button>
          <Button
            type="button"
            variant="danger"
            disabled={deleting}
            onClick={onDelete}
          >
            {deleting ? "Deleting…" : "Delete project"}
          </Button>
        </div>
      </form>
    </div>
  );
}
