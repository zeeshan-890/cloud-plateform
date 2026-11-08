"use client";

import Link from "next/link";
import { LinkButton } from "@/components/ui/button";
import { Gauge } from "@/components/ui/gauge";
import { LcdCounter } from "@/components/ui/lcd";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, SkeletonRows } from "@/components/ui/states";
import { Lamp, StatusBadge, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { projectHref } from "@/components/shell/nav";
import { appState, projectUrl, useSnapshots, type ProjectSnapshot } from "@/features/snapshots";
import { system, type Deployment } from "@/lib/api";
import { bytes, pct, relTime, shortSha } from "@/lib/format";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useResource } from "@/lib/use-resource";

interface Row extends Deployment {
  projectName: string;
}

function Checklist({ hasProject, firstId }: { hasProject: boolean; firstId?: string }) {
  const steps = [
    { n: 1, title: "Create a project", text: "A project holds one app and everything it needs.", href: "/projects/new", cta: "Create project", done: hasProject },
    { n: 2, title: "Connect a repository", text: "Tell JP Cloud where your code lives.", href: firstId ? projectHref(firstId, "repository") : "/projects/new", cta: "Connect repository", done: false },
    { n: 3, title: "Deploy", text: "Build it and put it online.", href: firstId ? projectHref(firstId, "deployments") : "/projects/new", cta: "Deploy", done: false },
  ];
  const current = steps.findIndex((s) => !s.done);
  return (
    <Panel variant="leather" title="Get started in three steps" data-testid="checklist">
      <ol className="m-0 flex list-none flex-col gap-4 p-0">
        {steps.map((s, i) => (
          <li key={s.n} className="flex flex-wrap items-center gap-4" aria-current={i === current ? "step" : undefined}>
            <Lamp state={s.done ? "ok" : i === current ? "warn" : "idle"} pulse={i === current} large label={s.done ? "done" : i === current ? "next" : "later"} />
            <div className="min-w-0 flex-1">
              <p className="m-0 font-semibold">{s.n}. {s.title}</p>
              <p className="m-0 text-[14px] opacity-85">{s.text}</p>
            </div>
            {i === current && (
              <LinkButton href={s.href} variant="primary">
                {s.cta}
              </LinkButton>
            )}
          </li>
        ))}
      </ol>
    </Panel>
  );
}

