"use client";

import { FormEvent, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import type { Pat } from "@/lib/types";

export default function KeysPage() {
  const [pats, setPats] = useState<Pat[]>([]);
  const [name, setName] = useState("");
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [createdToken, setCreatedToken] = useState("");

  async function load() {
    setLoading(true);
    setError("");
    try {
      const list = await api.listPats();
      setPats(list);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load API keys");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setCreating(true);
    setError("");
    setCreatedToken("");
    try {
      const pat = await api.createPat({ name });
      if (pat.token) setCreatedToken(pat.token);
      setName("");
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create key");
    } finally {
      setCreating(false);
    }
  }

  async function onDelete(id: string) {
    if (!confirm("Revoke this API key?")) return;
    setError("");
    try {
      await api.deletePat(id);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to revoke key");
    }
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="API keys"
        description="Personal access tokens for CLI and automation. Treat them like passwords."
      />

      <section className="mb-10 max-w-lg">
        <h2 className="mb-3 font-[family-name:var(--font-display)] text-lg font-semibold text-[var(--ink)]">
          Create token
        </h2>
        <form onSubmit={onCreate} className="flex flex-col gap-3">
          {error ? <Alert>{error}</Alert> : null}
          {createdToken ? (
            <Alert tone="success">
              Copy this token now — it won&apos;t be shown again:{" "}
              <code className="mt-1 block break-all font-[family-name:var(--font-mono)] text-xs">
                {createdToken}
              </code>
            </Alert>
          ) : null}
          <Input
            label="Name"
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="ci-deploy"
          />
          <Button type="submit" disabled={creating} className="w-fit">
            {creating ? "Creating…" : "Create token"}
          </Button>
        </form>
      </section>

      <section>
        <h2 className="mb-3 font-[family-name:var(--font-display)] text-lg font-semibold text-[var(--ink)]">
          Your tokens
        </h2>
        {loading ? (
          <p className="text-sm text-[var(--ink-muted)]">Loading…</p>
        ) : pats.length === 0 ? (
          <p className="text-sm text-[var(--ink-muted)]">No API keys yet.</p>
        ) : (
          <ul className="divide-y divide-[var(--border)] border-y border-[var(--border)]">
            {pats.map((pat) => (
              <li
                key={pat.id}
                className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-2"
              >
                <div>
                  <p className="font-medium text-[var(--ink)]">{pat.name}</p>
                  <p className="font-[family-name:var(--font-mono)] text-xs text-[var(--ink-faint)]">
                    {pat.token_prefix ? `${pat.token_prefix}…` : pat.id.slice(0, 8)}
                    {pat.created_at
                      ? ` · created ${new Date(pat.created_at).toLocaleDateString()}`
                      : ""}
                  </p>
                </div>
                <Button variant="danger" onClick={() => onDelete(pat.id)}>
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
