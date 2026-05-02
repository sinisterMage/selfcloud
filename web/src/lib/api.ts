// Tiny API client. Talks to the same origin so the dashboard works whether
// it's served by the embedded Go binary or by `vite dev` with the proxy.

const TOKEN_KEY = "selfcloud.token";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(t: string | null) {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
}

export type Json = Record<string, unknown> | unknown[];

async function request<T>(method: string, path: string, body?: Json | string | FormData | null, opts?: { auth?: boolean }): Promise<T> {
  const headers: Record<string, string> = {};
  const auth = opts?.auth ?? true;
  if (auth) {
    const t = getToken();
    if (t) headers["authorization"] = `Bearer ${t}`;
  }

  let payload: BodyInit | undefined;
  if (body && typeof body === "string") {
    payload = body;
  } else if (body instanceof FormData) {
    payload = body;
  } else if (body) {
    headers["content-type"] = "application/json";
    payload = JSON.stringify(body);
  }

  const res = await fetch(path, { method, headers, body: payload });
  if (res.status === 401 && auth) {
    setToken(null);
    if (location.pathname !== "/login") {
      location.assign("/login");
    }
    throw new ApiError(res.status, "unauthorized");
  }
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    const msg = (data as { error?: string })?.error ?? res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export const api = {
  get: <T>(path: string, opts?: { auth?: boolean }) => request<T>("GET", path, null, opts),
  post: <T>(path: string, body?: Json | string | FormData, opts?: { auth?: boolean }) => request<T>("POST", path, body, opts),
  put: <T>(path: string, body?: Json, opts?: { auth?: boolean }) => request<T>("PUT", path, body, opts),
  del: <T>(path: string, opts?: { auth?: boolean }) => request<T>("DELETE", path, null, opts),
};
