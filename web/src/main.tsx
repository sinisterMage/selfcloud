import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import App from "./App";
import { ThemeProvider } from "./lib/theme";
import { ProjectProvider } from "./lib/project";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 5_000,
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <ProjectProvider>
        <BrowserRouter>
          <QueryClientProvider client={queryClient}>
            <App />
            <Toaster
              position="top-right"
              toastOptions={{
                style: { background: "var(--toast-bg, #161b22)", color: "var(--toast-fg, #e6edf3)", border: "1px solid var(--toast-border, #1e242c)" },
              }}
            />
          </QueryClientProvider>
        </BrowserRouter>
      </ProjectProvider>
    </ThemeProvider>
  </React.StrictMode>
);
