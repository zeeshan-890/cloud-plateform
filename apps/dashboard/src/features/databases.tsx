"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input } from "@/components/ui/field";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { StatusBadge, Tag } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { databases, errorMessage, slugify, type ManagedDatabase } from "@/lib/api";
import { dateTime, isTransient } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

function ConnModal({ db, onClose }: { db: ManagedDatabase; onClose: () => void }) {
  const { orgId, projectId, canReveal } = useScope();
  const [reveal, setReveal] = useState(false);
  const conn = useResource(`dbconn:${db.id}:${reveal}`, (signal) => databases.connection(orgId, projectId, db.id, reveal, { signal }));
  return (
    <Modal open onClose={onClose} title={`${db.name} connection`} description="Passwords stay masked unless an admin reveals them.">
      <Loaded res={conn} rows={2}>
        {(c) => {
          const url = c.url ?? c.masked_url;
          return (
            <div className="well flex flex-col gap-3">
              {url ? <code className="mono break-all text-[13px]">{url}</code> : <p className="m-0">{db.connection_hint || "No connection details reported."}</p>}
              <div className="flex flex-wrap gap-2">
                {url && <CopyButton text={url} label="Copy URL" />}
                {canReveal && <Button size="sm" onClick={() => setReveal(!reveal)}>{reveal ? "Mask password" : "Reveal password"}</Button>}
              </div>
            </div>
          );
        }}
      </Loaded>
    </Modal>
  );
}

export function DatabasesPanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const list = useResource(`dbs:${projectId}`, (signal) => databases.list(orgId, projectId, { signal }), { interval: 4000 });
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [conn, setConn] = useState<ManagedDatabase | null>(null);
  const [del, setDel] = useState<ManagedDatabase | null>(null);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      await databases.create(orgId, projectId, { name: slugify(name), env: "production" });
      setName("");
      toast.success("Database is being provisioned");
      list.reload();
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      {can("member") && (
        <Panel title="New managed database">
          <form onSubmit={create} className="flex flex-wrap items-start gap-3">
            <div className="min-w-[240px] flex-1">
              <Input label="Name" hideLabel placeholder="main-db" value={name} onChange={(e) => setName(e.target.value)} error={err || undefined} hint="A dedicated PostgreSQL container on the project network." />
            </div>
            <Button type="submit" variant="primary" loading={busy} disabled={!name.trim()}>Create database</Button>
          </form>
        </Panel>
      )}
      <Panel title="Databases">
        <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No databases" body="Create a managed database above." />}>
          {(rows) => (
            <div className="grid-auto">
              {rows.map((d) => (
                <Panel key={d.id} variant="paper" as="article" className="flex flex-col gap-3">
                  <div className="flex flex-wrap items-center gap-3">
                    <h3 className="text-[20px]">{d.name}</h3>
                    {d.engine && <Tag>{d.engine}</Tag>}
                    <StatusBadge status={d.status} />
                  </div>
                  {d.error && <p role="alert" className="error-text !mt-0">{d.error}</p>}
                  <p className="mono m-0 text-[12.5px] opacity-80">created {dateTime(d.created_at)}</p>
                  <div className="flex gap-2">
                    <Button size="sm" onClick={() => setConn(d)} disabled={isTransient(d.status) || d.status === "failed"}>Connection</Button>
                    {can("admin") && <Button size="sm" variant="danger" className="ml-auto" onClick={() => setDel(d)}>Delete</Button>}
                  </div>
                </Panel>
              ))}
            </div>
          )}
        </Loaded>
      </Panel>
      {conn && <ConnModal db={conn} onClose={() => setConn(null)} />}
      <ConfirmDialog
        open={!!del}
        title="Delete database"
        body={<>All data in <strong>{del?.name}</strong> is permanently destroyed.</>}
        confirmLabel="Delete database and data"
        requireText={del?.name}
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          await databases.remove(orgId, projectId, del.id);
          toast.success("Database deleted");
          list.reload();
        }}
      />
    </div>
  );
}
