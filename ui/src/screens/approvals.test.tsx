import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { formatRequest } from "../components/ApiEquivalent";

// Spec 019 T010: deleting through approvals, the Secrets page and the
// Approvals page.

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

let calls: Array<{ url: string; init: RequestInit }>;

type Handler = (url: string, init: RequestInit) => Response | undefined;

function mockFetch(routes: Record<string, unknown>, handler: Handler = () => undefined) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const handled = handler(url, init);
      if (handled) return handled;
      if (url in routes && (init.method ?? "GET") === "GET") return json(200, routes[url]);
      return json(200, []);
    }),
  );
}

const goTo = (path: string) => window.history.replaceState(null, "", path);
const perms = (scope: string) => `/v1/authorization/permissions?scope=${encodeURIComponent(scope)}`;
const owner = { scope: "/", permissions: [{ actions: ["*"] }] };
const reader = { scope: "/", permissions: [{ actions: ["*/read"] }] };
const meta = (id: string, name: string, kind: string, ownerRefs?: Array<{ kind: string; id: string }>) => ({
  id,
  name,
  kind,
  ...(ownerRefs ? { ownerRefs } : {}),
  createdAt: "2026-10-19T00:00:00Z",
  updatedAt: "2026-10-19T00:00:00Z",
  resourceVersion: 1,
});

const app = { metadata: meta("app1", "storefront", "Application"), spec: {}, status: {} };
const comp = { metadata: meta("c1", "web", "Component", [{ kind: "Application", id: "app1" }]), spec: { image: "nginx:1.27" }, status: {} };
const secret = {
  metadata: meta("s1", "db-password", "Secret", [{ kind: "Application", id: "app1" }]),
  spec: { path: "storefront/db" },
  status: { conditions: [{ type: "Reachable", status: "True", reason: "ValueExists", lastTransitionTime: "2026-10-19T00:00:00Z" }] },
};

const pendingApproval = {
  metadata: meta("ap1", "delete Component/web", "Approval"),
  spec: { action: { verb: "delete", target: { kind: "Component", id: "c1" } }, reason: "retired" },
  status: {
    phase: "Pending",
    requestedBy: { actor: "agent:am-dev/agent-1", principal: { type: "agent", id: "am-dev/agent-1" }, sponsor: "sub-owner" },
    requestedAt: "2026-10-19T00:00:00Z",
    expiresAt: "2026-10-20T00:00:00Z",
    targetName: "web",
    blastRadius: [{ kind: "Component", id: "c1", name: "web" }],
    conditions: [{ type: "Decided", status: "False", reason: "AwaitingDecision", lastTransitionTime: "2026-10-19T00:00:00Z" }],
  },
};

