import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Download,
  FileText,
  Folder,
  HardDrive,
  RefreshCw,
  Trash2,
  UploadCloud,
} from "lucide-react";
import { toast } from "sonner";
import { api, getToken } from "../lib/api";
import { useProject } from "../lib/project";
import EmptyState from "../components/EmptyState";
import { SkeletonRows } from "../components/Skeleton";

interface ObjectInfo {
  key: string;
  size: number;
  etag?: string;
  lastModified: string;
  contentType?: string;
}

function formatSize(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export default function BucketBrowser() {
  const { name } = useParams<{ name: string }>();
  const qc = useQueryClient();
  const { project } = useProject();
  const [prefix, setPrefix] = useState("");

  const objects = useQuery<ObjectInfo[]>({
    queryKey: ["objects", project, name, prefix],
    queryFn: () =>
      api.get<ObjectInfo[]>(
        `/api/v1/projects/${project}/buckets/${name}/objects?prefix=${encodeURIComponent(prefix)}`
      ),
  });

  const remove = useMutation({
    mutationFn: (key: string) =>
      api.del(`/api/v1/projects/${project}/buckets/${name}/object?key=${encodeURIComponent(key)}`),
    onSuccess: (_d, key) => {
      toast.success(`Deleted ${key}`);
      qc.invalidateQueries({ queryKey: ["objects", project, name, prefix] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  return (
    <div className="p-8 space-y-6">
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <Link to="/buckets" className="inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
            <ArrowLeft size={12} /> All buckets
          </Link>
          <h1 className="text-2xl font-semibold tracking-tight font-mono">{name}</h1>
          <p className="text-muted text-sm">S3 objects in this bucket.</p>
        </div>
        <div className="flex items-center gap-2">
          <UploadButton bucket={name!} prefix={prefix} onUploaded={() => qc.invalidateQueries({ queryKey: ["objects", project, name, prefix] })} />
          <button className="btn" onClick={() => objects.refetch()} title="Refresh">
            <RefreshCw size={14} />
          </button>
        </div>
      </div>

      <div className="card p-3 flex items-center gap-2">
        <Folder size={14} className="text-muted ml-1" />
        <input
          className="input font-mono"
          placeholder="prefix/"
          value={prefix}
          onChange={(e) => setPrefix(e.target.value)}
        />
        <span className="text-xs text-muted shrink-0">{(objects.data ?? []).length} object(s)</span>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Key</th>
              <th className="px-4 py-2 text-left">Size</th>
              <th className="px-4 py-2 text-left">Modified</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {objects.isLoading && <SkeletonRows rows={3} cols={4} />}
            {!objects.isLoading &&
              (objects.data ?? []).map((o) => (
                <tr key={o.key} className="border-t border-border hover:bg-elevated/40">
                  <td className="px-4 py-2 font-mono">
                    <span className="inline-flex items-center gap-2">
                      <FileText size={14} className="text-muted" />
                      {o.key}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-muted">{formatSize(o.size)}</td>
                  <td className="px-4 py-2 text-muted text-xs">{new Date(o.lastModified).toLocaleString()}</td>
                  <td className="px-4 py-2 text-right">
                    <a
                      className="btn"
                      href={`/api/v1/projects/${project}/buckets/${name}/object?key=${encodeURIComponent(o.key)}&download=1&access_token=${encodeURIComponent(getToken() || "")}`}
                      title="Download"
                    >
                      <Download size={14} />
                    </a>
                    <button
                      className="btn-danger ml-2"
                      onClick={() => {
                        if (confirm(`Delete ${o.key}?`)) remove.mutate(o.key);
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            {!objects.isLoading && !objects.data?.length && (
              <tr>
                <td colSpan={4}>
                  <EmptyState
                    icon={HardDrive}
                    title="No objects"
                    description={
                      prefix
                        ? `Nothing matches the prefix "${prefix}".`
                        : "This bucket is empty. Upload your first object to get started."
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function UploadButton({ bucket, prefix, onUploaded }: { bucket: string; prefix: string; onUploaded: () => void }) {
  const { project } = useProject();
  const ref = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  return (
    <>
      <input
        ref={ref}
        type="file"
        className="hidden"
        onChange={async (e) => {
          const file = e.target.files?.[0];
          if (!file) return;
          setBusy(true);
          const key = (prefix ? prefix.replace(/\/?$/, "/") : "") + file.name;
          try {
            const res = await fetch(
              `/api/v1/projects/${project}/buckets/${bucket}/objects?key=${encodeURIComponent(key)}`,
              {
                method: "PUT",
                headers: {
                  "content-type": file.type || "application/octet-stream",
                  ...(getToken() ? { authorization: `Bearer ${getToken()}` } : {}),
                },
                body: file,
              }
            );
            if (!res.ok) throw new Error(`upload failed: ${res.status}`);
            toast.success(`Uploaded ${file.name}`);
            onUploaded();
          } catch (e) {
            toast.error(e instanceof Error ? e.message : "upload failed");
          } finally {
            setBusy(false);
            if (ref.current) ref.current.value = "";
          }
        }}
      />
      <button className="btn-primary" disabled={busy} onClick={() => ref.current?.click()}>
        <UploadCloud size={14} /> {busy ? "Uploading..." : "Upload"}
      </button>
    </>
  );
}
