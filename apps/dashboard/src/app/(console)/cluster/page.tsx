"use client";

import { ServersPanel } from "@/features/servers";
import { Gauge } from "@/components/ui/gauge";
import { LcdCounter } from "@/components/ui/lcd";
import { PageHeader, Panel, Readout } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Lamp } from "@/components/ui/status";
import { system } from "@/lib/api";
import { bytes, duration, pct } from "@/lib/format";
import { useOrg } from "@/lib/org-context";
import { useResource } from "@/lib/use-resource";

export default function ClusterPage() {
  const { isPlatformAdmin } = useOrg();
  const res = useResource(isPlatformAdmin ? "cluster" : null, (signal) => system.cluster({ signal }), { interval: 5000 });

  if (!isPlatformAdmin) {
    return (
      <>
        <PageHeader title="Cluster" />
        <EmptyState title="Platform administrators only" body="Host metrics are visible to the platform administrator." />
      </>
    );
  }
  return (
    <>
      <PageHeader title="Cluster" description="This installation runs on a single node. Figures are read live from the host and Docker." />
      <Loaded res={res} rows={4}>
        {({ node: n }) => {
          const mem = pct(n.memory_used_bytes, n.memory_total_bytes);
          const disk = pct(n.disk_used_bytes, n.disk_total_bytes);
          const cpu = n.cpu_percent ?? 0;
          return (
            <div className="flex flex-col gap-6">
              <Panel title={n.hostname || "Node"} actions={<span className="inline-flex items-center gap-2"><Lamp state="ok" pulse /> <span className="label">online</span></span>}>
                <div className="flex flex-wrap justify-around gap-6">
                  <Gauge label="CPU" value={cpu} readout={`${cpu.toFixed(0)}%${n.cpu_count ? ` of ${n.cpu_count} cores` : ""}`} />
                  <Gauge label="Memory" value={mem} readout={`${bytes(n.memory_used_bytes)} / ${bytes(n.memory_total_bytes)}`} />
                  <Gauge label="Disk" value={disk} readout={`${bytes(n.disk_used_bytes)} / ${bytes(n.disk_total_bytes)}`} />
                </div>
              </Panel>
              <div className="flex flex-wrap gap-4">
                <LcdCounter label="Containers running" value={n.containers_running ?? "n/a"} width={3} />
                <LcdCounter label="Containers total" value={n.containers_total ?? "n/a"} width={3} />
                {n.load && n.load[0] !== undefined && <LcdCounter label="Load 1m" value={n.load[0].toFixed(2)} />}
              </div>
              <Panel title="Host" variant="paper">
                <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
                  <Readout label="Hostname">{n.hostname || "-"}</Readout>
                  <Readout label="Platform">{[n.os, n.arch].filter(Boolean).join(" / ") || "-"}</Readout>
                  <Readout label="Uptime">{duration(n.uptime_seconds)}</Readout>
                  <Readout label="Docker">{n.docker_version || "-"}</Readout>
                </div>
              </Panel>
            </div>
          );
        }}
      </Loaded>
      <div className="mt-6"><ServersPanel /></div>
    </>
  );
}
