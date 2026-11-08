"use client";

import Link from "next/link";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/field";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { StatusBadge, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { AiResponseView } from "@/features/ai-view";
import { ai, deployments, git, type AiAnswer, type Deployment } from "@/lib/api";
import { isTransient, relTime, shortSha } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

export function DeploymentsPanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const list = useResource(`deps:${projectId}`, (signal) => deployments.list(orgId, projectId, { signal }), { interval: 5000 });
  const repos = useResource(`repos:${projectId}`, (signal) => git.repos(orgId, projectId, { signal }));
  const [deployOpen, setDeployOpen] = useState(false);
  const [repoId, setRepoId] = useState("");
  const [branch, setBranch] = useState("");
  const [busy, setBusy] = useState(false);
  const [rollback, setRollback] = useState<Deployment | null>(null);
  const [explain, setExplain] = useState<{ dep: Deployment; answer?: AiAnswer; error?: unknown; loading: boolean } | null>(null);

  const repoList = repos.data ?? [];
  const chosen = repoList.find((r) => r.id === repoId) ?? repoList[0];

  const startDeploy = async () => {
    setBusy(true);
    try {
      const d = await deployments.create(orgId, projectId, { repo_id: chosen?.id, git_branch: branch.trim() || chosen?.default_branch });
      toast.success("Deployment queued", d.id ? `#${d.id.slice(0, 8)}` : undefined);
      setDeployOpen(false);
      list.reload();
    } catch (err) {
      toast.error("Deploy failed to start", err);
    } finally {
      setBusy(false);
    }
  };

  const runExplain = async (dep: Deployment) => {
    setExplain({ dep, loading: true });
    try {
      const answer = await ai.explain(orgId, projectId, { deployment_id: dep.id, build_id: dep.build_id });
      setExplain({ dep, answer, loading: false });
    } catch (error) {
      setExplain({ dep, error, loading: false });
    }
  };

  const cols: Column<Deployment>[] = [
    { key: "s", header: "Status", cell: (d) => <StatusBadge status={d.status} /> },
    {
      key: "src",
      header: "Source",
      cell: (d) => (
        <div>
          <div className="mono text-[13px]">
            {d.git_branch || "-"} <span className="opacity-60">@</span> {shortSha(d.git_sha)}
          </div>
          {d.message && <div className="max-w-[320px] truncate text-[13px] opacity-75">{d.message}</div>}
          {d.error && <div className="max-w-[360px] break-words text-[13px]" style={{ color: "var(--fail-text)" }}>{d.error}</div>}
        </div>
      ),
    },
    { key: "strat", header: "Strategy", cell: (d) => <Tag>{d.strategy || "rolling"}</Tag> },
    { key: "when", header: "Created", cell: (d) => <span title={d.created_at}>{relTime(d.created_at)}</span> },
    {
      key: "act",
      header: "Actions",
      srOnlyHeader: true,
      cell: (d) => (
        <div className="flex flex-wrap gap-2">
          {d.build_id && (
            <Link className="btn btn--sm" href={`/project?id=${encodeURIComponent(projectId)}&tab=builds&build=${encodeURIComponent(d.build_id)}`}>
              Build log
            </Link>
          )}
          {d.status === "failed" && (
            <Button size="sm" onClick={() => void runExplain(d)}>
              Explain
            </Button>
          )}
          {can("member") && ["running", "succeeded", "superseded", "rolled_back"].includes(d.status) && (
            <Button size="sm" onClick={() => setRollback(d)}>
              Roll back to this
            </Button>
          )}
        </div>
      ),
    },
  ];

  return (
    <Panel
      title="Deployments"
      actions={
        can("member") && (
          <Button variant="primary" size="sm" onClick={() => setDeployOpen(true)} data-testid="deploy-open">
            Deploy now
          </Button>
        )
      }
    >
      <Loaded
        res={list}
        isEmpty={(d) => d.length === 0}
        empty={
          <EmptyState
            title="Nothing deployed yet"
            body={repoList.length === 0 ? "Connect a repository in the Git tab, then deploy it here." : "Press Deploy now to build and release the connected repository."}
          />
        }
      >
        {(rows) => (
          <DataTable caption="Deployments" columns={cols} rows={[...rows].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""))} rowKey={(d) => d.id} />
        )}
      </Loaded>
      {list.data?.some((d) => isTransient(d.status)) && <p className="muted mb-0 mt-3 text-[13px]">A deployment is in progress; this list refreshes every 5 seconds.</p>}

      <Modal
        open={deployOpen}
        onClose={() => setDeployOpen(false)}
        title="Deploy"
        locked={busy}
        description="Builds the selected branch and releases it with the project's strategy. A failed health check keeps the previous version serving."
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeployOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button variant="primary" onClick={() => void startDeploy()} loading={busy} disabled={repoList.length === 0}>
              Start deployment
            </Button>
          </>
        }
      >
        {repos.loading ? (
          <p className="muted">Loading repositories...</p>
        ) : repoList.length === 0 ? (
          <EmptyState title="No repository connected" body="Open the Git tab and connect a repository first." />
        ) : (
          <div className="flex flex-col gap-4">
            <Select label="Repository" value={chosen?.id ?? ""} onChange={(e) => setRepoId(e.target.value)}>
              {repoList.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.full_name}
                </option>
              ))}
            </Select>
            <Input label="Branch" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder={chosen?.default_branch ?? "main"} hint="Leave empty to use the default branch." />
          </div>
        )}
      </Modal>

      <ConfirmDialog
        open={!!rollback}
        title="Roll back"
        danger={false}
        body={
          <>
            Re-release the build from deployment <span className="mono">{rollback ? rollback.id.slice(0, 8) : ""}</span> ({rollback?.git_branch} @ {shortSha(rollback?.git_sha)}). The current version is replaced after the health check passes.
          </>
        }
        confirmLabel="Roll back"
        onClose={() => setRollback(null)}
        onConfirm={async () => {
          if (!rollback) return;
          await deployments.rollback(orgId, projectId, rollback.id);
          toast.success("Rollback started");
          list.reload();
        }}
      />

      <Modal open={!!explain} onClose={() => setExplain(null)} title="AI failure analysis" wide>
        {explain?.loading && <p className="muted">Reading build output...</p>}
        {explain?.error !== undefined && <AiResponseView error={explain.error} />}
        {explain?.answer && <AiResponseView answer={explain.answer} />}
      </Modal>
    </Panel>
  );
}
