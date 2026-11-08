"use client";

import { useRef } from "react";
import { Panel } from "@/components/ui/panel";
import { StripChart, type Point } from "@/components/ui/chart";
import { EmptyState, Loaded } from "@/components/ui/states";
import { LcdCounter } from "@/components/ui/lcd";
import { metrics, type MetricSummary } from "@/lib/api";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

interface Result {
  list: MetricSummary[];
  history: Record<string, Point[]>;
}

const MAX_SAMPLES = 120;

/**
 * If the API returns a time series we plot it. Otherwise the recorder pen draws the
 * real `latest` values as they arrive while this page is open (labelled as such).
 */
export function MetricsPanel() {
  const { orgId, projectId } = useScope();
  const hist = useRef<Record<string, Point[]>>({});
  const res = useResource<Result>(`metrics:${projectId}`, async (signal) => {
    const list = await metrics.project(orgId, projectId, { signal });
    for (const m of list) {
      const prev = hist.current[m.name] ?? [];
      const t = m.recorded_at ? new Date(m.recorded_at).getTime() : Date.now();
      if (prev.length === 0 || prev[prev.length - 1].t !== t) hist.current[m.name] = [...prev, { t, v: m.latest }].slice(-MAX_SAMPLES);
    }
    return { list, history: { ...hist.current } };
  }, { interval: 5000 });

  return (
    <Loaded res={res} isEmpty={(d) => d.list.length === 0} empty={<EmptyState title="No metrics reported" body="This project has not sent any metric samples yet." />}>
      {(d) => (
        <div className="flex flex-col gap-6">
          <div className="grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 150px), 1fr))" }}>
            {d.list.slice(0, 6).map((m) => (
              <LcdCounter key={m.name} label={m.name} value={Number.isInteger(m.latest) ? String(m.latest) : m.latest.toFixed(2)} unit={m.unit} />
            ))}
          </div>
          <Panel title="Recorder" variant="paper">
            <p className="mt-0 text-[13px] opacity-80">Samples are real values from the metrics service, plotted as they arrive while this page is open.</p>
            <div className="grid gap-5 lg:grid-cols-2">
              {d.list.map((m) => {
                const series = m.series?.map((p) => ({ t: new Date(p.t).getTime(), v: p.v })) ?? d.history[m.name] ?? [];
                return <StripChart key={m.name} label={m.name} unit={m.unit} points={series} />;
              })}
            </div>
          </Panel>
        </div>
      )}
    </Loaded>
  );
}
