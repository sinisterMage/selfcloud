import { useState } from "react";
import { Layers } from "lucide-react";
import { api } from "../lib/api";

export default function LoginPage({ onLogin }: { onLogin: (token: string) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { token } = await api.post<{ token: string }>("/api/v1/auth/login", { email, password }, { auth: false });
      onLogin(token);
    } catch (e) {
      setError(e instanceof Error ? e.message : "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-full items-center justify-center px-4">
      <form onSubmit={submit} className="card p-6 w-full max-w-sm space-y-4">
        <div className="flex items-center gap-2">
          <Layers className="text-accent" size={22} />
          <h1 className="font-semibold tracking-tight">Sign in to selfCloud</h1>
        </div>
        <div className="space-y-2">
          <label className="block text-sm text-muted">Email</label>
          <input className="input" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </div>
        <div className="space-y-2">
          <label className="block text-sm text-muted">Password</label>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </div>
        {error && <div className="text-sm text-danger">{error}</div>}
        <button className="btn-primary w-full" disabled={busy} type="submit">
          {busy ? "Signing in..." : "Sign in"}
        </button>
      </form>
    </div>
  );
}
