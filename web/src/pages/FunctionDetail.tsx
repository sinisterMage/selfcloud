import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Cpu,
  Github,
  Hammer,
  Plus,
  RefreshCw,
  Save,
  Send,
  Timer,
  Trash2,
  UploadCloud,
  Webhook,
  Zap,
} from "lucide-react";
import { toast } from "sonner";
import { api, getToken } from "../lib/api";
import { useProject } from "../lib/project";
import type { BuildRecord, FunctionRecord, FunctionTrigger } from "../lib/types";
import { Skeleton } from "../components/Skeleton";

interface InvocationRecord {
  at: string;
  method: string;
  path: string;
  status: number;
  durMs: number;
  bodyKb: number;
  error?: string;
  logsTail?: string;
}

export default function FunctionDetail() {
  const params = useParams<{ name: string }>();
  const name = params.name!;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { project } = useProject();

  const fn = useQuery<FunctionRecord>({
    queryKey: ["function", project, name],
    queryFn: () => api.get<FunctionRecord>(`/api/v1/projects/${project}/functions/${name}`),
  });

  const invocations = useQuery<InvocationRecord[]>({
    queryKey: ["invocations", project, name],
    queryFn: () => api.get<InvocationRecord[]>(`/api/v1/projects/${project}/functions/${name}/invocations`).catch(() => []),
    refetchInterval: 5000,
  });

  const remove = useMutation({
    mutationFn: () => api.del(`/api/v1/projects/${project}/functions/${name}`),
    onSuccess: () => {
      toast.success(`Deleted ${name}`);
      qc.invalidateQueries({ queryKey: ["functions", project] });
      navigate("/functions");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  if (fn.isLoading) {
    return (
      <div className="p-8 space-y-4">
        <Skeleton className="h-6 w-64" />
        <Skeleton className="h-4 w-32" />
      </div>
    );
  }
  if (!fn.data) {
    return (
      <div className="p-8 text-muted">Function not found.</div>
    );
  }

  const f = fn.data;

  return (
    <div className="p-8 space-y-6 max-w-6xl">
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <Link to="/functions" className="inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
            <ArrowLeft size={12} /> All functions
          </Link>
          <h1 className="text-2xl font-semibold tracking-tight font-mono">{f.meta.name}</h1>
          <div className="flex items-center gap-2 text-sm text-muted">
            <span className="badge">{f.runtime}</span>
            {f.handler && <span className="badge">template: {f.handler}</span>}
            {f.sourceRef ? (
              <span className="badge-success">deployed</span>
            ) : (
              <span className="badge-warn">no code</span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <ReuploadButton fn={f} />
          <button
            className="btn-danger"
            onClick={() => {
              if (confirm(`Delete function ${name}?`)) remove.mutate();
            }}
          >
            <Trash2 size={14} /> Delete
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <ConfigCard fn={f} />
        <InvokeTester fn={f} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <TriggersEditor fn={f} />
        <EnvEditor fn={f} />
      </div>

      {f.source?.type === "git" && <GitSourcePanel fn={f} />}

      <InvocationsPanel rows={invocations.data ?? []} loading={invocations.isLoading} />
    </div>
  );
}

function ReuploadButton({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const inputRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  return (
    <>
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        onChange={async (e) => {
          const f = e.target.files?.[0];
          if (!f) return;
          setBusy(true);
          try {
            const buf = await f.arrayBuffer();
            const res = await fetch(`/api/v1/projects/${project}/functions/${fn.meta.name}/code`, {
              method: "POST",
              headers: {
                "content-type": "application/octet-stream",
                ...(getToken() ? { authorization: `Bearer ${getToken()}` } : {}),
              },
              body: buf,
            });
            if (!res.ok) throw new Error(`upload failed: ${res.status}`);
            toast.success("Code re-deployed");
            qc.invalidateQueries({ queryKey: ["function", project, fn.meta.name] });
            qc.invalidateQueries({ queryKey: ["functions", project] });
          } catch (err) {
            toast.error(err instanceof Error ? err.message : "upload failed");
          } finally {
            setBusy(false);
            if (inputRef.current) inputRef.current.value = "";
          }
        }}
      />
      <button className="btn" onClick={() => inputRef.current?.click()} disabled={busy}>
        <UploadCloud size={14} /> {busy ? "Uploading..." : "Re-upload code"}
      </button>
    </>
  );
}

function ConfigCard({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [memoryMB, setMemoryMB] = useState(fn.memoryMB ?? 128);
  const [timeoutMs, setTimeoutMs] = useState(fn.timeoutMs ?? 5000);
  const dirty = memoryMB !== (fn.memoryMB ?? 128) || timeoutMs !== (fn.timeoutMs ?? 5000);

  async function save() {
    try {
      await api.post(`/api/v1/projects/${project}/functions`, {
        ...fn,
        memoryMB,
        timeoutMs,
      });
      toast.success("Saved");
      qc.invalidateQueries({ queryKey: ["function", project, fn.meta.name] });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "save failed");
    }
  }

  return (
    <div className="card p-5 space-y-4">
      <div className="flex items-center gap-2">
        <Cpu size={16} className="text-muted" />
        <div className="font-medium">Resources</div>
      </div>
      <div className="space-y-1">
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted">Memory</span>
          <span className="font-mono">{memoryMB} MiB</span>
        </div>
        <input
          type="range"
          min={32}
          max={1024}
          step={32}
          value={memoryMB}
          onChange={(e) => setMemoryMB(Number(e.target.value))}
          className="w-full accent-accent"
        />
      </div>
      <div className="space-y-1">
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted">Timeout</span>
          <span className="font-mono">{timeoutMs} ms</span>
        </div>
        <input
          type="range"
          min={500}
          max={60000}
          step={500}
          value={timeoutMs}
          onChange={(e) => setTimeoutMs(Number(e.target.value))}
          className="w-full accent-accent"
        />
      </div>
      <div className="flex items-center justify-end">
        <button className="btn-primary" onClick={save} disabled={!dirty}>
          <Save size={14} /> Save
        </button>
      </div>
    </div>
  );
}

function TriggersEditor({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [triggers, setTriggers] = useState<FunctionTrigger[]>(fn.triggers ?? []);

  async function save() {
    try {
      await api.post(`/api/v1/projects/${project}/functions`, { ...fn, triggers });
      toast.success("Triggers saved");
      qc.invalidateQueries({ queryKey: ["function", project, fn.meta.name] });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "save failed");
    }
  }

  return (
    <div className="card p-5 space-y-4">
      <div className="flex items-center gap-2">
        <Timer size={16} className="text-muted" />
        <div className="font-medium">Triggers</div>
      </div>
      <div className="space-y-2">
        {triggers.map((t, i) => (
          <div key={i} className="flex items-center gap-2">
            {t.http && (
              <>
                <span className="badge">HTTP</span>
                <input
                  className="input font-mono"
                  value={t.http.path}
                  onChange={(e) =>
                    setTriggers((cur) =>
                      cur.map((c, j) => (j === i ? { http: { ...c.http!, path: e.target.value } } : c))
                    )
                  }
                />
              </>
            )}
            {t.cron && (
              <>
                <span className="badge">cron</span>
                <input
                  className="input font-mono"
                  value={t.cron.schedule}
                  placeholder="*/5 * * * *"
                  onChange={(e) =>
                    setTriggers((cur) =>
                      cur.map((c, j) => (j === i ? { cron: { schedule: e.target.value } } : c))
                    )
                  }
                />
              </>
            )}
            <button
              className="btn"
              title="Remove"
              onClick={() => setTriggers((cur) => cur.filter((_, j) => j !== i))}
            >
              <Trash2 size={14} />
            </button>
          </div>
        ))}
        {!triggers.length && <p className="text-sm text-muted">No triggers.</p>}
      </div>
      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <button
            className="btn"
            onClick={() =>
              setTriggers((cur) => [...cur, { http: { path: "/", methods: ["GET", "POST"] } }])
            }
          >
            <Plus size={14} /> HTTP
          </button>
          <button
            className="btn"
            onClick={() => setTriggers((cur) => [...cur, { cron: { schedule: "*/5 * * * *" } }])}
          >
            <Plus size={14} /> Cron
          </button>
        </div>
        <button className="btn-primary" onClick={save}>
          <Save size={14} /> Save
        </button>
      </div>
    </div>
  );
}

function EnvEditor({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [rows, setRows] = useState<Array<[string, string]>>(() =>
    Object.entries(fn.env ?? {})
  );

  async function save() {
    const env: Record<string, string> = {};
    for (const [k, v] of rows) if (k) env[k] = v;
    try {
      await api.post(`/api/v1/projects/${project}/functions`, { ...fn, env });
      toast.success("Env saved");
      qc.invalidateQueries({ queryKey: ["function", project, fn.meta.name] });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "save failed");
    }
  }

  return (
    <div className="card p-5 space-y-4">
      <div className="flex items-center gap-2">
        <Zap size={16} className="text-muted" />
        <div className="font-medium">Environment variables</div>
      </div>
      <div className="space-y-2">
        {rows.map(([k, v], i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              className="input font-mono w-1/3"
              placeholder="KEY"
              value={k}
              onChange={(e) => setRows((cur) => cur.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))}
            />
            <input
              className="input font-mono flex-1"
              placeholder="value"
              value={v}
              onChange={(e) => setRows((cur) => cur.map((r, j) => (j === i ? [r[0], e.target.value] : r)))}
            />
            <button className="btn" onClick={() => setRows((cur) => cur.filter((_, j) => j !== i))}>
              <Trash2 size={14} />
            </button>
          </div>
        ))}
        {!rows.length && <p className="text-sm text-muted">No environment variables.</p>}
      </div>
      <div className="flex items-center justify-between">
        <button className="btn" onClick={() => setRows((cur) => [...cur, ["", ""]])}>
          <Plus size={14} /> Add
        </button>
        <button className="btn-primary" onClick={save}>
          <Save size={14} /> Save
        </button>
      </div>
    </div>
  );
}

function InvokeTester({ fn }: { fn: FunctionRecord }) {
  const { project } = useProject();
  const [method, setMethod] = useState("GET");
  const [path, setPath] = useState("/");
  const [body, setBody] = useState("");
  const [resp, setResp] = useState<{ status: number; headers: Headers; body: string; ms: number } | null>(null);
  const [busy, setBusy] = useState(false);

  async function send() {
    setBusy(true);
    setResp(null);
    const start = performance.now();
    try {
      const res = await fetch(`/api/v1/projects/${project}/functions/${fn.meta.name}/invoke?path=${encodeURIComponent(path)}`, {
        method,
        headers: {
          ...(getToken() ? { authorization: `Bearer ${getToken()}` } : {}),
          "content-type": "application/json",
        },
        body: method === "GET" || method === "HEAD" ? undefined : body || undefined,
      });
      const text = await res.text();
      setResp({ status: res.status, headers: res.headers, body: text, ms: Math.round(performance.now() - start) });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "invoke failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card p-5 space-y-3">
      <div className="flex items-center gap-2">
        <Send size={16} className="text-muted" />
        <div className="font-medium">Invoke now</div>
      </div>
      <div className="flex items-center gap-2">
        <select className="input w-28" value={method} onChange={(e) => setMethod(e.target.value)}>
          {["GET", "POST", "PUT", "DELETE", "PATCH"].map((m) => (
            <option key={m}>{m}</option>
          ))}
        </select>
        <input className="input font-mono flex-1" value={path} onChange={(e) => setPath(e.target.value)} />
        <button className="btn-primary" onClick={send} disabled={busy}>
          <Send size={14} /> {busy ? "..." : "Send"}
        </button>
      </div>
      {method !== "GET" && method !== "HEAD" && (
        <textarea
          className="input font-mono h-20"
          placeholder="request body"
          value={body}
          onChange={(e) => setBody(e.target.value)}
        />
      )}
      {resp && (
        <div className="space-y-2 border-t border-border pt-3">
          <div className="flex items-center justify-between text-sm">
            <span>
              <span className={resp.status < 400 ? "badge-success" : "badge-danger"}>{resp.status}</span>
              <span className="text-muted ml-2">{resp.ms} ms</span>
            </span>
            <span className="text-xs text-muted">{resp.body.length} bytes</span>
          </div>
          <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono overflow-auto max-h-64 whitespace-pre-wrap">
            {resp.body || "(empty)"}
          </pre>
        </div>
      )}
    </div>
  );
}

function GitSourcePanel({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [activeBuild, setActiveBuild] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>("");

  const builds = useQuery<BuildRecord[]>({
    queryKey: ["builds", project, fn.meta.name],
    queryFn: () =>
      api.get<BuildRecord[]>(`/api/v1/projects/${project}/functions/${fn.meta.name}/builds`).catch(() => []),
    refetchInterval: 4000,
  });

  const trigger = useMutation({
    mutationFn: () => api.post(`/api/v1/projects/${project}/functions/${fn.meta.name}/builds`, {}),
    onSuccess: () => {
      toast.success("Build queued");
      qc.invalidateQueries({ queryKey: ["builds", project, fn.meta.name] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "build trigger failed"),
  });

  const webhookURL = useMemo(() => {
    if (!fn.source?.git?.webhookToken) return null;
    return `${window.location.origin}/webhooks/git/${fn.source.git.webhookToken}`;
  }, [fn.source?.git?.webhookToken]);

  useEffect(() => {
    if (!activeBuild) return;
    const tok = getToken();
    if (!tok) return;
    setLogs("");
    const url = new URL(
      `/api/v1/projects/${project}/functions/${fn.meta.name}/builds/${activeBuild}/logs/ws`,
      window.location.href,
    );
    url.protocol = url.protocol.replace("http", "ws");
    url.searchParams.set("access_token", tok);
    const ws = new WebSocket(url.toString(), "selfcloud.v1");
    ws.onmessage = (msg) => {
      setLogs((cur) => cur + (typeof msg.data === "string" ? msg.data : ""));
    };
    return () => ws.close();
  }, [activeBuild, project, fn.meta.name]);

  return (
    <div className="card p-5 space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Github size={16} className="text-muted" />
          <div className="font-medium">Git source</div>
        </div>
        <button className="btn-primary" onClick={() => trigger.mutate()} disabled={trigger.isPending}>
          <RefreshCw size={14} /> {trigger.isPending ? "..." : "Build"}
        </button>
      </div>
      <div className="text-sm text-muted space-y-1 font-mono">
        <div>repo: {fn.source?.git?.url}</div>
        <div>ref: {fn.source?.git?.ref || "HEAD"}</div>
        {fn.source?.git?.subPath && <div>subPath: {fn.source.git.subPath}</div>}
        {fn.source?.git?.authSecret && (
          <div>
            auth: <span className="badge">{fn.source.git.authSecret}</span>
          </div>
        )}
      </div>

      {webhookURL && (
        <div className="space-y-1 text-sm">
          <div className="flex items-center gap-2 text-muted">
            <Webhook size={14} /> Push-to-deploy webhook URL
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 bg-elevated px-3 py-2 rounded font-mono text-xs break-all">
              {webhookURL}
            </code>
            <button
              className="btn"
              onClick={() => {
                navigator.clipboard.writeText(webhookURL);
                toast.success("Copied");
              }}
            >
              Copy
            </button>
          </div>
          <p className="text-xs text-muted">
            Add this URL on GitHub under Settings → Webhooks. Choose "application/json" and the
            "Just the push event" trigger.
          </p>
        </div>
      )}

      <div className="border-t border-border pt-3">
        <div className="flex items-center gap-2 mb-2">
          <Hammer size={14} className="text-muted" />
          <div className="text-sm font-medium">Recent builds</div>
        </div>
        <table className="w-full text-sm">
          <thead className="text-muted">
            <tr>
              <th className="text-left px-2 py-1">When</th>
              <th className="text-left px-2 py-1">Trigger</th>
              <th className="text-left px-2 py-1">Commit</th>
              <th className="text-left px-2 py-1">Status</th>
              <th className="text-left px-2 py-1">Logs</th>
            </tr>
          </thead>
          <tbody>
            {(builds.data ?? []).map((b) => (
              <tr key={b.meta.uid} className="border-t border-border">
                <td className="px-2 py-1 text-xs whitespace-nowrap">
                  {b.startedAt ? new Date(b.startedAt).toLocaleTimeString() : "-"}
                </td>
                <td className="px-2 py-1 text-xs">{b.trigger}</td>
                <td className="px-2 py-1 font-mono text-xs">{b.commitSha?.slice(0, 8) || "-"}</td>
                <td className="px-2 py-1">
                  <span
                    className={
                      b.status === "Succeeded"
                        ? "badge-success"
                        : b.status === "Failed"
                          ? "badge-danger"
                          : "badge"
                    }
                  >
                    {b.status}
                  </span>
                </td>
                <td className="px-2 py-1">
                  <button className="btn" onClick={() => setActiveBuild(b.meta.uid)}>
                    Tail
                  </button>
                </td>
              </tr>
            ))}
            {!builds.data?.length && (
              <tr>
                <td colSpan={5} className="px-2 py-3 text-center text-xs text-muted">
                  No builds yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
        {activeBuild && (
          <div className="mt-3 space-y-2">
            <div className="flex items-center justify-between text-xs">
              <span className="text-muted">build {activeBuild.slice(0, 8)}</span>
              <button className="btn" onClick={() => setActiveBuild(null)}>
                Close
              </button>
            </div>
            <pre className="bg-elevated rounded-md px-3 py-2 text-xs font-mono overflow-auto max-h-72 whitespace-pre-wrap">
              {logs || "(connecting...)"}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}

function InvocationsPanel({ rows, loading }: { rows: InvocationRecord[]; loading: boolean }) {
  return (
    <div className="card overflow-hidden">
      <div className="px-4 py-2 border-b border-border bg-elevated text-sm font-medium">Recent invocations</div>
      <table className="w-full text-sm">
        <thead className="text-muted">
          <tr>
            <th className="px-4 py-2 text-left">When</th>
            <th className="px-4 py-2 text-left">Method</th>
            <th className="px-4 py-2 text-left">Path</th>
            <th className="px-4 py-2 text-left">Status</th>
            <th className="px-4 py-2 text-left">Duration</th>
            <th className="px-4 py-2 text-left">Body</th>
          </tr>
        </thead>
        <tbody>
          {loading && (
            <tr>
              <td className="px-4 py-3" colSpan={6}>
                <Skeleton className="h-4 w-full" />
              </td>
            </tr>
          )}
          {!loading && !rows.length && (
            <tr>
              <td className="px-4 py-6 text-center text-muted" colSpan={6}>
                Nothing invoked yet.
              </td>
            </tr>
          )}
          {rows.map((r, i) => (
            <tr key={i} className="border-t border-border align-top">
              <td className="px-4 py-2 text-xs text-muted whitespace-nowrap">
                {new Date(r.at).toLocaleTimeString()}
              </td>
              <td className="px-4 py-2 font-mono text-xs">{r.method}</td>
              <td className="px-4 py-2 font-mono text-xs truncate max-w-[16rem]">{r.path}</td>
              <td className="px-4 py-2">
                <span className={r.status >= 400 ? "badge-danger" : "badge-success"}>{r.status}</span>
              </td>
              <td className="px-4 py-2 text-xs">{r.durMs} ms</td>
              <td className="px-4 py-2 text-xs">{r.bodyKb} KB</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
