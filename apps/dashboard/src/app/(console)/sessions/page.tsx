"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import type { Session } from "@/lib/types";

export default function SessionsPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    setLoading(true);
    setError("");
    try {
      const list = await api.listSessions();
      setSessions(list);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to load sessions",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function revoke(id: string) {
    if (!confirm("Revoke this session?")) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteSession(id);
      await load();
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to revoke session",
      );
    } finally {
      setBusy(false);
    }
  }

  async function revokeAll() {
    if (!confirm("Revoke all other sessions?")) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteAllSessions();
      await load();
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to revoke sessions",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Sessions"
        description="Active sign-ins across devices. Revoke anything you don’t recognize."
        actions={
          <Button variant="secondary" onClick={revokeAll} disabled={busy}>
            Revoke all
          </Button>
        }
      />

      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      {loading ? (
        <p className="text-sm text-[var(--ink-muted)]">Loading sessions…</p>
      ) : sessions.length === 0 ? (
        <p className="text-sm text-[var(--ink-muted)]">No sessions found.</p>
      ) : (
        <ul className="divide-y divide-[var(--border)] border-y border-[var(--border)]">
          {sessions.map((session) => (
            <li
              key={session.id}
              className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-2"
            >
              <div>
                <p className="font-medium text-[var(--ink)]">
                  {session.user_agent || "Unknown device"}
                  {session.current ? (
                    <span className="ml-2 text-xs font-normal text-[var(--ok)]">
                      Current
                    </span>
                  ) : null}
                </p>
                <p className="text-xs text-[var(--ink-faint)]">
                  {session.ip ? `${session.ip} · ` : ""}
                  {session.last_active_at
                    ? `Active ${new Date(session.last_active_at).toLocaleString()}`
                    : `Created ${new Date(session.created_at).toLocaleString()}`}
                </p>
              </div>
              {!session.current ? (
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() => revoke(session.id)}
                >
                  Revoke
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
