"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import {
  system,
  type CheckResult,
  type Diagnostic,
  type JoinToken,
  type PortScope,
  type RemoteNode,
  type ServerEntry,
  type ServerInput,
  type ServerPath,
  type ServerPort,
  type ServerRole,
} from "@/lib/api";
import { bytes, dateTime, relTime } from "@/lib/format";
import { useResource } from "@/lib/use-resource";

const ROLES: ServerRole[] = ["control-plane", "database", "api", "worker", "storage", "other"];
const PATHS: { v: ServerPath; l: string }[] = [
  { v: "private", l: "Private network" },
  { v: "public", l: "Public internet" },
  { v: "auto", l: "Auto (fastest)" },
];
const SCOPES: { v: PortScope; l: string }[] = [
  { v: "private", l: "Private only" },
  { v: "public", l: "Public" },
  { v: "both", l: "Both" },
];
const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
const blank = (): ServerInput => ({ name: "", role: "other", public_addr: "", private_addr: "", prefer: "private", ports: [], notes: "" });
const STATUS_TEXT = { ok: "Healthy", warn: "Warnings", error: "Problems" } as const;
const LEVEL_TEXT: Record<Diagnostic["level"], string> = { error: "Problem", warn: "Warning", ok: "OK", info: "Note" };

export function ServersPanel() {
  const toast = useToast();
  const list = useResource("cluster-servers", (signal) => system.servers({ signal }));
  const nodesRes = useResource("cluster-remote-nodes", (signal) => system.remoteNodes({ signal }), { interval: 15000 });
  const nodeByName = new Map((nodesRes.data ?? []).map((n) => [n.name, n]));
  const [mode, setMode] = useState<"add" | { manual: ServerInput; isNew: boolean } | null>(null);
  const [join, setJoin] = useState<{ token: JoinToken; startedAt: number } | null>(null);
  const [details, setDetails] = useState<string | null>(null);
  const [del, setDel] = useState<ServerEntry | null>(null);
  const [stopNode, setStopNode] = useState<ServerEntry | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const startJoin = async (b: Partial<ServerInput> & { name: string; node?: boolean }) => {
    const token = await system.createJoinToken(b);
    setMode(null);
    setJoin({ token, startedAt: Date.now() });
  };

  const checkNow = async (s: ServerEntry) => {
    setBusy(s.name);
    try {
      await system.checkServer(s.name);
      list.reload();
      setDetails(s.name);
    } catch (err) {
      toast.error("Could not check the server", err);
    } finally {
      setBusy(null);
    }
  };

  const cols: Column<ServerEntry>[] = [
    { key: "n", header: "Server", cell: (s) => <div><div className="font-semibold">{s.name}</div><div className="label">{s.role}</div></div> },
    { key: "st", header: "Status", cell: (s) => <div className="flex flex-col gap-1"><StatusTag s={s} />{s.node && <NodeTag n={nodeByName.get(s.name)} />}</div> },
    { key: "pub", header: "Public address", cell: (s) => <span className="mono">{s.public_addr || "-"}</span> },
    { key: "priv", header: "Private address", cell: (s) => <span className="mono">{s.private_addr || "-"}</span> },
    { key: "pref", header: "Traffic uses", cell: (s) => <Tag>{s.prefer}</Tag> },
    {
      key: "ports",
      header: "Ports",
      cell: (s) =>
        (s.ports ?? []).length === 0 ? "-" : <div className="flex flex-wrap gap-1">{(s.ports ?? []).map((p) => <Tag key={`${p.proto}${p.port}`}>{`${p.port}${p.label ? ` ${p.label}` : ""} - ${p.scope}`}</Tag>)}</div>,
    },
    {
      key: "x",
      header: "Actions",
      srOnlyHeader: true,
      cell: (s) => (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setDetails(s.name)} data-testid={`server-details-${s.name}`}>Details</Button>
          <Button size="sm" onClick={() => checkNow(s)} loading={busy === s.name} data-testid={`server-check-${s.name}`}>Check now</Button>
          <Button size="sm" onClick={() => startJoin({ name: s.name })} data-testid={`server-recheck-${s.name}`}>Re-check command</Button>
          {s.node && <Button size="sm" onClick={() => setStopNode(s)} data-testid={`server-stopnode-${s.name}`}>Stop running projects</Button>}
          <Button size="sm" onClick={() => setMode({ manual: { ...blank(), ...s, ports: s.ports ?? [] }, isNew: false })} data-testid={`server-edit-${s.name}`}>Edit</Button>
          <Button size="sm" variant="danger" onClick={() => setDel(s)} data-testid={`server-delete-${s.name}`}>Remove</Button>
        </div>
      ),
    },
  ];

  const current = details ? list.data?.find((s) => s.name === details) : undefined;
  const reloadAll = () => {
    list.reload();
    nodesRes.reload();
  };

  return (
    <>
      <Panel title="Servers" actions={<Button size="sm" variant="primary" onClick={() => setMode("add")} data-testid="server-add">Add server</Button>}>
        <p className="mb-4 text-[14px] opacity-80">
          The servers of this installation: how to reach each one over the public internet or a private network, which path traffic should use, and which ports
          are meant to be open. To add one, press <strong>Add server</strong> and run the single command it shows on that server. It checks the connections and
          reports any problem here. This is configuration and checking only; it does not move workloads.
        </p>
        <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No servers recorded" body="Press Add server to get the one-line command for a new server." />}>
          {(rows) => <DataTable caption="Servers" columns={cols} rows={rows} rowKey={(s) => s.name} />}
        </Loaded>
      </Panel>

      {mode === "add" && <AddServer onClose={() => setMode(null)} onJoin={startJoin} onManual={() => setMode({ manual: blank(), isNew: true })} />}
      {mode && mode !== "add" && (
        <ManualForm
          initial={mode.manual}
          isNew={mode.isNew}
          onClose={() => setMode(null)}
          onSaved={() => {
            setMode(null);
            toast.success("Server saved");
            list.reload();
          }}
        />
      )}
      {join && <JoinModal join={join} onClose={() => { setJoin(null); reloadAll(); }} onDone={reloadAll} />}
      {details && current && <DetailsModal s={current} node={nodeByName.get(current.name)} onClose={() => setDetails(null)} />}
      <ConfirmDialog
        open={!!stopNode}
        title="Stop running projects on this server"
        body={<>Projects will no longer be placed on <strong>{stopNode?.name}</strong>. This is refused while projects or containers are still there; move them first. The server stays in the inventory.</>}
        confirmLabel="Stop running projects"
        danger={false}
        onClose={() => setStopNode(null)}
        onConfirm={async () => {
          await system.deleteNode(stopNode!.name);
          toast.success("Server no longer runs projects");
          reloadAll();
        }}
      />
      <ConfirmDialog
        open={!!del}
        title="Remove server"
        body={<>Remove <strong>{del?.name}</strong> from the inventory? This only deletes the record; the server itself is not touched.</>}
        confirmLabel="Remove server"
        onClose={() => setDel(null)}
        onConfirm={async () => {
          await system.deleteServer(del!.name);
          toast.success("Server removed");
          list.reload();
        }}
      />
    </>
  );
}

