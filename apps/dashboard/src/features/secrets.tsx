"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Textarea } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { useToast } from "@/components/ui/toast";
import { ENVIRONMENTS, errorMessage, secrets, type EnvironmentName, type SecretMeta } from "@/lib/api";
import { relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

interface Edit {
  mode: "create" | "edit";
  secret?: SecretMeta;
}

export function SecretsPanel() {
  const { orgId, projectId, can, canReveal } = useScope();
  const toast = useToast();
  const [env, setEnv] = useState<EnvironmentName>("production");
  const list = useResource(`secrets:${projectId}:${env}`, (signal) => secrets.list(orgId, projectId, env, { signal }));
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState<string | null>(null);
  const [edit, setEdit] = useState<Edit | null>(null);
  const [removing, setRemoving] = useState<SecretMeta | null>(null);

  const reveal = async (s: SecretMeta) => {
    setRevealing(s.name);
    try {
      const v = await secrets.reveal(orgId, projectId, env, s.name);
      setRevealed((m) => ({ ...m, [`${env}:${s.name}`]: v }));
    } catch (err) {
      toast.error("Could not reveal", err);
    } finally {
      setRevealing(null);
    }
  };
  const hide = (s: SecretMeta) =>
    setRevealed((m) => {
      const n = { ...m };
      delete n[`${env}:${s.name}`];
      return n;
    });

  const cols: Column<SecretMeta>[] = [
    { key: "name", header: "Key", cell: (s) => <span className="mono text-[13px] font-medium">{s.name}</span> },
    {
      key: "value",
      header: "Value",
      cell: (s) => {
        const v = revealed[`${env}:${s.name}`];
        return v !== undefined ? (
          <span className="mono break-all text-[12.5px]" data-testid={`secret-value-${s.name}`}>{v || "(empty)"}</span>
        ) : (
          <span className="mono text-[12.5px] opacity-80" aria-label="Value hidden">{s.value_hint || "********"}</span>
        );
      },
    },
    { key: "ver", header: "Version", cell: (s) => <Tag tone="idle">v{s.current_version}</Tag> },
    { key: "upd", header: "Updated", cell: (s) => relTime(s.updated_at ?? s.created_at) },
    {
      key: "act",
      header: "Actions",
      srOnlyHeader: true,
      cell: (s) => {
        const v = revealed[`${env}:${s.name}`];
        return (
          <div className="flex flex-wrap gap-2">
            {canReveal &&
              (v !== undefined ? (
                <>
                  <Button size="sm" onClick={() => hide(s)} aria-label={`Hide ${s.name}`}>
                    <Icon name="eyeOff" size={14} /> Hide
                  </Button>
                  <CopyButton text={v} />
                </>
              ) : (
                <Button size="sm" onClick={() => void reveal(s)} loading={revealing === s.name} aria-label={`Reveal ${s.name}`}>
                  <Icon name="eye" size={14} /> Reveal
                </Button>
              ))}
            {can("member") && (
              <Button size="sm" onClick={() => setEdit({ mode: "edit", secret: s })} aria-label={`Edit ${s.name}`}>
                <Icon name="pen" size={14} /> Edit
              </Button>
            )}
            {can("admin") && (
              <Button size="sm" variant="danger" onClick={() => setRemoving(s)} aria-label={`Delete ${s.name}`}>
                <Icon name="trash" size={14} />
              </Button>
            )}
          </div>
        );
      },
    },
  ];

  return (
    <Panel
      title="Secrets and environment variables"
      actions={
        can("member") && (
          <Button variant="primary" size="sm" onClick={() => setEdit({ mode: "create" })} data-testid="secret-add">
            <Icon name="plus" size={14} /> Add variable
          </Button>
        )
      }
    >
      <p className="muted mb-4 mt-0 text-[14px]">
        Values are encrypted at rest and never listed. {canReveal ? "Revealing a value is recorded in the audit log." : "Values stay hidden for your access level."} Preview deployments only receive <strong>preview</strong> variables.
      </p>
      <Tabs label="Environment" value={env} onChange={setEnv} items={ENVIRONMENTS.map((e) => ({ id: e, label: e }))} className="mb-4" />
      <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title={`No variables in ${env}`} body="Add one for this environment. Add-ons also write their connection URL here." />}>
        {(rows) => <DataTable caption={`Variables in ${env}`} columns={cols} rows={rows} rowKey={(s) => s.name} />}
      </Loaded>

      {edit && (
        <SecretForm
          key={`${edit.mode}:${edit.secret?.name ?? ""}`}
          edit={edit}
          env={env}
          onClose={() => setEdit(null)}
          onSaved={() => {
            setEdit(null);
            setRevealed({});
            list.reload();
          }}
        />
      )}
      <ConfirmDialog
        open={!!removing}
        title="Delete variable"
        body={<>Delete <span className="mono">{removing?.name}</span> from <strong>{env}</strong>? Running apps keep their current value until redeployed.</>}
        confirmLabel="Delete variable"
        requireText={removing?.name}
        onClose={() => setRemoving(null)}
        onConfirm={async () => {
          if (!removing) return;
          await secrets.remove(orgId, projectId, env, removing.name);
          toast.success("Variable deleted", removing.name);
          list.reload();
        }}
      />
    </Panel>
  );
}

function SecretForm({ edit, env, onClose, onSaved }: { edit: Edit; env: string; onClose: () => void; onSaved: () => void }) {
  const { orgId, projectId } = useScope();
  const toast = useToast();
  const [name, setName] = useState(edit.secret?.name ?? "");
  const [value, setValue] = useState("");
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const creating = edit.mode === "create";
  const renamed = !creating && name !== edit.secret?.name;
  const nameOk = NAME_RE.test(name);
  const valid = nameOk && (creating ? value.length > 0 : value.length > 0 || renamed);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (creating) await secrets.create(orgId, projectId, env, { name, value });
      else await secrets.update(orgId, projectId, env, edit.secret!.name, { value: value || undefined, new_name: renamed ? name : undefined });
      toast.success(creating ? "Variable added" : "Variable saved", name);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      locked={busy}
      title={creating ? `Add variable to ${env}` : `Edit ${edit.secret?.name}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" type="submit" form="secret-form" loading={busy} disabled={!valid} data-testid="secret-save">Save</Button>
        </>
      }
    >
      <form id="secret-form" onSubmit={submit} className="flex flex-col gap-4">
        <Input label="Key" data-autofocus value={name} onChange={(e) => setName(e.target.value)} error={name && !nameOk ? "Letters, digits and underscore; cannot start with a digit." : undefined} hint={!creating ? "Changing the key renames the variable and keeps its history." : undefined} autoComplete="off" spellCheck={false} data-testid="secret-name" />
        <div>
          <Textarea
            label={creating ? "Value" : "New value"}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            hint={!creating ? "Leave empty to keep the current value (rename only)." : undefined}
            autoComplete="off"
            spellCheck={false}
            rows={3}
            style={show ? undefined : ({ WebkitTextSecurity: "disc" } as React.CSSProperties)}
            data-testid="secret-value"
          />
          <Button size="sm" className="mt-2" onClick={() => setShow(!show)} aria-pressed={show}>
            <Icon name={show ? "eyeOff" : "eye"} size={14} /> {show ? "Hide value" : "Show value"}
          </Button>
        </div>
        {error && <p role="alert" className="error-text !mt-0">{error}</p>}
      </form>
    </Modal>
  );
}
