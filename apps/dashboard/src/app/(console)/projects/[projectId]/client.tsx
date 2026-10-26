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

  // Resolve actual projectId from params or URL path in static export
  const rawId = params?.projectId;
  const actualProjectId =
    typeof window !== "undefined" && (!rawId || rawId === "default")
      ? window.location.pathname.split("/projects/")[1]?.split("/")[0]?.split("?")[0] || rawId
      : rawId;

  useEffect(() => {
    if (!currentOrg || !actualProjectId || actualProjectId === "default") return;
    let mounted = true;
    (async () => {
      setLoading(true);
      setError("");
      try {
        const p = await api.getProject(currentOrg.id, actualProjectId);
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
  }, [currentOrg, actualProjectId]);

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
          className="jp-card space-y-4 p-5 lg:col-span-2"
        >
          <div className="border-b border-[var(--border)] pb-3">
            <h2 className="text-base font-bold text-[var(--ink)]">
              General Settings
            </h2>
            <p className="text-xs text-[var(--ink-muted)]">Update project metadata and display details.</p>
          </div>
          <div>
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
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
            <label className="mb-1.5 block text-xs font-bold uppercase tracking-wider text-[var(--ink-secondary)]">
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={saving}
              rows={3}
              className="w-full rounded-md border border-[var(--border)] bg-white px-3 py-2 text-sm text-[var(--ink)] placeholder:text-[var(--ink-faint)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] focus:outline-none transition-all shadow-2xs"
              placeholder="What does this project do?"
            />
          </div>
          <div className="pt-2">
            <Button type="submit" disabled={saving}>
              {saving ? "Saving..." : "Save changes"}
            </Button>
          </div>
        </form>

        <div className="space-y-4">
          <div className="jp-card space-y-3 p-4">
            <h3 className="text-sm font-bold text-[var(--ink)]">
              Quick Links
            </h3>
            <ul className="space-y-2 text-xs font-medium text-[var(--ink-secondary)]">
              <li>
                <Link
                  href="/secrets"
                  className="flex items-center gap-1.5 hover:text-[var(--accent)] hover:translate-x-0.5 transition-all"
                >
                  <span className="text-[var(--accent)]">→</span> Environment Variables & Secrets
                </Link>
              </li>
              <li>
                <Link
                  href="/domains"
                  className="flex items-center gap-1.5 hover:text-[var(--accent)] hover:translate-x-0.5 transition-all"
                >
                  <span className="text-[var(--accent)]">→</span> Custom Domains & TLS
                </Link>
              </li>
              <li>
                <Link
                  href="/databases"
                  className="flex items-center gap-1.5 hover:text-[var(--accent)] hover:translate-x-0.5 transition-all"
                >
                  <span className="text-[var(--accent)]">→</span> Managed Databases
                </Link>
              </li>
              <li>
                <Link
                  href="/addons"
                  className="flex items-center gap-1.5 hover:text-[var(--accent)] hover:translate-x-0.5 transition-all"
                >
                  <span className="text-[var(--accent)]">→</span> Add-ons Marketplace
                </Link>
              </li>
            </ul>
          </div>

          <div className="jp-card space-y-3 border-red-300 bg-red-50/50 p-4">
            <h3 className="text-sm font-bold text-red-700">Danger Zone</h3>
            <p className="text-xs text-red-800/80 leading-relaxed">
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
