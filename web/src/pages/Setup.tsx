import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Layers } from "lucide-react";
import { api, setToken } from "../lib/api";

export default function SetupWizard() {
  const navigate = useNavigate();
  const [step, setStep] = useState(0);
  const [token, setBootstrap] = useState("");
  const [multiNode, setMultiNode] = useState(false);
  const [name, setName] = useState("admin");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function finish() {
    setBusy(true);
    setError(null);
    try {
      const out = await api.post<{ token: string }>(
        "/api/v1/setup/initialize",
        {
          bootstrapToken: token,
          adminEmail: email,
          adminName: name,
          adminPassword: password,
          multiNode,
        },
        { auth: false }
      );
      setToken(out.token);
      navigate("/", { replace: true });
      window.location.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "setup failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-full items-center justify-center px-4">
      <div className="card w-full max-w-lg p-6 space-y-6">
        <div className="flex items-center gap-2">
          <Layers className="text-accent" size={22} />
          <h1 className="font-semibold tracking-tight">Welcome to selfCloud</h1>
        </div>

        {step === 0 && (
          <div className="space-y-4">
            <p className="text-muted text-sm">
              Pick a deployment style. You can change this later from Settings.
            </p>
            <button
              className={`card w-full p-4 text-left ${!multiNode ? "border-accent" : ""}`}
              onClick={() => setMultiNode(false)}
            >
              <div className="font-medium">Single node</div>
              <div className="text-sm text-muted">
                Run everything on this machine. Perfect for homelab and small VPS deployments.
              </div>
            </button>
            <button
              className={`card w-full p-4 text-left ${multiNode ? "border-accent" : ""}`}
              onClick={() => setMultiNode(true)}
            >
              <div className="font-medium">Multi-node coordinator</div>
              <div className="text-sm text-muted">
                Designate this node as the cluster's first member. Add more nodes later from the Nodes panel.
              </div>
            </button>
            <div className="flex justify-end">
              <button className="btn-primary" onClick={() => setStep(1)}>
                Continue
              </button>
            </div>
          </div>
        )}

        {step === 1 && (
          <div className="space-y-4">
            <p className="text-muted text-sm">
              Paste the one-time bootstrap token shown by the installer (also in <code className="rounded bg-elevated px-1">/var/lib/selfcloud/bootstrap-token</code>).
            </p>
            <input className="input font-mono" value={token} onChange={(e) => setBootstrap(e.target.value)} placeholder="sct_..." />
            <div className="flex justify-between">
              <button className="btn" onClick={() => setStep(0)}>Back</button>
              <button className="btn-primary" disabled={!token} onClick={() => setStep(2)}>
                Continue
              </button>
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4">
            <p className="text-muted text-sm">Create the first admin account.</p>
            <div className="space-y-1">
              <label className="text-sm text-muted">Display name</label>
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Email</label>
              <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            <div className="space-y-1">
              <label className="text-sm text-muted">Password</label>
              <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            {error && <div className="text-sm text-danger">{error}</div>}
            <div className="flex justify-between">
              <button className="btn" onClick={() => setStep(1)}>Back</button>
              <button className="btn-primary" disabled={!email || !password || busy} onClick={finish}>
                {busy ? "Setting up..." : "Finish setup"}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