function StatusTag({ s }: { s: ServerEntry }) {
  if (!s.report?.status) return <span className="label">not checked yet</span>;
  const n = (s.report.diagnostics ?? []).filter((d) => d.level === "error" || d.level === "warn").length;
  return <Tag>{STATUS_TEXT[s.report.status]}{n > 0 ? ` (${n})` : ""}</Tag>;
}

function NodeTag({ n }: { n?: RemoteNode }) {
  if (!n) return <Tag>runs projects</Tag>;
  if (!n.reachable) return <Tag>{`runs projects - agent unreachable`}</Tag>;
  return <Tag>{`runs projects - ${n.projects} project${n.projects === 1 ? "" : "s"}`}</Tag>;
}

// ---------------------------------------------------------------- ports editor

function PortsEditor({ ports, onChange }: { ports: ServerPort[]; onChange: (p: ServerPort[]) => void }) {
  const setPort = (i: number, p: Partial<ServerPort>) => onChange(ports.map((x, j) => (j === i ? { ...x, ...p } : x)));
  return (
    <fieldset className="m-0 border-0 p-0">
      <legend className="label field-label">Ports this server should expose</legend>
      <div className="flex flex-col gap-2">
        {ports.map((p, i) => (
          <div key={i} className="grid items-end gap-2 md:grid-cols-[110px_130px_1fr_auto]">
            <Input label="Port" hideLabel type="number" min={1} max={65535} value={Number.isFinite(p.port) && p.port ? p.port : ""} onChange={(e) => setPort(i, { port: Number(e.target.value) })} placeholder="5432" data-testid={`server-port-${i}`} />
            <Select label="Open to" hideLabel value={p.scope} onChange={(e) => setPort(i, { scope: e.target.value as PortScope })} data-testid={`server-scope-${i}`}>
              {SCOPES.map((s) => <option key={s.v} value={s.v}>{s.l}</option>)}
            </Select>
            <Input label="Label" hideLabel value={p.label} onChange={(e) => setPort(i, { label: e.target.value })} placeholder="postgres" />
            <Button size="sm" type="button" onClick={() => onChange(ports.filter((_, j) => j !== i))}>Remove</Button>
          </div>
        ))}
        <div>
          <Button size="sm" type="button" onClick={() => onChange([...ports, { port: 0, proto: "tcp", scope: "private", label: "" }])} data-testid="server-add-port">Add port</Button>
        </div>
      </div>
    </fieldset>
  );
}

