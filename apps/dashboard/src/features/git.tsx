"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { ConfirmDialog } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, Loaded } from "@/components/ui/states";
import { StatusBadge, Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { ApiError, errorMessage, git, type ConnectedRepo, type WebhookInfo } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { useOrg } from "@/lib/org-context";
import { useResource } from "@/lib/use-resource";
import { CdBadges, ConnectedWebhookDialog, RepoSettingsDialog } from "./cd";
import { useScope } from "./scope";

function DeployKey() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const key = useResource(`dk:${projectId}`, (signal) => git.deployKey(orgId, projectId, { signal }));
  const [rotate, setRotate] = useState(false);
  const [busy, setBusy] = useState(false);
  const missing = key.error instanceof ApiError && key.error.status === 404;

  const generate = async () => {
    setBusy(true);
    try {
      await git.rotateDeployKey(orgId, projectId);
      toast.success("Deploy key generated", "Add the new public key to your repository.");
      key.reload();
    } catch (err) {
      toast.error("Could not generate key", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel title="Deploy key" variant="paper">
      <p className="mt-0 text-[14px]">Add the public key below as a read-only deploy key on your private repository so builds can clone it over SSH.</p>
      {missing ? (
        <EmptyState title="No deploy key yet" body="Generate a key pair. Only the public half is ever shown." action={can("admin") && <Button variant="primary" loading={busy} onClick={() => void generate()}>Generate deploy key</Button>} />
      ) : (
        <Loaded res={key} rows={2}>
          {(k) => (
            <div className="flex flex-col gap-3">
              <div className="flex flex-wrap gap-2">
                <Tag>{k.key_type}</Tag>
                {k.fingerprint && <Tag tone="idle">{k.fingerprint}</Tag>}
                <span className="text-[13px] opacity-75">created {dateTime(k.created_at)}</span>
              </div>
              <CodeBlock text={k.public_key} label="Public key" />
              {can("admin") && (
                <div>
                  <Button variant="danger" size="sm" onClick={() => setRotate(true)} loading={busy}>Rotate key</Button>
                </div>
              )}
            </div>
          )}
        </Loaded>
      )}
      <ConfirmDialog
        open={rotate}
        title="Rotate deploy key"
        body="The current key stops working immediately. Builds from private SSH repositories fail until you add the new public key to the repository."
        confirmLabel="Rotate key"
        onClose={() => setRotate(false)}
        onConfirm={async () => {
          await git.rotateDeployKey(orgId, projectId);
          toast.success("Deploy key rotated");
          key.reload();
        }}
      />
    </Panel>
  );
}

export function GitPanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const repos = useResource(`prepos:${projectId}`, (signal) => git.repos(orgId, projectId, { signal }));
  const installs = useResource(`inst:${orgId}`, (signal) => git.installations(orgId, { signal }));
  const available = useResource(installs.data && installs.data.length > 0 ? `avail:${orgId}` : null, (signal) => git.available(orgId, { signal }));
  const [cloneUrl, setCloneUrl] = useState("");
  const [branch, setBranch] = useState("main");
  const [rootDir, setRootDir] = useState("");
  const [token, setToken] = useState("");
  const [edit, setEdit] = useState<ConnectedRepo | null>(null);
  const [fresh, setFresh] = useState<{ repo: ConnectedRepo; info: WebhookInfo } | null>(null);
  const [pick, setPick] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [del, setDel] = useState<ConnectedRepo | null>(null);

  const connect = async (body: Parameters<typeof git.connect>[2]) => {
    setBusy(true);
    setErr("");
    try {
      const res = await git.connect(orgId, projectId, { ...body, root_dir: rootDir.trim() || undefined });
      toast.success("Repository connected", body.full_name);
      setCloneUrl("");
      setToken("");
      repos.reload();
      // GitHub App connections receive events through the App; every other kind needs the webhook step
      if (res.webhook && res.repo.auth_type !== "app") setFresh({ repo: res.repo, info: res.webhook });
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const connectUrl = (e: FormEvent) => {
    e.preventDefault();
    const u = cloneUrl.trim();
    const name = u.replace(/^https?:\/\/[^/]+\//, "").replace(/^git@[^:]+:/, "").replace(/\.git$/, "");
    const t = token.trim();
    void connect({ full_name: name, clone_url: u, default_branch: branch.trim() || "main", auth_type: u.startsWith("git@") ? "deploy_key" : t ? "token" : "none", token: t || undefined });
  };

  const cols: Column<ConnectedRepo>[] = [
    { key: "n", header: "Repository", cell: (r) => <span className="mono text-[13px] font-medium">{r.full_name}</span> },
    { key: "b", header: "Default branch", cell: (r) => <Tag>{r.default_branch}</Tag> },
    { key: "d", header: "Deployments", cell: (r) => <CdBadges repo={r} /> },
    { key: "c", header: "Connected", cell: (r) => dateTime(r.created_at) },
    {
      key: "x",
      header: "Actions",
      srOnlyHeader: true,
      cell: (r) =>
        can("admin") && (
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={() => setEdit(r)} aria-label={`Deployment settings for ${r.full_name}`} data-testid={`repo-settings-${r.full_name}`}>
              Deployment settings
            </Button>
            <Button size="sm" variant="danger" onClick={() => setDel(r)}>Disconnect</Button>
          </div>
        ),
    },
  ];

  const avail = available.data ?? [];
  return (
    <div className="flex flex-col gap-6">
      <Panel title="Connected repositories">
        <Loaded res={repos} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No repository connected" body="Connect one below to enable deployments." />}>
          {(rows) => <DataTable caption="Connected repositories" columns={cols} rows={rows} rowKey={(r) => r.id} />}
        </Loaded>
      </Panel>
      {can("admin") && (
        <Panel title="Connect a repository">
          <div className="mb-5">
            <Input
              label="Root directory (monorepo, optional)"
              placeholder="empty = the repository root, e.g. backend"
              value={rootDir}
              onChange={(e) => setRootDir(e.target.value)}
              hint="Several projects can connect the same repository, each with its own folder. It can be changed later in Deployment settings."
            />
          </div>
          {avail.length > 0 && (
            <div className="mb-5 grid items-end gap-3 sm:grid-cols-[1fr_auto]">
              <Select label="From your GitHub App installation" value={pick} onChange={(e) => setPick(e.target.value)}>
                <option value="">Choose a repository</option>
                {avail.map((r) => <option key={r.full_name} value={r.full_name}>{r.full_name}{r.private ? " (private)" : ""}</option>)}
              </Select>
              <Button
                variant="primary"
                disabled={!pick}
                loading={busy}
                onClick={() => {
                  const r = avail.find((x) => x.full_name === pick)!;
                  void connect({ full_name: r.full_name, clone_url: r.clone_url, default_branch: r.default_branch, installation_id: installs.data?.[0]?.installation_id, auth_type: "github_app" });
                }}
              >
                Connect
              </Button>
            </div>
          )}
          {available.error && <ErrorState error={available.error} onRetry={available.reload} />}
          {installs.data && installs.data.length === 0 && <p className="muted mt-0 text-[14px]">No GitHub App installed for this organization. Install it from the Git page, or connect by URL below.</p>}
          <form onSubmit={connectUrl} className="flex flex-col gap-3">
            <div className="grid items-end gap-3 sm:grid-cols-[1fr_160px]">
              <Input label="Clone URL" placeholder="https://github.com/org/repo.git or git@host:org/repo.git" value={cloneUrl} onChange={(e) => setCloneUrl(e.target.value)} hint="https only, or SSH with the deploy key below." />
              <Input label="Branch" value={branch} onChange={(e) => setBranch(e.target.value)} />
            </div>
            <div className="grid items-end gap-3 sm:grid-cols-[1fr_auto]">
              <Input
                label="Access token (optional)"
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                hint="For private repositories, and so JP Cloud can create the push webhook for you. Stored encrypted, never shown again."
              />
              <Button type="submit" loading={busy} disabled={!cloneUrl.trim()}>Connect URL</Button>
            </div>
          </form>
          {err && <p role="alert" className="error-text">{err}</p>}
        </Panel>
      )}
      <DeployKey />
      {edit && <RepoSettingsDialog key={edit.id} repo={edit} onClose={() => setEdit(null)} onSaved={() => repos.reload()} />}
      {fresh && <ConnectedWebhookDialog repo={fresh.repo} info={fresh.info} onClose={() => setFresh(null)} />}
      <ConfirmDialog
        open={!!del}
        title="Disconnect repository"
        body={<>Deployments from <strong>{del?.full_name}</strong> stop, and push events are ignored. Existing releases keep running.</>}
        confirmLabel="Disconnect"
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          await git.disconnect(orgId, projectId, del.id);
          toast.success("Repository disconnected");
          repos.reload();
        }}
      />
    </div>
  );
}

/** Org-level: GitHub App installations. */
export function GitInstallations() {
  const { currentOrg, can } = useOrg();
  const toast = useToast();
  const orgId = currentOrg?.id;
  const list = useResource(orgId ? `ginst:${orgId}` : null, (signal) => git.installations(orgId!, { signal }));
  const [busy, setBusy] = useState(false);

  const start = async () => {
    if (!orgId) return;
    setBusy(true);
    try {
      const s = await git.startInstall(orgId);
      if (s.message) toast.info("GitHub App", s.message);
      if (s.install_url) window.open(s.install_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      toast.error("Could not start installation", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel
      title="GitHub App"
      actions={
        <>
          <Button size="sm" onClick={list.reload}>Refresh</Button>
          {can("admin") && <Button variant="primary" size="sm" loading={busy} onClick={() => void start()}>Install / connect GitHub</Button>}
        </>
      }
    >
      <p className="mt-0 text-[14px]">Install the GitHub App to deploy private repositories and receive push webhooks. After installing on GitHub, press Refresh.</p>
      <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No installation" body="GitHub is not connected to this organization yet." />}>
        {(rows) => (
          <DataTable
            caption="GitHub installations"
            rows={rows}
            rowKey={(i) => i.id}
            columns={[
              { key: "a", header: "Account", cell: (i) => <span className="mono font-medium">{i.account_login}</span> },
              { key: "s", header: "Status", cell: (i) => <StatusBadge status={i.status} /> },
              { key: "d", header: "Installed", cell: (i) => dateTime(i.created_at) },
            ]}
          />
        )}
      </Loaded>
    </Panel>
  );
}
