"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Textarea } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { Switch } from "@/components/ui/switch";
import { useToast } from "@/components/ui/toast";
import { errorMessage, git, type ConnectedRepo, type RepoFolders, type WebhookInfo } from "@/lib/api";
import { useScope } from "./scope";

/* ------------------------------------------------------------------ helpers */

const lines = (s: string) => s.split("\n").map((x) => x.trim()).filter(Boolean);

export interface CdView {
  root_dir: string;
  auto_deploy: boolean;
  deploy_branch: string;
  default_branch: string;
  branch_previews: boolean;
  pr_previews: boolean;
  require_ci: boolean;
  watch_paths: string[];
  ignore_paths: string[];
}

export function cdOf(r: ConnectedRepo): CdView {
  return {
    root_dir: r.root_dir ?? "",
    auto_deploy: r.auto_deploy ?? true,
    deploy_branch: r.deploy_branch ?? "",
    default_branch: r.default_branch,
    branch_previews: r.branch_previews ?? true,
    pr_previews: r.pr_previews ?? true,
    require_ci: r.require_ci ?? false,
    watch_paths: r.watch_paths ?? [],
    ignore_paths: r.ignore_paths ?? [],
  };
}

/** What a push does with these settings, in plain sentences (also shown live while editing). */
export function describeCd(c: CdView): string[] {
  const branch = c.deploy_branch || c.default_branch || "main";
  const out: string[] = [];
  const watched = [c.root_dir, ...c.watch_paths].filter(Boolean);
  out.push(
    c.auto_deploy
      ? `A push to ${branch} deploys to production${c.require_ci ? ", once the repository's CI (GitHub Actions) has succeeded" : ""}.`
      : `Pushes to ${branch} do not deploy by themselves; deploy manually from the Deployments tab.`,
  );
  if (c.root_dir) out.push(`It builds the folder "${c.root_dir}" of the repository, so other folders (other projects) are not touched.`);
  if (watched.length > 0) out.push(`A push only deploys when a changed file is under ${watched.map((w) => `"${w}"`).join(", ")}.`);
  else out.push("Every push deploys, whichever files changed.");
  if (c.ignore_paths.length > 0) out.push(`Changes only to ${c.ignore_paths.map((w) => `"${w}"`).join(", ")} are ignored.`);
  out.push(c.branch_previews ? "Pushes to other branches create preview deployments." : "Pushes to other branches are ignored.");
  out.push(c.pr_previews ? "Pull requests get a preview deployment (removed when the PR closes)." : "Pull requests do not get previews.");
  out.push("A commit message containing [skip ci] or [skip deploy] is never deployed.");
  return out;
}

/** Compact summary for the repository table. */
export function CdBadges({ repo }: { repo: ConnectedRepo }) {
  const c = cdOf(repo);
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Tag tone={c.auto_deploy ? "ok" : "idle"} title={c.auto_deploy ? "Pushes deploy automatically" : "Automatic deployment is off"}>
        {c.auto_deploy ? "Auto-deploy" : "Manual"}
      </Tag>
      <Tag title="Folder of the repository this project builds">{c.root_dir ? `/${c.root_dir}` : "repo root"}</Tag>
      {c.require_ci && <Tag tone="info" title="Deploys after the repository's CI succeeded">after CI</Tag>}
      {(c.watch_paths.length > 0 || c.ignore_paths.length > 0) && <Tag title="Path filters are set">path filters</Tag>}
    </div>
  );
}

/* ------------------------------------------------------------------ webhook */

export function WebhookDetails({ info }: { info: WebhookInfo }) {
  return (
    <div className="flex flex-col gap-3" data-testid="webhook-details">
      <div>
        <p className="label field-label">Payload URL</p>
        <div className="flex items-center gap-2">
          <code className="well mono min-w-0 flex-1 break-all !py-2 text-[13px]" data-testid="webhook-url">{info.webhook_url}</code>
          <CopyButton text={info.webhook_url} />
        </div>
      </div>
      <div>
        <p className="label field-label">Secret (shown once)</p>
        <div className="flex items-center gap-2">
          <code className="well mono min-w-0 flex-1 break-all !py-2 text-[13px]" data-testid="webhook-secret">{info.webhook_secret}</code>
          <CopyButton text={info.webhook_secret} />
        </div>
      </div>
      <p className="muted m-0 text-[13.5px]">
        On GitHub: repository <strong>Settings &rarr; Webhooks &rarr; Add webhook</strong>. Content type <span className="mono">application/json</span>; events{" "}
        <span className="mono">push</span>, <span className="mono">pull_request</span>, <span className="mono">check_suite</span>, <span className="mono">workflow_run</span>.
      </p>
    </div>
  );
}