const portProblem = (ports: ServerPort[]) => (ports.some((p) => !Number.isInteger(p.port) || p.port < 1 || p.port > 65535) ? "Every port must be a number from 1 to 65535." : "");

// ---------------------------------------------------------------- add: one command

function AddServer({ onClose, onJoin, onManual }: { onClose: () => void; onJoin: (b: Partial<ServerInput> & { name: string; node?: boolean }) => Promise<void>; onManual: () => void }) {
  const toast = useToast();
  const [name, setName] = useState("");
  const [role, setRole] = useState<ServerRole>("other");
  const [prefer, setPrefer] = useState<ServerPath>("private");
  const [ports, setPorts] = useState<ServerPort[]>([]);
  const [runProjects, setRunProjects] = useState(false);
  const [busy, setBusy] = useState(false);
  const problem = !NAME_RE.test(name) ? "Name: lowercase letters, digits and hyphens, up to 32 characters." : portProblem(ports);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await onJoin({ name, role, prefer, ports, node: runProjects });
    } catch (err) {
      toast.error("Could not create the join command", err);
      setBusy(false);
    }
  };

  return (
    <Modal open onClose={onClose} title="Add a server" wide locked={busy} description="Name it and choose its ports. You then get one command to run on that server. Its addresses are detected automatically.">
      <form onSubmit={submit} className="flex flex-col gap-4" data-testid="server-join-form">
        <div className="grid gap-4 md:grid-cols-3">
          <Input label="Name" required value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="db-1" data-testid="server-name" />
          <Select label="Role" value={role} onChange={(e) => setRole(e.target.value as ServerRole)}>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </Select>
          <Select label="Traffic between servers uses" value={prefer} onChange={(e) => setPrefer(e.target.value as ServerPath)} data-testid="server-prefer">
            {PATHS.map((p) => <option key={p.v} value={p.v}>{p.l}</option>)}
          </Select>
        </div>
        <label className="flex items-start gap-2 text-[14.5px]">
          <input type="checkbox" className="mt-1" checked={runProjects} onChange={(e) => setRunProjects(e.target.checked)} data-testid="server-run-projects" />
          <span>
            <strong>Run projects on this server.</strong> The command also installs Docker if it is missing and a small node agent (listens on the private network only), so deployments, add-ons and databases can run here.
            Needs the control plane on the same private network.
          </span>
        </label>
        <PortsEditor ports={ports} onChange={setPorts} />
        {problem && name !== "" && <p className="text-[13.5px]" role="note">{problem}</p>}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Button type="button" onClick={onManual} disabled={busy} data-testid="server-manual">Enter details manually instead</Button>
          <div className="flex gap-2">
            <Button type="button" onClick={onClose} disabled={busy}>Cancel</Button>
            <Button type="submit" variant="primary" loading={busy} disabled={!!problem} data-testid="server-generate">Get the command</Button>
          </div>
        </div>
      </form>
    </Modal>
  );
}

