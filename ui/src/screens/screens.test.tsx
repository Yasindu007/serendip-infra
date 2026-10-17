import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { usePoll } from "../hooks/usePoll";
import { formatRequest } from "../components/ApiEquivalent";

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>;

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

let calls: Array<{ url: string; init: RequestInit }>;

function mockFetch(handler: Handler) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      return handler(url, init);
    }),
  );
}

function goTo(path: string) {
  window.history.replaceState(null, "", path);
}

beforeEach(() => {
  goTo("/");
  vi.stubGlobal("scrollTo", () => {}); // jsdom doesn't implement it
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const session = { actor: "person:admin", method: "session" };

describe("session gate", () => {
  it("sends a signed-out person to sign in, keeping where they were", async () => {
    goTo("/applications/abc?tab=x");
    mockFetch(() => json(401, { title: "unauthorized", status: 401 }));
    render(<App />);
    const link = await screen.findByRole("link", { name: "Sign in" });
    expect(link.getAttribute("href")).toBe("/auth/login?return=" + encodeURIComponent("/applications/abc?tab=x"));
  });

  it("shows the sign-in error by its fixed code only", async () => {
    goTo("/signin?error=%3Cscript%3Ealert(1)%3C%2Fscript%3E");
    mockFetch(() => json(401, {}));
    render(<App />);
    expect(await screen.findByText(/couldn't be verified/)).toBeTruthy();
    expect(document.body.innerHTML).not.toContain("<script>alert");
  });

  it("sends every request with the CSRF header and no credentials in JS", async () => {
    mockFetch((url) => (url === "/v1/session" ? json(200, session) : json(200, url === "/v1/readiness" ? {} : [])));
    render(<App />);
    await screen.findByRole("heading", { name: "Overview" });
    for (const c of calls) {
      const h = c.init.headers as Record<string, string>;
      expect(h["X-Serendip-Csrf"]).toBe("1");
      expect(Object.keys(h).map((k) => k.toLowerCase())).not.toContain("authorization");
      expect(c.init.credentials).toBe("same-origin");
    }
  });

  it("falls back to sign-in when a later request finds the session over", async () => {
    let ended = false;
    mockFetch((url) => {
      if (url === "/v1/session") return json(200, session);
      if (ended) return json(401, { title: "unauthorized", status: 401 });
      return json(200, url === "/v1/readiness" ? {} : []);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "Overview" });
    ended = true;
    fireEvent.click(screen.getByRole("link", { name: "Applications" }));
    expect(await screen.findByText("Your session ended. Sign in again to continue.")).toBeTruthy();
  });
});

describe("errors", () => {
  it("shows a 503's title, a retry note and the request ID", async () => {
    goTo("/applications");
    mockFetch((url) =>
      url === "/v1/session"
        ? json(200, session)
        : url === "/v1/applications"
          ? json(503, { title: "capability not ready", status: 503, detail: "the application platform is still being set up", instance: "req-123" })
          : json(200, []),
    );
    render(<App />);
    expect(await screen.findByText("Capability not ready")).toBeTruthy();
    expect(screen.getByText(/keeps retrying/)).toBeTruthy();
    expect(screen.getByText("req-123")).toBeTruthy();
  });

  it("says when Serendip can't be reached", async () => {
    goTo("/applications");
    mockFetch((url) => (url === "/v1/session" ? json(200, session) : Promise.reject(new TypeError("Failed to fetch"))));
    render(<App />);
    expect(await screen.findByText("Can't reach Serendip")).toBeTruthy();
  });
});

describe("create application", () => {
  it("sends exactly the request it shows", async () => {
    goTo("/applications/new");
    mockFetch((url, init) => {
      if (url === "/v1/session") return json(200, session);
      const created = { metadata: { id: "new-id", name: "storefront", resourceVersion: 1 }, spec: {}, status: {} };
      if (url === "/v1/applications" && init.method === "POST") return json(201, created);
      if (url === "/v1/applications/new-id") return json(200, created);
      return json(200, []);
    });
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "storefront" } });
    const shown = screen.getByText(/POST \/v1\/applications/).textContent;
    fireEvent.click(screen.getByRole("button", { name: "Create application" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.init.method === "POST")!;
    expect(shown).toBe(formatRequest({ method: "POST", path: "/v1/applications", body: JSON.parse(sent.init.body as string) }));
    expect(sent.init.headers).toMatchObject({ "X-Serendip-Csrf": "1" });
  });

  it("refuses an invalid name without sending anything", async () => {
    goTo("/applications/new");
    mockFetch((url) => (url === "/v1/session" ? json(200, session) : json(200, [])));
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Not Valid" } });
    fireEvent.click(screen.getByRole("button", { name: "Create application" }));
    expect(await screen.findByText(/Use lowercase letters/)).toBeTruthy();
    expect(calls.some((c) => c.init.method === "POST")).toBe(false);
  });
});

describe("no delete", () => {
  it("offers no delete action on a component", async () => {
    goTo("/applications/a1/components/c1");
    mockFetch((url) => {
      if (url === "/v1/session") return json(200, session);
      if (url.startsWith("/v1/components/")) return json(200, { metadata: { id: "c1", name: "web", resourceVersion: 1 }, spec: { image: "nginx" }, status: {} });
      if (url.startsWith("/v1/applications/")) return json(200, { metadata: { id: "a1", name: "storefront", resourceVersion: 1 }, spec: {}, status: {} });
      return json(200, []);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "web" });
    expect(screen.queryByRole("button", { name: /delete/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /delete/i })).toBeNull();
  });
});

function Poller({ onLoad }: { onLoad: (s: AbortSignal) => Promise<number> }) {
  const p = usePoll(onLoad, 1_000, "k");
  return <span>{p.data ?? "none"}</span>;
}

describe("usePoll", () => {
  it("stops polling while the tab is hidden and resumes when shown", async () => {
    vi.useFakeTimers();
    let hidden = false;
    vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
    const load = vi.fn(async () => 1);
    render(<Poller onLoad={load} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(load).toHaveBeenCalledTimes(1);
    hidden = true;
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(load).toHaveBeenCalledTimes(1);
    hidden = false;
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("aborts the request in flight when the screen goes away", async () => {
    let seen: AbortSignal | undefined;
    const load = (s: AbortSignal) => {
      seen = s;
      return new Promise<number>(() => {});
    };
    const { unmount } = render(<Poller onLoad={load} />);
    await waitFor(() => expect(seen).toBeDefined());
    unmount();
    expect(seen!.aborted).toBe(true);
  });
});
