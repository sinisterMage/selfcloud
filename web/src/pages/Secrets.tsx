import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, KeyRound, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { useProject } from "../lib/project";
import type { SecretRecord } from "../lib/types";
import { SkeletonRows } from "../components/Skeleton";
import EmptyState from "../components/EmptyState";

export default function SecretsPage() {
  const qc = useQueryClient();
  const { project } = useProject();
  const secs = useQuery<SecretRecord[]>({
    queryKey: ["secrets", project],
    queryFn: () => api.get<SecretRecord[]>(`/api/v1/projects/${project}/secrets`).catch(() => []),
  });
  const [open, setOpen] = useState(false);

  const remove = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/secrets/${name}`),
    onSuccess: (_d, name) => {
      toast.success(`Deleted ${name}`);
      qc.invalidateQueries({ queryKey: ["secrets", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  return (
    <div className="p-8 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Secrets</h1>
          <p className="text-muted">
            Encrypted at rest with AES-256-GCM. Reference from env vars with{" "}
            <code className="font-mono">secret://name</code>.
          </p>
        </div>
        <button className="btn-primary" onClick={() => setOpen(true)}>
          <Plus size={14} /> New secret
        </button>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Name</th>
              <th className="px-4 py-2 text-left">Description</th>
              <th className="px-4 py-2 text-left">Version</th>
              <th className="px-4 py-2 text-left">Updated</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {secs.isLoading && <SkeletonRows rows={3} cols={5} />}
            {!secs.isLoading &&
              (secs.data ?? []).map((sec) => (
                <tr key={sec.meta.uid || sec.meta.name} className="border-t border-border hover:bg-elevated/40">
                  <td className="px-4 py-2 font-mono">{sec.meta.name}</td>
                  <td className="px-4 py-2 text-muted">{sec.description || ""}</td>
                  <td className="px-4 py-2"><span className="badge">v{sec.version}</span></td>
                  <td className="px-4 py-2 text-xs text-muted">
                    {sec.meta.updatedAt ? new Date(sec.meta.updatedAt).toLocaleString() : "-"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <RevealButton name={sec.meta.name} />
                    <button
                      className="btn-danger ml-2"
                      title="Delete"
                      onClick={() => {
                        if (confirm(`Delete secret ${sec.meta.name}?`)) remove.mutate(sec.meta.name);
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            {!secs.isLoading && !secs.data?.length && (
              <tr>
                <td colSpan={5}>
                  <EmptyState
                    icon={KeyRound}
                    title="No secrets yet"
                    description="Store API tokens, database passwords, and signing keys. Reference them by name from container env vars or function configs."
                    action={
                      <button className="btn-primary" onClick={() => setOpen(true)}>
                        <Plus size={14} /> New secret
                      </button>
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {open && <NewSecretDialog onClose={() => setOpen(false)} />}
    </div>
  );
}

function RevealButton({ name }: { name: string }) {
  const { project } = useProject();
  const [show, setShow] = useState(false);
  const [value, setValue] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reveal() {
    setBusy(true);
    try {
      const res = await api.post<{ value: string }>(`/api/v1/projects/${project}/secrets/${name}/reveal`, {});
      setValue(res.value);
      setShow(true);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "reveal failed");
    } finally {
      setBusy(false);
    }
  }

  if (!show) {
    return (
      <button className="btn" title="Reveal (admin only)" onClick={reveal} disabled={busy}>
        <Eye size={14} />
      </button>
    );
  }
  return (
    <span className="inline-flex items-center gap-2">
      <code className="font-mono text-xs bg-elevated px-2 py-1 rounded">{value}</code>
      <button
        className="btn"
        title="Hide"
        onClick={() => {
          setShow(false);
          setValue(null);
        }}
      >
        <EyeOff size={14} />
      </button>
    </span>
  );
}

function NewSecretDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/projects/${project}/secrets`, { name, description, value });
      toast.success(`Created ${name}`);
      qc.invalidateQueries({ queryKey: ["secrets", project] });
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-bg/70 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-md p-6 space-y-4">
        <h2 className="font-semibold">New secret</h2>
        <div className="space-y-1">
          <label className="text-sm text-muted">Name</label>
          <input
            className="input font-mono"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="github-token"
            required
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Description</label>
          <input
            className="input"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Personal access token used for repo cloning"
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Value</label>
          <textarea
            className="input font-mono h-24"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            required
            spellCheck={false}
          />
          <p className="text-xs text-muted">
            Encrypted at rest with the cluster master key. Plaintext only returned via the explicit
            reveal endpoint (admin only).
          </p>
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn-primary" disabled={busy}>
            <Plus size={14} /> {busy ? "Creating..." : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}