export default function OverviewPage() {
  const { currentOrg, can, access, isPlatformAdmin, restricted } = useOrg();
  const { projects, activeProject, loading, error, reload } = useProjects();
  const snaps = useSnapshots(currentOrg?.id, projects, access);
  const host = useResource(isPlatformAdmin ? "ov-host" : null, (signal) => system.cluster({ signal }), { interval: 10000 });

  const header = (
    <PageHeader
      title="Overview"
      eyebrow={currentOrg?.name}
      description={
        activeProject ? (
          <>
            Everything in this organization at a glance. Pick up where you left off: <Link href={projectHref(activeProject.id)}>{activeProject.name}</Link>.
          </>
        ) : (
          "Everything in this organization at a glance."
        )
      }
      actions={
        projects.length > 0 &&
        can("admin") && (
          <LinkButton href="/projects/new" variant="primary">
            New project
          </LinkButton>
        )
      }
    />
  );

  const hero = header;
  if (loading) return <>{hero}<SkeletonRows rows={5} label="Loading overview" /></>;
  if (error) return <>{hero}<ErrorState error={error} onRetry={reload} /></>;
  if (projects.length === 0 && restricted) {
    return (
      <>
        {hero}
        <EmptyState title="No projects shared with you yet" body="An administrator gives you access project by project. Ask them to add you to the project you work on." />
      </>
    );
  }
  if (projects.length === 0) return <>{hero}<Checklist hasProject={false} /></>;

  const data: ProjectSnapshot[] | undefined = snaps.data;
  const running = data?.filter((s) => appState(s) === "running").length;
  const allDeps: Row[] = (data ?? [])
    .flatMap((s) => (s.deployments ?? []).map((d) => ({ ...d, projectName: s.project.name })))
    .sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
  const failedDeps = allDeps.filter((d) => d.status === "failed").length;
  const degraded = data?.filter((s) => s.failed.length > 0).length ?? 0;
  const attention = (data ?? []).filter((s) => appState(s) === "failed" || s.deployments?.[0]?.status === "failed");
  const neverDeployed = data ? data.every((s) => (s.deployments ?? []).length === 0 && s.deployments !== null) : false;

  const node = host.data?.node;

  const cols: Column<Row>[] = [
    { key: "p", header: "Project", cell: (d) => <Link href={projectHref(d.project_id, "deployments")}>{d.projectName}</Link> },
    { key: "s", header: "Status", cell: (d) => <StatusBadge status={d.status} /> },
    { key: "b", header: "Branch", cell: (d) => <span className="mono text-[13px]">{d.git_branch || "-"}</span> },
    { key: "c", header: "Commit", cell: (d) => <span className="mono text-[13px]">{shortSha(d.git_sha)}</span> },
    { key: "t", header: "When", cell: (d) => relTime(d.created_at) },
  ];

  return (
    <>
      {hero}
      <div className="flex flex-col gap-6">
        {neverDeployed && can("admin") && <Checklist hasProject firstId={projects[0].id} />}

        <div className="grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 150px), 1fr))" }}>
          <LcdCounter label="Projects" value={projects.length} width={3} />
          <LcdCounter label="Apps running" value={running ?? "..."} width={3} unit={data ? `/ ${projects.length}` : undefined} />
          <LcdCounter label="Failed deploys" value={data ? failedDeps : "..."} width={3} />
          <LcdCounter label="Services degraded" value={data ? degraded : "..."} width={3} />
        </div>

        {attention.length > 0 && (
          <Panel title="Needs attention">
            <ul className="m-0 flex list-none flex-col gap-2 p-0">
              {attention.map((s) => (
                <li key={s.project.id} className="well flex flex-wrap items-center gap-3 !py-2.5">
                  <Lamp state="fail" />
                  <Link href={projectHref(s.project.id, appState(s) === "failed" ? "runtime" : "deployments")} className="font-semibold">
                    {s.project.name}
                  </Link>
                  <span className="text-[14px] opacity-85">{appState(s) === "failed" ? "an instance is failing" : "the latest deployment failed"}</span>
                  <Tag tone="fail" className="ml-auto">look into it</Tag>
                </li>
              ))}
            </ul>
          </Panel>
        )}

        {degraded > 0 && (
          <Panel variant="paper" className="!py-3">
            <div className="flex items-center gap-3">
              <Lamp state="warn" pulse />
              <p className="m-0 text-[14.5px]">{degraded} project{degraded === 1 ? "" : "s"} could not report every service. The counts above only include what answered.</p>
            </div>
          </Panel>
        )}

        <Panel title="Projects at a glance" actions={<Link href="/projects">All projects</Link>}>
          {snaps.error && !snaps.data ? (
            <ErrorState error={snaps.error} onRetry={snaps.reload} />
          ) : snaps.loading ? (
            <SkeletonRows rows={3} label="Loading projects" />
          ) : (
            <ul className="m-0 flex list-none flex-col gap-2 p-0">
              {(data ?? []).map((s) => {
                const st = appState(s);
                const url = projectUrl(s);
                return (
                  <li key={s.project.id} className="well flex flex-wrap items-center gap-x-4 gap-y-1 !py-2.5">
                    <Lamp state={st === "running" ? "ok" : st === "failed" ? "fail" : st === "unknown" ? "info" : "idle"} label={st} />
                    <Link href={projectHref(s.project.id)} className="min-w-[8rem] font-semibold">{s.project.name}</Link>
                    <span className="mono min-w-0 flex-1 truncate text-[12.5px] opacity-80">{url ? url.replace(/^https?:\/\//, "") : "no live address yet"}</span>
                    <span className="text-[13px] opacity-80">{s.deployments?.[0] ? `deployed ${relTime(s.deployments[0].created_at)}` : "not deployed"}</span>
                  </li>
                );
              })}
            </ul>
          )}
        </Panel>

        {node && (
          <Panel title="Server resources" actions={<Link href="/cluster">System health</Link>}>
            <div className="flex flex-wrap justify-around gap-6">
              <Gauge label="CPU" value={node.cpu_percent ?? 0} readout={`${(node.cpu_percent ?? 0).toFixed(0)}%`} />
              <Gauge label="Memory" value={pct(node.memory_used_bytes, node.memory_total_bytes)} readout={`${bytes(node.memory_used_bytes)} / ${bytes(node.memory_total_bytes)}`} />
              <Gauge label="Disk" value={pct(node.disk_used_bytes, node.disk_total_bytes)} readout={`${bytes(node.disk_used_bytes)} / ${bytes(node.disk_total_bytes)}`} />
            </div>
          </Panel>
        )}

        <Panel title="Recent deployments">
          {snaps.loading ? (
            <SkeletonRows rows={4} label="Loading deployments" />
          ) : allDeps.length === 0 ? (
            <p className="muted m-0">No deployments yet. Open a project and press Deploy.</p>
          ) : (
            <DataTable caption="Recent deployments across projects" columns={cols} rows={allDeps.slice(0, 8)} rowKey={(d) => d.id} />
          )}
        </Panel>
      </div>
    </>
  );
}
