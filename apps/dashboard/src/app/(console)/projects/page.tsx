"use client";

import Link from "next/link";
import { useState } from "react";
import { LinkButton } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, SkeletonRows } from "@/components/ui/states";
import { Lamp } from "@/components/ui/status";
import { projectHref } from "@/components/shell/nav";
import { appState, projectUrl, useSnapshots } from "@/features/snapshots";
import { levelOf } from "@/lib/access";
import { relTime } from "@/lib/format";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";

export default function ProjectsPage() {
  const { currentOrg, can, access, restricted } = useOrg();
  const { projects, loading, error, reload } = useProjects();
  const snaps = useSnapshots(currentOrg?.id, projects, access);
  const [q, setQ] = useState("");
  const byId = new Map((snaps.data ?? []).map((s) => [s.project.id, s]));
  const needle = q.trim().toLowerCase();
  const shown = projects.filter((p) => !needle || `${p.name} ${p.slug ?? ""} ${p.description ?? ""}`.toLowerCase().includes(needle));

  return (
    <>
      <PageHeader
        title="Projects"
        eyebrow={currentOrg?.name}
        description="Each project is one app with its own domains, settings and add-ons. Open one to work on it."
        actions={
          can("admin") && (
            <LinkButton href="/projects/new" variant="primary">
              <Icon name="plus" size={16} />
              New project
            </LinkButton>
          )
        }
      />
      {loading ? (
        <SkeletonRows rows={4} label="Loading projects" />
      ) : error ? (
        <ErrorState error={error} onRetry={reload} />
      ) : projects.length === 0 ? (
        <EmptyState
          title={restricted ? "No projects shared with you yet" : "No projects yet"}
          body={restricted ? "An administrator gives you access project by project. Ask them to add you to the project you work on." : "Create your first project, then connect a repository and deploy."}
          action={can("admin") ? <LinkButton href="/projects/new" variant="primary">Create project</LinkButton> : undefined}
        />
      ) : (
        <>
          {projects.length > 3 && (
            <div className="mb-5 max-w-md">
              <Input label="Search projects" hideLabel type="search" placeholder="Search projects..." value={q} onChange={(e) => setQ(e.target.value)} data-testid="project-search" />
            </div>
          )}
          {shown.length === 0 ? (
            <EmptyState title="No match" body="Try a different name." />
          ) : (
            <div className="grid-auto">
              {shown.map((p) => {
                const snap = byId.get(p.id);
                const state = snap ? appState(snap) : null;
                const url = snap ? projectUrl(snap) : null;
                const last = snap?.deployments?.[0];
                const lamp = state === "running" ? "ok" : state === "failed" ? "fail" : state === "unknown" ? "info" : "idle";
                const word = state === "none" ? "not deployed" : state === "unknown" ? "unreachable" : (state ?? "checking");
                return (
                  <Panel key={p.id} variant="paper" as="article" className="flex flex-col gap-3">
                    <div className="flex items-start gap-3">
                      <div className="min-w-0 flex-1">
                        <h2 className="truncate">
                          <Link href={projectHref(p.id)} className="no-underline" style={{ color: "var(--ink)" }}>
                            {p.name}
                          </Link>
                        </h2>
                        <p className="m-0 truncate text-[13.5px] opacity-80">{p.description || p.slug}</p>
                      </div>
                      <span className="inline-flex items-center gap-2">
                        <Lamp state={lamp} pulse={state === "running"} label={`App is ${word}`} />
                        <span className="mono text-[11.5px]">{word}</span>
                      </span>
                    </div>
                    <div className="rule !my-0" />
                    <div className="flex flex-col gap-1 text-[13.5px]">
                      {url ? (
                        <a href={url} target="_blank" rel="noopener noreferrer" className="mono truncate">
                          {url.replace(/^https?:\/\//, "")}
                        </a>
                      ) : (
                        <span className="mono opacity-60">no live address yet</span>
                      )}
                      <span className="opacity-75">{last ? `Last deploy ${relTime(last.created_at)} (${last.status})` : `Created ${relTime(p.created_at)}`}</span>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <LinkButton href={projectHref(p.id)} size="sm">Open</LinkButton>
                      {levelOf(access, p.id, "deployments") !== "none" && <LinkButton href={projectHref(p.id, "deployments")} size="sm">Deploy</LinkButton>}
                      {levelOf(access, p.id, "logs") !== "none" && <LinkButton href={projectHref(p.id, "logs")} size="sm">Logs</LinkButton>}
                    </div>
                  </Panel>
                );
              })}
            </div>
          )}
        </>
      )}
    </>
  );
}