/** Installs the hook on GitHub for connections that hold an access token; the other kinds get the manual steps. */
function InstallWebhookButton({ repo, onDone }: { repo: ConnectedRepo; onDone?: () => void }) {
  const { orgId, projectId } = useScope();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  if (repo.auth_type !== "token") return null;
  return (
    <div>
      <Button
        size="sm"
        variant="primary"
        loading={busy}
        onClick={async () => {
          setBusy(true);
          setErr("");
          try {
            const res = await git.installWebhook(orgId, projectId, repo.id);
            toast.success(res.created ? "Webhook created on GitHub" : "Webhook updated on GitHub", "Pushes now deploy automatically.");
            onDone?.();
          } catch (e) {
            setErr(errorMessage(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Icon name="bolt" size={15} />
        Create the webhook on GitHub for me
      </Button>
      {err && <p role="alert" className="error-text">{err}</p>}
    </div>
  );
}

/** Shown right after connecting a repository that needs a webhook, with the secret that cannot be shown again. */
export function ConnectedWebhookDialog({ repo, info, onClose }: { repo: ConnectedRepo; info: WebhookInfo; onClose: () => void }) {
  return (
    <Modal
      open
      onClose={onClose}
      wide
      title="Finish setup: connect the webhook"
      description={<>Auto-deploy for <strong>{repo.full_name}</strong> needs GitHub to tell JP Cloud about pushes.</>}
      footer={<Button onClick={onClose}>Done</Button>}
    >
      <div className="flex flex-col gap-4">
        <InstallWebhookButton repo={repo} onDone={onClose} />
        <p className="muted m-0 text-[13.5px]">{repo.auth_type === "token" ? "Or add it by hand with these values:" : "Add the webhook on GitHub with these values:"}</p>
        <WebhookDetails info={info} />
      </div>
    </Modal>
  );
}

/* ------------------------------------------------------------------ folder browser */

function FolderBrowser({ repo, onPick }: { repo: ConnectedRepo; onPick: (path: string) => void }) {
  const { orgId, projectId } = useScope();
  const [path, setPath] = useState("");
  const [data, setData] = useState<RepoFolders | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const open = async (p: string) => {
    setBusy(true);
    setErr("");
    try {
      setData(await git.folders(orgId, projectId, repo.id, p));
      setPath(p);
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!data && !busy && !err) {
    return (
      <Button size="sm" onClick={() => void open("")} data-testid="browse-folders">
        <Icon name="folder" size={15} />
        Browse the repository
      </Button>
    );
  }
  const parts = path ? path.split("/") : [];
  return (
    <div className="well flex flex-col gap-2" data-testid="folder-browser">
      <div className="flex flex-wrap items-center gap-1 text-[13.5px]">
        <button type="button" className="btn btn--ghost btn--sm" onClick={() => void open("")}>repo</button>
        {parts.map((seg, i) => (
          <span key={i} className="flex items-center gap-1">
            <span aria-hidden="true">/</span>
            <button type="button" className="btn btn--ghost btn--sm" onClick={() => void open(parts.slice(0, i + 1).join("/"))}>{seg}</button>
          </span>
        ))}
        {busy && <Spinner size={14} />}
      </div>
      {err && <p role="alert" className="error-text m-0">{err}</p>}
      {data && (
        <>
          {data.markers.length > 0 && (
            <p className="m-0 flex flex-wrap items-center gap-1.5 text-[13px]">
              Found here: {data.markers.map((m) => <Tag key={m} tone="info">{m}</Tag>)}
            </p>
          )}
          <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
            {data.folders.length === 0 && <li className="muted text-[13.5px]">No sub-folders.</li>}
            {data.folders.map((f) => (
              <li key={f.path}>
                <button type="button" className="btn btn--sm" onClick={() => void open(f.path)}>
                  <Icon name="folder" size={14} />
                  {f.name}
                </button>
              </li>
            ))}
          </ul>
          <div className="flex gap-2">
            <Button size="sm" variant="primary" onClick={() => onPick(path)} data-testid="use-folder">
              Use {path ? `"${path}"` : "the repository root"}
            </Button>
          </div>
        </>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ settings dialog */

export function RepoSettingsDialog({ repo, onClose, onSaved }: { repo: ConnectedRepo; onClose: () => void; onSaved: () => void }) {
  const { orgId, projectId } = useScope();
  const toast = useToast();
  const start = cdOf(repo);
  const [root, setRoot] = useState(start.root_dir);
  const [auto, setAuto] = useState(start.auto_deploy);
  const [branch, setBranch] = useState(start.deploy_branch);
  const [bprev, setBprev] = useState(start.branch_previews);
  const [pprev, setPprev] = useState(start.pr_previews);
  const [ci, setCi] = useState(start.require_ci);
  const [watch, setWatch] = useState(start.watch_paths.join("\n"));
  const [ignore, setIgnore] = useState(start.ignore_paths.join("\n"));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [secret, setSecret] = useState<WebhookInfo | null>(null);
  const [rotating, setRotating] = useState(false);

  const view: CdView = {
    root_dir: root.trim().replace(/^\.?\/+|\/+$/g, ""),
    auto_deploy: auto,
    deploy_branch: branch.trim(),
    default_branch: repo.default_branch,
    branch_previews: bprev,
    pr_previews: pprev,
    require_ci: ci,
    watch_paths: lines(watch),
    ignore_paths: lines(ignore),
  };

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      await git.updateRepo(orgId, projectId, repo.id, {
        root_dir: root.trim(),
        auto_deploy: auto,
        deploy_branch: branch.trim(),
        branch_previews: bprev,
        pr_previews: pprev,
        require_ci: ci,
        watch_paths: lines(watch),
        ignore_paths: lines(ignore),
      });
      toast.success("Deployment settings saved", repo.full_name);
      onSaved();
      onClose();
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      wide
      locked={busy}
      onClose={onClose}
      title="Deployment settings"
      description={<span className="mono">{repo.full_name}</span>}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} onClick={() => void save()} data-testid="cd-save">Save settings</Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <section className="flex flex-col gap-3" aria-label="Folder">
          <h3 className="m-0 text-[16px]">Folder to build</h3>
          <Input
            label="Root directory"
            placeholder="empty = the repository root, e.g. backend or apps/web"
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            hint="Monorepo: set the folder this project builds. Its Dockerfile and jp.yaml are read from there; two projects can use two folders of one repository."
          />
          {repo.auth_type !== "deploy_key" && <FolderBrowser repo={repo} onPick={setRoot} />}
        </section>

        <section className="flex flex-col gap-3" aria-label="When to deploy">
          <h3 className="m-0 text-[16px]">When to deploy</h3>
          <Switch checked={auto} onChange={setAuto} label="Auto-deploy" description="Deploy to production when a commit is pushed to the production branch." />
          <Input label="Production branch" placeholder={`${repo.default_branch} (default branch)`} value={branch} onChange={(e) => setBranch(e.target.value)} hint="Leave empty to use the repository's default branch." />
          <Switch checked={ci} onChange={setCi} label="Wait for CI" description="Deploy only after the repository's GitHub Actions run for that commit succeeded. A failed run never deploys." />
          <Switch checked={bprev} onChange={setBprev} label="Preview other branches" description="Pushes to any other branch create a preview deployment (no production secrets)." />
          <Switch checked={pprev} onChange={setPprev} label="Preview pull requests" description="Each pull request gets its own preview, removed when it closes." />
        </section>

        <section className="grid gap-3 md:grid-cols-2" aria-label="Path filters">
          <Textarea
            label="Also watch these paths"
            rows={3}
            placeholder={"packages/shared/**\nlibs/common"}
            value={watch}
            onChange={(e) => setWatch(e.target.value)}
            hint="One per line. The root directory is always watched; add shared code that should also trigger a deploy."
          />
          <Textarea
            label="Ignore changes to"
            rows={3}
            placeholder={"**/*.md\ndocs/**"}
            value={ignore}
            onChange={(e) => setIgnore(e.target.value)}
            hint="One per line. A push that only touches these paths does not deploy."
          />
        </section>

        <section className="well flex flex-col gap-1.5" aria-label="What this does" data-testid="cd-summary">
          <p className="label m-0">What this does</p>
          <ul className="m-0 flex list-disc flex-col gap-1 pl-5 text-[14px]">
            {describeCd(view).map((l) => <li key={l}>{l}</li>)}
          </ul>
        </section>

        <section className="flex flex-col gap-3" aria-label="Webhook">
          <h3 className="m-0 text-[16px]">Webhook</h3>
          {repo.auth_type === "app" ? (
            <p className="muted m-0 text-[14px]">This repository is connected through the GitHub App: pushes, pull requests and CI results already arrive through the App. Nothing to set up.</p>
          ) : (
            <>
              <p className="muted m-0 text-[14px]">GitHub tells JP Cloud about pushes through a webhook. It is signed with a secret that is only shown once.</p>
              <div className="flex flex-wrap items-start gap-3">
                <InstallWebhookButton repo={repo} />
                <Button size="sm" loading={rotating} onClick={async () => {
                  setRotating(true);
                  try {
                    setSecret(await git.rotateWebhook(orgId, projectId, repo.id));
                  } catch (e) {
                    setErr(errorMessage(e));
                  } finally {
                    setRotating(false);
                  }
                }}>
                  Show the webhook URL and a new secret
                </Button>
              </div>
              {secret && <WebhookDetails info={secret} />}
            </>
          )}
        </section>
        {err && <p role="alert" className="error-text m-0">{err}</p>}
      </div>
    </Modal>
  );
}