beforeEach(() => {
  goTo("/");
  vi.stubGlobal("scrollTo", () => {});
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("deleting through an approval", () => {
  it("confirms a component's delete with what it removes and the request, then shows its approval", async () => {
    goTo("/applications/app1/components/c1");
    mockFetch(
      { "/v1/session": { actor: "person:pat", method: "session" }, [perms("Component/c1")]: owner, "/v1/components/c1": comp, "/v1/applications/app1": app },
      (url, init) => (init.method === "DELETE" ? json(204, undefined, { "Serendip-Approval-Id": "ap9" }) : url === "/v1/approvals/ap9" ? json(404, { title: "resource not found", status: 404 }) : undefined),
    );
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete component" }));

    const confirm = await screen.findByRole("group", { name: "Confirm deleting web" });
    const table = await within(confirm).findByRole("table");
    expect(within(table).getByText("web")).toBeTruthy();
    expect(within(confirm).getByText(/recorded as an approval/)).toBeTruthy();
    expect(within(confirm).getByText("DELETE /v1/components/c1")).toBeTruthy();
    expect(calls.some((c) => c.init.method === "DELETE")).toBe(false); // nothing until confirmed

    fireEvent.click(within(confirm).getByRole("button", { name: "Delete component" }));
    await waitFor(() => expect(window.location.pathname).toBe("/approvals/ap9"));
    const del = calls.filter((c) => c.init.method === "DELETE");
    expect(del.map((c) => c.url)).toEqual(["/v1/components/c1"]);
  });

  it("lists everything an application's cascade removes, and says Secrets' values are erased", async () => {
    goTo("/applications/app1");
    mockFetch(
      {
        "/v1/session": { actor: "person:pat", method: "session" },
        [perms("Application/app1")]: owner,
        "/v1/applications/app1": app,
        "/v1/components/c1": comp,
        "/v1/secrets/s1": secret,
        "/v1/graph/Application/app1/children": [
          { Kind: "Component", ID: "c1" },
          { Kind: "Secret", ID: "s1" },
        ],
      },
      () => undefined,
    );
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete application" }));
    const confirm = await screen.findByRole("group", { name: "Confirm deleting storefront" });
    const table = await within(confirm).findByRole("table");
    for (const name of ["storefront", "web", "db-password"]) expect(within(table).getByText(name)).toBeTruthy();
    expect(within(confirm).getByText(/value is erased from the secrets store/)).toBeTruthy();
    expect(within(confirm).getByText("DELETE /v1/applications/app1?cascade=true")).toBeTruthy();
  });

  it("shows a Reader the delete disabled, with the reason", async () => {
    goTo("/applications/app1/components/c1");
    mockFetch({ "/v1/session": { actor: "person:pat", method: "session" }, [perms("Component/c1")]: reader, "/v1/components/c1": comp, "/v1/applications/app1": app });
    render(<App />);
    const button = await screen.findByRole("button", { name: "Delete component" });
    await waitFor(() => expect(screen.getByText("Your roles here don't allow deleting this component.")).toBeTruthy());
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("secrets", () => {
  it("lists secrets with their owner and value state, and never reads a value", async () => {
    goTo("/secrets");
    mockFetch({ "/v1/session": { actor: "person:pat", method: "session" }, "/v1/secrets": [secret], "/v1/applications": [app] });
    render(<App />);
    expect(await screen.findByRole("link", { name: "db-password" })).toBeTruthy();
    expect(screen.getByText("storefront/db")).toBeTruthy();
    expect(await screen.findByRole("link", { name: "storefront" })).toBeTruthy();
    expect(screen.getByText("Value stored")).toBeTruthy();
    expect(calls.some((c) => c.url.includes("/value"))).toBe(false);
  });

  it("deletes a secret from its page, never reading its value", async () => {
    goTo("/secrets/s1");
    mockFetch(
      { "/v1/session": { actor: "person:pat", method: "session" }, [perms("Secret/s1")]: owner, "/v1/secrets/s1": secret, "/v1/applications": [app] },
      (_url, init) => (init.method === "DELETE" ? json(204, undefined, { "Serendip-Approval-Id": "ap7" }) : undefined),
    );
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete secret" }));
    const confirm = await screen.findByRole("group", { name: "Confirm deleting db-password" });
    await within(confirm).findByRole("table");
    expect(within(confirm).getByText(/value is erased/)).toBeTruthy();
    fireEvent.click(within(confirm).getByRole("button", { name: "Delete secret" }));
    await waitFor(() => expect(window.location.pathname).toBe("/approvals/ap7"));
    expect(calls.some((c) => c.url.includes("/value"))).toBe(false);
  });
});

describe("approvals", () => {
  it("lists what's waiting first, and says so in the rail", async () => {
    goTo("/approvals");
    mockFetch(
      { "/v1/session": { actor: "person:pat", method: "session" } },
      (url) => (url.startsWith("/v1/approvals?phase=Pending") ? json(200, [pendingApproval]) : undefined),
    );
    render(<App />);
    const waiting = await screen.findByRole("region", { name: "Waiting for a decision" }).catch(() => screen.findByText("Waiting for a decision"));
    expect(waiting).toBeTruthy();
    expect(await screen.findByRole("link", { name: "Delete component web" })).toBeTruthy();
    expect(screen.getByText("retired")).toBeTruthy();
    expect(await screen.findByRole("link", { name: "Approvals (1 waiting)" })).toBeTruthy();
  });

  it("lets a person allowed the delete approve, with a comment, sending what it shows", async () => {
    goTo("/approvals/ap1");
    mockFetch(
      { "/v1/session": { actor: "person:pat", method: "session" }, [perms("Component/c1")]: owner, "/v1/approvals/ap1": pendingApproval },
      (_url, init) => (init.method === "POST" ? json(200, { ...pendingApproval, status: { ...pendingApproval.status, phase: "Approved" } }) : undefined),
    );
    render(<App />);
    expect(await screen.findByText("Sponsor")).toBeTruthy();
    // Disabled until the caller's access is known, then offered.
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    const confirm = await screen.findByRole("group", { name: "Confirm approving" });
    fireEvent.change(within(confirm).getByLabelText("Comment"), { target: { value: "checked with the team" } });
    const shown = within(confirm).getByText(/POST \/v1\/approvals\/ap1\/approve/).textContent;
    fireEvent.click(within(confirm).getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const post = calls.find((c) => c.init.method === "POST")!;
    expect(post.url).toBe("/v1/approvals/ap1/approve");
    expect(JSON.parse(post.init.body as string)).toEqual({ comment: "checked with the team" });
    expect(shown).toBe(formatRequest({ method: "POST", path: "/v1/approvals/ap1/approve", body: { comment: "checked with the team" } }));
  });

  it("never offers the requester their own decision", async () => {
    goTo("/approvals/ap1");
    mockFetch({ "/v1/session": { actor: "agent:am-dev/agent-1", method: "bearer" }, [perms("Component/c1")]: owner, "/v1/approvals/ap1": pendingApproval });
    render(<App />);
    const approve = await screen.findByRole("button", { name: "Approve" });
    await waitFor(() => expect(screen.getAllByText("You asked for this, so someone else decides it.").length).toBeGreaterThan(0));
    expect((approve as HTMLButtonElement).disabled).toBe(true);
  });
});
