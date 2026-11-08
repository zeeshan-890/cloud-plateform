"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Lamp, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { CreateUserDialog, CredentialsCard, EditUserDialog, UserSessionsDialog, lastLogin, userStatus } from "@/features/users";
import { orgs, users, type Invite, type ManagedUser, type Role } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { summarizeGrant } from "@/lib/access";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useResource } from "@/lib/use-resource";

export default function TeamPage() {
  const { currentOrg, can } = useOrg();
  const { user: me } = useAuth();
  const { projects } = useProjects();
  const toast = useToast();
  const orgId = currentOrg?.id;
  const isOwner = can("owner");
  const allowed = can("admin");

  const list = useResource(allowed && orgId ? `users:${orgId}` : null, (signal) => users.list(orgId!, { signal }));
  const invites = useResource(allowed && orgId ? `invites:${orgId}` : null, (signal) => orgs.invites(orgId!, { signal }));

  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<ManagedUser | null>(null);
  const [sessionsFor, setSessionsFor] = useState<ManagedUser | null>(null);
  const [resetFor, setResetFor] = useState<ManagedUser | null>(null);
  const [fresh, setFresh] = useState<{ username: string; password: string } | null>(null);
  const [deleting, setDeleting] = useState<ManagedUser | null>(null);
  const [menu, setMenu] = useState<string | null>(null);

  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [busy, setBusy] = useState(false);
  const [issued, setIssued] = useState<Invite | null>(null);

  if (!allowed) {
    return (
      <>
        <PageHeader title="Team & access" />
        <EmptyState title="Administrators only" body="Only organization owners and admins manage people and their access." />
      </>
    );
  }

  const toggleDisabled = async (u: ManagedUser) => {
    const disable = !(u.disabled || u.status === "disabled");
    try {
      await users.update(orgId!, u.id, { disabled: disable });
      toast.success(disable ? "User disabled" : "User enabled", u.username || u.name);
      list.reload();
    } catch (err) {
      toast.error("Could not update the user", err);
    }
  };

  const cols: Column<ManagedUser>[] = [
    {
      key: "who",
      header: "User",
      cell: (u) => (
        <div>
          <div className="font-semibold">{u.name} {u.id === me?.id && <Tag tone="info">you</Tag>}</div>
          <div className="mono text-[12.5px] opacity-75">{u.username ? `@${u.username}` : u.email}</div>
        </div>
      ),
    },
    { key: "role", header: "Role", cell: (u) => <Tag tone={u.role === "owner" ? "accent" : u.role === "admin" ? "info" : "default"}>{u.role === "member" ? "restricted" : u.role}</Tag> },
    {
      key: "projects",
      header: "Projects",
      cell: (u) => (u.role === "owner" || u.role === "admin" ? <span className="opacity-75">all</span> : <span className="mono">{u.projects_count ?? u.grants?.length ?? 0}</span>),
    },
    { key: "login", header: "Last login", cell: (u) => lastLogin(u) },
    {
      key: "status",
      header: "Status",
      cell: (u) => {
        const s = userStatus(u);
        return (
          <span className="inline-flex items-center gap-2">
            <Lamp state={s.state} />
            <span className="mono text-[12px]">{s.label}</span>
          </span>
        );
      },
    },
    {
      key: "act",
      header: "Actions",
      srOnlyHeader: true,
      cell: (u) => {
        const self = u.id === me?.id;
        const locked = u.role === "owner" || (u.role === "admin" && !isOwner);
        return (
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => setEditing(u)} disabled={u.role === "owner" && !isOwner} aria-label={`Edit access for ${u.username || u.name}`}>
              <Icon name="pen" size={14} /> Edit access
            </Button>
            {!self && !locked && (
              <div className="relative">
                <Button size="sm" icon aria-label={`More actions for ${u.username || u.name}`} aria-haspopup="menu" aria-expanded={menu === u.id} onClick={() => setMenu(menu === u.id ? null : u.id)}>
                  <Icon name="more" size={16} />
                </Button>
                {menu === u.id && (
                  <div role="menu" className="panel !p-1.5" style={{ position: "absolute", right: 0, top: "110%", zIndex: 40, minWidth: 190 }} onMouseLeave={() => setMenu(null)}>
                    {[
                      { k: "reset", label: "Reset password", run: () => setResetFor(u) },
                      { k: "sess", label: "Sessions", run: () => setSessionsFor(u) },
                      { k: "dis", label: u.disabled || u.status === "disabled" ? "Enable user" : "Disable user", run: () => void toggleDisabled(u) },
                      { k: "del", label: "Delete user", run: () => setDeleting(u) },
                    ].map((m) => (
                      <button
                        key={m.k}
                        role="menuitem"
                        type="button"
                        className="nav-item w-full text-left"
                        style={{ background: "none", cursor: "pointer", fontSize: 14 }}
                        onClick={() => {
                          setMenu(null);
                          m.run();
                        }}
                      >
                        {m.label}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      },
    },
  ];

  const invite = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const inv = await orgs.invite(orgId!, { email: email.trim(), role });
      setIssued({ ...inv, email: inv.email || email.trim() });
      setEmail("");
      invites.reload();
    } catch (err) {
      toast.error("Could not invite", err);
    } finally {
      setBusy(false);
    }
  };
  const link = issued?.token && typeof window !== "undefined" ? `${window.location.origin}/register/?invite_token=${encodeURIComponent(issued.token)}&email=${encodeURIComponent(issued.email)}` : "";

  const others = (list.data ?? []).filter((u) => u.id !== me?.id);

  return (
    <>
      <PageHeader
        title="Team & access"
        eyebrow={currentOrg?.name}
        description="Create accounts for your people and choose exactly which projects and areas each one can use."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)} data-testid="user-create">
            <Icon name="plus" size={16} /> Create user
          </Button>
        }
      />
      <div className="flex flex-col gap-6">
        <Panel title="People">
          <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="Just you so far" body="Create the first teammate and give them access to a project." action={<Button variant="primary" onClick={() => setCreating(true)}>Create user</Button>} />}>
            {(rows) => (
              <>
                <DataTable caption="People in this organization" columns={cols} rows={rows} rowKey={(u) => u.id} />
                {others.length === 0 && <p className="muted mb-0 mt-4 text-[14px]">Next step: create a user and pick the projects they may open.</p>}
              </>
            )}
          </Loaded>
          <p className="muted mb-0 mt-4 text-[13px]">Restricted members only see the projects and areas you grant. Owners and admins see everything.</p>
          {(list.data ?? []).some((u) => u.grants && u.grants.length > 0) && (
            <details className="mt-3">
              <summary className="label cursor-pointer">Who can open what</summary>
              <ul className="muted mb-0 mt-2 list-none p-0 text-[13.5px]">
                {(list.data ?? []).filter((u) => (u.grants ?? []).length > 0).map((u) => (
                  <li key={u.id} className="mb-1">
                    <strong>{u.username || u.name}</strong>:{" "}
                    {(u.grants ?? []).map((g) => `${projects.find((p) => p.id === g.project_id)?.name ?? "project"} (${summarizeGrant(g)})`).join(", ")}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </Panel>

        <details className="panel panel--paper !p-4">
          <summary className="label cursor-pointer">Advanced: invite someone by email</summary>
          <form onSubmit={invite} className="mt-4 grid items-end gap-4 sm:grid-cols-[1fr_180px_auto]">
            <Input label="Email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} data-testid="invite-email" />
            <Select label="Role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="member">member</option>
              <option value="viewer">viewer</option>
              {isOwner && <option value="admin">admin</option>}
            </Select>
            <Button type="submit" loading={busy} disabled={!email.trim()} data-testid="invite-submit">Send invite</Button>
          </form>
          {issued && (
            <div className="well mt-4 flex flex-col gap-2">
              <p className="m-0 text-[14.5px]">Invitation for <strong>{issued.email}</strong>. {link ? "Send them this link. It is shown only now." : "The server did not return a link."}</p>
              {link && (<><code className="mono break-all text-[12.5px]">{link}</code><div><CopyButton text={link} label="Copy invite link" /></div></>)}
            </div>
          )}
          <Loaded res={invites} isEmpty={(d) => d.length === 0} empty={<p className="muted mb-0 mt-4 text-[14px]">No pending invitations.</p>} rows={1}>
            {(rows) => (
              <div className="mt-4">
                <DataTable
                  caption="Pending invitations"
                  rows={rows}
                  rowKey={(i) => i.id}
                  columns={[
                    { key: "e", header: "Email", cell: (i) => <span className="mono text-[13px]">{i.email}</span> },
                    { key: "r", header: "Role", cell: (i) => <Tag>{i.role}</Tag> },
                    { key: "c", header: "Sent", cell: (i) => dateTime(i.created_at) },
                    {
                      key: "x",
                      header: "Actions",
                      srOnlyHeader: true,
                      cell: (i) => (
                        <Button size="sm" onClick={async () => { try { await orgs.revokeInvite(orgId!, i.id); invites.reload(); } catch (err) { toast.error("Could not revoke", err); } }}>
                          Revoke
                        </Button>
                      ),
                    },
                  ]}
                />
              </div>
            )}
          </Loaded>
        </details>
      </div>

      {creating && orgId && <CreateUserDialog orgId={orgId} projects={projects} canMakeAdmin={isOwner} onClose={() => setCreating(false)} onCreated={() => list.reload()} />}
      {editing && orgId && <EditUserDialog orgId={orgId} user={editing} projects={projects} canMakeAdmin={isOwner} onClose={() => setEditing(null)} onSaved={() => list.reload()} />}
      {sessionsFor && orgId && <UserSessionsDialog orgId={orgId} user={sessionsFor} onClose={() => setSessionsFor(null)} />}

      <ConfirmDialog
        open={!!resetFor}
        title="Reset password"
        danger={false}
        body={<><strong>{resetFor?.username || resetFor?.name}</strong> is signed out everywhere and gets a new temporary password. You will see it once.</>}
        confirmLabel="Reset password"
        onClose={() => setResetFor(null)}
        onConfirm={async () => {
          const target = resetFor!;
          const res = await users.resetPassword(orgId!, target.id);
          setFresh({ username: target.username || target.email || target.name, password: res.generated_password ?? res.password ?? "" });
          list.reload();
        }}
      />
      <Modal open={!!fresh} onClose={() => setFresh(null)} title="New temporary password" locked>
        {fresh && (fresh.password ? <CredentialsCard username={fresh.username} password={fresh.password} onDone={() => setFresh(null)} /> : <p className="m-0">The server did not return a password. Reset again to generate one.</p>)}
      </Modal>
      <ConfirmDialog
        open={!!deleting}
        title="Delete user"
        body={<>This permanently deletes <strong>{deleting?.username || deleting?.name}</strong> and signs them out everywhere.</>}
        confirmLabel="Delete user"
        requireText={deleting?.username || deleting?.name}
        onClose={() => setDeleting(null)}
        onConfirm={async () => {
          await users.remove(orgId!, deleting!.id);
          toast.success("User deleted");
          list.reload();
        }}
      />
    </>
  );
}
