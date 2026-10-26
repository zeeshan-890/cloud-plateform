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

export default function ProjectDetailClient() {
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
      const updated = await api.updateProject(
        currentOrg.id,
        project.id,
        { name: name.trim(), description: description.trim() },
      );
      setProject(updated);
      setSuccess("Project settings saved.");
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
    const confirmed = window.confirm(
      `Are you sure you want to delete "${project.name}"? This cannot be undone.`,
    );
    if (!confirmed) return;
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
    return (
      <div className="flex h-64 items-center justify-center text-sm text-[var(--color-fg-muted)]">
        Loading project...
      </div>
    );
  }

  if (!project) {
    return (
      <div className="space-y-4">
        <Alert tone="error">
          {error || "Project not found or you do not have permission to view it."}
        </Alert>
        <Link href="/projects">
          <Button variant="secondary">Back to projects</Button>
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <PageHeader
        title={project.name}
        description={`ID: ${project.id}${project.created_at ? ` • Created ${new Date(project.created_at).toLocaleDateString()}` : ""}`}
        actions={
          <div className="flex gap-2">
            <Link href={`/deployments`}>
              <Button variant="secondary">Deployments</Button>
            </Link>
            <Link href={`/git`}>
              <Button variant="secondary">Git</Button>
            </Link>
          </div>
        }
      />

      {error ? <Alert tone="error">{error}</Alert> : null}
      {success ? <Alert tone="success">{success}</Alert> : null}

      <div className="grid gap-6 lg:grid-cols-3">
        <form
          onSubmit={onSave}
          className="space-y-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6 lg:col-span-2"
        >
          <h2 className="text-base font-semibold text-[var(--color-fg)]">
            General Settings
          </h2>
          <div>
            <label className="mb-1 block text-xs font-medium text-[var(--color-fg-muted)]">
              Project Name
            </label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              disabled={saving}
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[var(--color-fg-muted)]">
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={saving}
              rows={3}
              className="w-full rounded-md border border-[var(--color-border)] bg-transparent px-3 py-2 text-sm text-[var(--color-fg)] placeholder:text-[var(--color-fg-subtle)] focus:border-[var(--color-primary)] focus:outline-none"
              placeholder="What does this project do?"
            />
          </div>
          <div className="pt-2">
            <Button type="submit" disabled={saving}>
              {saving ? "Saving..." : "Save changes"}
            </Button>
          </div>
        </form>

        <div className="space-y-6">
          <div className="space-y-3 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6">
            <h3 className="text-sm font-semibold text-[var(--color-fg)]">
              Quick Links
            </h3>
            <ul className="space-y-2 text-xs text-[var(--color-fg-muted)]">
              <li>
                <Link
                  href="/secrets"
                  className="hover:text-[var(--color-primary)]"
                >
                  → Environment Variables & Secrets
                </Link>
              </li>
              <li>
                <Link
                  href="/domains"
                  className="hover:text-[var(--color-primary)]"
                >
                  → Custom Domains & TLS
                </Link>
              </li>
              <li>
                <Link
                  href="/databases"
                  className="hover:text-[var(--color-primary)]"
                >
                  → Managed Databases
                </Link>
              </li>
              <li>
                <Link
                  href="/addons"
                  className="hover:text-[var(--color-primary)]"
                >
                  → Add-ons Marketplace
                </Link>
              </li>
            </ul>
          </div>

          <div className="space-y-3 rounded-xl border border-red-500/20 bg-red-500/5 p-6">
            <h3 className="text-sm font-semibold text-red-500">Danger Zone</h3>
            <p className="text-xs text-[var(--color-fg-muted)]">
              Deleting this project deletes all associated deployments,
              secrets, and routing configurations permanently.
            </p>
            <Button
              variant="danger"
              onClick={onDelete}
              disabled={deleting}
            >
              {deleting ? "Deleting..." : "Delete project"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
