"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { PageHeader, Panel } from "@/components/ui/panel";
import { EmptyState, Loaded } from "@/components/ui/states";
import { Tag } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { system, type SettingField } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useResource } from "@/lib/use-resource";

function PlatformSettings() {
  const toast = useToast();
  const res = useResource("settings", (signal) => system.settings({ signal }));
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const save = async (fields: SettingField[]) => {
    const body: Record<string, string> = {};
    for (const f of fields) if (f.editable && draft[f.key] !== undefined) body[f.key] = draft[f.key];
    if (Object.keys(body).length === 0) return;
    setBusy(true);
    try {
      await system.patchSettings(body);
      setDraft({});
      toast.success("Settings saved", "Changes apply to services that read them; some need a restart.");
      res.reload();
    } catch (err) {
      toast.error("Could not save settings", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel title="Platform settings" variant="paper">
      <Loaded res={res} isEmpty={(d) => d.length === 0} empty={<p className="m-0">The server reports no settings.</p>} rows={4}>
        {(fields) => {
          const groups = Array.from(new Set(fields.map((f) => f.group || "General")));
          return (
            <form
              className="flex flex-col gap-8"
              onSubmit={(e) => {
                e.preventDefault();
                void save(fields);
              }}
            >
              {groups.map((g) => (
                <fieldset key={g} className="m-0 border-0 p-0">
                  <legend className="label mb-3">{g}</legend>
                  <div className="grid gap-4 md:grid-cols-2">
                    {fields.filter((f) => (f.group || "General") === g).map((f) => (
                      <Input
                        key={f.key}
                        label={
                          <>
                            {f.label || f.key} {!f.editable && <Tag tone="idle">read-only</Tag>}
                          </>
                        }
                        type={f.secret ? "password" : "text"}
                        autoComplete="off"
                        disabled={!f.editable}
                        value={draft[f.key] ?? (f.secret ? "" : f.value === null || f.value === undefined ? "" : String(f.value))}
                        placeholder={f.secret ? (f.is_set ? "(stored, leave empty to keep)" : "(not set)") : undefined}
                        onChange={(e) => setDraft((d) => ({ ...d, [f.key]: e.target.value }))}
                        hint={f.description || (f.source ? `source: ${f.source}` : undefined)}
                      />
                    ))}
                  </div>
                </fieldset>
              ))}
              <div><Button type="submit" variant="primary" loading={busy} disabled={Object.keys(draft).length === 0}>Save settings</Button></div>
            </form>
          );
        }}
      </Loaded>
    </Panel>
  );
}


export default function PlatformPage() {
  const { isPlatformAdmin } = useOrg();
  return (
    <>
      <PageHeader title="Platform settings" eyebrow="Platform" description="Email and AI keys for the whole installation. Read-only rows show what the platform is really using." />
      {isPlatformAdmin ? <PlatformSettings /> : <EmptyState title="Platform administrators only" body="Ask the platform administrator to change these settings." />}
    </>
  );
}
