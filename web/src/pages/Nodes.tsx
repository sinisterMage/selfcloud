import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Copy, Plus } from "lucide-react";
import { api } from "../lib/api";
import type { ClusterConfig, Node } from "../lib/types";

export default function NodesPage() {
  const cluster = useQuery<ClusterConfig>({
    queryKey: ["cluster"],
    queryFn: () => api.get<ClusterConfig>("/api/v1/cluster"),
  });
  const nodes = useQuery<Node[]>({
    queryKey: ["nodes"],
    queryFn: () => api.get<Node[]>("/api/v1/cluster/nodes").catch(() => []),
  });
  const [issued, setIssued] = useState<{ command: string; expiresAt: string } | null>(null);

  const issue = useMutation({
    mutationFn: () =>
      api.post<{ command: string; expiresAt: string }>("/api/v1/cluster/join-tokens", { ttl: "24h" }),
    onSuccess: setIssued,
  });

  return (
    <div className="p-8 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Nodes</h1>
        <p className="text-muted">Machines participating in this selfCloud cluster.</p>
      </div>

      <div className="card p-4">
        <div className="flex items-center justify-between">
          <div>
            <div className="font-medium">Mode</div>
            <div className="text-muted text-sm">
              {cluster.data?.multiNode ? "Multi-node coordinator" : "Single node"}
              {cluster.data?.multiNode && (
                <> · replication factor <span className="font-mono">{cluster.data.replicationFactor ?? 1}</span></>
              )}
            </div>
          </div>
          {cluster.data?.multiNode && (
            <button className="btn-primary" onClick={() => issue.mutate()} disabled={issue.isPending}>
              <Plus size={14} /> {issue.isPending ? "Generating..." : "Add node"}
            </button>
          )}
        </div>
      </div>

      {issued && (
        <div className="card p-4 space-y-2">
          <div className="text-muted text-sm">Run this on the new machine (token expires {new Date(issued.expiresAt).toLocaleString()}):</div>
          <div className="flex items-center gap-2">
            <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono break-all flex-1">{issued.command}</pre>
            <button className="btn" onClick={() => navigator.clipboard.writeText(issued.command)}>
              <Copy size={14} />
            </button>
          </div>
        </div>
      )}

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">ID</th>
              <th className="px-4 py-2 text-left">Address</th>
              <th className="px-4 py-2 text-left">Roles</th>
              <th className="px-4 py-2 text-left">Status</th>
              <th className="px-4 py-2 text-left">Version</th>
            </tr>
          </thead>
          <tbody>
            {(nodes.data ?? []).map((n) => (
              <tr key={n.meta.uid} className="border-t border-border">
                <td className="px-4 py-2 font-mono">{n.meta.name}</td>
                <td className="px-4 py-2 font-mono text-muted">{n.address}</td>
                <td className="px-4 py-2">
                  {n.roles?.map((r) => <span key={r} className="badge mr-1">{r}</span>)}
                </td>
                <td className="px-4 py-2">
                  <span className={n.status?.phase === "Running" ? "badge-success" : "badge"}>
                    {n.status?.phase || "—"}
                  </span>
                </td>
                <td className="px-4 py-2 font-mono text-muted">{n.version}</td>
              </tr>
            ))}
            {!nodes.data?.length && (
              <tr><td colSpan={5} className="px-4 py-12 text-center text-muted">This machine is the only node so far.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
