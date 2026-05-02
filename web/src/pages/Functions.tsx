import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, Upload, UploadCloud, Zap } from "lucide-react";
import { api, getToken } from "../lib/api";
import type { FunctionRecord } from "../lib/types";

const project = "default";

async function uploadCode(name: string, file: File) {
  const buf = await file.arrayBuffer();
  const res = await fetch(`/api/v1/projects/${project}/functions/${name}/code`, {
    method: "POST",
    headers: {
      "content-type": "application/octet-stream",
      ...(getToken() ? { authorization: `Bearer ${getToken()}` } : {}),
    },
    body: buf,
  });
  if (!res.ok) {
    throw new Error(`upload failed: ${res.status} ${res.statusText}`);
  }
}

export default function FunctionsPage() {
  const qc = useQueryClient();
  const fns = useQuery<FunctionRecord[]>({
    queryKey: ["functions"],
    queryFn: () => api.get<FunctionRecord[]>(`/api/v1/projects/${project}/functions`).catch(() => []),
  });
  const [open, setOpen] = useState(false);

  const remove = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/functions/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["functions"] }),
  });

  return (
    <div className="p-8 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Functions</h1>
          <p className="text-muted">Wasm and microVM functions, triggered by HTTP or cron.</p>
        </div>
        <button className="btn-primary" onClick={() => setOpen(true)}>
          <Plus size={14} /> New function
        </button>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Name</th>
              <th className="px-4 py-2 text-left">Runtime</th>
              <th className="px-4 py-2 text-left">Triggers</th>
              <th className="px-4 py-2 text-left">Status</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {(fns.data ?? []).map((f) => (
              <tr key={f.meta.uid} className="border-t border-border">
                <td className="px-4 py-2 font-mono">{f.meta.name}</td>
                <td className="px-4 py-2"><span className="badge">{f.runtime}</span></td>
                <td className="px-4 py-2 text-muted">
                  {(f.triggers ?? []).map((t, i) => (
                    <span key={i} className="badge mr-1">
                      {t.http ? `HTTP ${t.http.path}` : t.cron ? `cron ${t.cron.schedule}` : "?"}
                    </span>
                  ))}
                </td>
                <td className="px-4 py-2">
                  {!f.sourceRef ? (
                    <span className="badge-warn">no code</span>
                  ) : (
                    <span className={f.status?.phase === "Running" ? "badge-success" : "badge"}>
                      {f.status?.phase || "Ready"}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  <UploadCodeButton fn={f} />
                  <button
                    className="btn ml-2"
                    title="Invoke (opens in new tab)"
                    onClick={() => window.open(`/fn/${project}/${f.meta.name}`, "_blank")}
                  >
                    <Zap size={14} />
                  </button>
                  <button className="btn-danger ml-2" onClick={() => remove.mutate(f.meta.name)}>
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
            {!fns.data?.length && (
              <tr><td colSpan={5} className="px-4 py-12 text-center text-muted">No functions yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {open && <NewFunctionDialog onClose={() => setOpen(false)} />}
    </div>
  );
}

function UploadCodeButton({ fn }: { fn: FunctionRecord }) {
  const qc = useQueryClient();
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
            await uploadCode(fn.meta.name, f);
            qc.invalidateQueries({ queryKey: ["functions"] });
          } catch (err) {
            alert(err instanceof Error ? err.message : "upload failed");
          } finally {
            setBusy(false);
            if (inputRef.current) inputRef.current.value = "";
          }
        }}
      />
      <button
        className="btn"
        title={fn.sourceRef ? "Replace code" : "Upload code"}
        onClick={() => inputRef.current?.click()}
        disabled={busy}
      >
        <UploadCloud size={14} />
      </button>
    </>
  );
}

function NewFunctionDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState<"wasm" | "firecracker">("wasm");
  const [path, setPath] = useState("/");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post(`/api/v1/projects/${project}/functions`, {
        meta: { project, name },
        runtime,
        triggers: [{ http: { path, methods: ["GET", "POST"] } }],
        memoryMB: 128,
        timeoutMs: 5000,
      });
      if (file) {
        const buf = await file.arrayBuffer();
        await fetch(`/api/v1/projects/${project}/functions/${name}/code`, {
          method: "POST",
          headers: {
            "content-type": "application/octet-stream",
            ...(localStorage.getItem("selfcloud.token") ? { authorization: `Bearer ${localStorage.getItem("selfcloud.token")}` } : {}),
          },
          body: buf,
        });
      }
      qc.invalidateQueries({ queryKey: ["functions"] });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-bg/80 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-md p-6 space-y-4">
        <h2 className="font-semibold">New function</h2>
        <div className="space-y-1">
          <label className="text-sm text-muted">Name</label>
          <input className="input font-mono" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Runtime</label>
          <select className="input" value={runtime} onChange={(e) => setRuntime(e.target.value as "wasm" | "firecracker")}>
            <option value="wasm">Wasmtime (WASI)</option>
            <option value="firecracker">Firecracker microVM</option>
          </select>
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">HTTP path</label>
          <input className="input font-mono" value={path} onChange={(e) => setPath(e.target.value)} />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Code</label>
          <input className="input" type="file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          <p className="text-xs text-muted">{runtime === "wasm" ? "Upload a .wasm file (WASI Preview 2)." : "Upload a tarball with rootfs and entrypoint."}</p>
        </div>
        {error && <div className="text-sm text-danger">{error}</div>}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn-primary" disabled={busy}>
            <Upload size={14} /> {busy ? "Creating..." : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}
