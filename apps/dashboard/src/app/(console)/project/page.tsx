"use client";

import Link from "next/link";
import { Suspense, useEffect, useState, type FormEvent } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/field";
import { LcdCounter } from "@/components/ui/lcd";
import { ConfirmDialog } from "@/components/ui/modal";
import { PageHeader, Panel, Readout } from "@/components/ui/panel";
import { EmptyState, ErrorState, SkeletonRows } from "@/components/ui/states";
import { StatusBadge } from "@/components/ui/status";
import { TabPanel } from "@/components/ui/tabs";
import { PROJECT_NAV_ITEMS, projectHref, projectTabFrom } from "@/components/shell/nav";
import { Lamp } from "@/components/ui/status";
import { levelOf, visibleAreas } from "@/lib/access";
import type { Area } from "@/lib/api";
import { useToast } from "@/components/ui/toast";
import { AddonsPanel } from "@/features/addons";
import { AiResponseView } from "@/features/ai-view";
import { BuildsPanel } from "@/features/builds";
import { DatabasesPanel } from "@/features/databases";
import { DeploymentsPanel } from "@/features/deployments";
import { DomainsPanel } from "@/features/domains";
import { GitPanel } from "@/features/git";
import { LogsPanel } from "@/features/logs";
import { MetricsPanel } from "@/features/metrics";
import { RuntimePanel } from "@/features/runtime";
import { ScopeProvider, useScope } from "@/features/scope";
import { SecretsPanel } from "@/features/secrets";
import { StoragePanel } from "@/features/storage";
import { ai, deployments, domains, git, projects, runtime, type AiAnswer } from "@/lib/api";
import type { ProjectTab } from "@/components/shell/nav";
import { dateTime, relTime } from "@/lib/format";
import { PlacementPanel } from "@/features/servers";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useResource } from "@/lib/use-resource";

function Overview() {
  const { orgId, projectId, project } = useScope();
  const { access, can } = useOrg();
  const may = (a: Area) => levelOf(access, projectId, a) !== "none";
  const deps = useResource(may("deployments") ? `ov-d:${projectId}` : null, (s) => deployments.list(orgId, projectId, { signal: s }), { interval: 10000 });
  const inst = useResource(may("runtime") ? `ov-i:${projectId}` : null, (s) => runtime.instances(orgId, projectId, { signal: s }), { interval: 10000 });
  const doms = useResource(may("domains") ? `ov-m:${projectId}` : null, (s) => domains.list(orgId, projectId, { signal: s }));
  const [suggest, setSuggest] = useState<{ answer?: AiAnswer; error?: unknown; loading: boolean } | null>(null);
  const latest = [...(deps.data ?? [])].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""))[0];
  const running = inst.data?.filter((i) => i.status === "running").length;
  const live = doms.data?.filter((d) => d.domain.routed).length;
  const sys = doms.data?.find((d) => d.domain.kind === "system")?.domain;
  const repos = useResource(may("repository") && can("admin") ? `ov-r:${projectId}` : null, (s) => git.repos(orgId, projectId, { signal: s }));
  const next =
    repos.data && repos.data.length === 0
      ? { text: "Connect the repository your code lives in.", href: projectHref(projectId, "repository"), cta: "Connect repository" }
      : deps.data && deps.data.length === 0
        ? { text: "Everything is connected. Ship your first version.", href: projectHref(projectId, "deployments"), cta: "Go to deployments" }
        : null;

  const askAi = async () => {
    setSuggest({ loading: true });
    try {
      const r = await ai.overview(orgId, projectId);
      const sug = Array.isArray(r.suggestions) ? r.suggestions : r.suggestions ? r.suggestions.split("\n").map((x) => x.replace(/^[-*\d.\s]+/, "")).filter(Boolean) : [];
      setSuggest({ loading: false, answer: { ...r, answer: r.overview ?? r.answer, recommendations: r.recommendations ?? sug, severity: r.risk_level ?? r.severity } });
    } catch (error) {
      setSuggest({ loading: false, error });
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <div className="grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 150px), 1fr))" }}>
        {may("runtime") && <LcdCounter label="Instances running" value={running ?? "..."} width={2} />}
        {may("domains") && <LcdCounter label="Domains live" value={live ?? "..."} width={2} />}
        {may("deployments") && <LcdCounter label="Deployments" value={deps.data ? deps.data.length : "..."} width={3} />}
      </div>
      {next && (
        <Panel variant="leather" className="flex flex-wrap items-center gap-4" data-testid="next-step">
          <div className="min-w-0 flex-1">
            <p className="label mb-1">Next step</p>
            <p className="m-0 text-[16px]">{next.text}</p>
          </div>
          <Link href={next.href} className="btn btn--primary">{next.cta}</Link>
        </Panel>
      )}
      <Panel title="Project" variant="paper">
        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
          <Readout label="Slug">{project.slug}</Readout>
          <Readout label="Created">{dateTime(project.created_at)}</Readout>
          <Readout label="System address">
            {sys?.url ? <a href={sys.url} target="_blank" rel="noopener noreferrer">{sys.hostname}</a> : (sys?.hostname ?? project.system_domain ?? "-")}
          </Readout>
          <Readout label="Latest deployment" mono={false}>
            {latest ? <span className="inline-flex items-center gap-2"><StatusBadge status={latest.status} /> <span className="opacity-75">{relTime(latest.created_at)}</span></span> : "none yet"}
          </Readout>
        </div>
        {project.description && <p className="mb-0 mt-4">{project.description}</p>}
      </Panel>
      {may("logs") && <Panel title="AI review" actions={<Button size="sm" onClick={() => void askAi()} loading={suggest?.loading}>Analyze this project</Button>}>
        {!suggest ? <p className="muted m-0 text-[14px]">Ask the assistant to review recent deployments and logs and suggest next steps. Nothing is sent until you press the button.</p> : suggest.loading ? <SkeletonRows rows={2} label="Analyzing" /> : <AiResponseView answer={suggest.answer} error={suggest.error} />}
      </Panel>}
    </div>
  );
}

