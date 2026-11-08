"use client";

import { useState } from "react";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { LogTape, textToLines } from "@/components/ui/code";
import { Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, Loaded, SkeletonRows } from "@/components/ui/states";
import { Lamp, StatusBadge, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { builds, type Build, type SecurityScan } from "@/lib/api";
import { dateTime, isTransient, relTime, shortSha } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

function ScanSummary({ scan }: { scan: SecurityScan | null | undefined }) {
  if (!scan) {
    return (
      <div className="well flex items-start gap-3">
        <Lamp state="idle" className="mt-1" />
        <p className="m-0 text-[14.5px]">No security scan was reported for this build. Scan results appear here when the build service attaches them.</p>
      </div>
    );
  }
  const total = (scan.critical ?? 0) + (scan.high ?? 0) + (scan.medium ?? 0) + (scan.low ?? 0);
  const clean = total === 0 && scan.status !== "failed";
  return (
    <div className="well flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <Lamp state={clean ? "ok" : (scan.critical ?? 0) + (scan.high ?? 0) > 0 ? "fail" : "warn"} />
        <strong>{clean ? "No known vulnerabilities" : `${total} finding${total === 1 ? "" : "s"}`}</strong>
        {scan.scanner && <Tag tone="idle">{scan.scanner}</Tag>}
      </div>
      <div className="flex flex-wrap gap-2">
        <Tag tone="fail">critical {scan.critical ?? 0}</Tag>
        <Tag tone="fail">high {scan.high ?? 0}</Tag>
        <Tag tone="warn">medium {scan.medium ?? 0}</Tag>
        <Tag tone="info">low {scan.low ?? 0}</Tag>
      </div>
      {scan.summary && <p className="m-0 text-[14px]">{scan.summary}</p>}
      {scan.findings && scan.findings.length > 0 && (
        <ul className="m-0 list-none p-0 text-[13px]">
          {scan.findings.slice(0, 10).map((f, i) => (
            <li key={i} className="mono border-t border-[var(--line)] py-1.5">
              <span className="font-medium">{f.severity?.toUpperCase()}</span> {f.package} {f.id} {f.fixed_in ? `(fixed in ${f.fixed_in})` : ""}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function BuildDetail({ build }: { build: Build }) {
  const { orgId, projectId } = useScope();
  const live = isTransient(build.status);
  const logs = useResource(`blog:${build.id}`, (signal) => builds.logs(orgId, projectId, build.id, { signal }), { interval: live ? 2000 : undefined });
  const full = useResource(`build:${build.id}`, (signal) => builds.get(orgId, projectId, build.id, { signal }), { interval: live ? 4000 : undefined });
  const current = full.data ?? build;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <StatusBadge status={current.status} />
        <span className="mono text-[13px]">
          {current.git_branch || "-"} @ {shortSha(current.git_sha)}
        </span>
        {current.framework && <Tag tone="info">{current.framework}</Tag>}
        <span className="muted text-[13px]">started {dateTime(current.started_at ?? current.created_at)}</span>
        {live && <Tag tone="warn">live</Tag>}
      </div>
      {current.error && (
        <p role="alert" className="error-text !mt-0">
          {current.error}
        </p>
      )}
      <div>
        <h3 className="label mb-2">Build log</h3>
        {logs.error && !logs.data ? (
          <ErrorState error={logs.error} onRetry={logs.reload} />
        ) : logs.loading ? (
          <SkeletonRows rows={3} label="Loading build log" />
        ) : (
          <LogTape label={`Build log for ${build.id.slice(0, 8)}`} lines={textToLines(logs.data?.logs ?? current.logs ?? "")} follow={live} empty="The builder has not printed anything yet." />
        )}
      </div>
      <div>
        <h3 className="label mb-2">Security scan</h3>
        <ScanSummary scan={current.security_scan} />
      </div>
    </div>
  );
}

export function BuildsPanel() {
  const { orgId, projectId } = useScope();
  const params = useSearchParams();
  const list = useResource(`builds:${projectId}`, (signal) => builds.list(orgId, projectId, { signal }), { interval: 5000 });
  const [picked, setPicked] = useState<string | null>(params.get("build"));
  const rows = [...(list.data ?? [])].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
  const selected = rows.find((b) => b.id === picked) ?? null;

  const cols: Column<Build>[] = [
    { key: "s", header: "Status", cell: (b) => <StatusBadge status={b.status} /> },
    { key: "src", header: "Source", cell: (b) => <span className="mono text-[13px]">{b.git_branch || "-"} @ {shortSha(b.git_sha)}</span> },
    { key: "fw", header: "Framework", cell: (b) => (b.framework ? <Tag>{b.framework}</Tag> : <span className="opacity-60">-</span>) },
    { key: "w", header: "Started", cell: (b) => relTime(b.started_at ?? b.created_at) },
    {
      key: "a",
      header: "Open",
      srOnlyHeader: true,
      cell: (b) => (
        <Button size="sm" aria-pressed={picked === b.id} onClick={() => setPicked(picked === b.id ? null : b.id)}>
          {picked === b.id ? "Hide log" : "View log"}
        </Button>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-6">
      <Panel title="Builds">
        <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No builds yet" body="Builds appear when a deployment starts." />}>
          {() => <DataTable caption="Builds" columns={cols} rows={rows} rowKey={(b) => b.id} />}
        </Loaded>
      </Panel>
      {selected && (
        <Panel title={`Build ${selected.id.slice(0, 8)}`} variant="paper">
          <BuildDetail key={selected.id} build={selected} />
        </Panel>
      )}
    </div>
  );
}
