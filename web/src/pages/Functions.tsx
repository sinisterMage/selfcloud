import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Github, Plus, Trash2, Upload, UploadCloud, Zap } from "lucide-react";
import { toast } from "sonner";
import { api, getToken } from "../lib/api";
import { useProject } from "../lib/project";
import type { FunctionRecord, SecretRecord } from "../lib/types";
import { SkeletonRows } from "../components/Skeleton";
import EmptyState from "../components/EmptyState";

interface FirecrackerTemplate {
  name: string;
  description: string;
  available: boolean;
  bootstrap?: string;
}

async function uploadCode(project: string, name: string, file: File) {
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
  const { project } = useProject();
  const fns = useQuery<FunctionRecord[]>({
    queryKey: ["functions", project],
    queryFn: () => api.get<FunctionRecord[]>(`/api/v1/projects/${project}/functions`).catch(() => []),
  });
  const [open, setOpen] = useState(false);

  const remove = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/functions/${name}`),
    onSuccess: (_d, name) => {
      toast.success(`Deleted ${name}`);
      qc.invalidateQueries({ queryKey: ["functions", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
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
            {fns.isLoading && <SkeletonRows rows={3} cols={5} />}
            {!fns.isLoading &&
              (fns.data ?? []).map((f) => (
                <tr key={f.meta.uid} className="border-t border-border hover:bg-elevated/40">
                  <td className="px-4 py-2 font-mono">
                    <Link className="hover:text-accent" to={`/functions/${f.meta.name}`}>
                      {f.meta.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2"><span className="badge">{f.runtime}</span></td>
                  <td className="px-4 py-2 text-muted">
                    {(f.triggers ?? []).map((t, i) => (
                      <span key={i} className="badge mr-1">
                        {t.http ? `HTTP ${t.http.path}` : t.cron ? `cron ${t.cron.schedule}` : "?"}
                      </span>
                    ))}
                    {!f.triggers?.length && <span className="text-xs">no triggers</span>}
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
                    <button
                      className="btn-danger ml-2"
                      title="Delete"
                      onClick={() => {
                        if (confirm(`Delete function ${f.meta.name}?`)) remove.mutate(f.meta.name);
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            {!fns.isLoading && !fns.data?.length && (
              <tr>
                <td colSpan={5}>
                  <EmptyState
                    icon={Zap}
                    title="No functions yet"
                    description="Deploy a Wasm function or a Firecracker microVM function. Wasm cold-starts in single-digit milliseconds; microVMs run any Linux binary."
                    action={
                      <button className="btn-primary" onClick={() => setOpen(true)}>
                        <Plus size={14} /> New function
                      </button>
                    }
                  />
                </td>
              </tr>
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
            await uploadCode(project, fn.meta.name, f);
            toast.success(`Uploaded ${f.name} to ${fn.meta.name}`);
            qc.invalidateQueries({ queryKey: ["functions", project] });
          } catch (err) {
            toast.error(err instanceof Error ? err.message : "upload failed");
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

type SourceTab = "upload" | "git";

function NewFunctionDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [source, setSource] = useState<SourceTab>("upload");
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState<"wasm" | "firecracker">("wasm");
  const [path, setPath] = useState("/");
  const [handler, setHandler] = useState<string>("");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);

  // Git source state
  const [gitURL, setGitURL] = useState("");
  const [gitRef, setGitRef] = useState("main");
  const [subPath, setSubPath] = useState("");
  const [authSecret, setAuthSecret] = useState("");
  const [language, setLanguage] = useState("auto");
  const [output, setOutput] = useState("");
  const [buildCommands, setBuildCommands] = useState("");

  const tpls = useQuery<FirecrackerTemplate[]>({
    queryKey: ["fc-templates"],
    queryFn: () => api.get<FirecrackerTemplate[]>("/api/v1/runtime/firecracker/templates").catch(() => []),
    enabled: runtime === "firecracker",
  });
  const secrets = useQuery<SecretRecord[]>({
    queryKey: ["secrets", project],
    queryFn: () => api.get<SecretRecord[]>(`/api/v1/projects/${project}/secrets`).catch(() => []),
    enabled: source === "git",
  });

  useEffect(() => {
    if (runtime !== "firecracker") return;
    if (handler) return;
    const first = tpls.data?.[0];
    if (first) setHandler(first.name);
  }, [runtime, tpls.data, handler]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        meta: { project, name },
        runtime,
        handler: runtime === "firecracker" ? handler : undefined,
        triggers: [{ http: { path, methods: ["GET", "POST"] } }],
        memoryMB: 128,
        timeoutMs: 5000,
      };
      if (source === "git") {
        const cmds = buildCommands
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean);
        body.source = {
          type: "git",
          git: {
            url: gitURL,
            ref: gitRef,
            subPath: subPath || undefined,
            authSecret: authSecret || undefined,
            build: {
              language: language === "auto" ? undefined : language,
              output: output || undefined,
              commands: cmds.length > 0 ? ["sh", "-lc", cmds.join(" && ")] : undefined,
              template: runtime === "firecracker" ? handler : undefined,
            },
          },
        };
      }
      await api.post(`/api/v1/projects/${project}/functions`, body);
      if (source === "upload" && file) {
        await uploadCode(project, name, file);
      }
      toast.success(`Created ${name}`);
      qc.invalidateQueries({ queryKey: ["functions", project] });
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "create failed");
    } finally {
      setBusy(false);
    }
  }

  const selectedTpl = tpls.data?.find((t) => t.name === handler);

  return (
    <div className="fixed inset-0 bg-bg/70 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-lg p-6 space-y-4 max-h-[90vh] overflow-auto">
        <h2 className="font-semibold">New function</h2>

        <div className="border-b border-border flex gap-4 text-sm">
          <button
            type="button"
            className={`pb-2 ${source === "upload" ? "border-b-2 border-accent text-accent" : "text-muted hover:text-fg"}`}
            onClick={() => setSource("upload")}
          >
            <Upload size={14} className="inline mr-1" /> Upload
          </button>
          <button
            type="button"
            className={`pb-2 ${source === "git" ? "border-b-2 border-accent text-accent" : "text-muted hover:text-fg"}`}
            onClick={() => setSource("git")}
          >
            <Github size={14} className="inline mr-1" /> Git repo
          </button>
        </div>

        <div className="space-y-1">
          <label className="text-sm text-muted">Name</label>
          <input className="input font-mono" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Runtime</label>
          <select className="input" value={runtime} onChange={(e) => setRuntime(e.target.value as "wasm" | "firecracker")}>
            <option value="wasm">wazero (WASI Preview 1) — recommended</option>
            <option value="firecracker">Firecracker microVM</option>
          </select>
        </div>
        {runtime === "firecracker" && (
          <div className="space-y-1">
            <label className="text-sm text-muted">Rootfs template</label>
            <select className="input" value={handler} onChange={(e) => setHandler(e.target.value)}>
              {(tpls.data ?? []).length === 0 && <option value="">(none registered)</option>}
              {(tpls.data ?? []).map((t) => (
                <option key={t.name} value={t.name} disabled={!t.available}>
                  {t.name} {t.available ? "" : "— not built"}
                </option>
              ))}
            </select>
            {selectedTpl && !selectedTpl.available && (
              <p className="text-xs text-warning">
                Template missing on disk. Run on the host: <code className="font-mono">{selectedTpl.bootstrap}</code>
              </p>
            )}
          </div>
        )}
        <div className="space-y-1">
          <label className="text-sm text-muted">HTTP path</label>
          <input className="input font-mono" value={path} onChange={(e) => setPath(e.target.value)} />
        </div>

        {source === "upload" && (
          <div className="space-y-1">
            <label className="text-sm text-muted">Code</label>
            <input className="input" type="file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            <p className="text-xs text-muted">
              {runtime === "wasm"
                ? "Upload a .wasm file (WASI Preview 1). TinyGo, Rust (wasm32-wasi), Zig all work."
                : "Upload a .tar containing your entrypoint and a manifest.json describing it."}
            </p>
          </div>
        )}

        {source === "git" && (
          <div className="space-y-3">
            <div className="space-y-1">
              <label className="text-sm text-muted">Repository URL</label>
              <input
                className="input font-mono"
                placeholder="https://github.com/octocat/hello-fn.git"
                value={gitURL}
                onChange={(e) => setGitURL(e.target.value)}
                required
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="text-sm text-muted">Ref</label>
                <input
                  className="input font-mono"
                  value={gitRef}
                  onChange={(e) => setGitRef(e.target.value)}
                  placeholder="main"
                />
              </div>
              <div className="space-y-1">
                <label className="text-sm text-muted">Sub-path</label>
                <input
                  className="input font-mono"
                  value={subPath}
                  onChange={(e) => setSubPath(e.target.value)}
                  placeholder="(repo root)"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Auth secret (PAT)</label>
              <select className="input" value={authSecret} onChange={(e) => setAuthSecret(e.target.value)}>
                <option value="">(public repo — no auth)</option>
                {(secrets.data ?? []).map((s) => (
                  <option key={s.meta.name} value={s.meta.name}>
                    {s.meta.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted">
                Reference a project secret containing a fine-grained personal access token. Leave blank for public repos.
              </p>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="text-sm text-muted">Language</label>
                <select className="input" value={language} onChange={(e) => setLanguage(e.target.value)}>
                  <option value="auto">Auto-detect</option>
                  <option value="rust">Rust → wasm32-wasi</option>
                  <option value="tinygo">TinyGo → wasm32-wasi</option>
                  <option value="node">Node.js</option>
                  <option value="python">Python</option>
                </select>
              </div>
              <div className="space-y-1">
                <label className="text-sm text-muted">Output path</label>
                <input
                  className="input font-mono"
                  value={output}
                  onChange={(e) => setOutput(e.target.value)}
                  placeholder="dist/handler.wasm"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Build commands (one per line; optional)</label>
              <textarea
                className="input font-mono h-20"
                value={buildCommands}
                onChange={(e) => setBuildCommands(e.target.value)}
                placeholder={"cargo build --release --target wasm32-wasi\ncp target/wasm32-wasi/release/*.wasm /out/handler.wasm"}
                spellCheck={false}
              />
            </div>
          </div>
        )}

        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn-primary" disabled={busy}>
            {source === "git" ? <Github size={14} /> : <Upload size={14} />}{" "}
            {busy ? "Creating..." : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}
