"use client";

import { FormEvent, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input } from "@/components/ui/field";
import { Icon } from "@/components/ui/icons";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded, SkeletonRows } from "@/components/ui/states";
import { Lamp, StatusBadge, Tag } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { addons, ApiError, errorMessage, slugify, type AddonCatalogItem, type ManagedAddon } from "@/lib/api";
import { dateTime, isTransient, bytes, type LampState } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

const ENV_CHOICES = ["production", "preview", "staging", "development"];

export function isProvisionable(c: AddonCatalogItem): boolean {
  return c.provisionable !== false;
}

export function filterCatalog(items: AddonCatalogItem[], q: string, cat: string): AddonCatalogItem[] {
  const needle = q.trim().toLowerCase();
  return items.filter((c) => (cat === "all" || c.category === cat) && (!needle || `${c.name} ${c.id} ${c.description} ${c.category}`.toLowerCase().includes(needle)));
}

function ProvisionSteps({ status }: { status: string }) {
  const failed = status === "failed" || status === "error";
  const ready = status === "ready" || status === "running";
  const steps: Array<{ label: string; state: LampState }> = [
    { label: "Requested", state: "ok" },
    { label: "Starting container", state: ready || failed ? "ok" : "warn" },
    { label: failed ? "Failed" : "Ready", state: failed ? "fail" : ready ? "ok" : "idle" },
  ];
  return (
    <ol className="m-0 flex list-none flex-wrap gap-4 p-0" aria-label="Provisioning progress">
      {steps.map((s) => (
        <li key={s.label} className="flex items-center gap-2 text-[13px]">
          <Lamp state={s.state} pulse={s.state === "warn"} />
          {s.label}
        </li>
      ))}
    </ol>
  );
}

