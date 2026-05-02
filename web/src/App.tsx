import { useEffect, useState } from "react";
import { Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { api, getToken, setToken } from "./lib/api";
import type { SetupStatus } from "./lib/types";
import Layout from "./components/Layout";
import LoginPage from "./pages/Login";
import SetupWizard from "./pages/Setup";
import OverviewPage from "./pages/Overview";
import ContainersPage from "./pages/Containers";
import BucketsPage from "./pages/Buckets";
import FunctionsPage from "./pages/Functions";
import NodesPage from "./pages/Nodes";
import SettingsPage from "./pages/Settings";

export default function App() {
  const [status, setStatus] = useState<SetupStatus | "loading" | "error">("loading");

  useEffect(() => {
    api
      .get<SetupStatus>("/api/v1/setup/status", { auth: false })
      .then(setStatus)
      .catch(() => setStatus("error"));
  }, []);

  if (status === "loading") {
    return (
      <div className="flex h-full items-center justify-center text-muted">
        Loading selfCloud...
      </div>
    );
  }

  if (status === "error") {
    return (
      <div className="flex h-full items-center justify-center text-danger">
        Could not reach the selfcloud control plane. Is the server running?
      </div>
    );
  }

  if (!status.initialized) {
    return (
      <Routes>
        <Route path="/setup" element={<SetupWizard />} />
        <Route path="*" element={<Navigate to="/setup" replace />} />
      </Routes>
    );
  }

  return <AuthenticatedApp />;
}

function AuthenticatedApp() {
  const navigate = useNavigate();
  const [authed, setAuthed] = useState(!!getToken());

  useEffect(() => {
    if (!authed) navigate("/login");
  }, [authed, navigate]);

  return (
    <Routes>
      <Route
        path="/login"
        element={
          <LoginPage
            onLogin={(t) => {
              setToken(t);
              setAuthed(true);
              navigate("/");
            }}
          />
        }
      />
      <Route element={<Layout onLogout={() => { setToken(null); setAuthed(false); }} />}>
        <Route index element={<OverviewPage />} />
        <Route path="/containers" element={<ContainersPage />} />
        <Route path="/buckets" element={<BucketsPage />} />
        <Route path="/functions" element={<FunctionsPage />} />
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/" />} />
      </Route>
    </Routes>
  );
}
