import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, Play, Plus, Square, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { useProject } from "../lib/project";
import type { Container } from "../lib/types";
import { SkeletonRows } from "../components/Skeleton";
import EmptyState from "../components/EmptyState";

export default function ContainersPage() {
  const qc = useQueryClient();
  const { project } = useProject();
  const list = useQuery<Container[]>({
    queryKey: ["containers", project],
    queryFn: () => api.get<Container[]>(`/api/v1/projects/${project}/containers`).catch(() => []),
  });
  const [open, setOpen] = useState(false);

  const start = useMutation({
    mutationFn: (name: string) => api.post(`/api/v1/projects/${project}/containers/${name}/start`),
    onSuccess: (_, name) => {
      toast.success(`Started ${name}`);
      qc.invalidateQueries({ queryKey: ["containers", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "start failed"),
  });
  const stop = useMutation({
    mutationFn: (name: string) => api.post(`/api/v1/projects/${project}/containers/${name}/stop`),
    onSuccess: (_, name) => {
      toast.success(`Stopped ${name}`);
      qc.invalidateQueries({ queryKey: ["containers", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "stop failed"),
  });
  const remove = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/containers/${name}`),
    onSuccess: (_, name) => {
      toast.success(`Deleted ${name}`);
      qc.invalidateQueries({ queryKey: ["containers", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  return (
    <div className="p-8 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Containers</h1>
          <p className="text-muted">OCI containers running on this cluster.</p>
        </div>
        <button className="btn-primary" onClick={() => setOpen(true)}>
          <Plus size={14} /> New container
        </button>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Name</th>
              <th className="px-4 py-2 text-left">Image</th>
              <th className="px-4 py-2 text-left">Status</th>
              <th className="px-4 py-2 text-left">Node</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && <SkeletonRows rows={3} cols={5} />}
            {!list.isLoading &&
              (list.data ?? []).map((c) => (
                <tr key={c.meta.uid} className="border-t border-border hover:bg-elevated/40">
                  <td className="px-4 py-2 font-mono">
                    <Link className="hover:text-accent" to={`/containers/${c.meta.name}`}>
                      {c.meta.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2 font-mono text-muted">{c.image}</td>
                  <td className="px-4 py-2">
                    <span className={c.status.phase === "Running" ? "badge-success" : "badge"}>
                      {c.status.phase || "—"}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-muted">{c.nodeId || "self"}</td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-2">
                      <button className="btn" title="Start" onClick={() => start.mutate(c.meta.name)}>
                        <Play size={14} />
                      </button>
                      <button className="btn" title="Stop" onClick={() => stop.mutate(c.meta.name)}>
                        <Square size={14} />
                      </button>
                      <button
                        className="btn-danger"
                        title="Delete"
                        onClick={() => {
                          if (confirm(`Delete container ${c.meta.name}?`)) remove.mutate(c.meta.name);
                        }}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            {!list.isLoading && !list.data?.length && (
              <tr>
                <td colSpan={5}>
                  <EmptyState
                    icon={Boxes}
                    title="No containers yet"
                    description="Run any OCI image from Docker Hub or your private registry. Bridge networking and port publishing are wired up automatically."
                    action={
                      <button className="btn-primary" onClick={() => setOpen(true)}>
                        <Plus size={14} /> New container
                      </button>
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {open && <NewContainerDialog onClose={() => setOpen(false)} />}
    </div>
  );
}

function NewContainerDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [name, setName] = useState("");
  const [image, setImage] = useState("docker.io/library/nginx:alpine");
  const [hostPort, setHostPort] = useState(8080);
  const [containerPort, setContainerPort] = useState(80);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    try {
      await api.post(`/api/v1/projects/${project}/containers`, {
        meta: { project, name },
        image,
        ports: [{ host: hostPort, container: containerPort, protocol: "tcp" }],
        restartPolicy: "Always",
      });
      toast.success(`Created ${name}`);
      qc.invalidateQueries({ queryKey: ["containers", project] });
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "create failed");
    }
  }

  return (
    <div className="fixed inset-0 bg-bg/70 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-md p-6 space-y-4">
        <h2 className="font-semibold">New container</h2>
        <div className="space-y-1">
          <label className="text-sm text-muted">Name</label>
          <input className="input font-mono" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Image</label>
          <input className="input font-mono" value={image} onChange={(e) => setImage(e.target.value)} />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1">
            <label className="text-sm text-muted">Host port</label>
            <input className="input" type="number" value={hostPort} onChange={(e) => setHostPort(Number(e.target.value))} />
          </div>
          <div className="space-y-1">
            <label className="text-sm text-muted">Container port</label>
            <input className="input" type="number" value={containerPort} onChange={(e) => setContainerPort(Number(e.target.value))} />
          </div>
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn-primary">Create</button>
        </div>
      </form>
    </div>
  );
}
