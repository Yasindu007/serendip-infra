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

export async function apiFetch<T>(method: "GET" | "POST" | "PUT" | "DELETE", path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await request(method, path, body, signal);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Sends one request; a non-2xx answer becomes an ApiError. */
async function request(method: "GET" | "POST" | "PUT" | "DELETE", path: string, body?: unknown, signal?: AbortSignal, stream = false): Promise<Response> {
  // A stream's body may last minutes: its timeout covers only the wait
  // for the answer's headers.
  const timer = new AbortController();
  const id = stream ? setTimeout(() => timer.abort(), TIMEOUT_MS) : undefined;
  const timeout = stream ? timer.signal : AbortSignal.timeout(TIMEOUT_MS);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const headers: Record<string, string> = { Accept: stream ? "application/x-ndjson" : "application/json", "X-Serendip-Csrf": "1" };
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
    clearTimeout(id);
    if (signal?.aborted) throw err; // the caller cancelled: not an error to show
    if (timeout.aborted) throw new NetworkError("Serendip didn't answer within 15 seconds.");
    throw new NetworkError("Can't reach Serendip.");
  }
  clearTimeout(id);

  if (res.ok) return res;

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

/**
 * DELETE records an Approval with every delete (spec 019): the server names
 * it in Serendip-Approval-Id. Returns that ID, or undefined if absent.
 */
export async function apiDelete(path: string): Promise<string | undefined> {
  return (await request("DELETE", path)).headers.get("Serendip-Approval-Id") ?? undefined;
}

/** One page of a paged list (GET /v1/approvals) and the cursor for the next. */
export async function apiPage<T>(path: string, signal?: AbortSignal): Promise<{ items: T[]; next: string | undefined }> {
  const res = await request("GET", path, undefined, signal);
  const items = ((await res.json()) as T[] | null) ?? [];
  return { items, next: res.headers.get("Serendip-Next-After") ?? undefined };
}

/** A list endpoint answers a JSON array, or null when empty. */
export async function apiList<T>(path: string, signal?: AbortSignal): Promise<T[]> {
  return (await apiFetch<T[] | null>("GET", path, undefined, signal)) ?? [];
}

/**
 * Reads an NDJSON stream (GET .../logs?follow=true), calling onItem for
 * each object until the server ends it with {"end": reason}, which is
 * returned. Undefined means the connection closed without saying why, so
 * lines may be missing. Cancel with signal.
 */
export async function apiStream<T>(path: string, onItem: (item: T) => void, signal: AbortSignal): Promise<string | undefined> {
  const res = await request("GET", path, undefined, signal, true);
  if (!res.body) return undefined;
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return undefined;
      buf += value;
      let nl: number;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const text = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (text === "") continue;
        const item = JSON.parse(text) as T & { end?: unknown };
        if (typeof item.end === "string") return item.end;
        onItem(item);
      }
    }
  } catch (err) {
    if (signal.aborted) throw err;
    throw new NetworkError("The log stream was cut off.");
  } finally {
    reader.releaseLock();
  }
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
