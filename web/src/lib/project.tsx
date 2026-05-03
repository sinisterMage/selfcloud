import { createContext, useContext, useEffect, useMemo, useState } from "react";

interface ProjectContextValue {
  project: string;
  setProject: (p: string) => void;
}

const ProjectContext = createContext<ProjectContextValue | null>(null);
const KEY = "selfcloud.project";

export function ProjectProvider({ children }: { children: React.ReactNode }) {
  const [project, setProject] = useState<string>(() => {
    if (typeof window === "undefined") return "default";
    return window.localStorage.getItem(KEY) || "default";
  });

  useEffect(() => {
    window.localStorage.setItem(KEY, project);
  }, [project]);

  const value = useMemo(() => ({ project, setProject }), [project]);
  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>;
}

export function useProject() {
  const ctx = useContext(ProjectContext);
  if (!ctx) throw new Error("useProject must be used inside <ProjectProvider>");
  return ctx;
}