function ConnectionModal({ addon, onClose }: { addon: ManagedAddon; onClose: () => void }) {
  const { orgId, projectId, can, canReveal } = useScope();
  const [reveal, setReveal] = useState(false);
  const conn = useResource(`conn:${addon.id}:${reveal}`, (signal) => addons.connection(orgId, projectId, addon.id, reveal, { signal }));
  const backups = useResource(`bk:${addon.id}`, (signal) => addons.backups(orgId, projectId, addon.id, { signal }));
  const toast = useToast();
  const unsupported = backups.error instanceof ApiError && [404, 405, 501].includes(backups.error.status);
  const envNames = addon.injected_env ?? addon.environments ?? [];
  return (
    <Modal open onClose={onClose} wide title={`${addon.name} connection`} description="Your app receives this through an environment variable. Passwords are masked unless an admin reveals them.">
      <div className="flex flex-col gap-5">
        <Loaded res={conn} rows={2}>
          {(c) => {
            const url = c.url ?? c.masked_url;
            return (
              <div className="well flex flex-col gap-3">
                {url && (
                  <div>
                    <div className="label mb-1">Connection URL</div>
                    <code className="mono break-all text-[13px]" data-testid="conn-url">{url}</code>
                  </div>
                )}
                <dl className="m-0 grid gap-x-6 gap-y-2 sm:grid-cols-2">
                  {[["Host", c.host], ["Port", c.port], ["User", c.username], ["Database", c.database]].map(([k, v]) =>
                    v !== undefined && v !== "" ? (
                      <div key={String(k)}>
                        <dt className="label">{k}</dt>
                        <dd className="mono m-0 text-[13px]">{String(v)}</dd>
                      </div>
                    ) : null,
                  )}
                  {c.password !== undefined && (
                    <div>
                      <dt className="label">Password</dt>
                      <dd className="mono m-0 break-all text-[13px]">{c.password}</dd>
                    </div>
                  )}
                </dl>
                <div className="flex flex-wrap gap-2">
                  {url && <CopyButton text={url} label="Copy URL" />}
                  {c.password && <CopyButton text={c.password} label="Copy password" />}
                  {canReveal && !reveal && (
                    <Button size="sm" onClick={() => setReveal(true)}>
                      <Icon name="eye" size={14} /> Reveal password
                    </Button>
                  )}
                  {reveal && (
                    <Button size="sm" onClick={() => setReveal(false)}>
                      <Icon name="eyeOff" size={14} /> Mask again
                    </Button>
                  )}
                </div>
              </div>
            );
          }}
        </Loaded>
        <div>
          <div className="label mb-2">Injected into your app as</div>
          {(conn.data?.env_vars ?? envNames).length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {(conn.data?.env_vars ?? envNames).map((n) => (
                <Tag key={n} tone="accent">{n}</Tag>
              ))}
            </div>
          ) : (
            <p className="muted m-0 text-[14px]">See the Secrets tab; the add-on stored its URL there{addon.secret_ref ? ` as ${addon.secret_ref}` : ""}.</p>
          )}
        </div>
        <div>
          <div className="mb-2 flex items-center gap-3">
            <h3 className="label m-0">Backups</h3>
            {!unsupported && can("member") && (
              <Button
                size="sm"
                onClick={async () => {
                  try {
                    await addons.createBackup(orgId, projectId, addon.id);
                    toast.success("Backup started");
                    backups.reload();
                  } catch (err) {
                    toast.error("Backup failed", err);
                  }
                }}
              >
                Back up now
              </Button>
            )}
          </div>
          {unsupported ? (
            <p className="muted m-0 text-[14px]">Backups are not available for this add-on engine.</p>
          ) : (
            <Loaded res={backups} rows={1} isEmpty={(d) => d.length === 0} empty={<p className="muted m-0 text-[14px]">No backups yet.</p>}>
              {(rows) => (
                <ul className="m-0 flex list-none flex-col gap-2 p-0">
                  {rows.map((b) => (
                    <li key={b.id} className="well flex flex-wrap items-center gap-3 !py-2">
                      <StatusBadge status={b.status} />
                      <span className="text-[13px]">{dateTime(b.created_at)}</span>
                      <span className="mono text-[12.5px]">{bytes(b.size_bytes)}</span>
                      {b.error && <span className="error-text !mt-0">{b.error}</span>}
                      {can("admin") && b.status === "ready" && (
                        <Button
                          size="sm"
                          className="ml-auto"
                          onClick={async () => {
                            try {
                              await addons.restoreBackup(orgId, projectId, addon.id, b.id);
                              toast.success("Restore started");
                            } catch (err) {
                              toast.error("Restore failed", err);
                            }
                          }}
                        >
                          Restore
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </Loaded>
          )}
        </div>
      </div>
    </Modal>
  );
}

function InstalledAddon({ addon, onChanged }: { addon: ManagedAddon; onChanged: () => void }) {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const [conn, setConn] = useState(false);
  const [del, setDel] = useState(false);
  const [busy, setBusy] = useState(false);
  const running = addon.status === "ready" || addon.status === "running";
  const act = async (a: "start" | "stop" | "restart") => {
    setBusy(true);
    try {
      await addons.action(orgId, projectId, addon.id, a);
      toast.success(`Add-on ${a} requested`);
      onChanged();
    } catch (err) {
      toast.error(`Could not ${a}`, err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel variant="paper" as="article" className="flex flex-col gap-3" data-testid={`addon-${addon.name}`}>
      <div className="flex flex-wrap items-center gap-3">
        <h3 className="text-[20px]">{addon.name}</h3>
        <Tag>{addon.engine}</Tag>
        <StatusBadge status={addon.status} />
      </div>
      {(isTransient(addon.status) || addon.status === "failed") && <ProvisionSteps status={addon.status} />}
      {addon.error && <p role="alert" className="error-text !mt-0">{addon.error}</p>}
      <div className="mono text-[12.5px] opacity-80">
        {addon.endpoint && <div>endpoint: {addon.endpoint}</div>}
        {(addon.injected_env?.length ?? 0) > 0 && <div>env: {addon.injected_env!.join(", ")}</div>}
        <div>created {dateTime(addon.created_at)}</div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => setConn(true)} disabled={!running && addon.status !== "stopped"}>
          Connection
        </Button>
        {can("member") && (
          <>
            <Button size="sm" icon aria-label={`Start ${addon.name}`} disabled={busy || running || isTransient(addon.status)} onClick={() => void act("start")}><Icon name="play" size={14} /></Button>
            <Button size="sm" icon aria-label={`Stop ${addon.name}`} disabled={busy || !running} onClick={() => void act("stop")}><Icon name="stop" size={14} /></Button>
            <Button size="sm" icon aria-label={`Restart ${addon.name}`} disabled={busy || !running} onClick={() => void act("restart")}><Icon name="restart" size={14} /></Button>
          </>
        )}
        {can("admin") && (
          <Button size="sm" variant="danger" className="ml-auto" onClick={() => setDel(true)}>
            Delete
          </Button>
        )}
      </div>
      {conn && <ConnectionModal addon={addon} onClose={() => setConn(false)} />}
      <ConfirmDialog
        open={del}
        title="Delete add-on"
        body={<>The container <strong>{addon.name}</strong> and its data are permanently removed, and its connection variable is deleted from Secrets.</>}
        confirmLabel="Delete add-on and data"
        requireText={addon.name}
        onClose={() => setDel(false)}
        onConfirm={async () => {
          await addons.remove(orgId, projectId, addon.id);
          toast.success("Add-on deleted", addon.name);
          onChanged();
        }}
      />
    </Panel>
  );
}

function ProvisionModal({ item, onClose, onDone }: { item: AddonCatalogItem; onClose: () => void; onDone: () => void }) {
  const { orgId, projectId } = useScope();
  const toast = useToast();
  const [name, setName] = useState(slugify(`${item.id}-1`));
  const [envs, setEnvs] = useState<string[]>(["production", "preview"]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await addons.create(orgId, projectId, { engine: item.id, name: slugify(name), env: envs[0], envs });
      toast.success(`${item.name} is being provisioned`, "Watch the status in Installed add-ons.");
      onDone();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      locked={busy}
      title={`Provision ${item.name}`}
      description={`Runs a dedicated ${item.name} container on this project's private network. Credentials are generated and stored as secrets.`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" type="submit" form="prov-form" loading={busy} disabled={!name.trim() || envs.length === 0} data-testid="provision-submit">Provision</Button>
        </>
      }
    >
      <form id="prov-form" onSubmit={submit} className="flex flex-col gap-4">
        <Input label="Instance name" data-autofocus value={name} onChange={(e) => setName(e.target.value)} hint="Reachable inside the project network as addon-<name>." data-testid="provision-name" />
        <fieldset className="m-0 border-0 p-0">
          <legend className="label field-label">Inject connection variable into</legend>
          <div className="flex flex-wrap gap-4">
            {ENV_CHOICES.map((e) => (
              <label key={e} className="flex items-center gap-2 text-[14.5px]">
                <input type="checkbox" checked={envs.includes(e)} onChange={(ev) => setEnvs(ev.target.checked ? [...envs, e] : envs.filter((x) => x !== e))} />
                {e}
              </label>
            ))}
          </div>
        </fieldset>
        {item.secret_keys && item.secret_keys.length > 0 && (
          <p className="hint">Your app will read: {item.secret_keys.map((k) => k.replace("{NAME}", name.toUpperCase().replace(/-/g, "_"))).join(", ")}</p>
        )}
        {error && <p role="alert" className="error-text !mt-0">{error}</p>}
      </form>
    </Modal>
  );
}

export function AddonsPanel() {
  const { orgId, projectId, can } = useScope();
  const catalog = useResource(`catalog:${projectId}`, (signal) => addons.catalog(orgId, projectId, { signal }));
  const installed = useResource(`addons:${projectId}`, (signal) => addons.list(orgId, projectId, { signal }), {
    interval: 3000,
  });
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("all");
  const [pick, setPick] = useState<AddonCatalogItem | null>(null);

  const cats = useMemo(() => ["all", ...Array.from(new Set((catalog.data ?? []).map((c) => c.category)))], [catalog.data]);
  const shown = filterCatalog(catalog.data ?? [], q, cat);
  const provisionable = (catalog.data ?? []).filter(isProvisionable).length;

  return (
    <div className="flex flex-col gap-6">
      <Panel title={`Installed add-ons${installed.data ? ` (${installed.data.length})` : ""}`}>
        <Loaded res={installed} isEmpty={(d) => d.length === 0} empty={<EmptyState title="Nothing installed" body="Pick an engine from the marketplace below." />}>
          {(rows) => (
            <div className="grid-auto">
              {rows.map((a) => (
                <InstalledAddon key={a.id} addon={a} onChanged={installed.reload} />
              ))}
            </div>
          )}
        </Loaded>
      </Panel>

      <Panel title="Marketplace">
        {catalog.loading ? (
          <SkeletonRows rows={3} label="Loading catalog" />
        ) : (
          <>
            <p className="muted mt-0 text-[14px]" data-testid="catalog-count">
              {provisionable} of {catalog.data?.length ?? 0} engines can be provisioned on this platform.
              {!can("admin") && " Provisioning needs the member role or above."}
            </p>
            <div className="mb-4 flex flex-wrap items-end gap-3">
              <div className="min-w-[220px] flex-1">
                <Input label="Search" hideLabel type="search" placeholder="Search engines..." value={q} onChange={(e) => setQ(e.target.value)} data-testid="addon-search" />
              </div>
              <div role="group" aria-label="Category" className="flex flex-wrap gap-1">
                {cats.map((c) => (
                  <button key={c} type="button" aria-pressed={cat === c} className={`tag cursor-pointer ${cat === c ? "tag--accent" : "tag--idle"}`} onClick={() => setCat(c)}>
                    {c}
                  </button>
                ))}
              </div>
            </div>
          </>
        )}
        <Loaded res={catalog} isEmpty={(d) => d.length === 0} empty={<EmptyState title="Catalog is empty" />} rows={3}>
          {() =>
            shown.length === 0 ? (
              <EmptyState title="No match" body="Try a different search or category." />
            ) : (
              <div className="grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 250px), 1fr))" }}>
                {shown.map((c) => {
                  const ok = isProvisionable(c);
                  return (
                    <div key={c.id} className="well flex flex-col gap-2" data-testid={`catalog-${c.id}`} style={ok ? undefined : { opacity: 0.6 }}>
                      <div className="flex items-center gap-2">
                        <strong className="text-[16px]">{c.name}</strong>
                        <Tag className="ml-auto" tone={ok ? "default" : "idle"}>{ok ? c.category : "unavailable"}</Tag>
                      </div>
                      <p className="m-0 flex-1 text-[13.5px] opacity-85">{c.description}</p>
                      {!ok && c.unavailable_reason && <p className="m-0 text-[12.5px]">{c.unavailable_reason}</p>}
                      <Button size="sm" variant={ok ? "primary" : "default"} disabled={!ok || !can("member")} onClick={() => setPick(c)} aria-label={`Provision ${c.name}`}>
                        {ok ? "Provision" : "Not available"}
                      </Button>
                    </div>
                  );
                })}
              </div>
            )
          }
        </Loaded>
      </Panel>
      {pick && (
        <ProvisionModal
          item={pick}
          onClose={() => setPick(null)}
          onDone={() => {
            setPick(null);
            installed.reload();
          }}
        />
      )}
    </div>
  );
}