// ---------------------------------------------------------------- waiting for the command to be run

function JoinModal({ join, onClose, onDone }: { join: { token: JoinToken; startedAt: number }; onClose: () => void; onDone: () => void }) {
  const { token, startedAt } = join;
  const [server, setServer] = useState<ServerEntry | null>(null);
  const onDoneRef = useRef(onDone);
  useEffect(() => {
    onDoneRef.current = onDone;
  });

  useEffect(() => {
    if (server) return;
    let stop = false;
    const tick = async () => {
      try {
        const all = await system.servers();
        const s = all.find((x) => x.name === token.name);
        if (!stop && s?.report?.at && new Date(s.report.at).getTime() >= startedAt - 2000) {
          setServer(s);
          onDoneRef.current();
        }
      } catch {
        /* keep waiting */
      }
    };
    const id = setInterval(tick, 3000);
    return () => {
      stop = true;
      clearInterval(id);
    };
  }, [server, token.name, startedAt]);

  return (
    <Modal open onClose={onClose} title={server ? `${token.name}: result` : `Run this on ${token.name}`} wide description={server ? undefined : `One command, valid until ${dateTime(token.expires_at)} and usable once.`}>
      {server ? (
        <Findings s={server} />
      ) : (
        <div className="flex flex-col gap-4" data-testid="join-waiting">
          <p className="text-[14.5px]">
            Open a shell on the server (SSH) and paste this. It reads the server&apos;s addresses, opens the planned ports in its own firewall, tests every other server and reports back here.
          </p>
          <div className="well flex flex-col gap-2">
            <code className="mono break-all text-[13px]" data-testid="join-command">{token.command}</code>
            <div><CopyButton text={token.command} label="Copy command" /></div>
          </div>
          <p className="text-[13.5px] opacity-80" role="status">Waiting for the server to run it. This window updates by itself.</p>
        </div>
      )}
      <div className="mt-4 flex justify-end"><Button onClick={onClose}>{server ? "Close" : "Close (the command stays valid)"}</Button></div>
    </Modal>
  );
}

// ---------------------------------------------------------------- findings

function Findings({ s }: { s: ServerEntry }) {
  const r = s.report;
  if (!r) return <EmptyState title="Not checked yet" body="Run the join command on this server, or press Check now, to test its connections." />;
  const diags = r.diagnostics ?? [];
  const order = { error: 0, warn: 1, info: 2, ok: 3 } as const;
  const sorted = [...diags].sort((a, b) => order[a.level] - order[b.level]);
  return (
    <div className="flex flex-col gap-4" data-testid="findings">
      <p className="text-[15px]" data-testid="findings-status">
        <strong>{r.status === "ok" ? "Everything checked is working." : r.status === "warn" ? "Connected, with warnings." : "Problems found. Fix the items marked Problem, then re-check."}</strong>{" "}
        <span className="opacity-70">Last checked {relTime(r.at)}.</span>
      </p>
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {sorted.map((d, i) => (
          <li key={`${d.code}${i}`} className="well" data-level={d.level}>
            <div><Tag>{LEVEL_TEXT[d.level]}</Tag> <span>{d.message}</span></div>
            {d.fix && (d.level === "error" || d.level === "warn") && <div className="mt-1 text-[13.5px] opacity-90"><strong>What to do:</strong> {d.fix}</div>}
          </li>
        ))}
      </ul>
    </div>
  );
}

