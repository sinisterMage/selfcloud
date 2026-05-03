import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Copy, KeyRound, Network, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ClusterConfig } from "../lib/types";

interface ApiToken {
  meta: { name: string; uid: string; createdAt?: string };
  isAdmin?: boolean;
  expiresAt?: string;
  plaintext?: string;
}

interface Meta {
  version: string;
  goVersion: string;
  goos: string;
  goarch: string;
  uptimeSec: number;
}

interface Health {
  ok?: boolean;
  ready?: boolean;
}

interface JoinToken {
  id: string;
  issuedBy: string;
  issuedAt: string;
  expiresAt: string;
  consumedBy?: string;
}

interface IssuedToken {
  id: string;
  token: string;
  expiresAt: string;
  command: string;
}

export default function SettingsPage() {
  const qc = useQueryClient();
  const meta = useQuery<Meta>({
    queryKey: ["meta"],
    queryFn: () => api.get<Meta>("/api/v1/meta", { auth: false }),
  });
  const health = useQuery<Health>({
    queryKey: ["healthz"],
    queryFn: () =>
      fetch("/healthz")
        .then((r) => r.json() as Promise<Health>)
        .catch(() => ({ ok: false })),
    refetchInterval: 5000,
  });
  const ready = useQuery<Health>({
    queryKey: ["readyz"],
    queryFn: () =>
      fetch("/readyz").then(async (r) => ({ ready: r.status === 200, ...(await r.json()) })),
    refetchInterval: 5000,
  });
  const cluster = useQuery<ClusterConfig>({
    queryKey: ["cluster"],
    queryFn: () => api.get<ClusterConfig>("/api/v1/cluster"),
  });
  const tokens = useQuery<JoinToken[]>({
    queryKey: ["join-tokens"],
    queryFn: () => api.get<JoinToken[]>("/api/v1/cluster/join-tokens").catch(() => []),
  });

  const apiTokens = useQuery<ApiToken[]>({
    queryKey: ["api-tokens"],
    queryFn: () => api.get<ApiToken[]>("/api/v1/auth/tokens").catch(() => []),
  });

  const [multi, setMulti] = useState<boolean | null>(null);
  const [rf, setRf] = useState<number | null>(null);
  const [issued, setIssued] = useState<IssuedToken | null>(null);
  const [newTokenName, setNewTokenName] = useState("");
  const [newTokenTTL, setNewTokenTTL] = useState("");
  const [issuedToken, setIssuedToken] = useState<ApiToken | null>(null);

  const effectiveMulti = multi ?? cluster.data?.multiNode ?? false;
  const effectiveRf = rf ?? cluster.data?.replicationFactor ?? 1;

  const save = useMutation({
    mutationFn: () =>
      api.put<ClusterConfig>("/api/v1/cluster", {
        multiNode: effectiveMulti,
        replicationFactor: effectiveRf,
      }),
    onSuccess: () => {
      toast.success("Cluster updated");
      setMulti(null);
      setRf(null);
      qc.invalidateQueries({ queryKey: ["cluster"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "save failed"),
  });

  const issue = useMutation({
    mutationFn: (ttl: string) =>
      api.post<IssuedToken>("/api/v1/cluster/join-tokens", { ttl }),
    onSuccess: (d) => {
      setIssued(d);
      toast.success("Join token issued");
      qc.invalidateQueries({ queryKey: ["join-tokens"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "issue failed"),
  });

  const createToken = useMutation({
    mutationFn: () =>
      api.post<ApiToken>("/api/v1/auth/tokens", {
        name: newTokenName.trim(),
        ttl: newTokenTTL.trim() || undefined,
      }),
    onSuccess: (t) => {
      setIssuedToken(t);
      toast.success(`Created ${t.meta.name}`);
      setNewTokenName("");
      setNewTokenTTL("");
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "create failed"),
  });

  const deleteToken = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/auth/tokens/${name}`),
    onSuccess: (_d, name) => {
      toast.success(`Revoked ${name}`);
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "revoke failed"),
  });

  return (
    <div className="p-8 space-y-6 max-w-4xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-muted">Cluster-wide configuration and diagnostics.</p>
      </div>

      {/* Diagnostics --------------------------------------------------- */}
      <div className="card p-4 space-y-3">
        <div className="flex items-center gap-2 font-medium">
          <Activity size={16} /> Diagnostics
        </div>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <div className="text-muted">Version</div>
            <div className="font-mono">{meta.data?.version ?? "—"}</div>
          </div>
          <div>
            <div className="text-muted">Runtime</div>
            <div className="font-mono">
              {meta.data ? `${meta.data.goVersion} ${meta.data.goos}/${meta.data.goarch}` : "—"}
            </div>
          </div>
          <div>
            <div className="text-muted">Uptime</div>
            <div className="font-mono">{meta.data ? `${meta.data.uptimeSec}s` : "—"}</div>
          </div>
          <div>
            <div className="text-muted">Health</div>
            <div className="font-mono flex items-center gap-2">
              <span className={health.data?.ok ? "badge-success" : "badge"}>
                /healthz {health.data?.ok ? "ok" : "?"}
              </span>
              <span className={ready.data?.ready ? "badge-success" : "badge"}>
                /readyz {ready.data?.ready ? "ready" : "warming"}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* Cluster config ----------------------------------------------- */}
      <div className="card p-4 space-y-4">
        <div className="flex items-center gap-2 font-medium">
          <Network size={16} /> Cluster mode
        </div>
        <div className="grid gap-4">
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              className="mt-1"
              checked={effectiveMulti}
              onChange={(e) => setMulti(e.target.checked)}
            />
            <div>
              <div className="font-medium">Multi-node</div>
              <div className="text-muted text-sm">
                Enable Raft replication and storage spreading across machines. Single-node
                clusters skip cross-node coordination for lower latency.
              </div>
            </div>
          </label>
          <label className="grid gap-1">
            <div className="text-sm font-medium">Replication factor</div>
            <input
              type="number"
              className="input w-32"
              min={1}
              max={5}
              value={effectiveRf}
              onChange={(e) => setRf(Math.max(1, Number(e.target.value) || 1))}
              disabled={!effectiveMulti}
            />
            <div className="text-muted text-xs">
              Number of object replicas Garage should keep across storage nodes (typically
              1 for solo, 3 for HA).
            </div>
          </label>
        </div>
        <div className="flex justify-end">
          <button
            className="btn-primary"
            onClick={() => save.mutate()}
            disabled={save.isPending || (multi === null && rf === null)}
          >
            <Save size={14} /> {save.isPending ? "Saving..." : "Save"}
          </button>
        </div>
      </div>

      {/* Join tokens -------------------------------------------------- */}
      <div className="card p-4 space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 font-medium">
            <Plus size={16} /> Join tokens
          </div>
          <button
            className="btn-primary"
            onClick={() => issue.mutate("24h")}
            disabled={issue.isPending}
          >
            <Plus size={14} /> {issue.isPending ? "Issuing..." : "Issue (24h)"}
          </button>
        </div>
        {issued && (
          <div className="space-y-2">
            <div className="text-muted text-sm">
              Run this on the new machine (token expires{" "}
              {new Date(issued.expiresAt).toLocaleString()}):
            </div>
            <div className="flex items-center gap-2">
              <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono break-all flex-1">
                {issued.command}
              </pre>
              <button
                className="btn"
                onClick={() => {
                  navigator.clipboard.writeText(issued.command);
                  toast.success("Copied");
                }}
              >
                <Copy size={14} />
              </button>
            </div>
          </div>
        )}
        <div className="overflow-hidden rounded-md border border-border">
          <table className="w-full text-sm">
            <thead className="bg-elevated text-muted">
              <tr>
                <th className="px-4 py-2 text-left">ID</th>
                <th className="px-4 py-2 text-left">Issued</th>
                <th className="px-4 py-2 text-left">Expires</th>
                <th className="px-4 py-2 text-left">Status</th>
              </tr>
            </thead>
            <tbody>
              {(tokens.data ?? []).map((t) => (
                <tr key={t.id} className="border-t border-border">
                  <td className="px-4 py-2 font-mono">{t.id}</td>
                  <td className="px-4 py-2 text-muted">{new Date(t.issuedAt).toLocaleString()}</td>
                  <td className="px-4 py-2 text-muted">
                    {new Date(t.expiresAt).toLocaleString()}
                  </td>
                  <td className="px-4 py-2">
                    {t.consumedBy ? (
                      <span className="badge">consumed by {t.consumedBy}</span>
                    ) : new Date(t.expiresAt) < new Date() ? (
                      <span className="badge">expired</span>
                    ) : (
                      <span className="badge-success">pending</span>
                    )}
                  </td>
                </tr>
              ))}
              {!tokens.data?.length && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-muted">
                    No join tokens. Issue one above to add a node.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* API tokens -------------------------------------------------- */}
      <div className="card p-4 space-y-4">
        <div className="flex items-center gap-2 font-medium">
          <KeyRound size={16} /> Personal access tokens
        </div>
        <div className="grid grid-cols-3 gap-2">
          <input
            className="input col-span-1"
            placeholder="name"
            value={newTokenName}
            onChange={(e) => setNewTokenName(e.target.value.replace(/[^a-z0-9-]/gi, "").slice(0, 32))}
          />
          <input
            className="input col-span-1"
            placeholder="ttl (e.g. 720h, blank=forever)"
            value={newTokenTTL}
            onChange={(e) => setNewTokenTTL(e.target.value)}
          />
          <button
            className="btn-primary"
            onClick={() => createToken.mutate()}
            disabled={createToken.isPending || newTokenName.trim().length < 2}
          >
            <Plus size={14} /> Create
          </button>
        </div>
        {issuedToken && issuedToken.plaintext && (
          <div className="space-y-2 rounded-md border border-amber-500/40 p-3">
            <div className="text-amber-400 text-sm">
              Copy this token now &mdash; it will not be shown again.
            </div>
            <div className="flex items-center gap-2">
              <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono break-all flex-1">
                {issuedToken.plaintext}
              </pre>
              <button
                className="btn"
                onClick={() => {
                  navigator.clipboard.writeText(issuedToken.plaintext!);
                  toast.success("Copied");
                }}
              >
                <Copy size={14} />
              </button>
            </div>
          </div>
        )}
        <div className="overflow-hidden rounded-md border border-border">
          <table className="w-full text-sm">
            <thead className="bg-elevated text-muted">
              <tr>
                <th className="px-4 py-2 text-left">Name</th>
                <th className="px-4 py-2 text-left">Created</th>
                <th className="px-4 py-2 text-left">Expires</th>
                <th className="px-4 py-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {(apiTokens.data ?? []).map((t) => (
                <tr key={t.meta.name} className="border-t border-border">
                  <td className="px-4 py-2 font-mono">{t.meta.name}</td>
                  <td className="px-4 py-2 text-muted">
                    {t.meta.createdAt ? new Date(t.meta.createdAt).toLocaleString() : "—"}
                  </td>
                  <td className="px-4 py-2 text-muted">
                    {t.expiresAt ? new Date(t.expiresAt).toLocaleString() : "never"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      className="btn-ghost"
                      onClick={() => deleteToken.mutate(t.meta.name)}
                      title={`Revoke ${t.meta.name}`}
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
              {!apiTokens.data?.length && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-muted">
                    No tokens yet. Create one above to script against the API.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
