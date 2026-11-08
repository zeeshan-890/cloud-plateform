"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { LogTape } from "@/components/ui/code";
import { Input, Select } from "@/components/ui/field";
import { Panel } from "@/components/ui/panel";
import { ErrorState, SkeletonRows } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { logs } from "@/lib/api";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

const SOURCES = ["", "app", "build", "runtime", "platform"];
const LEVELS = ["", "debug", "info", "warn", "error"];

export function LogsPanel() {
  const { orgId, projectId } = useScope();
  const [source, setSource] = useState("");
  const [level, setLevel] = useState("");
  const [q, setQ] = useState("");
  const [applied, setApplied] = useState("");
  const [limit, setLimit] = useState("200");
  const [tail, setTail] = useState(true);
  const key = `logs:${projectId}:${source}:${level}:${applied}:${limit}`;
  const res = useResource(key, (signal) => logs.query(orgId, projectId, { source: source || undefined, level: level || undefined, q: applied || undefined, limit: Number(limit) }, { signal }), { interval: tail ? 3000 : undefined });
  // API returns newest first; the tape reads oldest to newest.
  const entries = [...(res.data ?? [])].sort((a, b) => a.logged_at.localeCompare(b.logged_at));

  return (
    <Panel
      title="Logs"
      actions={
        <>
          {tail && <Tag tone="ok">live</Tag>}
          <Button size="sm" aria-pressed={tail} onClick={() => setTail(!tail)}>{tail ? "Pause tail" : "Live tail"}</Button>
          <Button size="sm" onClick={res.reload}>Refresh</Button>
        </>
      }
    >
      <form
        className="mb-4 grid items-end gap-3 sm:grid-cols-[1fr_150px_150px_110px_auto]"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(q.trim());
        }}
      >
        <Input label="Search" type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="text in message" />
        <Select label="Source" value={source} onChange={(e) => setSource(e.target.value)}>
          {SOURCES.map((s) => <option key={s} value={s}>{s || "all"}</option>)}
        </Select>
        <Select label="Level" value={level} onChange={(e) => setLevel(e.target.value)}>
          {LEVELS.map((s) => <option key={s} value={s}>{s || "all"}</option>)}
        </Select>
        <Select label="Lines" value={limit} onChange={(e) => setLimit(e.target.value)}>
          {["100", "200", "500", "1000"].map((s) => <option key={s}>{s}</option>)}
        </Select>
        <Button type="submit">Apply</Button>
      </form>
      {res.error && !res.data ? (
        <ErrorState error={res.error} onRetry={res.reload} />
      ) : res.loading ? (
        <SkeletonRows rows={4} label="Loading logs" />
      ) : (
        <LogTape
          label="Project logs"
          follow={tail}
          maxHeight={560}
          empty="No log lines match these filters."
          lines={entries.map((e) => ({ text: `[${e.source}] ${e.message}`, level: e.level, at: new Date(e.logged_at).toLocaleTimeString() }))}
        />
      )}
    </Panel>
  );
}
