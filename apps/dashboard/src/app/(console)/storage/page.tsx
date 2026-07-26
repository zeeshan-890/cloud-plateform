"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { Project, StorageObject } from "@/lib/types";

export default function StoragePage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [objects, setObjects] = useState<StorageObject[]>([]);
  const [bucket, setBucket] = useState("");
  const [mode, setMode] = useState("");
  const [key, setKey] = useState("");
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        const list = await api.listProjects(currentOrg.id);
        if (!mounted) return;
        setProjects(list);
        if (list[0]) setProjectId(list[0].id);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load projects");
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  async function load(pid: string) {
    if (!currentOrg || !pid) return;
    await api.getStorageBucket(currentOrg.id, pid);
    const res = await api.listStorageObjects(currentOrg.id, pid);
    setObjects(res.objects);
    setBucket(res.bucket || "");
    setMode(res.mode || "");
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await load(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load storage");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onUpload() {
    if (!currentOrg || !projectId || !key.trim() || !text) return;
    setBusy(true);
    setError("");
    try {
      const dataBase64 = btoa(unescape(encodeURIComponent(text)));
      await api.uploadStorageObject(currentOrg.id, projectId, key.trim(), dataBase64, "text/plain");
      setKey("");
      setText("");
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Upload failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSigned(objectKey: string) {
    if (!currentOrg || !projectId) return;
    try {
      const res = await api.signedStorageURL(currentOrg.id, projectId, objectKey);
      window.open(res.url, "_blank");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Signed URL failed");
    }
  }

  async function onDelete(objectKey: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteStorageObject(currentOrg.id, projectId, objectKey);
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Storage requires an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Storage"
        description="Object storage buckets per project. Upload text objects, get signed URLs, list and delete."
      />
      {error ? <Alert>{error}</Alert> : null}

      <div className="mb-6 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Project</span>
          <select
            className="rounded-md border border-[var(--border)] bg-[var(--paper)] px-3 py-2"
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        {bucket ? (
          <p className="text-sm text-[var(--ink-muted)]">
            Bucket <span className="font-medium text-[var(--ink)]">{bucket}</span>
            {mode ? ` · mode ${mode}` : null}
          </p>
        ) : null}
      </div>

      <div className="mb-8 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Object key</span>
          <Input value={key} onChange={(e) => setKey(e.target.value)} placeholder="docs/readme.txt" />
        </label>
        <label className="flex min-w-[240px] flex-1 flex-col gap-1 text-sm">
          <span className="text-[var(--ink-muted)]">Content</span>
          <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="Hello storage" />
        </label>
        <Button onClick={onUpload} disabled={busy || !key.trim() || !text}>
          Upload
        </Button>
      </div>

      {objects.length === 0 ? (
        <EmptyState title="No objects" description="Upload a small text object to get started." />
      ) : (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-[var(--ink-muted)]">
              <th className="py-2 font-medium">Key</th>
              <th className="py-2 font-medium">Size</th>
              <th className="py-2 font-medium">Type</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {objects.map((o) => (
              <tr key={o.id || o.key} className="border-b border-[var(--border)]">
                <td className="py-2 font-medium">{o.key}</td>
                <td className="py-2 text-[var(--ink-muted)]">{o.size_bytes} B</td>
                <td className="py-2 text-[var(--ink-muted)]">{o.content_type}</td>
                <td className="py-2 text-right">
                  <Button variant="ghost" onClick={() => onSigned(o.key)} disabled={busy}>
                    Sign
                  </Button>
                  <Button variant="ghost" onClick={() => onDelete(o.key)} disabled={busy}>
                    Delete
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
