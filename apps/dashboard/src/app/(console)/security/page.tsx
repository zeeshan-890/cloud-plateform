"use client";

import { Button } from "@/components/ui/button";
import { LcdCounter } from "@/components/ui/lcd";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Lamp, Tag } from "@/components/ui/status";
import { system, type SecurityCheck } from "@/lib/api";
import { dateTime, type LampState } from "@/lib/format";
import { useOrg } from "@/lib/org-context";
import { useResource } from "@/lib/use-resource";

function lamp(s: string): LampState {
  if (s === "pass") return "ok";
  if (s === "warn") return "warn";
  if (s === "fail") return "fail";
  return "idle";
}

export default function SecurityPage() {
  const { isPlatformAdmin } = useOrg();
  const res = useResource(isPlatformAdmin ? "security" : null, (signal) => system.security({ signal }));

  if (!isPlatformAdmin) {
    return (
      <>
        <PageHeader title="Security center" />
        <EmptyState title="Platform administrators only" body="These checks inspect the whole installation, so only the platform administrator can see them." />
      </>
    );
  }
  return (
    <>
      <PageHeader title="Security center" description="Live checks of this installation's configuration. Nothing here is a score: each lamp is a real test that passed or did not." actions={<Button onClick={res.reload} loading={res.refreshing}>Re-run checks</Button>} />
      <Loaded res={res} isEmpty={(d) => d.checks.length === 0} empty={<EmptyState title="No checks reported" />} rows={5}>
        {(d) => {
          const count = (s: string) => d.checks.filter((c) => c.status === s).length;
          return (
            <div className="flex flex-col gap-6">
              <div className="flex flex-wrap gap-4">
                <LcdCounter label="Passing" value={count("pass")} width={2} />
                <LcdCounter label="Warnings" value={count("warn")} width={2} />
                <LcdCounter label="Failing" value={count("fail")} width={2} />
              </div>
              <Panel title="Checks">
                <ul className="m-0 flex list-none flex-col gap-3 p-0">
                  {d.checks.map((c: SecurityCheck) => (
                    <li key={c.id} className="well flex flex-col gap-1" data-testid={`check-${c.id}`}>
                      <div className="flex flex-wrap items-center gap-3">
                        <Lamp state={lamp(c.status)} large label={c.status} />
                        <strong className="text-[16px]">{c.title}</strong>
                        {c.category && <Tag tone="idle">{c.category}</Tag>}
                        <Tag tone={c.status === "pass" ? "ok" : c.status === "fail" ? "fail" : c.status === "warn" ? "warn" : "idle"} className="ml-auto">{c.status}</Tag>
                      </div>
                      {c.detail && <p className="m-0 text-[14px] opacity-90">{c.detail}</p>}
                      {c.status !== "pass" && c.remediation && (
                        <p className="m-0 text-[14px]"><strong>Fix:</strong> {c.remediation}</p>
                      )}
                    </li>
                  ))}
                </ul>
                {d.generated_at && <p className="hint">Checked {dateTime(d.generated_at)}</p>}
              </Panel>
            </div>
          );
        }}
      </Loaded>
    </>
  );
}
