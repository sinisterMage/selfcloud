import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Plus, Send, Trash2, Webhook } from "lucide-react";
import { toast } from "sonner";
import { api, getToken } from "../lib/api";
import { useProject } from "../lib/project";
import type {
  Container,
  EventRecord,
  EventRule,
  FunctionRecord,
  WebhookDelivery,
} from "../lib/types";
import { SkeletonRows } from "../components/Skeleton";
import EmptyState from "../components/EmptyState";

type Tab = "timeline" | "rules";

export default function EventsPage() {
  const [tab, setTab] = useState<Tab>("timeline");
  return (
    <div className="p-8 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Events</h1>
          <p className="text-muted">
            Lifecycle, S3, log-pattern and in-app events. Define rules to webhook out, invoke
            functions, or restart containers when they fire.
          </p>
        </div>
      </div>
      <div className="border-b border-border flex gap-4 text-sm">
        <button
          className={`pb-2 ${tab === "timeline" ? "border-b-2 border-accent text-accent" : "text-muted hover:text-fg"}`}
          onClick={() => setTab("timeline")}
        >
          Timeline
        </button>
        <button
          className={`pb-2 ${tab === "rules" ? "border-b-2 border-accent text-accent" : "text-muted hover:text-fg"}`}
          onClick={() => setTab("rules")}
        >
          Rules
        </button>
      </div>
      {tab === "timeline" ? <Timeline /> : <Rules />}
    </div>
  );
}

