import { useQuery } from "@tanstack/react-query";
import { Boxes, HardDrive, Network, Zap } from "lucide-react";
import { api } from "../lib/api";
import type { Container, Bucket, FunctionRecord, Node } from "../lib/types";

const project = "default";

function useResources<T>(path: string) {
  return useQuery<T[]>({
    queryKey: [path],
    queryFn: () => api.get<T[]>(path).catch(() => [] as T[]),
  });
}

export default function OverviewPage() {
  const containers = useResources<Container>(`/api/v1/projects/${project}/containers`);
  const buckets = useResources<Bucket>(`/api/v1/projects/${project}/buckets`);
  const fns = useResources<FunctionRecord>(`/api/v1/projects/${project}/functions`);
  const nodes = useResources<Node>(`/api/v1/cluster/nodes`);

  const cards = [
    { label: "Containers", icon: Boxes, n: containers.data?.length ?? 0 },
    { label: "Buckets", icon: HardDrive, n: buckets.data?.length ?? 0 },
    { label: "Functions", icon: Zap, n: fns.data?.length ?? 0 },
    { label: "Nodes", icon: Network, n: Math.max(1, nodes.data?.length ?? 0) },
  ];

  return (
    <div className="p-8 space-y-8">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Overview</h1>
        <p className="text-muted">Your private cloud at a glance.</p>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {cards.map((c) => (
          <div key={c.label} className="card p-5 flex items-center gap-4">
            <div className="rounded-lg bg-accent/10 p-3 text-accent">
              <c.icon size={20} />
            </div>
            <div>
              <div className="text-3xl font-semibold">{c.n}</div>
              <div className="text-muted text-sm">{c.label}</div>
            </div>
          </div>
        ))}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="card p-5">
          <div className="font-medium mb-3">Recent containers</div>
          {(containers.data ?? []).slice(0, 5).map((c) => (
            <div key={c.meta.uid} className="flex items-center justify-between py-2 border-b border-border last:border-b-0">
              <div>
                <div className="font-mono text-sm">{c.meta.name}</div>
                <div className="text-muted text-xs">{c.image}</div>
              </div>
              <span className={c.status.phase === "Running" ? "badge-success" : "badge"}>{c.status.phase || "—"}</span>
            </div>
          ))}
          {!containers.data?.length && <div className="text-muted text-sm">Nothing yet. Create your first container from the Containers tab.</div>}
        </div>
        <div className="card p-5">
          <div className="font-medium mb-3">Recent functions</div>
          {(fns.data ?? []).slice(0, 5).map((f) => (
            <div key={f.meta.uid} className="flex items-center justify-between py-2 border-b border-border last:border-b-0">
              <div>
                <div className="font-mono text-sm">{f.meta.name}</div>
                <div className="text-muted text-xs">{f.runtime}</div>
              </div>
              <span className="badge">{f.triggers?.length ?? 0} triggers</span>
            </div>
          ))}
          {!fns.data?.length && <div className="text-muted text-sm">Deploy a Wasm or Firecracker function from the Functions tab.</div>}
        </div>
      </div>
    </div>
  );
}
