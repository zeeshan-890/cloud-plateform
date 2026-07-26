"use client";

import { FormEvent, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { OrgMember, Role } from "@/lib/types";
import Link from "next/link";

const ROLES: Role[] = ["owner", "admin", "member", "viewer"];

export default function TeamPage() {
  const { currentOrg, loading: orgLoading } = useOrg();
  const [members, setMembers] = useState<OrgMember[]>([]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [loading, setLoading] = useState(true);
  const [inviting, setInviting] = useState(false);
  const [error, setError] = useState("");
  const [inviteToken, setInviteToken] = useState("");
  const [success, setSuccess] = useState("");

  async function loadMembers() {
    if (!currentOrg) return;
    setLoading(true);
    setError("");
    try {
      const list = await api.listMembers(currentOrg.id);
      setMembers(list);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load members");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (orgLoading) return;
    if (!currentOrg) {
      setMembers([]);
      setLoading(false);
      return;
    }
    loadMembers();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentOrg, orgLoading]);

  async function onInvite(e: FormEvent) {
    e.preventDefault();
    if (!currentOrg) return;
    setInviting(true);
    setError("");
    setSuccess("");
    setInviteToken("");
    try {
      const invite = await api.inviteMember(currentOrg.id, { email, role });
      setSuccess(`Invite sent to ${email}.`);
      if (invite.token) setInviteToken(invite.token);
      setEmail("");
      await loadMembers();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to send invite");
    } finally {
      setInviting(false);
    }
  }

  if (!orgLoading && !currentOrg) {
    return (
      <EmptyState
        title="No organization"
        description="Create an organization before inviting teammates."
        action={
          <Link href="/orgs/new">
            <Button>New organization</Button>
          </Link>
        }
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Team"
        description={`Members of ${currentOrg?.name || "your organization"}.`}
      />

      <section className="mb-10 max-w-lg">
        <h2 className="mb-3 font-[family-name:var(--font-display)] text-lg font-semibold text-[var(--ink)]">
          Invite member
        </h2>
        <form onSubmit={onInvite} className="flex flex-col gap-3">
          {error ? <Alert>{error}</Alert> : null}
          {success ? <Alert tone="success">{success}</Alert> : null}
          {inviteToken ? (
            <Alert tone="info">
              Invite token (share securely):{" "}
              <code className="font-[family-name:var(--font-mono)] text-xs break-all">
                {inviteToken}
              </code>
              . Accept at{" "}
              <Link href="/invite/accept" className="underline">
                /invite/accept
              </Link>
              .
            </Alert>
          ) : null}
          <Input
            label="Email"
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-[var(--ink)]">Role</span>
            <select
              className="rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2.5 text-sm text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)]/30"
              value={role}
              onChange={(e) => setRole(e.target.value as Role)}
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </label>
          <Button type="submit" disabled={inviting} className="w-fit">
            {inviting ? "Sending…" : "Send invite"}
          </Button>
        </form>
      </section>

      <section>
        <h2 className="mb-3 font-[family-name:var(--font-display)] text-lg font-semibold text-[var(--ink)]">
          Members
        </h2>
        {loading ? (
          <p className="text-sm text-[var(--ink-muted)]">Loading members…</p>
        ) : members.length === 0 ? (
          <p className="text-sm text-[var(--ink-muted)]">No members found.</p>
        ) : (
          <ul className="divide-y divide-[var(--border)] border-y border-[var(--border)]">
            {members.map((m) => (
              <li
                key={m.user_id}
                className="flex flex-col gap-1 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-2"
              >
                <div>
                  <p className="font-medium text-[var(--ink)]">{m.name}</p>
                  <p className="text-sm text-[var(--ink-muted)]">{m.email}</p>
                </div>
                <span className="text-xs uppercase tracking-wider text-[var(--ink-faint)]">
                  {m.role}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
