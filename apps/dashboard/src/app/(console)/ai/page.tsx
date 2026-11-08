"use client";

import { FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Select, Textarea } from "@/components/ui/field";
import { ConfirmDialog } from "@/components/ui/modal";
import { PageHeader, Panel, Readout } from "@/components/ui/panel";
import { Loaded } from "@/components/ui/states";
import { Lamp, Tag } from "@/components/ui/status";
import { useToast } from "@/components/ui/toast";
import { AiResponseView } from "@/features/ai-view";
import { ai, type AiAnswer } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import { useProjects } from "@/lib/project-context";
import { useResource } from "@/lib/use-resource";

const PROVIDERS = ["gemini", "openai", "anthropic"];

export default function AiPage() {
  const { currentOrg, can } = useOrg();
  const { projects, activeProject } = useProjects();
  const toast = useToast();
  const orgId = currentOrg?.id;
  const status = useResource(orgId ? `aist:${orgId}` : null, (signal) => ai.status(orgId!, { signal }));
  const config = useResource(orgId && can("admin") ? `aicfg:${orgId}` : null, (signal) => ai.config(orgId!, { signal }));
  const [prompt, setPrompt] = useState("");
  const [projectId, setProjectId] = useState("");
  const [busy, setBusy] = useState(false);
  const [answer, setAnswer] = useState<AiAnswer | undefined>();
  const [error, setError] = useState<unknown>();
  const [provider, setProvider] = useState("gemini");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [saving, setSaving] = useState(false);
  const [clearOpen, setClearOpen] = useState(false);

  const ask = async (e: FormEvent) => {
    e.preventDefault();
    if (!orgId) return;
    setBusy(true);
    setError(undefined);
    setAnswer(undefined);
    try {
      setAnswer(await ai.ask(orgId, prompt.trim(), projectId || activeProject?.id));
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!orgId) return;
    setSaving(true);
    try {
      await ai.setConfig(orgId, { provider, api_key: apiKey, model: model.trim() || undefined });
      setApiKey("");
      toast.success("AI configuration saved");
      config.reload();
      status.reload();
    } catch (err) {
      toast.error("Could not save", err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <PageHeader title="AI assistant" eyebrow={currentOrg?.name} description="Explain failures, read logs and answer questions about your projects." />
      <div className="flex flex-col gap-6">
        <Panel title="Provider status">
          <Loaded res={status} rows={1}>
            {(s) => (
              <div className="flex flex-wrap items-center gap-6">
                <span className="inline-flex items-center gap-2">
                  <Lamp state={s.configured === false || s.mode === "unconfigured" ? "fail" : "ok"} large />
                  <strong>{s.configured === false || s.mode === "unconfigured" ? "Not configured" : "Ready"}</strong>
                </span>
                <Readout label="Provider">{s.provider || "-"}</Readout>
                <Readout label="Model">{s.model || "-"}</Readout>
                {s.mode && <Tag tone="idle">{s.mode}</Tag>}
              </div>
            )}
          </Loaded>
        </Panel>

        <Panel title="Ask">
          <form onSubmit={ask} className="flex flex-col gap-4">
            <Select label="Context project" value={projectId || activeProject?.id || ""} onChange={(e) => setProjectId(e.target.value)}>
              {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
            <Textarea label="Question" value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={4} placeholder="Why did my last deployment fail?" data-testid="ai-prompt" />
            <div><Button type="submit" variant="primary" loading={busy} disabled={!prompt.trim()}>Ask</Button></div>
          </form>
          {(answer || error !== undefined) && (
            <div className="well mt-5" aria-live="polite">
              <AiResponseView answer={answer} error={error} />
            </div>
          )}
        </Panel>

        {can("admin") ? (
          <Panel title="Organization AI key" variant="paper">
            <Loaded res={config} rows={1}>
              {(c) => (
                <div className="mb-4 flex flex-wrap items-center gap-4">
                  <Lamp state={c.has_key ? "ok" : "idle"} />
                  <span>{c.has_key ? `Key stored (${c.masked_key || "hidden"})` : "No organization key. The platform default is used if one exists."}</span>
                  {c.has_key && <Button size="sm" variant="danger" onClick={() => setClearOpen(true)}>Remove key</Button>}
                </div>
              )}
            </Loaded>
            <form onSubmit={save} className="grid items-end gap-3 sm:grid-cols-[160px_1fr_180px_auto]">
              <Input label="Provider" list="providers" value={provider} onChange={(e) => setProvider(e.target.value)} />
              <datalist id="providers">{PROVIDERS.map((p) => <option key={p} value={p} />)}</datalist>
              <Input label="API key" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} hint="Stored encrypted, never shown again." />
              <Input label="Model (optional)" value={model} onChange={(e) => setModel(e.target.value)} />
              <Button type="submit" variant="primary" loading={saving} disabled={!apiKey.trim()}>Save</Button>
            </form>
          </Panel>
        ) : (
          <p className="muted">Only admins can change the organization AI key.</p>
        )}
      </div>
      <ConfirmDialog
        open={clearOpen}
        title="Remove AI key"
        body="AI features stop working for this organization unless a platform default exists."
        confirmLabel="Remove key"
        onClose={() => setClearOpen(false)}
        onConfirm={async () => {
          await ai.clearConfig(orgId!);
          toast.success("AI key removed");
          config.reload();
          status.reload();
        }}
      />
    </>
  );
}