function DetailsModal({ s, node, onClose }: { s: ServerEntry; node?: RemoteNode; onClose: () => void }) {
  const r = s.report;
  const rows: CheckResult[] = [...(r?.checks ?? []), ...(r?.inbound ?? []).map((c) => ({ ...c, peer: `control plane → ${c.peer}` }))];
  const cols: Column<CheckResult>[] = [
    { key: "p", header: "From → to", cell: (c) => c.peer },
    { key: "path", header: "Network", cell: (c) => c.path },
    { key: "a", header: "Address", cell: (c) => <span className="mono">{c.addr}:{c.port}</span> },
    { key: "r", header: "Result", cell: (c) => (c.ok ? `open${c.ms ? `, ${c.ms.toFixed(1)} ms` : ""}` : c.error === "refused" ? "reachable, nothing listening" : `blocked (${c.error || "no answer"})`) },
  ];
  return (
    <Modal open onClose={onClose} title={s.name} wide>
      <div className="flex flex-col gap-5" data-testid="server-details">
        <Findings s={s} />
        {r && (
          <div className="grid gap-3 text-[13.5px] sm:grid-cols-2">
            <div><span className="label">Hostname</span><div>{r.hostname || "-"}</div></div>
            <div><span className="label">Provider / OS</span><div>{[r.provider, r.os].filter(Boolean).join(" / ") || "-"}</div></div>
            <div><span className="label">Private address / network</span><div className="mono">{r.private_ip || "-"} {r.private_cidr ? `(${r.private_cidr})` : ""}</div></div>
            <div><span className="label">Public address seen by the panel</span><div className="mono">{r.public_ip || "-"}</div></div>
            <div><span className="label">Local firewall</span><div>{r.firewall || "-"}</div></div>
            <div><span className="label">Docker</span><div>{r.docker ? "installed" : "not installed"}</div></div>
          </div>
        )}
        {s.node && (
          <div className="well text-[13.5px]" data-testid="node-info">
            <strong>Runs projects.</strong>{" "}
            {!node ? "Loading..." : node.reachable
              ? `Agent answered in ${node.latency_ms ?? 0} ms. Docker ${node.docker_version}, ${node.architecture ?? "unknown arch"}, ${node.cpus} CPU, ${bytes(node.memory_total_bytes ?? 0)} RAM, ${node.containers_running} container${node.containers_running === 1 ? "" : "s"} running, ${node.projects} project${node.projects === 1 ? "" : "s"} placed here.`
              : `The control plane cannot reach the agent: ${node.error ?? "no answer"}`}
          </div>
        )}
        {rows.length > 0 && <DataTable caption="Connection tests" columns={cols} rows={rows} rowKey={(c) => `${c.peer}${c.path}${c.addr}${c.port}`} />}
      </div>
      <div className="mt-4 flex justify-end"><Button onClick={onClose}>Close</Button></div>
    </Modal>
  );
}

// ---------------------------------------------------------------- manual form