function SettingsTab() {
  const { orgId, project, can } = useScope();
  const { isPlatformAdmin } = useOrg();
  const router = useRouter();
  const toast = useToast();
  const { reload } = useProjects();
  const [name, setName] = useState(project.name);
  const [desc, setDesc] = useState(project.description ?? "");
  const [busy, setBusy] = useState(false);
  const [del, setDel] = useState(false);
  const [report, setReport] = useState<string[] | null>(null);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await projects.update(orgId, project.id, { name: name.trim(), description: desc });
      toast.success("Project saved");
      reload();
    } catch (err) {
      toast.error("Could not save", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <Panel title="General">
        <form onSubmit={save} className="flex max-w-xl flex-col gap-4">
          <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} disabled={!can("admin")} />
          <Textarea label="Description" value={desc} onChange={(e) => setDesc(e.target.value)} disabled={!can("admin")} />
          {can("admin") ? <div><Button type="submit" variant="primary" loading={busy} disabled={!name.trim()}>Save</Button></div> : <p className="hint">Only admins can edit project settings.</p>}
        </form>
      </Panel>
      {isPlatformAdmin && <PlacementPanel projectId={project.id} />}
      {can("admin") && (
        <Panel title="Danger zone" variant="leather">
          <p className="mt-0">Deleting a project removes its containers, domains, secrets, add-ons, databases and storage. This cannot be undone.</p>
          <Button variant="danger" onClick={() => setDel(true)} data-testid="project-delete">Delete project</Button>
          {report && (
            <div className="well mt-4" role="alert">
              <strong>Deleted with problems. These parts did not clean up:</strong>
              <ul className="mb-0 mt-2">{report.map((r) => <li key={r}>{r}</li>)}</ul>
            </div>
          )}
        </Panel>
      )}
      <ConfirmDialog
        open={del}
        title="Delete project"
        body={<>Everything in <strong>{project.name}</strong> is destroyed.</>}
        confirmLabel="Delete project"
        requireText={project.slug || project.name}
        onClose={() => setDel(false)}
        onConfirm={async () => {
          const res = (await projects.remove(orgId, project.id)) as { failures?: Array<string | { service?: string; error?: string }> } | undefined;
          const failures = (res?.failures ?? []).map((f) => (typeof f === "string" ? f : `${f.service ?? "service"}: ${f.error ?? "failed"}`));
          reload();
          if (failures.length > 0) {
            setReport(failures);
            toast.error("Project deleted with cleanup problems");
          } else {
            toast.success("Project deleted");
            router.push("/projects");
          }
        }}
      />
    </div>
  );
}

