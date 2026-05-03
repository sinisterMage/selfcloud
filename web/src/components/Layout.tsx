import { useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import {
  Activity,
  Boxes,
  ChevronsLeft,
  ChevronsRight,
  Cloud,
  HardDrive,
  KeyRound,
  Layers,
  LogOut,
  Moon,
  Network,
  Plus,
  Settings,
  Sun,
  Trash2,
  X,
  Zap,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { cn } from "../lib/cn";
import { useTheme } from "../lib/theme";
import { useProject } from "../lib/project";
import { api } from "../lib/api";
import type { Project } from "../lib/types";

const items = [
  { to: "/", label: "Overview", icon: Cloud, end: true },
  { to: "/containers", label: "Containers", icon: Boxes },
  { to: "/buckets", label: "S3 Buckets", icon: HardDrive },
  { to: "/functions", label: "Functions", icon: Zap },
  { to: "/secrets", label: "Secrets", icon: KeyRound },
  { to: "/events", label: "Events", icon: Activity },
  { to: "/nodes", label: "Nodes", icon: Network },
  { to: "/settings", label: "Settings", icon: Settings },
];

const COLLAPSE_KEY = "selfcloud.sidebar";

export default function Layout({ onLogout, userEmail }: { onLogout: () => void; userEmail?: string }) {
  const qc = useQueryClient();
  const { theme, toggle } = useTheme();
  const { project, setProject } = useProject();
  const [collapsed, setCollapsed] = useState<boolean>(() =>
    typeof window === "undefined" ? false : window.localStorage.getItem(COLLAPSE_KEY) === "1"
  );
  const [showProjects, setShowProjects] = useState(false);

  useEffect(() => {
    window.localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
  }, [collapsed]);

  const projects = useQuery<Project[]>({
    queryKey: ["projects"],
    queryFn: () => api.get<Project[]>("/api/v1/projects").catch(() => [] as Project[]),
  });

  return (
    <div className="flex h-full">
      <aside
        className={cn(
          "flex flex-col border-r border-border bg-surface transition-[width] duration-150",
          collapsed ? "w-14" : "w-60"
        )}
      >
        <div className={cn("flex items-center gap-2 px-4 py-4 border-b border-border", collapsed && "justify-center px-2")}>
          <Layers className="text-accent shrink-0" size={22} />
          {!collapsed && <span className="font-semibold tracking-tight">selfCloud</span>}
        </div>
        <nav className="flex-1 px-2 py-3 space-y-0.5">
          {items.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              title={it.label}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                  collapsed && "justify-center px-2",
                  isActive ? "bg-accent/15 text-accent" : "text-muted hover:text-fg hover:bg-elevated"
                )
              }
            >
              <it.icon size={16} className="shrink-0" />
              {!collapsed && it.label}
            </NavLink>
          ))}
        </nav>
        <button
          onClick={() => setCollapsed((c) => !c)}
          className={cn(
            "mx-2 mt-0 mb-1 flex items-center justify-center gap-2 rounded-md px-2 py-2 text-xs",
            "text-muted hover:text-fg hover:bg-elevated"
          )}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        >
          {collapsed ? <ChevronsRight size={14} /> : <><ChevronsLeft size={14} /> Collapse</>}
        </button>
      </aside>

      <main className="flex-1 flex flex-col overflow-hidden">
        <header className="flex items-center justify-between border-b border-border bg-surface/60 backdrop-blur px-6 h-12 shrink-0">
          <div className="flex items-center gap-3 text-sm">
            <span className="text-muted">Project</span>
            <select
              className="rounded-md border border-border bg-surface px-2 py-1 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-accent/40"
              value={project}
              onChange={(e) => setProject(e.target.value)}
            >
              {(projects.data ?? [{ meta: { name: "default" } } as Project]).map((p) => (
                <option key={p.meta.name} value={p.meta.name}>
                  {p.meta.name}
                </option>
              ))}
              {!projects.data?.some((p) => p.meta.name === project) && (
                <option value={project}>{project}</option>
              )}
            </select>
            <button
              className="btn-ghost"
              title="Manage projects"
              onClick={() => setShowProjects(true)}
            >
              <Plus size={14} />
            </button>
          </div>
          <div className="flex items-center gap-2">
            <button className="btn-ghost" onClick={toggle} title={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}>
              {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
            </button>
            {userEmail && <span className="text-xs text-muted hidden sm:inline">{userEmail}</span>}
            <button className="btn-ghost" onClick={onLogout} title="Sign out">
              <LogOut size={16} />
            </button>
          </div>
        </header>
        <div className="flex-1 overflow-auto">
          <Outlet />
        </div>
      </main>

      {showProjects && (
        <ProjectsManager
          current={project}
          projects={projects.data ?? []}
          onClose={() => setShowProjects(false)}
          onSelect={(name) => setProject(name)}
          onMutated={() => qc.invalidateQueries({ queryKey: ["projects"] })}
        />
      )}
    </div>
  );
}

function ProjectsManager({
  current,
  projects,
  onClose,
  onSelect,
  onMutated,
}: {
  current: string;
  projects: Project[];
  onClose: () => void;
  onSelect: (name: string) => void;
  onMutated: () => void;
}) {
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api.post<Project>("/api/v1/projects", {
        meta: { name: name.trim() },
        displayName: displayName.trim() || undefined,
      }),
    onSuccess: (p) => {
      toast.success(`Created ${p.meta.name}`);
      onSelect(p.meta.name);
      onMutated();
      setName("");
      setDisplayName("");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "create failed"),
  });

  const remove = useMutation({
    mutationFn: (n: string) => api.del(`/api/v1/projects/${n}`),
    onSuccess: (_d, n) => {
      toast.success(`Deleted ${n}`);
      if (n === current) onSelect("default");
      onMutated();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "delete failed"),
  });

  return (
    <div className="fixed inset-0 z-50 bg-black/40 flex items-center justify-center p-4" onClick={onClose}>
      <div className="card max-w-lg w-full p-5 space-y-4" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Projects</h2>
          <button className="btn-ghost" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <div className="overflow-hidden rounded-md border border-border">
          <table className="w-full text-sm">
            <thead className="bg-elevated text-muted">
              <tr>
                <th className="px-3 py-2 text-left">Name</th>
                <th className="px-3 py-2 text-left">Display</th>
                <th className="px-3 py-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {projects.map((p) => (
                <tr key={p.meta.name} className="border-t border-border">
                  <td className="px-3 py-2 font-mono">{p.meta.name}</td>
                  <td className="px-3 py-2 text-muted">{p.displayName ?? "—"}</td>
                  <td className="px-3 py-2 text-right">
                    {p.meta.name !== "default" && (
                      <button
                        className="btn-ghost"
                        onClick={() => remove.mutate(p.meta.name)}
                        title={`Delete ${p.meta.name}`}
                      >
                        <Trash2 size={14} />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <input
            className="input"
            placeholder="name (lowercase, no spaces)"
            value={name}
            onChange={(e) => setName(e.target.value.replace(/[^a-z0-9-]/g, "").slice(0, 32))}
          />
          <input
            className="input"
            placeholder="display name (optional)"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="flex justify-end">
          <button
            className="btn-primary"
            onClick={() => create.mutate()}
            disabled={create.isPending || name.trim().length < 2}
          >
            <Plus size={14} /> {create.isPending ? "Creating..." : "Create project"}
          </button>
        </div>
      </div>
    </div>
  );
}
