import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, KeyRound } from "lucide-react";
import { api } from "../lib/api";
import type { AccessKey, Bucket } from "../lib/types";

const project = "default";

export default function BucketsPage() {
  const qc = useQueryClient();
  const buckets = useQuery<Bucket[]>({
    queryKey: ["buckets"],
    queryFn: () => api.get<Bucket[]>(`/api/v1/projects/${project}/buckets`).catch(() => []),
  });
  const keys = useQuery<AccessKey[]>({
    queryKey: ["access-keys"],
    queryFn: () => api.get<AccessKey[]>(`/api/v1/projects/${project}/access-keys`).catch(() => []),
  });

  const [newBucket, setNewBucket] = useState("");
  const [newKeyOpen, setNewKeyOpen] = useState(false);

  const createBucket = useMutation({
    mutationFn: (name: string) =>
      api.post(`/api/v1/projects/${project}/buckets`, {
        meta: { project, name },
      }),
    onSuccess: () => {
      setNewBucket("");
      qc.invalidateQueries({ queryKey: ["buckets"] });
    },
  });

  const deleteBucket = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/buckets/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["buckets"] }),
  });

  return (
    <div className="p-8 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">S3 buckets</h1>
        <p className="text-muted">S3-compatible object storage backed by Garage.</p>
      </div>

      <div className="card p-4 flex items-center gap-2">
        <input
          className="input flex-1 font-mono"
          placeholder="bucket-name"
          value={newBucket}
          onChange={(e) => setNewBucket(e.target.value)}
        />
        <button className="btn-primary" onClick={() => createBucket.mutate(newBucket)} disabled={!newBucket}>
          <Plus size={14} /> Create
        </button>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Bucket</th>
              <th className="px-4 py-2 text-left">Garage ID</th>
              <th className="px-4 py-2 text-left">Status</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {(buckets.data ?? []).map((b) => (
              <tr key={b.meta.uid} className="border-t border-border">
                <td className="px-4 py-2 font-mono">{b.meta.name}</td>
                <td className="px-4 py-2 font-mono text-muted">{b.garageId || "—"}</td>
                <td className="px-4 py-2">
                  <span className={b.status.phase === "Running" ? "badge-success" : "badge"}>
                    {b.status.phase || "—"}
                  </span>
                </td>
                <td className="px-4 py-2">
                  <div className="flex justify-end">
                    <button className="btn-danger" onClick={() => deleteBucket.mutate(b.meta.name)}>
                      <Trash2 size={14} />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!buckets.data?.length && (
              <tr><td colSpan={4} className="px-4 py-12 text-center text-muted">No buckets yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <h2 className="font-medium">Access keys</h2>
          <button className="btn" onClick={() => setNewKeyOpen(true)}><KeyRound size={14} /> New key</button>
        </div>
        <div className="card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-elevated text-muted">
              <tr>
                <th className="px-4 py-2 text-left">Name</th>
                <th className="px-4 py-2 text-left">Bucket</th>
                <th className="px-4 py-2 text-left">Permissions</th>
                <th className="px-4 py-2 text-left">Access key id</th>
              </tr>
            </thead>
            <tbody>
              {(keys.data ?? []).map((k) => (
                <tr key={k.meta.uid} className="border-t border-border">
                  <td className="px-4 py-2 font-mono">{k.meta.name}</td>
                  <td className="px-4 py-2 font-mono text-muted">{k.bucketName || "*"}</td>
                  <td className="px-4 py-2"><span className="badge">{k.permissions}</span></td>
                  <td className="px-4 py-2 font-mono text-muted">{k.accessKeyId}</td>
                </tr>
              ))}
              {!keys.data?.length && (
                <tr><td colSpan={4} className="px-4 py-12 text-center text-muted">No keys yet.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {newKeyOpen && <NewKeyDialog onClose={() => setNewKeyOpen(false)} buckets={buckets.data ?? []} />}
    </div>
  );
}

function NewKeyDialog({ onClose, buckets }: { onClose: () => void; buckets: Bucket[] }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [bucket, setBucket] = useState("");
  const [perm, setPerm] = useState("read");
  const [secret, setSecret] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const out = await api.post<AccessKey>(`/api/v1/projects/${project}/access-keys`, {
      name,
      bucket,
      permissions: perm,
    });
    qc.invalidateQueries({ queryKey: ["access-keys"] });
    setSecret(out.secretAccessKey ?? null);
  }

  return (
    <div className="fixed inset-0 bg-bg/80 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-md p-6 space-y-4">
        <h2 className="font-semibold">New access key</h2>
        {secret ? (
          <div className="space-y-2">
            <p className="text-sm text-muted">
              The secret is shown only once. Copy it now.
            </p>
            <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono break-all">{secret}</pre>
            <div className="flex justify-end">
              <button type="button" className="btn-primary" onClick={onClose}>Done</button>
            </div>
          </div>
        ) : (
          <>
            <div className="space-y-1">
              <label className="text-sm text-muted">Name</label>
              <input className="input font-mono" value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Bucket</label>
              <select className="input" value={bucket} onChange={(e) => setBucket(e.target.value)}>
                <option value="">All buckets</option>
                {buckets.map((b) => (
                  <option key={b.meta.name} value={b.meta.name}>{b.meta.name}</option>
                ))}
              </select>
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Permissions</label>
              <select className="input" value={perm} onChange={(e) => setPerm(e.target.value)}>
                <option value="read">Read</option>
                <option value="write">Read + write</option>
                <option value="owner">Owner</option>
              </select>
            </div>
            <div className="flex justify-end gap-2">
              <button type="button" className="btn" onClick={onClose}>Cancel</button>
              <button type="submit" className="btn-primary">Create</button>
            </div>
          </>
        )}
      </form>
    </div>
  );
}