function ProjectDetail() {
  const params = useSearchParams();
  const id = params.get("id");
  const tab = projectTabFrom(params.get("tab"));
  const { currentOrg, access } = useOrg();
  const { setActiveProjectId } = useProjects();
  const lvl = id ? levelOf(access, id, tab as Area) : "none";
  const res = useResource(currentOrg && id && lvl !== "none" ? `project:${currentOrg.id}:${id}` : null, (signal) => projects.get(currentOrg!.id, id!, { signal }));

  useEffect(() => {
    if (id) setActiveProjectId(id);
  }, [id, setActiveProjectId]);

  if (!id) return <EmptyState title="No project selected" body="Pick a project to open its workspace." action={<Link className="btn btn--primary" href="/projects">Choose a project</Link>} />;
  const allowedAreas = visibleAreas(access, id);
  if (lvl === "none") {
    const first = allowedAreas[0];
    return (
      <>
        <PageHeader title="No access to this section" description={allowedAreas.length === 0 ? "You have not been given access to this project." : "Your account does not include this part of the project."} />
        <EmptyState
          title="This area is not part of your access"
          body="Ask a project administrator if you need it. Everything you can use is in the menu."
          action={first ? <Link className="btn btn--primary" href={projectHref(id, first as ProjectTab)}>Go to {PROJECT_NAV_ITEMS[first as ProjectTab].label}</Link> : <Link className="btn" href="/projects">Back to projects</Link>}
        />
      </>
    );
  }
  if (res.error && (res.error.forbidden || res.error.notFound)) {
    return <EmptyState title="No access to this project" body="It may not exist, or you have not been given access to it." action={<Link className="btn" href="/projects">Back to projects</Link>} />;
  }
  if (res.error) return <><PageHeader title="Project" /><ErrorState error={res.error} onRetry={res.reload} /></>;
  if (res.loading || !res.data) return <SkeletonRows rows={5} label="Loading project" />;
  const project = res.data;
  const item = PROJECT_NAV_ITEMS[tab];

  return (
    <ScopeProvider project={project} area={tab as Area}>
      <PageHeader title={item.label} eyebrow={project.name} description={item.description} />
      {lvl === "view" && (access.known && !access.full) && (
        <div className="well mb-5 flex items-center gap-3 !py-2.5" role="note" data-testid="view-only">
          <Lamp state="info" />
          <p className="m-0 text-[14.5px]">You have view-only access to this section. Buttons that change things are hidden.</p>
        </div>
      )}
      <TabPanel key={tab}>
        {tab === "overview" && <Overview />}
        {tab === "deployments" && <DeploymentsPanel />}
        {tab === "builds" && <BuildsPanel />}
        {tab === "runtime" && <RuntimePanel />}
        {tab === "domains" && <DomainsPanel />}
        {tab === "secrets" && <SecretsPanel />}
        {tab === "addons" && <AddonsPanel />}
        {tab === "databases" && <DatabasesPanel />}
        {tab === "storage" && <StoragePanel />}
        {tab === "logs" && <LogsPanel />}
        {tab === "metrics" && <MetricsPanel />}
        {tab === "repository" && <GitPanel />}
        {tab === "settings" && <SettingsTab />}
      </TabPanel>
    </ScopeProvider>
  );
}

export default function ProjectPage() {
  return (
    <Suspense fallback={<SkeletonRows rows={5} label="Loading project" />}>
      <ProjectDetail />
    </Suspense>
  );
}
