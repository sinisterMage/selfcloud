import { NavLink, Outlet } from "react-router-dom";
import {
  Boxes,
  Cloud,
  HardDrive,
  Layers,
  LogOut,
  Network,
  Settings,
  Zap,
} from "lucide-react";
import { cn } from "../lib/cn";

const items = [
  { to: "/", label: "Overview", icon: Cloud, end: true },
  { to: "/containers", label: "Containers", icon: Boxes },
  { to: "/buckets", label: "S3 Buckets", icon: HardDrive },
  { to: "/functions", label: "Functions", icon: Zap },
  { to: "/nodes", label: "Nodes", icon: Network },
  { to: "/settings", label: "Settings", icon: Settings },
];

export default function Layout({ onLogout }: { onLogout: () => void }) {
  return (
    <div className="flex h-full">
      <aside className="flex w-60 flex-col border-r border-border bg-surface">
        <div className="flex items-center gap-2 px-5 py-4 border-b border-border">
          <Layers className="text-accent" size={22} />
          <span className="font-semibold tracking-tight">selfCloud</span>
        </div>
        <nav className="flex-1 px-2 py-3 space-y-0.5">
          {items.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-accent/15 text-accent"
                    : "text-muted hover:text-fg hover:bg-elevated"
                )
              }
            >
              <it.icon size={16} />
              {it.label}
            </NavLink>
          ))}
        </nav>
        <button
          onClick={onLogout}
          className="m-3 mt-0 flex items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-sm text-muted hover:text-danger hover:border-danger/40"
        >
          <LogOut size={14} /> Sign out
        </button>
      </aside>
      <main className="flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
