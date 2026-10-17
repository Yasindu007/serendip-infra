// The console's only way to talk to Serendip (constitution 7): same-origin
// fetches to /v1 with the session cookie and the CSRF header. No token ever
// passes through here; the browser holds only the HttpOnly cookie.
import type { Problem } from "./types";

const TIMEOUT_MS = 15_000;

export class ApiError extends Error {
  readonly status: number;
  readonly title: string;
  readonly detail: string | undefined;
  readonly requestId: string | undefined;
  readonly retryAfterSeconds: number | undefined;

  constructor(status: number, title: string, detail?: string, requestId?: string, retryAfterSeconds?: number) {
    super(detail ? `${title}: ${detail}` : title);
    this.status = status;
    this.title = title;
    this.detail = detail;
    this.requestId = requestId;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

/** Thrown when the network or the timeout, not the server, failed. */
export class NetworkError extends Error {}

type SessionEndedListener = () => void;
const sessionEnded = new Set<SessionEndedListener>();

/** Called whenever any request learns the session is over (a 401). */
export function onSessionEnded(fn: SessionEndedListener): () => void {
  sessionEnded.add(fn);
  return () => sessionEnded.delete(fn);
}

export async function apiFetch<T>(method: "GET" | "POST" | "PUT", path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const timeout = AbortSignal.timeout(TIMEOUT_MS);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const headers: Record<string, string> = { Accept: "application/json", "X-Serendip-Csrf": "1" };
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal: combined,
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });
  } catch (err) {
    if (signal?.aborted) throw err; // the caller cancelled: not an error to show
    if (timeout.aborted) throw new NetworkError("Serendip didn't answer within 15 seconds.");
    throw new NetworkError("Can't reach Serendip.");
  }

  if (res.ok) {
    if (res.status === 204) return undefined as T;
    const data = (await res.json()) as T;
    return data;
  }

  let problem: Partial<Problem> = {};
  try {
    problem = (await res.json()) as Partial<Problem>;
  } catch {
    // Not problem+json: fall back to the status line.
  }
  if (res.status === 401) sessionEnded.forEach((fn) => fn());
  const retry = Number(res.headers.get("Retry-After"));
  throw new ApiError(
    res.status,
    typeof problem.title === "string" ? problem.title : res.statusText || `HTTP ${res.status}`,
    typeof problem.detail === "string" ? problem.detail : undefined,
    typeof problem.instance === "string" ? problem.instance : (res.headers.get("X-Request-Id") ?? undefined),
    Number.isFinite(retry) && retry > 0 ? retry : undefined,
  );
}

/** A list endpoint answers a JSON array, or null when empty. */
export async function apiList<T>(path: string, signal?: AbortSignal): Promise<T[]> {
  return (await apiFetch<T[] | null>("GET", path, undefined, signal)) ?? [];
}

/** Ends the console session and returns where the browser should go next. */
export async function signOut(): Promise<string> {
  const res = await fetch("/auth/logout", {
    method: "POST",
    headers: { "X-Serendip-Csrf": "1" },
    credentials: "same-origin",
    redirect: "error",
  });
  const out = (await res.json().catch(() => ({}))) as { location?: unknown };
  return typeof out.location === "string" ? out.location : "/";
}

/** True for errors the user should see (not a cancelled request). */
export function isShown(err: unknown): err is ApiError | NetworkError {
  return err instanceof ApiError || err instanceof NetworkError;
}
