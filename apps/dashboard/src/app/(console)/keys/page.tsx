"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { ConfirmDialog } from "@/components/ui/modal";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { auth, type Pat } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";

const SCOPES = [
  { id: "read", label: "Read" },
  { id: "write", label: "Write" },
  { id: "deploy", label: "Deploy" },
];
const EXPIRY = [
  { v: "30", l: "30 days" },
  { v: "90", l: "90 days" },
  { v: "365", l: "1 year" },
  { v: "0", l: "Never" },
];

export default function KeysPage() {
  const toast = useToast();
  const list = useResource("pats", (signal) => auth.pats({ signal }));
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(["read"]);
  const [expiry, setExpiry] = useState("90");
  const [busy, setBusy] = useState(false);
  const [fresh, setFresh] = useState<Pat | null>(null);
  const [revoke, setRevoke] = useState<Pat | null>(null);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const days = Number(expiry);
      const p = await auth.createPat({ name: name.trim(), scopes, expires_in_days: days || undefined, expires_at: days ? new Date(Date.now() + days * 86400000).toISOString() : undefined });
      setFresh(p);
      setName("");
      list.reload();
    } catch (err) {
      toast.error("Could not create key", err);
    } finally {
      setBusy(false);
    }
  };

  const cols: Column<Pat>[] = [
    { key: "n", header: "Name", cell: (p) => <div><div className="font-semibold">{p.name}</div><div className="mono text-[12px] opacity-70">{p.token_prefix ? `${p.token_prefix}...` : ""}</div></div> },
    { key: "s", header: "Scopes", cell: (p) => <div className="flex gap-1">{(p.scopes ?? []).map((s) => <Tag key={s}>{s}</Tag>)}</div> },
    { key: "u", header: "Last used", cell: (p) => (p.last_used_at ? relTime(p.last_used_at) : "never") },
    { key: "e", header: "Expires", cell: (p) => (p.expires_at ? dateTime(p.expires_at) : "never") },
    { key: "x", header: "Actions", srOnlyHeader: true, cell: (p) => <Button size="sm" variant="danger" onClick={() => setRevoke(p)}>Revoke</Button> },
  ];

  return (
    <>
      <PageHeader title="API keys" description="Personal access tokens for the API and CI. A token is shown once, when you create it." />
      <div className="flex flex-col gap-6">
        <Panel title="Create a key">
          <form onSubmit={create} className="grid items-end gap-4 md:grid-cols-[1fr_180px_auto]">
            <Input label="Name" required value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-pipeline" data-testid="pat-name" />
            <Select label="Expires" value={expiry} onChange={(e) => setExpiry(e.target.value)}>
              {EXPIRY.map((x) => <option key={x.v} value={x.v}>{x.l}</option>)}
            </Select>
            <Button type="submit" variant="primary" loading={busy} disabled={!name.trim() || scopes.length === 0} data-testid="pat-create">Create key</Button>
            <fieldset className="m-0 border-0 p-0 md:col-span-3">
              <legend className="label field-label">Scopes</legend>
              <div className="flex flex-wrap gap-5">
                {SCOPES.map((s) => (
                  <label key={s.id} className="flex items-center gap-2 text-[14.5px]">
                    <input type="checkbox" checked={scopes.includes(s.id)} onChange={(e) => setScopes(e.target.checked ? [...scopes, s.id] : scopes.filter((x) => x !== s.id))} />
                    {s.label}
                  </label>
                ))}
              </div>
            </fieldset>
          </form>
          {fresh?.token && (
            <div className="well mt-5 flex flex-col gap-2" role="status">
              <strong>Copy your token now. It will not be shown again.</strong>
              <code className="mono break-all text-[13px]" data-testid="pat-token">{fresh.token}</code>
              <div className="flex gap-2"><CopyButton text={fresh.token} label="Copy token" /><Button size="sm" onClick={() => setFresh(null)}>I have saved it</Button></div>
            </div>
          )}
        </Panel>
        <Panel title="Your keys">
          <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No keys" body="Create one above for scripts and CI." />}>
            {(rows) => <DataTable caption="Personal access tokens" columns={cols} rows={rows} rowKey={(p) => p.id} />}
          </Loaded>
        </Panel>
      </div>
      <ConfirmDialog
        open={!!revoke}
        title="Revoke key"
        body={<>Anything using <strong>{revoke?.name}</strong> stops working immediately.</>}
        confirmLabel="Revoke key"
        onClose={() => setRevoke(null)}
        onConfirm={async () => {
          await auth.revokePat(revoke!.id);
          toast.success("Key revoked");
          list.reload();
        }}
      />
    </>
  );
}
