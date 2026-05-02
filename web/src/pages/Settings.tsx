import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

interface Meta {
  version: string;
  goVersion: string;
  goos: string;
  goarch: string;
  uptimeSec: number;
}

export default function SettingsPage() {
  const meta = useQuery<Meta>({
    queryKey: ["meta"],
    queryFn: () => api.get<Meta>("/api/v1/meta", { auth: false }),
  });
  return (
    <div className="p-8 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-muted">Cluster-wide configuration and diagnostics.</p>
      </div>
      <div className="card p-4 grid grid-cols-2 gap-4 text-sm">
        <div>
          <div className="text-muted">Version</div>
          <div className="font-mono">{meta.data?.version}</div>
        </div>
        <div>
          <div className="text-muted">Runtime</div>
          <div className="font-mono">{meta.data ? `${meta.data.goVersion} ${meta.data.goos}/${meta.data.goarch}` : "—"}</div>
        </div>
        <div>
          <div className="text-muted">Uptime</div>
          <div className="font-mono">{meta.data ? `${meta.data.uptimeSec}s` : "—"}</div>
        </div>
      </div>
    </div>
  );
}
