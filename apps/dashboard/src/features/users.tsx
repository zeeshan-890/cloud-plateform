"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { Modal } from "@/components/ui/modal";
import { Lamp } from "@/components/ui/status";
import { Switch } from "@/components/ui/switch";
import { AREAS, AREA_LABELS, PRESET_DEFS, PRESET_ORDER, grantAreas, grantReveals } from "@/lib/access";
import { errorMessage, users, type Area, type CreateUserInput, type Grant, type Level, type ManagedUser, type Preset, type Project, type Session } from "@/lib/api";
import { relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { Loaded } from "@/components/ui/states";
import { useToast } from "@/components/ui/toast";

export const USERNAME_RE = /^[a-z0-9._-]{3,32}$/;

/* --------------------------------------------------------------- one-time credentials */
export function CredentialsCard({ username, password, onDone }: { username: string; password: string; onDone: () => void }) {
  return (
    <div className="flex flex-col gap-4" data-testid="credentials">
      <div className="well flex items-start gap-3 !py-3" role="alert">
        <Lamp state="warn" pulse className="mt-1" />
        <p className="m-0 text-[14.5px]">
          <strong>Copy these now. The password will not be shown again.</strong> The user must choose their own password at first sign-in.
        </p>
      </div>
      <div className="panel panel--paper !p-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <div className="label mb-1">Username</div>
            <code className="mono block break-all text-[15px]" data-testid="cred-username">{username}</code>
            <div className="mt-2"><CopyButton text={username} label="Copy username" /></div>
          </div>
          <div>
            <div className="label mb-1">Temporary password</div>
            <code className="mono block break-all text-[15px]" data-testid="cred-password">{password}</code>
            <div className="mt-2"><CopyButton text={password} label="Copy password" /></div>
          </div>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <CopyButton text={`Username: ${username}\nPassword: ${password}`} label="Copy both" />
        <Button variant="primary" onClick={onDone} data-testid="cred-done">I have saved it</Button>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ access editor */
const LEVELS: Level[] = ["none", "view", "manage"];

export function GrantEditor({ projects, value, onChange }: { projects: Project[]; value: Grant[]; onChange: (g: Grant[]) => void }) {
  const free = projects.filter((p) => !value.some((g) => g.project_id === p.id));
  const [pick, setPick] = useState("");
  const set = (i: number, g: Grant) => onChange(value.map((x, j) => (j === i ? g : x)));

  const toCustom = (g: Grant): Grant => ({ project_id: g.project_id, preset: "custom", areas: grantAreas(g), secrets_reveal: grantReveals(g) });

  return (
    <div className="flex flex-col gap-4" data-testid="grant-editor">
      {value.length === 0 && <p className="muted m-0 text-[14px]">No project access yet. This person will see nothing until you add a project.</p>}
      {value.map((g, i) => {
        const project = projects.find((p) => p.id === g.project_id);
        const preset: Preset = g.preset ?? "custom";
        const areas = grantAreas(g);
        return (
          <fieldset key={g.project_id} className="well m-0 flex flex-col gap-3" data-testid={`grant-${project?.name ?? g.project_id}`}>
            <legend className="sr-only">Access to {project?.name}</legend>
            <div className="flex flex-wrap items-end gap-3">
              <div className="min-w-0 flex-1">
                <div className="label">Project</div>
                <strong className="text-[16px]">{project?.name ?? g.project_id}</strong>
              </div>
              <div className="w-[190px]">
                <Select
                  label="Access level"
                  value={preset}
                  onChange={(e) => {
                    const p = e.target.value as Preset;
                    set(i, p === "custom" ? toCustom(g) : { project_id: g.project_id, preset: p });
                  }}
                  data-testid="preset-select"
                >
                  {PRESET_ORDER.map((p) => (
                    <option key={p} value={p}>
                      {p === "custom" ? "Custom..." : PRESET_DEFS[p].label}
                    </option>
                  ))}
                </Select>
              </div>
              <Button size="sm" variant="ghost" onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label={`Remove access to ${project?.name}`}>
                <Icon name="x" size={15} /> Remove
              </Button>
            </div>
            {preset !== "custom" ? (
              <p className="m-0 text-[13.5px] opacity-85">
                {PRESET_DEFS[preset as Exclude<Preset, "custom">].blurb}
                {PRESET_DEFS[preset as Exclude<Preset, "custom">].secrets_reveal && " Can reveal secret values."}
              </p>
            ) : (
              <div className="flex flex-col gap-3">
                <div className="ledger-wrap">
                  <table className="ledger">
                    <caption className="sr-only">Per-area access for {project?.name}</caption>
                    <thead>
                      <tr>
                        <th>Area</th>
                        <th>Access</th>
                      </tr>
                    </thead>
                    <tbody>
                      {AREAS.map((a: Area) => (
                        <tr key={a}>
                          <td>{AREA_LABELS[a]}</td>
                          <td>
                            <div role="radiogroup" aria-label={`${AREA_LABELS[a]} access`} className="flex gap-1">
                              {LEVELS.map((l) => (
                                <button
                                  key={l}
                                  type="button"
                                  role="radio"
                                  aria-checked={areas[a] === l}
                                  className={`tag cursor-pointer ${areas[a] === l ? (l === "manage" ? "tag--accent" : l === "view" ? "tag--info" : "tag--idle") : ""}`}
                                  style={areas[a] === l ? undefined : { opacity: 0.55 }}
                                  onClick={() => set(i, { ...g, preset: "custom", areas: { ...areas, [a]: l }, secrets_reveal: grantReveals(g) })}
                                >
                                  {l}
                                </button>
                              ))}
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <Switch label="Can reveal secret values" description="Show plaintext variables and add-on passwords. Leave off unless needed." checked={grantReveals(g)} onChange={(v) => set(i, { ...g, preset: "custom", areas, secrets_reveal: v })} />
              </div>
            )}
          </fieldset>
        );
      })}
      {free.length > 0 && (
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[220px] flex-1">
            <Select label="Add a project" value={pick} onChange={(e) => setPick(e.target.value)} data-testid="grant-add-select">
              <option value="">Choose a project...</option>
              {free.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </div>
          <Button
            disabled={!pick}
            onClick={() => {
              onChange([...value, { project_id: pick, preset: "viewer" }]);
              setPick("");
            }}
            data-testid="grant-add"
          >
            <Icon name="plus" size={15} /> Add project
          </Button>
        </div>
      )}
    </div>
  );
}

/* --------------------------------------------------------------------- create user */
export function CreateUserDialog({ orgId, projects, canMakeAdmin, onClose, onCreated }: { orgId: string; projects: Project[]; canMakeAdmin: boolean; onClose: () => void; onCreated: () => void }) {
  const toast = useToast();
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"admin" | "member">("member");
  const [auto, setAuto] = useState(true);
  const [password, setPassword] = useState("");
  const [mustChange, setMustChange] = useState(true);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<{ username: string; password: string } | null>(null);

  const nameOk = USERNAME_RE.test(username);
  const valid = name.trim() !== "" && nameOk && (auto || password.length >= 8);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const body: CreateUserInput = {
      name: name.trim(),
      username,
      email: email.trim() || undefined,
      password: auto ? undefined : password,
      role,
      must_change_password: mustChange,
      grants: role === "member" ? grants : [],
    };
    try {
      const res = await users.create(orgId, body);
      onCreated();
      const pw = res.generated_password ?? res.password ?? (auto ? "" : password);
      if (pw) setCreated({ username, password: pw });
      else {
        toast.success("User created", username);
        onClose();
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <Modal open onClose={onClose} title="User created" wide locked>
        <CredentialsCard username={created.username} password={created.password} onDone={onClose} />
      </Modal>
    );
  }

  return (
    <Modal
      open
      wide
      onClose={onClose}
      locked={busy}
      title="Create user"
      description="They sign in with the username and password below. No email is needed."
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" type="submit" form="create-user" loading={busy} disabled={!valid} data-testid="user-create-submit">Create user</Button>
        </>
      }
    >
      <form id="create-user" onSubmit={submit} className="flex flex-col gap-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Full name" data-autofocus value={name} onChange={(e) => setName(e.target.value)} data-testid="user-name" />
          <Input
            label="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value.toLowerCase())}
            autoCapitalize="none"
            spellCheck={false}
            error={username && !nameOk ? "3-32 characters: lowercase letters, digits, dot, dash, underscore." : undefined}
            hint="Used to sign in. Cannot be changed later."
            data-testid="user-username"
          />
          <Input label="Email (optional)" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <Select label="Role" value={role} onChange={(e) => setRole(e.target.value as "admin" | "member")} hint={role === "admin" ? "Admins can use every project and manage users." : "Members only see projects you grant them."} data-testid="user-role">
            <option value="member">Member (restricted)</option>
            {canMakeAdmin && <option value="admin">Admin (full access)</option>}
          </Select>
        </div>

        <div className="flex flex-col gap-3">
          <Switch label="Generate a strong password" description="Recommended. You will see it once after creating the user." checked={auto} onChange={setAuto} />
          {!auto && <Input label="Password" type="text" autoComplete="off" value={password} onChange={(e) => setPassword(e.target.value)} hint="At least 8 characters." data-testid="user-password" />}
          <Switch label="Ask them to choose a new password at first sign-in" checked={mustChange} onChange={setMustChange} />
        </div>

        {role === "member" ? (
          <div>
            <h3 className="label mb-3">Project access</h3>
            <GrantEditor projects={projects} value={grants} onChange={setGrants} />
          </div>
        ) : (
          <p className="muted m-0 text-[14px]">Admins automatically have full access to every project.</p>
        )}
        {error && <p role="alert" className="error-text !mt-0">{error}</p>}
      </form>
    </Modal>
  );
}

/* ----------------------------------------------------------------------- edit user */
export function EditUserDialog({ orgId, user, projects, canMakeAdmin, onClose, onSaved }: { orgId: string; user: ManagedUser; projects: Project[]; canMakeAdmin: boolean; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [name, setName] = useState(user.name);
  const [role, setRole] = useState<string>(user.role);
  const [grants, setGrants] = useState<Grant[]>(user.grants ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const restricted = role !== "owner" && role !== "admin";

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (name.trim() !== user.name || role !== user.role) await users.update(orgId, user.id, { name: name.trim(), role });
      if (restricted) await users.setGrants(orgId, user.id, grants);
      toast.success("Access saved", user.username || user.name);
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      wide
      onClose={onClose}
      locked={busy}
      title={`Edit ${user.username || user.name}`}
      description="Changes apply immediately. Their open sessions keep working with the new access."
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" type="submit" form="edit-user" loading={busy} data-testid="user-save">Save</Button>
        </>
      }
    >
      <form id="edit-user" onSubmit={save} className="flex flex-col gap-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Full name" value={name} onChange={(e) => setName(e.target.value)} />
          <Select label="Role" value={role} onChange={(e) => setRole(e.target.value)} disabled={user.role === "owner" || (user.role === "admin" && !canMakeAdmin)}>
            {user.role === "owner" && <option value="owner">Owner</option>}
            <option value="member">Member (restricted)</option>
            {(canMakeAdmin || user.role === "admin") && <option value="admin">Admin (full access)</option>}
          </Select>
        </div>
        {restricted ? <GrantEditor projects={projects} value={grants} onChange={setGrants} /> : <p className="muted m-0 text-[14px]">Owners and admins have full access to every project.</p>}
        {error && <p role="alert" className="error-text !mt-0">{error}</p>}
      </form>
    </Modal>
  );
}

/* ------------------------------------------------------------------------ sessions */
export function UserSessionsDialog({ orgId, user, onClose }: { orgId: string; user: ManagedUser; onClose: () => void }) {
  const toast = useToast();
  const res = useResource(`usess:${user.id}`, (signal) => users.sessions(orgId, user.id, { signal }));
  return (
    <Modal open onClose={onClose} title={`Sessions - ${user.username || user.name}`} description="Where this person is signed in. Revoking signs that device out immediately.">
      <Loaded res={res} isEmpty={(d) => d.length === 0} empty={<p className="muted m-0">No active sessions.</p>} rows={2}>
        {(list: Session[]) => (
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {list.map((s) => (
              <li key={s.id} className="well flex flex-wrap items-center gap-3 !py-2.5">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[14px]">{s.user_agent || "Unknown device"}</div>
                  <div className="mono text-[12px] opacity-75">{s.ip || "-"} - active {relTime(s.last_active_at ?? s.created_at)}</div>
                </div>
                <Button
                  size="sm"
                  variant="danger"
                  onClick={async () => {
                    try {
                      await users.revokeSession(orgId, user.id, s.id);
                      toast.success("Session revoked");
                      res.reload();
                    } catch (err) {
                      toast.error("Could not revoke", err);
                    }
                  }}
                >
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Loaded>
    </Modal>
  );
}

export function userStatus(u: ManagedUser): { label: string; state: "ok" | "idle" | "warn" } {
  if (u.disabled || u.status === "disabled") return { label: "disabled", state: "idle" };
  if (u.must_change_password) return { label: "must set password", state: "warn" };
  return { label: "active", state: "ok" };
}

export function lastLogin(u: ManagedUser): string {
  return u.last_login_at ? `${relTime(u.last_login_at)}` : "never";
}

