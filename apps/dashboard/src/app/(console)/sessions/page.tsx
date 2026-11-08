"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/modal";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { auth, type Session } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { dateTime, relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";

export default function SessionsPage() {
  const toast = useToast();
  const { logout } = useAuth();
  const list = useResource("sessions", (signal) => auth.sessions({ signal }));
  const [revoke, setRevoke] = useState<Session | null>(null);
  const [all, setAll] = useState(false);

  const cols: Column<Session>[] = [
    { key: "d", header: "Device", cell: (s) => <div><div className="max-w-[360px] truncate">{s.user_agent || "Unknown device"}</div>{s.current && <Tag tone="ok">this session</Tag>}</div> },
    { key: "ip", header: "IP", cell: (s) => <span className="mono text-[13px]">{s.ip || "-"}</span> },
    { key: "c", header: "Signed in", cell: (s) => dateTime(s.created_at) },
    { key: "a", header: "Last active", cell: (s) => relTime(s.last_active_at) },
    { key: "x", header: "Actions", srOnlyHeader: true, cell: (s) => <Button size="sm" variant="danger" onClick={() => setRevoke(s)}>{s.current ? "Sign out" : "Revoke"}</Button> },
  ];

  return (
    <>
      <PageHeader title="Sessions" description="Everywhere you are signed in. Revoke anything you do not recognise." actions={<Button variant="danger" onClick={() => setAll(true)}>Sign out everywhere</Button>} />
      <Panel>
        <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No active sessions" />}>
          {(rows) => <DataTable caption="Active sessions" columns={cols} rows={rows} rowKey={(s) => s.id} />}
        </Loaded>
      </Panel>
      <ConfirmDialog
        open={!!revoke}
        title={revoke?.current ? "Sign out" : "Revoke session"}
        body={revoke?.current ? "You will be signed out of this browser." : "That device is signed out and its tokens stop working."}
        confirmLabel={revoke?.current ? "Sign out" : "Revoke"}
        onClose={() => setRevoke(null)}
        onConfirm={async () => {
          await auth.revokeSession(revoke!.id);
          if (revoke!.current) await logout();
          else {
            toast.success("Session revoked");
            list.reload();
          }
        }}
      />
      <ConfirmDialog
        open={all}
        title="Sign out everywhere"
        body="Every session, including this one, is revoked. You will need to sign in again."
        confirmLabel="Sign out everywhere"
        onClose={() => setAll(false)}
        onConfirm={async () => {
          const rows = list.data ?? (await auth.sessions());
          const results = await Promise.allSettled(rows.map((s) => auth.revokeSession(s.id)));
          const failed = results.filter((r) => r.status === "rejected").length;
          if (failed > 0) throw new Error(`${failed} session(s) could not be revoked. Try again.`);
          await logout();
        }}
      />
    </>
  );
}
