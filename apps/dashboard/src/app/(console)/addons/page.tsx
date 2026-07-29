"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { AddonCatalogItem, ManagedAddon, Project } from "@/lib/types";

export default function AddonsPage() {
  const { currentOrg } = useOrg();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectId, setProjectId] = useState("");
  const [catalog, setCatalog] = useState<AddonCatalogItem[]>([]);
  const [addons, setAddons] = useState<ManagedAddon[]>([]);
  const [mode, setMode] = useState("");
  const [engine, setEngine] = useState("redis");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [busy, setBusy] = useState(false);
  const selected = catalog.find((c) => c.id === engine);

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
    const [cat, list] = await Promise.all([
      api.addonCatalog(currentOrg.id, pid),
      api.listAddons(currentOrg.id, pid),
    ]);
    setCatalog(cat.catalog);
    setMode(cat.mode || list.mode || "");
    setAddons(list.addons);
    if (cat.catalog[0] && !cat.catalog.find((c) => c.id === engine)) {
      setEngine(cat.catalog[0].id);
    }
  }

  useEffect(() => {
    if (!projectId) return;
    let mounted = true;
    (async () => {
      try {
        await load(projectId);
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load add-ons");
        }
      }
    })();
    return () => {
      mounted = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, projectId]);

  async function onCreate() {
    if (!currentOrg || !projectId || !name.trim() || !engine) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const created = await api.createAddon(currentOrg.id, projectId, engine, name.trim());
      setName("");
      setSuccess(
        `Provisioned ${created.engine}/${created.name}. Secret ref: ${created.secret_ref || "created"}.`,
      );
      await load(projectId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    if (!currentOrg || !projectId) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      await api.deleteAddon(currentOrg.id, projectId, id);
      setSuccess("Add-on deleted.");
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
        description="Add-ons require an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Add-ons"
        description="One-click Redis, Postgres, MySQL, MongoDB, RabbitMQ, Kafka, and SQLite. Connection URLs are stored as secrets."
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}
      {success ? (
        <div className="mb-4">
          <Alert tone="success">{success}</Alert>
        </div>
      ) : null}

      <div className="mb-6 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
        <p className="text-sm text-[var(--ink-muted)]">
          Provision mode:{" "}
          <span className="font-medium text-[var(--ink)]">{mode || "unknown"}</span>
        </p>
        <p className="mt-2 text-xs text-[var(--ink-faint)]">
          Simulate mode mints realistic credentials. Shared mode provisions against broker services
          from Compose profile <code className="font-[family-name:var(--font-mono)]">addons</code>.
        </p>
      </div>

      <label className="mb-4 block max-w-sm text-sm text-[var(--ink-muted)]">
        Project
        <select
          className="mt-1 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[var(--ink)]"
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

      {catalog.length > 0 ? (
        <div className="mb-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {catalog.map((c) => (
            <button
              key={c.id}
              type="button"
              onClick={() => setEngine(c.id)}
              className={`rounded-md border px-4 py-3 text-left transition-colors ${
                engine === c.id
                  ? "border-[var(--ink)] bg-[var(--surface)]"
                  : "border-[var(--border)] hover:bg-[var(--surface-hover)]"
              }`}
            >
              <div className="text-sm font-medium text-[var(--ink)]">{c.name}</div>
              <div className="mt-1 text-xs uppercase tracking-wide text-[var(--ink-faint)]">
                {c.category}
              </div>
              <p className="mt-2 text-xs text-[var(--ink-muted)]">{c.description}</p>
            </button>
          ))}
        </div>
      ) : null}

      {selected ? (
        <div className="mb-6 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
          <h2 className="text-sm font-semibold text-[var(--ink)]">
            Selected: {selected.name} ({selected.id})
          </h2>
          <p className="mt-1 text-xs text-[var(--ink-muted)]">{selected.description}</p>
          {selected.secret_keys?.length ? (
            <p className="mt-2 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-faint)]">
              Secret key pattern: {selected.secret_keys.join(", ")}
            </p>
          ) : null}
        </div>
      ) : null}

      <div className="mb-8 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
        <h2 className="mb-3 text-sm font-semibold text-[var(--ink)]">Create add-on instance</h2>
        <div className="flex max-w-xl flex-wrap items-end gap-3">
          <label className="block flex-1 text-sm text-[var(--ink-muted)]">
            Name
            <Input
              className="mt-1"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="cache"
            />
          </label>
          <Button type="button" onClick={onCreate} disabled={busy || !projectId || !name.trim()}>
            Provision {engine}
          </Button>
        </div>
        <p className="mt-2 text-xs text-[var(--ink-faint)]">
          Tip: use short names like <code>cache</code>, <code>queue</code>,{" "}
          <code>analytics</code>. One secret reference will be generated per add-on.
        </p>
      </div>

      {addons.length === 0 ? (
        <EmptyState
          title="No add-ons yet"
          description="Pick an engine above and provision an instance for this project."
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
                <th className="py-2 pr-3 font-medium">Engine</th>
                <th className="py-2 pr-3 font-medium">Name</th>
                <th className="py-2 pr-3 font-medium">Status</th>
                <th className="py-2 pr-3 font-medium">Mode</th>
                <th className="py-2 pr-3 font-medium">Secret Ref</th>
                <th className="py-2 pr-3 font-medium">Hint</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {addons.map((a) => (
                <tr key={a.id} className="border-b border-[var(--border)]">
                  <td className="py-3 pr-3 font-medium text-[var(--ink)]">{a.engine}</td>
                  <td className="py-3 pr-3 text-[var(--ink)]">{a.name}</td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{a.status}</td>
                  <td className="py-3 pr-3 text-[var(--ink-muted)]">{a.mode}</td>
                  <td className="py-3 pr-3 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {a.secret_ref || "—"}
                  </td>
                  <td className="py-3 pr-3 font-[family-name:var(--font-mono)] text-xs text-[var(--ink-muted)]">
                    {a.connection_hint || "—"}
                  </td>
                  <td className="py-3 text-right">
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={busy}
                      onClick={() => onDelete(a.id)}
                    >
                      Delete
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