function Timeline() {
  const { project } = useProject();
  const [filter, setFilter] = useState("");
  const [live, setLive] = useState<EventRecord[]>([]);
  const wsRef = useRef<WebSocket | null>(null);

  const initial = useQuery<EventRecord[]>({
    queryKey: ["events", project],
    queryFn: () => api.get<EventRecord[]>(`/api/v1/projects/${project}/events?limit=200`).catch(() => []),
  });

  useEffect(() => {
    setLive(initial.data ?? []);
  }, [initial.data]);

  useEffect(() => {
    const tok = getToken();
    if (!tok) return;
    const url = new URL(`/api/v1/projects/${project}/events/ws`, window.location.href);
    url.protocol = url.protocol.replace("http", "ws");
    url.searchParams.set("access_token", tok);
    const ws = new WebSocket(url.toString(), "selfcloud.v1");
    wsRef.current = ws;
    ws.onmessage = (msg) => {
      try {
        const ev: EventRecord = JSON.parse(typeof msg.data === "string" ? msg.data : "");
        setLive((cur) => {
          if (cur.find((e) => e.uid === ev.uid)) return cur;
          return [ev, ...cur].slice(0, 500);
        });
      } catch {
        // ignore
      }
    };
    ws.onerror = () => undefined;
    return () => {
      ws.close();
    };
  }, [project]);

  const filtered = useMemo(() => {
    if (!filter) return live;
    const f = filter.toLowerCase();
    return live.filter(
      (e) =>
        e.type.toLowerCase().includes(f) ||
        (e.subject ?? "").toLowerCase().includes(f) ||
        Object.values(e.data ?? {}).some((v) => v.toLowerCase().includes(f))
    );
  }, [live, filter]);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <input
          className="input flex-1"
          placeholder="Filter by type, subject, or data"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <span className="text-xs text-muted">{filtered.length} events</span>
      </div>
      <div className="card overflow-hidden">
        {filtered.length === 0 && (
          <EmptyState
            icon={Activity}
            title="No events yet"
            description="Events show up here whenever a container starts, crashes, a function is invoked, or an S3 PUT/DELETE goes through the proxy."
          />
        )}
        {filtered.length > 0 && (
          <ul className="divide-y divide-border">
            {filtered.map((ev) => (
              <li key={ev.uid} className="px-4 py-3 hover:bg-elevated/40">
                <div className="flex items-center gap-2 text-sm">
                  <span className="badge">{ev.type}</span>
                  {ev.subject && <span className="font-mono text-xs">{ev.subject}</span>}
                  <span className="ml-auto text-xs text-muted">{new Date(ev.at).toLocaleString()}</span>
                </div>
                {ev.data && Object.keys(ev.data).length > 0 && (
                  <pre className="mt-1 text-xs font-mono text-muted whitespace-pre-wrap break-all">
                    {JSON.stringify(ev.data, null, 2)}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function Rules() {
  const qc = useQueryClient();
  const { project } = useProject();
  const rules = useQuery<EventRule[]>({
    queryKey: ["event-rules", project],
    queryFn: () => api.get<EventRule[]>(`/api/v1/projects/${project}/event-rules`).catch(() => []),
  });
  const [open, setOpen] = useState(false);
  const [deliveriesFor, setDeliveriesFor] = useState<string | null>(null);

  const remove = useMutation({
    mutationFn: (name: string) => api.del(`/api/v1/projects/${project}/event-rules/${name}`),
    onSuccess: (_d, name) => {
      toast.success(`Deleted ${name}`);
      qc.invalidateQueries({ queryKey: ["event-rules", project] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  const test = useMutation({
    mutationFn: (name: string) => api.post(`/api/v1/projects/${project}/event-rules/${name}/test`, {}),
    onSuccess: () => toast.success("Test event queued"),
    onError: (e) => toast.error(e instanceof Error ? e.message : "test failed"),
  });

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted">
          Rules fire when an event matches their type and subject. Multiple sinks per rule are OK.
        </p>
        <button className="btn-primary" onClick={() => setOpen(true)}>
          <Plus size={14} /> New rule
        </button>
      </div>
      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-elevated text-muted">
            <tr>
              <th className="px-4 py-2 text-left">Name</th>
              <th className="px-4 py-2 text-left">Match</th>
              <th className="px-4 py-2 text-left">Action</th>
              <th className="px-4 py-2 text-left">Last fired</th>
              <th className="px-4 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {rules.isLoading && <SkeletonRows rows={3} cols={5} />}
            {!rules.isLoading &&
              (rules.data ?? []).map((rule) => (
                <tr key={rule.meta.uid || rule.meta.name} className="border-t border-border hover:bg-elevated/40">
                  <td className="px-4 py-2 font-mono">{rule.meta.name}</td>
                  <td className="px-4 py-2 text-xs">
                    <div className="flex flex-wrap gap-1">
                      {(rule.match.types ?? ["*"]).map((t) => (
                        <span key={t} className="badge">{t}</span>
                      ))}
                    </div>
                    {rule.match.subject && (
                      <span className="text-muted font-mono">{rule.match.subject}</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-xs">
                    {rule.action.webhook && <div>webhook: <code>{rule.action.webhook.url}</code></div>}
                    {rule.action.invoke && <div>invoke: <code>{rule.action.invoke.function}</code></div>}
                    {rule.action.container && (
                      <div>
                        {rule.action.container.action} <code>{rule.action.container.container}</code>
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted">
                    {rule.lastFiredAt ? new Date(rule.lastFiredAt).toLocaleString() : "never"}
                    {!!rule.fireCount && <span className="ml-1">({rule.fireCount}x)</span>}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {rule.action.webhook && (
                      <button
                        className="btn"
                        title="Recent deliveries"
                        onClick={() => setDeliveriesFor(rule.meta.name)}
                      >
                        <Webhook size={14} />
                      </button>
                    )}
                    <button
                      className="btn ml-2"
                      title="Send test event"
                      onClick={() => test.mutate(rule.meta.name)}
                    >
                      <Send size={14} />
                    </button>
                    <button
                      className="btn-danger ml-2"
                      title="Delete"
                      onClick={() => {
                        if (confirm(`Delete rule ${rule.meta.name}?`)) remove.mutate(rule.meta.name);
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            {!rules.isLoading && !rules.data?.length && (
              <tr>
                <td colSpan={5}>
                  <EmptyState
                    icon={Activity}
                    title="No rules yet"
                    description="Define a rule like 'when container.crash on payments-api, restart it' or 'when s3.put happens in uploads/, invoke thumb-fn'."
                    action={
                      <button className="btn-primary" onClick={() => setOpen(true)}>
                        <Plus size={14} /> New rule
                      </button>
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {open && <NewRuleDialog onClose={() => setOpen(false)} />}
      {deliveriesFor && (
        <DeliveriesPanel rule={deliveriesFor} onClose={() => setDeliveriesFor(null)} />
      )}
    </div>
  );
}

function DeliveriesPanel({ rule, onClose }: { rule: string; onClose: () => void }) {
  const { project } = useProject();
  const dlvs = useQuery<WebhookDelivery[]>({
    queryKey: ["deliveries", project, rule],
    queryFn: () =>
      api
        .get<WebhookDelivery[]>(`/api/v1/projects/${project}/event-rules/${rule}/deliveries`)
        .catch(() => []),
    refetchInterval: 4000,
  });
  return (
    <div className="fixed inset-0 bg-bg/70 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <div className="card w-full max-w-3xl p-6 space-y-3 max-h-[85vh] overflow-auto">
        <div className="flex items-center justify-between">
          <h2 className="font-semibold">
            <Webhook size={14} className="inline mr-1" /> Deliveries — {rule}
          </h2>
          <button className="btn" onClick={onClose}>Close</button>
        </div>
        {!dlvs.data?.length && (
          <p className="text-sm text-muted">No deliveries yet. Hit "Send test event" to try.</p>
        )}
        {!!dlvs.data?.length && (
          <table className="w-full text-sm">
            <thead className="text-muted">
              <tr>
                <th className="text-left px-2 py-1">When</th>
                <th className="text-left px-2 py-1">Event</th>
                <th className="text-left px-2 py-1">Attempt</th>
                <th className="text-left px-2 py-1">Status</th>
                <th className="text-left px-2 py-1">Error</th>
              </tr>
            </thead>
            <tbody>
              {dlvs.data!.map((d) => (
                <tr key={d.meta.name} className="border-t border-border">
                  <td className="px-2 py-1 text-xs whitespace-nowrap">
                    {new Date(d.startedAt).toLocaleString()}
                  </td>
                  <td className="px-2 py-1 font-mono text-xs">{d.eventType}</td>
                  <td className="px-2 py-1 text-xs">#{d.attempt}</td>
                  <td className="px-2 py-1">
                    {d.status && d.status > 0 ? (
                      <span
                        className={
                          d.status >= 200 && d.status < 300 ? "badge-success" : "badge-danger"
                        }
                      >
                        {d.status}
                      </span>
                    ) : (
                      <span className="badge">err</span>
                    )}
                  </td>
                  <td className="px-2 py-1 text-xs text-muted truncate max-w-md">{d.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

const EVENT_TYPES = [
  "container.start",
  "container.crash",
  "container.stop",
  "container.log",
  "function.invoked",
  "function.error",
  "s3.put",
  "s3.delete",
  "app.event",
  "cron",
];

function NewRuleDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { project } = useProject();
  const [name, setName] = useState("");
  const [type, setType] = useState<string>("container.crash");
  const [subject, setSubject] = useState("");
  const [actionType, setActionType] = useState<"webhook" | "invoke" | "container">("webhook");
  const [webhookURL, setWebhookURL] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [invokeFn, setInvokeFn] = useState("");
  const [ctrName, setCtrName] = useState("");
  const [ctrAction, setCtrAction] = useState<"start" | "stop" | "restart">("restart");
  const [busy, setBusy] = useState(false);

  const fns = useQuery<FunctionRecord[]>({
    queryKey: ["functions", project],
    queryFn: () => api.get<FunctionRecord[]>(`/api/v1/projects/${project}/functions`).catch(() => []),
    enabled: actionType === "invoke",
  });
  const ctrs = useQuery<Container[]>({
    queryKey: ["containers", project],
    queryFn: () => api.get<Container[]>(`/api/v1/projects/${project}/containers`).catch(() => []),
    enabled: actionType === "container",
  });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const action: EventRule["action"] = {};
      if (actionType === "webhook") action.webhook = { url: webhookURL, secret: webhookSecret || undefined };
      if (actionType === "invoke") action.invoke = { function: invokeFn };
      if (actionType === "container") action.container = { container: ctrName, action: ctrAction };
      await api.post(`/api/v1/projects/${project}/event-rules`, {
        meta: { project, name },
        match: { types: [type], subject: subject || undefined },
        action,
        enabled: true,
      });
      toast.success(`Created rule ${name}`);
      qc.invalidateQueries({ queryKey: ["event-rules", project] });
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-bg/70 backdrop-blur-sm flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="card w-full max-w-lg p-6 space-y-4">
        <h2 className="font-semibold">New event rule</h2>
        <div className="space-y-1">
          <label className="text-sm text-muted">Name</label>
          <input className="input font-mono" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="grid grid-cols-2 gap-2">
          <div className="space-y-1">
            <label className="text-sm text-muted">When event</label>
            <select className="input" value={type} onChange={(e) => setType(e.target.value)}>
              {EVENT_TYPES.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label className="text-sm text-muted">Matches subject (optional)</label>
            <input
              className="input font-mono"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder="container or bucket name, regex for log"
            />
          </div>
        </div>
        <div className="space-y-1">
          <label className="text-sm text-muted">Then</label>
          <select className="input" value={actionType} onChange={(e) => setActionType(e.target.value as never)}>
            <option value="webhook">Send webhook</option>
            <option value="invoke">Invoke function</option>
            <option value="container">Container action</option>
          </select>
        </div>
        {actionType === "webhook" && (
          <div className="space-y-2">
            <input
              className="input font-mono"
              placeholder="https://example.com/webhook"
              value={webhookURL}
              onChange={(e) => setWebhookURL(e.target.value)}
              required
            />
            <input
              className="input font-mono"
              placeholder="HMAC secret (optional)"
              value={webhookSecret}
              onChange={(e) => setWebhookSecret(e.target.value)}
            />
          </div>
        )}
        {actionType === "invoke" && (
          <select
            className="input"
            value={invokeFn}
            onChange={(e) => setInvokeFn(e.target.value)}
            required
          >
            <option value="">Select a function...</option>
            {(fns.data ?? []).map((f) => (
              <option key={f.meta.name} value={f.meta.name}>
                {f.meta.name}
              </option>
            ))}
          </select>
        )}
        {actionType === "container" && (
          <div className="grid grid-cols-2 gap-2">
            <select
              className="input"
              value={ctrName}
              onChange={(e) => setCtrName(e.target.value)}
              required
            >
              <option value="">Select a container...</option>
              {(ctrs.data ?? []).map((c) => (
                <option key={c.meta.name} value={c.meta.name}>
                  {c.meta.name}
                </option>
              ))}
            </select>
            <select className="input" value={ctrAction} onChange={(e) => setCtrAction(e.target.value as never)}>
              <option value="restart">Restart</option>
              <option value="stop">Stop</option>
              <option value="start">Start</option>
            </select>
          </div>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn-primary" disabled={busy}>
            <Plus size={14} /> {busy ? "Creating..." : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}
