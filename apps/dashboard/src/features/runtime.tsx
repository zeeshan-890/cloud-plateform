"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { LogTape } from "@/components/ui/code";
import { Icon } from "@/components/ui/icons";
import { ConfirmDialog } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, Loaded, SkeletonRows } from "@/components/ui/states";
import { StatusBadge, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { runtime, type RuntimeInstance } from "@/lib/api";
import { relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

function InstanceLogs({ inst }: { inst: RuntimeInstance }) {
  const { orgId, projectId } = useScope();
  const [live, setLive] = useState(true);
  const logs = useResource(`rlogs:${inst.id}`, (signal) => runtime.logs(orgId, projectId, inst.id, 300, { signal }), { interval: live ? 3000 : undefined });
  return (
    <Panel title={`Logs - ${inst.container_name || inst.id.slice(0, 8)}`} variant="paper" actions={<Button size="sm" aria-pressed={live} onClick={() => setLive(!live)}>{live ? "Pause tail" : "Resume tail"}</Button>}>
      {logs.error && !logs.data ? (
        <ErrorState error={logs.error} onRetry={logs.reload} />
      ) : logs.loading ? (
        <SkeletonRows rows={3} label="Loading logs" />
      ) : (
        <LogTape label="Container output" follow={live} lines={(logs.data?.entries ?? []).map((e) => ({ text: e.message, level: e.level, at: e.logged_at ? new Date(e.logged_at).toLocaleTimeString() : undefined }))} empty="This container has not written any output." />
      )}
    </Panel>
  );
}

export function RuntimePanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const list = useResource(`inst:${projectId}`, (signal) => runtime.instances(orgId, projectId, { signal }), { interval: 5000 });
  const containers = useResource(`cont:${projectId}`, (signal) => runtime.containers(orgId, projectId, { signal }), { interval: 10000 });
  const [picked, setPicked] = useState<string | null>(null);
  const [removing, setRemoving] = useState<RuntimeInstance | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const act = async (i: RuntimeInstance, action: "start" | "stop" | "restart") => {
    setBusyId(i.id);
    try {
      await runtime.action(orgId, projectId, i.id, action);
      toast.success(`Instance ${action} requested`);
      list.reload();
    } catch (err) {
      toast.error(`Could not ${action}`, err);
    } finally {
      setBusyId(null);
    }
  };

  const cols: Column<RuntimeInstance>[] = [
    { key: "name", header: "Instance", cell: (i) => (<div><div className="mono text-[13px] font-medium">{i.container_name || i.id.slice(0, 8)}</div><div className="mono max-w-[260px] truncate text-[12px] opacity-70">{i.image_ref}</div></div>) },
    { key: "status", header: "State", cell: (i) => <StatusBadge status={i.status} /> },
    { key: "health", header: "Health", cell: (i) => <StatusBadge status={i.health_status || "none"} /> },
    { key: "kind", header: "Kind", cell: (i) => <div className="flex gap-1"><Tag>{i.role || i.kind}</Tag>{i.is_preview && <Tag tone="info">preview</Tag>}{i.color && <Tag tone="idle">{i.color}</Tag>}</div> },
    { key: "r", header: "Restarts", cell: (i) => <span className="mono">{i.restart_count ?? 0}</span> },
    { key: "up", header: "Updated", cell: (i) => relTime(i.updated_at) },
    {
      key: "act",
      header: "Actions",
      srOnlyHeader: true,
      cell: (i) => (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" aria-pressed={picked === i.id} onClick={() => setPicked(picked === i.id ? null : i.id)}>
            Logs
          </Button>
          {can("member") && (
            <>
              <Button size="sm" icon aria-label={`Start ${i.container_name}`} disabled={busyId === i.id || i.status === "running"} onClick={() => void act(i, "start")}>
                <Icon name="play" size={14} />
              </Button>
              <Button size="sm" icon aria-label={`Stop ${i.container_name}`} disabled={busyId === i.id || i.status !== "running"} onClick={() => void act(i, "stop")}>
                <Icon name="stop" size={14} />
              </Button>
              <Button size="sm" icon aria-label={`Restart ${i.container_name}`} disabled={busyId === i.id} onClick={() => void act(i, "restart")}>
                <Icon name="restart" size={14} />
              </Button>
            </>
          )}
          {can("admin") && (
            <Button size="sm" variant="danger" icon aria-label={`Delete ${i.container_name}`} onClick={() => setRemoving(i)}>
              <Icon name="trash" size={14} />
            </Button>
          )}
        </div>
      ),
    },
  ];

  const sel = list.data?.find((i) => i.id === picked);
  return (
    <div className="flex flex-col gap-6">
      <Panel title="Runtime instances">
        <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No instances" body="Instances appear when a deployment is released." />}>
          {(rows) => <DataTable caption="Runtime instances" columns={cols} rows={rows} rowKey={(i) => i.id} />}
        </Loaded>
      </Panel>
      {sel && <InstanceLogs key={sel.id} inst={sel} />}
      <Panel title="Project containers" variant="paper">
        <Loaded res={containers} isEmpty={(d) => d.length === 0} empty={<p className="m-0 opacity-75">No containers are running for this project.</p>}>
          {(rows) => (
            <DataTable
              caption="Containers"
              rows={rows}
              rowKey={(c) => c.id}
              columns={[
                { key: "n", header: "Container", cell: (c) => <span className="mono text-[13px]">{c.name}</span> },
                { key: "i", header: "Image", cell: (c) => <span className="mono text-[12.5px]">{c.image}</span> },
                { key: "s", header: "State", cell: (c) => <StatusBadge status={c.state} /> },
              ]}
            />
          )}
        </Loaded>
      </Panel>
      <ConfirmDialog
        open={!!removing}
        title="Delete instance"
        body={<>The container <span className="mono">{removing?.container_name}</span> is stopped and removed. A redeploy creates a new one.</>}
        confirmLabel="Delete instance"
        requireText={removing?.container_name || "delete"}
        onClose={() => setRemoving(null)}
        onConfirm={async () => {
          if (!removing) return;
          await runtime.remove(orgId, projectId, removing.id);
          toast.success("Instance deleted");
          list.reload();
        }}
      />
    </div>
  );
}
