import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { formatRequest } from "../components/ApiEquivalent";
import { allows, matches, parseScopeParam, scopeParam } from "../lib/permissions";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let calls: Array<{ url: string; init: RequestInit }>;

function mockFetch(routes: Record<string, unknown>, fallback: (url: string, init: RequestInit) => Response = () => json(200, [])) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const key = `${init.method ?? "GET"} ${url}`;
      if (key in routes) return json(200, routes[key]);
      if (url in routes && (init.method ?? "GET") === "GET") return json(200, routes[url]);
      return fallback(url, init);
    }),
  );
}

const goTo = (path: string) => window.history.replaceState(null, "", path);

beforeEach(() => {
  goTo("/");
  vi.stubGlobal("scrollTo", () => {});
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const session = { actor: "person:pat", method: "session" };
const reader = { scope: "/", permissions: [{ actions: ["*/read"] }] };
const owner = { scope: "/", permissions: [{ actions: ["*"] }] };
const perms = (scope: string) => `/v1/authorization/permissions?scope=${encodeURIComponent(scope)}`;

const app = { metadata: { id: "app1", name: "storefront", kind: "Application", createdAt: "2026-10-18T00:00:00Z", updatedAt: "2026-10-18T00:00:00Z", resourceVersion: 1 }, spec: {}, status: {} };
const comp = {
  metadata: { id: "c1", name: "web", kind: "Component", ownerRefs: [{ kind: "Application", id: "app1" }], createdAt: "2026-10-18T00:00:00Z", updatedAt: "2026-10-18T00:00:00Z", resourceVersion: 1 },
  spec: { image: "nginx:1.27" },
  status: {},
};

const roles = [
  { metadata: { id: "r-owner", name: "Owner", kind: "RoleDefinition" }, spec: { roleName: "Owner", permissions: [{ actions: ["*"] }] }, status: {}, builtIn: true },
  { metadata: { id: "r-reader", name: "Reader", kind: "RoleDefinition" }, spec: { roleName: "Reader", permissions: [{ actions: ["*/read"] }] }, status: {}, builtIn: true },
  {
    metadata: { id: "r-secrets", name: "Secrets User", kind: "RoleDefinition" },
    spec: { roleName: "Secrets User", permissions: [{ actions: ["serendip/secrets/read"], dataActions: ["serendip/secrets/value/read"] }] },
    status: {},
    builtIn: true,
  },
];

describe("permission matching", () => {
  it("mirrors the server's wildcard rules", () => {
    expect(matches("*/read", "serendip/components/read")).toBe(true);
    expect(matches("serendip/*", "SERENDIP/Components/Write")).toBe(true);
    expect(matches("serendip/components/read", "serendip/components/write")).toBe(false);
    expect(matches("*/read", "serendip/components/write")).toBe(false);
  });

  it("subtracts notActions within a block and keeps data actions separate", () => {
    const contributor = [{ actions: ["*"], notActions: ["serendip/authorization/*/write"] }];
    expect(allows(contributor, "serendip/components/write")).toBe(true);
    expect(allows(contributor, "serendip/authorization/roleAssignments/write")).toBe(false);
    expect(allows([{ actions: ["*"] }], "serendip/secrets/value/read", true)).toBe(false);
    // Another block still grants what one block excludes: roles add up.
    expect(allows([...contributor, { actions: ["serendip/authorization/roleAssignments/write"] }], "serendip/authorization/roleAssignments/write")).toBe(true);
  });

  it("round-trips scopes", () => {
    expect(scopeParam({})).toBe("/");
    expect(scopeParam({ kind: "Component", id: "c1" })).toBe("Component/c1");
    expect(parseScopeParam("Component/c1")).toEqual({ kind: "Component", id: "c1" });
    expect(parseScopeParam("/")).toEqual({});
    expect(parseScopeParam("bad")).toBeUndefined();
    expect(parseScopeParam("a/b/c")).toBeUndefined();
  });
});

describe("permission-aware controls", () => {
  it("shows a Reader no create, edit or delete controls", async () => {
    mockFetch({ "/v1/session": session, [perms("/")]: reader, [perms("Application/app1")]: reader, [perms("Component/c1")]: reader, "/v1/applications": [app], "/v1/applications/app1": app, "/v1/components/c1": comp });
    goTo("/applications");
    render(<App />);
    await screen.findByRole("link", { name: "storefront" });
    await waitFor(() => expect(calls.some((c) => c.url === perms("/"))).toBe(true));
    expect(screen.queryByRole("link", { name: "Create application" })).toBeNull();
    cleanup();

    goTo("/applications/app1");
    render(<App />);
    await screen.findByRole("heading", { name: "storefront" });
    await waitFor(() => expect(calls.some((c) => c.url === perms("Application/app1"))).toBe(true));
    expect(screen.queryByRole("link", { name: "Create component" })).toBeNull();
    cleanup();

    goTo("/applications/app1/components/c1");
    render(<App />);
    await screen.findByRole("heading", { name: "web" });
    const edit = await screen.findByRole("button", { name: "Edit component" });
    await waitFor(() => expect(screen.getByText("Your roles here don't allow changing this component.")).toBeTruthy());
    expect((edit as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByRole("link", { name: "Edit component" })).toBeNull();
    // Delete is an action on what's on screen: shown disabled, with why
    // (ui/DESIGN.md, spec 019).
    const del = await screen.findByRole("button", { name: "Delete component" });
    expect((del as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Your roles here don't allow deleting this component.")).toBeTruthy();
  });

  it("offers an Owner the same controls", async () => {
    mockFetch({ "/v1/session": session, [perms("/")]: owner, [perms("Component/c1")]: owner, "/v1/applications": [app], "/v1/applications/app1": app, "/v1/components/c1": comp });
    goTo("/applications");
    render(<App />);
    expect(await screen.findByRole("link", { name: "Create application" })).toBeTruthy();
    cleanup();
    goTo("/applications/app1/components/c1");
    render(<App />);
    expect(await screen.findByRole("link", { name: "Edit component" })).toBeTruthy();
  });

  it("keeps controls disabled while access is unknown", async () => {
    mockFetch({ "/v1/session": session, "/v1/applications/app1": app, "/v1/components/c1": comp }, (url) =>
      url.startsWith("/v1/authorization/permissions") ? json(503, { title: "not ready", status: 503 }) : json(200, []),
    );
    goTo("/applications/app1/components/c1");
    render(<App />);
    expect(((await screen.findByRole("button", { name: "Edit component" })) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("access control", () => {
  const scope = "Application/app1";
  const assignments = [
    {
      metadata: { id: "ra1", name: "x", kind: "RoleAssignment" },
      spec: { principal: { type: "person", id: "sub-ann" }, roleDefinitionId: "r-reader", scope: { kind: "Application", id: "app1" } },
      status: {},
    },
    { metadata: { id: "ra2", name: "x", kind: "RoleAssignment" }, spec: { principal: { type: "group", id: "platform" }, roleDefinitionId: "r-owner", scope: {} }, status: {}, inherited: true },
    {
      metadata: { id: "ra3", name: "x", kind: "RoleAssignment" },
      spec: { principal: { type: "apiKey", id: "ci" }, roleDefinitionId: "r-secrets", scope: { kind: "Project", id: "p1" } },
      status: {},
      inherited: true,
    },
  ];
  const principals = [{ type: "person", id: "sub-ann", displayName: "ann", groups: ["platform"], firstSeen: "2026-10-18T00:00:00Z", lastSeen: "2026-10-18T00:00:00Z" }];
  const routes = (p: unknown) => ({
    "/v1/session": session,
    [perms(scope)]: p,
    [`/v1/authorization/roleAssignments?scope=${encodeURIComponent(scope)}`]: assignments,
    "/v1/authorization/roleDefinitions": roles,
    "/v1/authorization/principals": principals,
    "/v1/applications/app1": app,
  });

  it("labels inherited assignments with where they come from", async () => {
    mockFetch(routes(reader));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const table = (await screen.findAllByRole("table"))[0]!;
    const rows = within(table).getAllByRole("row").slice(1);
    expect(within(rows[0]!).getByText("This scope")).toBeTruthy();
    expect(within(rows[0]!).getByText("ann")).toBeTruthy();
    expect(within(rows[1]!).getByText("Inherited from the whole platform")).toBeTruthy();
    expect(within(rows[2]!).getByText("Inherited from Project p1")).toBeTruthy();
    expect(within(rows[2]!).getByText("Secrets User")).toBeTruthy();
    // A Reader can't assign, and nobody gets a remove control.
    expect(await screen.findByText("Your roles here don't include assigning roles.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Assign role" })).toBeNull();
    expect(screen.queryByRole("button", { name: /remove|delete/i })).toBeNull();
  });

  it("sends exactly the request it shows when adding an assignment", async () => {
    mockFetch(routes(owner), (url, init) => (init.method === "POST" && url === "/v1/authorization/roleAssignments" ? json(201, assignments[0]) : json(200, [])));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Add a role assignment" });
    fireEvent.change(within(form).getByLabelText("Person"), { target: { value: "sub-bob" } });
    await waitFor(() => expect(within(form).getByRole("option", { name: "Reader" })).toBeTruthy());
    fireEvent.change(within(form).getByLabelText("Role"), { target: { value: "r-reader" } });
    fireEvent.change(within(form).getByLabelText("Description"), { target: { value: "on call" } });
    const shown = within(form).getByText(/POST \/v1\/authorization\/roleAssignments/).textContent ?? "";
    fireEvent.click(within(form).getByRole("button", { name: "Assign role" }));
    await screen.findByText("Reader assigned to sub-bob.");

    const post = calls.find((c) => c.init.method === "POST");
    expect(post).toBeTruthy();
    const sent = JSON.parse(post!.init.body as string);
    expect(sent).toEqual({
      metadata: {},
      spec: { principal: { type: "person", id: "sub-bob" }, roleDefinitionId: "r-reader", scope: { kind: "Application", id: "app1" }, description: "on call" },
    });
    expect(shown).toBe(formatRequest({ method: "POST", path: "/v1/authorization/roleAssignments", body: sent }));
  });

  it("takes a person's name and sends their subject", async () => {
    mockFetch(routes(owner), (url, init) => (init.method === "POST" && url === "/v1/authorization/roleAssignments" ? json(201, assignments[0]) : json(200, [])));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Add a role assignment" });
    await screen.findAllByText("ann");
    // Suggestions lead with the name; the subject is the secondary label.
    const option = form.querySelector('datalist option[value="ann"]');
    expect(option?.getAttribute("label")).toBe("sub-ann");
    fireEvent.change(within(form).getByLabelText("Person"), { target: { value: "ann" } });
    expect(within(form).getByText("sub-ann", { selector: ".mono" })).toBeTruthy();
    await waitFor(() => expect(within(form).getByRole("option", { name: "Reader" })).toBeTruthy());
    fireEvent.change(within(form).getByLabelText("Role"), { target: { value: "r-reader" } });
    const shown = within(form).getByText((_, el) => el?.tagName === "PRE").textContent ?? "";
    expect(shown).toContain('"id": "sub-ann"');
    fireEvent.click(within(form).getByRole("button", { name: "Assign role" }));
    await screen.findByText("Reader assigned to ann.");
    const sent = JSON.parse(calls.find((c) => c.init.method === "POST")!.init.body as string);
    expect(sent.spec.principal).toEqual({ type: "person", id: "sub-ann" });
  });

  it("checks a person's access through their groups", async () => {
    mockFetch(routes(reader));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Check access" });
    await screen.findAllByText("ann");
    fireEvent.change(within(form).getByLabelText("Principal"), { target: { value: "ann" } });
    fireEvent.click(within(form).getByRole("button", { name: "Check access" }));
    expect(await screen.findByText("Group platform")).toBeTruthy();
    expect(screen.getByRole("status", { name: "" }).textContent).toMatch(/At their last sign-in \(.+\) they were in platform\./);
    expect(screen.getByText("Direct")).toBeTruthy();
  });

  it("lets an Owner remove an assignment made here, after confirming", async () => {
    mockFetch(routes(owner), (_url, init) => (init.method === "DELETE" ? new Response(null, { status: 204 }) : json(200, [])));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const table = (await screen.findAllByRole("table"))[0]!;
    await waitFor(() => expect(within(table).getAllByRole("button", { name: /^Remove/ })).toHaveLength(1));
    // Inherited assignments are removed where they were made.
    expect(within(table).queryByRole("button", { name: "Remove Owner from platform" })).toBeNull();
    fireEvent.click(within(table).getByRole("button", { name: "Remove Reader from ann" }));
    const confirm = screen.getByRole("group", { name: "Confirm removing a role assignment" });
    expect(within(confirm).getByText("DELETE /v1/authorization/roleAssignments/ra1")).toBeTruthy();
    expect(calls.some((c) => c.init.method === "DELETE")).toBe(false);
    fireEvent.click(within(confirm).getByRole("button", { name: "Remove assignment" }));
    await waitFor(() => expect(screen.queryByRole("group", { name: "Confirm removing a role assignment" })).toBeNull());
    const del = calls.filter((c) => c.init.method === "DELETE");
    expect(del.map((c) => c.url)).toEqual(["/v1/authorization/roleAssignments/ra1"]);
  });

  it("shows why the last Owner can't be removed", async () => {
    mockFetch(routes(owner), (_url, init) =>
      init.method === "DELETE" ? json(409, { title: "conflict", status: 409, detail: `policy: forbidden: the last Owner at "/" can't be removed` }) : json(200, []),
    );
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Remove Reader from ann" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove assignment" }));
    expect(await screen.findByText(/last Owner at "\/" can't be removed/)).toBeTruthy();
  });

  it("offers a Reader no remove control", async () => {
    mockFetch(routes(reader));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    await screen.findByText("Your roles here don't include assigning roles.");
    expect(screen.queryByRole("button", { name: /^Remove/ })).toBeNull();
  });

  const agentRoles = roles.map((r) => ({ ...r, assignableToAgents: r.metadata.id !== "r-owner" }));
  const withAgents = (p: unknown) => ({ ...routes(p), "/v1/authorization/roleDefinitions": agentRoles });

  it("assigns a role to an agent with a sponsor, offering only roles an agent may hold", async () => {
    mockFetch(withAgents(owner), (url, init) => (init.method === "POST" && url === "/v1/authorization/roleAssignments" ? json(201, assignments[0]) : json(200, [])));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Add a role assignment" });
    await screen.findAllByText("ann");
    fireEvent.change(within(form).getByLabelText("Principal type"), { target: { value: "agent" } });
    const roleSelect = within(form).getByLabelText("Role");
    expect(within(roleSelect).queryByRole("option", { name: "Owner" })).toBeNull();
    expect(within(roleSelect).getByRole("option", { name: "Reader" })).toBeTruthy();
    fireEvent.change(within(form).getByLabelText("Agent"), { target: { value: "am-dev/agent-1" } });
    // No sponsor: refused before anything is sent.
    fireEvent.click(within(form).getByRole("button", { name: "Assign role" }));
    expect(await within(form).findByText("An agent needs a sponsor: the person accountable for it.")).toBeTruthy();
    expect(calls.some((c) => c.init.method === "POST")).toBe(false);

    fireEvent.change(within(form).getByLabelText("Sponsor"), { target: { value: "ann" } });
    fireEvent.change(roleSelect, { target: { value: "r-reader" } });
    const shown = within(form).getByText((_, el) => el?.tagName === "PRE").textContent ?? "";
    fireEvent.click(within(form).getByRole("button", { name: "Assign role" }));
    await screen.findByText("Reader assigned to am-dev/agent-1.");
    const sent = JSON.parse(calls.find((c) => c.init.method === "POST")!.init.body as string);
    expect(sent.spec.principal).toEqual({ type: "agent", id: "am-dev/agent-1", sponsor: { type: "person", id: "sub-ann" } });
    expect(shown).toBe(formatRequest({ method: "POST", path: "/v1/authorization/roleAssignments", body: sent }));
  });

  it("suggests people from the identity provider, not only those who signed in", async () => {
    const q = (text: string) => `/v1/authorization/directory?type=person&q=${text}`;
    mockFetch({ ...routes(owner), [q("car")]: { source: "live", truncated: true, items: [{ type: "person", id: "sub-carol", displayName: "carol" }] } }, () =>
      json(200, { source: "live", truncated: false, items: [] }),
    );
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Add a role assignment" });
    fireEvent.change(within(form).getByLabelText("Person"), { target: { value: "car" } });
    await waitFor(() => expect(form.querySelector('datalist option[value="carol"]')?.getAttribute("label")).toBe("sub-carol"));
    expect(within(form).getByText("More match than are shown: type more of the name.")).toBeTruthy();
    fireEvent.change(within(form).getByLabelText("Person"), { target: { value: "carol" } });
    expect(within(form).getByText("sub-carol", { selector: ".mono" })).toBeTruthy();
  });

  it("says when suggestions only come from sign-ins", async () => {
    mockFetch(routes(owner), (url) =>
      url.startsWith("/v1/authorization/directory") ? json(200, { source: "signedIn", truncated: false, items: [{ type: "person", id: "sub-ann", displayName: "ann" }] }) : json(200, []),
    );
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Add a role assignment" });
    expect(await within(form).findByText(/identity provider can't be reached right now/)).toBeTruthy();
  });

  it("checks a person's access with their groups read live", async () => {
    mockFetch({ ...routes(reader), "/v1/authorization/principals/person/sub-ann/groups": { source: "live", groups: ["platform", "t018-readers"] } });
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Check access" });
    await screen.findAllByText("ann");
    fireEvent.change(within(form).getByLabelText("Principal"), { target: { value: "ann" } });
    fireEvent.click(within(form).getByRole("button", { name: "Check access" }));
    expect(await screen.findByText("Live from the identity provider: ann is in platform, t018-readers.")).toBeTruthy();
    expect(screen.getByText("Group platform")).toBeTruthy();
  });

  it("says when the identity provider has no such person", async () => {
    mockFetch(routes(reader), (url) => (url.endsWith("/groups") ? json(404, { title: "not found", status: 404 }) : json(200, [])));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Check access" });
    fireEvent.change(within(form).getByLabelText("Principal"), { target: { value: "sub-gone" } });
    fireEvent.click(within(form).getByRole("button", { name: "Check access" }));
    expect(await screen.findByText(/identity provider has no such person/)).toBeTruthy();
  });

  it("explains that agents have no groups", async () => {
    mockFetch(routes(reader));
    goTo(`/access?scope=${encodeURIComponent(scope)}`);
    render(<App />);
    const form = await screen.findByRole("form", { name: "Check access" });
    fireEvent.change(within(form).getByLabelText("Principal type"), { target: { value: "agent" } });
    fireEvent.change(within(form).getByLabelText("Principal"), { target: { value: "am-dev/agent-1" } });
    fireEvent.click(within(form).getByRole("button", { name: "Check access" }));
    expect(await screen.findByText(/Agents have no groups/)).toBeTruthy();
    // No live group lookup is made for an agent.
    expect(calls.some((c) => c.url.includes("/groups"))).toBe(false);
  });

  it("refuses a malformed scope", async () => {
    mockFetch({ "/v1/session": session });
    goTo("/access?scope=nonsense");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /not found|no such page/i })).toBeTruthy();
  });
});
