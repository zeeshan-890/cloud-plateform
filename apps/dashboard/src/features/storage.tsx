"use client";

import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/code";
import { Icon } from "@/components/ui/icons";
import { ConfirmDialog, Modal } from "@/components/ui/modal";
import { Panel } from "@/components/ui/panel";
import { EmptyState, ErrorState, Loaded, SkeletonRows } from "@/components/ui/states";
import { DataTable, type Column } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { storage, type StorageObject } from "@/lib/api";
import { bytes, dateTime, pct } from "@/lib/format";
import { useResource } from "@/lib/use-resource";
import { useScope } from "./scope";

export function StoragePanel() {
  const { orgId, projectId, can } = useScope();
  const toast = useToast();
  const bucket = useResource(`bucket:${projectId}`, (signal) => storage.bucket(orgId, projectId, { signal }));
  const hasBucket = !!bucket.data;
  const objects = useResource(hasBucket ? `objs:${projectId}` : null, (signal) => storage.objects(orgId, projectId, undefined, { signal }));
  const usage = useResource(hasBucket ? `usage:${projectId}` : null, (signal) => storage.usage(orgId, projectId, { signal }));
  const fileRef = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<{ name: string; fraction: number } | null>(null);
  const [del, setDel] = useState<StorageObject | null>(null);
  const [signed, setSigned] = useState<{ key: string; url: string; seconds: number } | null>(null);
  const [creating, setCreating] = useState(false);

  const upload = async (file: File) => {
    setProgress({ name: file.name, fraction: 0 });
    try {
      await storage.upload(orgId, projectId, file.name, file, (f) => setProgress({ name: file.name, fraction: f }));
      toast.success("Uploaded", file.name);
      objects.reload();
      usage.reload();
    } catch (err) {
      toast.error("Upload failed", err);
    } finally {
      setProgress(null);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const download = async (o: StorageObject) => {
    try {
      const blob = await storage.download(orgId, projectId, o.key);
      const href = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = href;
      a.download = o.key.split("/").pop() || "download";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(href);
    } catch (err) {
      toast.error("Download failed", err);
    }
  };

  const sign = async (o: StorageObject) => {
    try {
      const r = await storage.signedUrl(orgId, projectId, o.key, "15m");
      setSigned({ key: o.key, url: r.url, seconds: r.expires_in_seconds });
    } catch (err) {
      toast.error("Could not sign URL", err);
    }
  };

  if (bucket.error && !bucket.data) return <ErrorState error={bucket.error} onRetry={bucket.reload} />;
  if (bucket.loading) return <SkeletonRows rows={3} label="Loading storage" />;

  if (!bucket.data) {
    return (
      <EmptyState
        title="No bucket yet"
        body="Create the project's private object-storage bucket to upload files."
        action={
          can("member") && (
            <Button
              variant="primary"
              loading={creating}
              onClick={async () => {
                setCreating(true);
                try {
                  await storage.createBucket(orgId, projectId);
                  toast.success("Bucket created");
                  bucket.reload();
                } catch (err) {
                  toast.error("Could not create bucket", err);
                } finally {
                  setCreating(false);
                }
              }}
            >
              Create bucket
            </Button>
          )
        }
      />
    );
  }

  const u = usage.data;
  const cols: Column<StorageObject>[] = [
    { key: "k", header: "Key", cell: (o) => <span className="mono break-all text-[13px]">{o.key}</span> },
    { key: "s", header: "Size", cell: (o) => <span className="mono">{bytes(o.size_bytes)}</span> },
    { key: "t", header: "Type", cell: (o) => <span className="mono text-[12.5px]">{o.content_type || "-"}</span> },
    { key: "m", header: "Modified", cell: (o) => dateTime(o.updated_at ?? o.created_at) },
    {
      key: "a",
      header: "Actions",
      srOnlyHeader: true,
      cell: (o) => (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" icon aria-label={`Download ${o.key}`} onClick={() => void download(o)}><Icon name="download" size={14} /></Button>
          <Button size="sm" icon aria-label={`Signed URL for ${o.key}`} onClick={() => void sign(o)}><Icon name="link" size={14} /></Button>
          {can("admin") && <Button size="sm" variant="danger" icon aria-label={`Delete ${o.key}`} onClick={() => setDel(o)}><Icon name="trash" size={14} /></Button>}
        </div>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-6">
      <Panel
        title={`Bucket ${bucket.data.name}`}
        actions={
          can("member") && (
            <>
              <input ref={fileRef} type="file" className="sr-only" aria-label="Choose file to upload" onChange={(e) => e.target.files?.[0] && void upload(e.target.files[0])} />
              <Button variant="primary" size="sm" onClick={() => fileRef.current?.click()} disabled={!!progress}>
                <Icon name="upload" size={14} /> Upload file
              </Button>
            </>
          )
        }
      >
        {u && (
          <div className="mb-4">
            <div className="label mb-1">
              Used {bytes(u.used_bytes)}
              {u.quota_bytes ? ` of ${bytes(u.quota_bytes)}` : ""} {u.object_count !== undefined && `- ${u.object_count} objects`}
            </div>
            {u.quota_bytes ? (
              <div className="well progress-track !p-0" role="progressbar" aria-valuenow={Math.round(pct(u.used_bytes, u.quota_bytes))} aria-valuemin={0} aria-valuemax={100} aria-label="Storage used">
                <div className="progress-bar" style={{ width: `${pct(u.used_bytes, u.quota_bytes)}%` }} />
              </div>
            ) : null}
          </div>
        )}
        {progress && (
          <div className="mb-4" role="status">
            <div className="label mb-1">Uploading {progress.name} - {Math.round(progress.fraction * 100)}%</div>
            <div className="well progress-track !p-0" role="progressbar" aria-valuenow={Math.round(progress.fraction * 100)} aria-valuemin={0} aria-valuemax={100} aria-label="Upload progress">
              <div className="progress-bar" style={{ width: `${progress.fraction * 100}%` }} />
            </div>
          </div>
        )}
        <Loaded res={objects} isEmpty={(d) => d.length === 0} empty={<EmptyState title="Bucket is empty" body="Upload a file to see it here." />}>
          {(rows) => <DataTable caption="Stored objects" columns={cols} rows={rows} rowKey={(o) => o.key} />}
        </Loaded>
      </Panel>
      <ConfirmDialog
        open={!!del}
        title="Delete object"
        body={<>Permanently delete <span className="mono break-all">{del?.key}</span>?</>}
        confirmLabel="Delete object"
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          await storage.remove(orgId, projectId, del.key);
          toast.success("Object deleted");
          objects.reload();
          usage.reload();
        }}
      />
      <Modal open={!!signed} onClose={() => setSigned(null)} title="Signed URL" description={`Anyone with this link can download ${signed?.key} for ${signed ? Math.round(signed.seconds / 60) : 0} minutes.`}>
        <code className="code-block block">{signed?.url}</code>
        <div className="mt-3">{signed && <CopyButton text={signed.url} label="Copy link" />}</div>
      </Modal>
    </div>
  );
}