function ManualForm({ initial, isNew, onClose, onSaved }: { initial: ServerInput; isNew: boolean; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [v, setV] = useState<ServerInput>(initial);
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof ServerInput>(k: K, val: ServerInput[K]) => setV((x) => ({ ...x, [k]: val }));

  const problem = !NAME_RE.test(v.name)
    ? "Name: lowercase letters, digits and hyphens, up to 32 characters."
    : !v.public_addr.trim() && !v.private_addr.trim()
      ? "Give the server a public or a private address."
      : v.prefer === "private" && !v.private_addr.trim()
        ? "Traffic is set to the private network, so a private address is required."
        : v.prefer === "public" && !v.public_addr.trim()
          ? "Traffic is set to the public internet, so a public address is required."
          : portProblem(v.ports) || (v.ports.some((p) => p.scope !== "private" && !v.public_addr.trim()) ? "A public port needs a public address." : "");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await system.saveServer({ ...v, name: v.name.trim(), public_addr: v.public_addr.trim(), private_addr: v.private_addr.trim(), notes: v.notes.trim() });
      onSaved();
    } catch (err) {
      toast.error("Could not save the server", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={isNew ? "Add server manually" : `Edit ${initial.name}`} wide locked={busy}>
      <form onSubmit={submit} className="flex flex-col gap-4" data-testid="server-form">
        <div className="grid gap-4 md:grid-cols-2">
          <Input label="Name" required value={v.name} disabled={!isNew} onChange={(e) => set("name", e.target.value.toLowerCase())} placeholder="db-1" data-testid="server-name" />
          <Select label="Role" value={v.role} onChange={(e) => set("role", e.target.value as ServerRole)}>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </Select>
          <Input label="Public address" value={v.public_addr} onChange={(e) => set("public_addr", e.target.value)} placeholder="203.0.113.10" hint="Internet IP or DNS name" data-testid="server-public" />
          <Input label="Private address" value={v.private_addr} onChange={(e) => set("private_addr", e.target.value)} placeholder="10.0.0.5" hint="Address on your private network / VPC" data-testid="server-private" />
          <Select label="Traffic between servers uses" value={v.prefer} onChange={(e) => set("prefer", e.target.value as ServerPath)} data-testid="server-prefer">
            {PATHS.map((p) => <option key={p.v} value={p.v}>{p.l}</option>)}
          </Select>
          <Input label="Notes" value={v.notes} onChange={(e) => set("notes", e.target.value)} placeholder="Runs Postgres and Redis" />
        </div>
        <PortsEditor ports={v.ports} onChange={(p) => set("ports", p)} />
        {problem && <p className="text-[13.5px]" role="note">{problem}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!!problem} data-testid="server-save">Save server</Button>
        </div>
      </form>
    </Modal>
  );
}

// ---------------------------------------------------------------- project placement (platform admins)

export function PlacementPanel({ projectId }: { projectId: string }) {
  const toast = useToast();
  const nodes = useResource("cluster-remote-nodes", (signal) => system.remoteNodes({ signal }));
  const current = useResource(`placement-${projectId}`, (signal) => system.placement(projectId, { signal }));
  const [value, setValue] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const selected = value ?? current.data ?? "";
  const options = nodes.data ?? [];

  const save = async () => {
    setBusy(true);
    try {
      await system.setPlacement(projectId, selected);
      toast.success(selected ? `New deployments run on ${selected}` : "New deployments run on the control plane");
      setValue(null);
      current.reload();
    } catch (err) {
      toast.error("Could not change where this project runs", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel title="Runs on">
      <p className="mt-0 text-[14px] opacity-80">
        The server this project&apos;s containers, add-ons and databases run on. The change applies to the <strong>next deployment</strong>; what is running now keeps running until
        it is replaced, so moving is zero-downtime. A project with add-ons or databases cannot be moved until they are removed.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <Select label="Server" value={selected} onChange={(e) => setValue(e.target.value)} data-testid="placement-select">
          <option value="">Control plane (this server)</option>
          {options.map((n) => (
            <option key={n.name} value={n.name} disabled={!n.enabled}>{n.name}{n.reachable ? "" : " (unreachable)"}</option>
          ))}
        </Select>
        <Button variant="primary" loading={busy} disabled={selected === (current.data ?? "")} onClick={save} data-testid="placement-save">Save</Button>
      </div>
      {options.length === 0 && <p className="hint mb-0">No other server runs projects yet. Add one under Cluster.</p>}
    </Panel>
  );
}
