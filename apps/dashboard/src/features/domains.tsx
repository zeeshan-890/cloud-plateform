"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Input } from "@/components/ui/field";
import { ConfirmDialog } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Lamp, StatusBadge, Tag } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { domains, errorMessage, type DnsDiagnostics, type DomainDetail } from "@/lib/api";
import { dateTime, type LampState } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

const HOST_RE = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

export function normalizeHost(raw: string): string {
  return raw.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/\.$/, "");
}

export function validHost(h: string): boolean {
  return HOST_RE.test(h);
}

function sslLamp(s: string | undefined): { state: LampState; label: string; pulse?: boolean } {
  switch (s) {
    case "active":
    case "issued":
    case "valid":
      return { state: "ok", label: "SSL active" };
    case "pending":
    case "issuing":
      return { state: "warn", label: "SSL issuing", pulse: true };
    case "failed":
    case "error":
      return { state: "fail", label: "SSL failed" };
    case "tls_disabled":
      return { state: "idle", label: "HTTPS not enabled on this platform" };
    case "none":
      return { state: "idle", label: "No SSL until verified" };
    default:
      return { state: "info", label: `SSL ${s || "unknown"}` };
  }
}

function Steps({ step }: { step: 1 | 2 | 3 | 4 }) {
  const labels = ["Add domain", "Publish DNS records", "Verify", "Live"];
  return (
    <ol className="m-0 flex list-none flex-wrap gap-2 p-0" aria-label="Domain setup progress">
      {labels.map((l, i) => {
        const n = i + 1;
        const done = n < step || step === 4;
        const cur = n === step;
        return (
          <li key={l} className="flex items-center gap-2" aria-current={cur ? "step" : undefined}>
            <Lamp state={done ? "ok" : cur ? "warn" : "idle"} pulse={cur} />
            <span className="label !text-[12px]" style={{ opacity: done || cur ? 1 : 0.6 }}>
              {n}. {l}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function DomainCard({ detail, diag, onVerify, verifying, onDelete, canWrite, canDelete }: {
  detail: DomainDetail;
  diag?: DnsDiagnostics & { verified?: boolean };
  onVerify: () => void;
  verifying: boolean;
  onDelete: () => void;
  canWrite: boolean;
  canDelete: boolean;
}) {
  const d = detail.domain;
  const isSystem = d.kind === "system";
  const verified = d.routed || ["verified", "active"].includes(d.status);
  const step: 1 | 2 | 3 | 4 = verified ? 4 : diag ? 3 : 2;
  const ssl = sslLamp(d.ssl_status);

  return (
    <Panel variant="paper" as="article" className="flex flex-col gap-4" data-testid={`domain-${d.hostname}`}>
      <div className="flex flex-wrap items-center gap-3">
        <h3 className="mono min-w-0 break-all text-[18px] font-medium normal-case tracking-normal">{d.hostname}</h3>
        {isSystem && <Tag tone="info">system subdomain</Tag>}
        <StatusBadge status={d.status} />
        <div className="ml-auto flex gap-2">
          {d.url && verified && (
            <a className="btn btn--sm" href={d.url} target="_blank" rel="noopener noreferrer">
              Open
            </a>
          )}
          {canDelete && !isSystem && (
            <Button size="sm" variant="danger" onClick={onDelete} aria-label={`Delete ${d.hostname}`}>
              Delete
            </Button>
          )}
        </div>
      </div>

      <div className="flex flex-wrap gap-x-6 gap-y-2">
        <span className="inline-flex items-center gap-2 text-[14px]">
          <Lamp state={d.routed ? "ok" : "idle"} />
          {d.routed ? "Routed to your app" : "Not routed (needs verification)"}
        </span>
        <span className="inline-flex items-center gap-2 text-[14px]" data-testid="ssl-status">
          <Lamp state={ssl.state} pulse={ssl.pulse} />
          {ssl.label}
          {d.ssl_expires_at && <span className="opacity-70"> - expires {dateTime(d.ssl_expires_at)}</span>}
        </span>
      </div>
      {d.ssl_error && <p className="error-text !mt-0">{d.ssl_error}</p>}

      {!isSystem && !verified && (
        <>
          <Steps step={step} />
          <div>
            <h4 className="label mb-2">Step 2 - publish these records at your DNS provider</h4>
            {detail.records.length === 0 ? (
              <p className="m-0 text-[14px] opacity-80">The server did not return DNS records for this domain.</p>
            ) : (
              <div className="ledger-wrap">
                <table className="ledger" data-testid="dns-records">
                  <thead>
                    <tr>
                      <th>Type</th>
                      <th>Name</th>
                      <th>Value</th>
                      <th>Purpose</th>
                      <th>
                        <span className="sr-only">Copy</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.records.map((r, i) => (
                      <tr key={`${r.type}${r.name}${i}`}>
                        <td>
                          <Tag>{r.type}</Tag>
                        </td>
                        <td className="mono break-all text-[12.5px]">{r.name}</td>
                        <td className="mono break-all text-[12.5px]">{r.value}</td>
                        <td className="text-[13px]">{r.purpose || "-"}</td>
                        <td>
                          <CopyButton text={r.value} label="Copy value" />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <p className="hint">DNS changes can take a few minutes to spread. Ownership (TXT) is required; the routing record points traffic at this platform.</p>
          </div>
          <div>
            <h4 className="label mb-2">Step 3 - check it</h4>
            <div className="flex flex-wrap items-center gap-3">
              {canWrite && (
                <Button variant="primary" onClick={onVerify} loading={verifying} data-testid="verify-domain">
                  Verify DNS now
                </Button>
              )}
              {d.dns_last_checked && <span className="text-[13px] opacity-75">last checked {dateTime(d.dns_last_checked)}</span>}
            </div>
            {(diag || d.dns_error) && (
              <div className="well mt-3 flex flex-col gap-1" role="status" data-testid="verify-result">
                <div className="flex items-center gap-2">
                  <Lamp state={diag?.verified ? "ok" : "fail"} />
                  <strong>{diag?.verified ? "Verified" : "Not verified yet"}</strong>
                  {diag?.method && <Tag tone="idle">{diag.method}</Tag>}
                </div>
                <p className="m-0 text-[14px]">{diag?.detail || d.dns_error}</p>
                {diag?.records && diag.records.length > 0 && (
                  <p className="mono m-0 break-all text-[12.5px] opacity-85">Resolver saw: {diag.records.join(", ")}</p>
                )}
              </div>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}

export function DomainsPanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const list = useResource(`domains:${projectId}`, (signal) => domains.list(orgId, projectId, { signal }), { interval: 15000 });
  const [host, setHost] = useState("");
  const [adding, setAdding] = useState(false);
  const [err, setErr] = useState("");
  const [verifying, setVerifying] = useState<string | null>(null);
  const [diags, setDiags] = useState<Record<string, DnsDiagnostics & { verified?: boolean }>>({});
  const [removing, setRemoving] = useState<DomainDetail | null>(null);

  const hostNorm = normalizeHost(host);
  const hostBad = host.trim() !== "" && !validHost(hostNorm);

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (!validHost(hostNorm)) return;
    setAdding(true);
    setErr("");
    try {
      await domains.add(orgId, projectId, { hostname: hostNorm, verification_type: "txt" });
      setHost("");
      toast.success("Domain added", "Now publish the DNS records below.");
      list.reload();
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setAdding(false);
    }
  };

  const verify = async (d: DomainDetail) => {
    setVerifying(d.domain.id);
    try {
      const res = await domains.verify(orgId, projectId, d.domain.id);
      setDiags((m) => ({ ...m, [d.domain.id]: { ...(res.diagnostics ?? {}), verified: res.verified } }));
      if (res.verified) toast.success("Domain verified", d.domain.hostname);
      list.reload();
    } catch (e2) {
      toast.error("Verification could not run", e2);
    } finally {
      setVerifying(null);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      {can("member") && (
        <Panel title="Step 1 - add a domain">
          <form onSubmit={add} className="flex flex-wrap items-start gap-3">
            <div className="min-w-[260px] flex-1">
              <Input label="Hostname" hideLabel placeholder="app.example.com" value={host} onChange={(e) => setHost(e.target.value)} error={hostBad ? "Enter a valid hostname like app.example.com" : err || undefined} hint="Use a domain you control. You will prove ownership with a DNS record." data-testid="domain-input" />
            </div>
            <Button type="submit" variant="primary" loading={adding} disabled={!validHost(hostNorm)} data-testid="domain-add">
              Add domain
            </Button>
          </form>
        </Panel>
      )}
      <Loaded res={list} isEmpty={(d) => d.length === 0} empty={<EmptyState title="No domains" body="Add a custom domain above. Every project also gets a system subdomain once it is created." />}>
        {(rows) => (
          <div className="flex flex-col gap-5">
            {rows.map((d) => (
              <DomainCard key={d.domain.id} detail={d} diag={diags[d.domain.id]} verifying={verifying === d.domain.id} onVerify={() => void verify(d)} onDelete={() => setRemoving(d)} canWrite={can("member")} canDelete={can("admin")} />
            ))}
          </div>
        )}
      </Loaded>
      <ConfirmDialog
        open={!!removing}
        title="Delete domain"
        body={<>Traffic to <strong className="mono">{removing?.domain.hostname}</strong> stops being routed and its certificate is removed.</>}
        confirmLabel="Delete domain"
        requireText={removing?.domain.hostname}
        onClose={() => setRemoving(null)}
        onConfirm={async () => {
          if (!removing) return;
          await domains.remove(orgId, projectId, removing.domain.id);
          toast.success("Domain deleted");
          list.reload();
        }}
      />
    </div>
  );
}
