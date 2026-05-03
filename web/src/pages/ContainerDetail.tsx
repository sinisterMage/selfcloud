import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Play, Square, Terminal, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { api, getToken } from "../lib/api";
import { useProject } from "../lib/project";
import type { Container } from "../lib/types";
import { Skeleton } from "../components/Skeleton";

export default function ContainerDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { project } = useProject();

  const c = useQuery<Container>({
    queryKey: ["container", project, name],
    queryFn: () => api.get<Container>(`/api/v1/projects/${project}/containers/${name}`),
    refetchInterval: 5000,
  });

  const start = useMutation({
    mutationFn: () => api.post(`/api/v1/projects/${project}/containers/${name}/start`),
    onSuccess: () => {
      toast.success("Started");
      qc.invalidateQueries({ queryKey: ["container", project, name] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "start failed"),
  });
  const stop = useMutation({
    mutationFn: () => api.post(`/api/v1/projects/${project}/containers/${name}/stop`),
    onSuccess: () => {
      toast.success("Stopped");
      qc.invalidateQueries({ queryKey: ["container", project, name] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "stop failed"),
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/api/v1/projects/${project}/containers/${name}`),
    onSuccess: () => {
      toast.success(`Deleted ${name}`);
      navigate("/containers");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  if (c.isLoading) {
    return (
      <div className="p-8 space-y-4">
        <Skeleton className="h-6 w-64" />
        <Skeleton className="h-4 w-32" />
      </div>
    );
  }
  if (!c.data) return <div className="p-8 text-muted">Container not found.</div>;
  const ctn = c.data;

  return (
    <div className="p-8 space-y-6 max-w-6xl">
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <Link to="/containers" className="inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
            <ArrowLeft size={12} /> All containers
          </Link>
          <h1 className="text-2xl font-semibold tracking-tight font-mono">{ctn.meta.name}</h1>
          <div className="flex items-center gap-2 text-sm text-muted">
            <span className="font-mono">{ctn.image}</span>
            <span className={ctn.status?.phase === "Running" ? "badge-success" : "badge"}>
              {ctn.status?.phase || "—"}
            </span>
            {ctn.status?.ipAddress && <span className="badge font-mono">{ctn.status.ipAddress}</span>}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button className="btn" onClick={() => start.mutate()} disabled={ctn.status?.phase === "Running"}>
            <Play size={14} /> Start
          </button>
          <button className="btn" onClick={() => stop.mutate()} disabled={ctn.status?.phase !== "Running"}>
            <Square size={14} /> Stop
          </button>
          <button
            className="btn-danger"
            onClick={() => {
              if (confirm(`Delete container ${name}?`)) remove.mutate();
            }}
          >
            <Trash2 size={14} /> Delete
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="card p-5 space-y-2 text-sm">
          <div className="font-medium">Specification</div>
          <Row label="Restart policy" value={ctn.restartPolicy} />
          <Row label="CPU (millicores)" value={String(ctn.resources?.cpuMillicores ?? "—")} />
          <Row label="Memory (MiB)" value={String(ctn.resources?.memoryMB ?? "—")} />
          <Row label="Node" value={ctn.nodeId || "self"} />
        </div>
        <div className="card p-5 space-y-2 text-sm">
          <div className="font-medium">Ports</div>
          {(ctn.ports ?? []).map((p, i) => (
            <Row key={i} label={`${p.protocol}/${p.host}`} value={`-> :${p.container}`} mono />
          ))}
          {!ctn.ports?.length && <div className="text-muted text-xs">No published ports.</div>}
        </div>
        <div className="card p-5 space-y-2 text-sm">
          <div className="font-medium">Env</div>
          {Object.entries(ctn.env ?? {}).map(([k, v]) => (
            <Row key={k} label={k} value={v} mono />
          ))}
          {!Object.keys(ctn.env ?? {}).length && <div className="text-muted text-xs">No env vars.</div>}
        </div>
      </div>

      <LogsPanel name={ctn.meta.name} />
      <ExecPanel name={ctn.meta.name} />
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3 py-1 border-b border-border last:border-b-0">
      <span className="text-muted">{label}</span>
      <span className={mono ? "font-mono text-xs truncate" : ""}>{value}</span>
    </div>
  );
}

function LogsPanel({ name }: { name: string }) {
  const { project } = useProject();
  const ref = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm | null>(null);
  const fit = useRef<FitAddon | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    const t = new XTerm({
      convertEol: true,
      fontSize: 12,
      theme: { background: "#0f141a", foreground: "#e6edf3" },
      disableStdin: true,
    });
    const f = new FitAddon();
    t.loadAddon(f);
    t.open(ref.current);
    f.fit();
    term.current = t;
    fit.current = f;
    const onResize = () => f.fit();
    window.addEventListener("resize", onResize);

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/api/v1/projects/${project}/containers/${name}/logs/ws`;
    const ws = new WebSocket(url + (getToken() ? `?access_token=${encodeURIComponent(getToken()!)}` : ""));
    ws.binaryType = "arraybuffer";
    ws.onmessage = (e) => {
      if (typeof e.data === "string") t.write(e.data);
      else t.write(new Uint8Array(e.data as ArrayBuffer));
    };
    ws.onerror = () => t.write("\r\n[ws error]\r\n");
    ws.onclose = () => t.write("\r\n[disconnected]\r\n");

    return () => {
      window.removeEventListener("resize", onResize);
      ws.close();
      t.dispose();
    };
  }, [project, name]);

  return (
    <div className="card overflow-hidden">
      <div className="px-4 py-2 border-b border-border bg-elevated text-sm font-medium">Logs</div>
      <div className="bg-[#0f141a] p-2">
        <div ref={ref} className="h-72" />
      </div>
    </div>
  );
}

function ExecPanel({ name }: { name: string }) {
  const { project } = useProject();
  const [open, setOpen] = useState(false);
  const [cmd, setCmd] = useState("/bin/sh");
  const ref = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!open || !ref.current) return;
    const t = new XTerm({
      cursorBlink: true,
      fontSize: 12,
      theme: { background: "#0f141a", foreground: "#e6edf3" },
    });
    const f = new FitAddon();
    t.loadAddon(f);
    t.open(ref.current);
    f.fit();
    term.current = t;
    fit.current = f;

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/api/v1/projects/${project}/containers/${name}/exec`;
    const ws = new WebSocket(url + (getToken() ? `?access_token=${encodeURIComponent(getToken()!)}` : ""));
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;
    ws.onopen = () => {
      ws.send(JSON.stringify({ cmd: cmd.split(/\s+/) }));
    };
    ws.onmessage = (e) => {
      if (typeof e.data === "string") t.write(e.data);
      else t.write(new Uint8Array(e.data as ArrayBuffer));
    };
    ws.onerror = () => t.write("\r\n[ws error]\r\n");
    ws.onclose = () => t.write("\r\n[disconnected]\r\n");

    const dataDisp = t.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(d);
    });
    const onResize = () => f.fit();
    window.addEventListener("resize", onResize);

    return () => {
      dataDisp.dispose();
      window.removeEventListener("resize", onResize);
      ws.close();
      t.dispose();
    };
  }, [open, project, name, cmd]);

  return (
    <div className="card overflow-hidden">
      <div className="px-4 py-2 border-b border-border bg-elevated text-sm font-medium flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Terminal size={14} />
          <span>Exec</span>
        </div>
        {!open ? (
          <div className="flex items-center gap-2">
            <input className="input w-48 font-mono" value={cmd} onChange={(e) => setCmd(e.target.value)} />
            <button className="btn-primary" onClick={() => setOpen(true)}>
              Connect
            </button>
          </div>
        ) : (
          <button
            className="btn"
            onClick={() => {
              wsRef.current?.close();
              setOpen(false);
            }}
          >
            Disconnect
          </button>
        )}
      </div>
      {open && (
        <div className="bg-[#0f141a] p-2">
          <div ref={ref} className="h-72" />
        </div>
      )}
    </div>
  );
}
