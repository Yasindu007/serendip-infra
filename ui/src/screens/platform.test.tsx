import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { formatRequest } from "../components/ApiEquivalent";

// Spec 020's console: the Deploy and Runtime tabs, the configuration form
// from schemas, topology, the Platform and Packages pages, and greyed-out
// Build and Observability sections.

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

let calls: Array<{ url: string; init: RequestInit }>;

type Perms = Array<{ actions: string[]; notActions?: string[] }>;
const owner: Perms = [{ actions: ["*"] }];
const reader: Perms = [{ actions: ["*/read"] }];

/** routes answer GETs by URL; permissions come from permsAt(scope); other may answer anything else. */
function mockFetch(routes: Record<string, unknown>, permsAt: (scope: string) => Perms = () => owner, other?: (url: string, init: RequestInit) => Response | undefined) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const method = init.method ?? "GET";
      const answer = other?.(url, init);
      if (answer) return answer;
      if (url.startsWith("/v1/authorization/permissions?scope=")) {
        const scope = decodeURIComponent(url.slice(url.indexOf("=") + 1));
        return json(200, { scope, permissions: permsAt(scope) });
      }
      if (method === "GET" && url in routes) return json(200, routes[url]);
      if (method === "GET" && url === "/v1/readiness") return json(200, {});
      return json(200, []);
    }),
  );
}

const goTo = (path: string) => window.history.replaceState(null, "", path);

/** The enabled button of that name: Guarded shows a disabled one while permissions load. */
const enabled = (name: string) =>
  waitFor(() => {
    const b = screen.getAllByRole("button", { name }).find((x) => !(x as HTMLButtonElement).disabled);
    if (!b) throw new Error(`no enabled ${name} button yet`);
    return b;
  });

beforeEach(() => {
  goTo("/");
  vi.stubGlobal("scrollTo", () => {});
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const at = "2026-10-23T10:00:00Z";
const meta = (id: string, name: string, kind: string, ownerRefs?: Array<{ kind: string; id: string }>) => ({
  id,
  name,
  kind,
  createdAt: at,
  updatedAt: at,
  resourceVersion: 4,
  ...(ownerRefs ? { ownerRefs } : {}),
});
const ready = [{ type: "Ready", status: "True", reason: "Deployed", lastTransitionTime: at }];

const project = { metadata: meta("p1", "shop", "Project"), spec: { pipeline: { paths: [{ from: "e1", to: ["e2"] }, { from: "e2", to: ["e3"] }] } }, status: {} };
const envs = [
  { metadata: meta("e3", "production", "Environment", [{ kind: "Project", id: "p1" }]), spec: {}, status: { availability: "single-instance" } },
  { metadata: meta("e1", "development", "Environment", [{ kind: "Project", id: "p1" }]), spec: {}, status: {} },
  { metadata: meta("e2", "staging", "Environment", [{ kind: "Project", id: "p1" }]), spec: {}, status: {} },
];
const app = { metadata: meta("app1", "storefront", "Application", [{ kind: "Project", id: "p1" }]), spec: {}, status: { conditions: ready } };
const comp = { metadata: meta("c1", "web", "Component", [{ kind: "Application", id: "app1" }]), spec: { image: "nginx:1.27" }, status: { conditions: ready } };
const devDeploy = {
  metadata: meta("d1", "web", "Deployment", [{ kind: "Environment", id: "e1" }]),
  spec: { component: "c1" },
  status: { conditions: ready, release: "web-3", history: ["web-2", "web-1"] },
};
const stgDeploy = {
  metadata: meta("d2", "web", "Deployment", [{ kind: "Environment", id: "e2" }]),
  spec: { component: "c1", release: "web-2", overrides: { env: { MODE: "staging" } } },
  status: { conditions: ready, release: "web-2" },
};
const otherDeploy = { ...devDeploy, metadata: meta("d9", "api", "Deployment", [{ kind: "Environment", id: "e1" }]), spec: { component: "c9" } };

const base = {
  "/v1/session": { actor: "person:pat", method: "session" },
  "/v1/applications/app1": app,
  "/v1/components/c1": comp,
  "/v1/projects/p1": project,
  "/v1/environments?ownerId=p1": envs,
  "/v1/deployments": [devDeploy, stgDeploy, otherDeploy],
};

describe("deploy tab", () => {
  it("draws the pipeline in order, with each Environment's release", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch(base);
    render(<App />);
    const pipeline = await screen.findByRole("list", { name: "Pipeline of shop" });
    const stages = within(pipeline).getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(stages).toEqual(["development", "staging", "production"]);
    expect(within(pipeline).getByText("web-3")).toBeTruthy();
    expect(within(pipeline).getByText("1 variable")).toBeTruthy();
    // Production takes releases only by promotion; new ones enter at development.
    expect(within(pipeline).getByText("Nothing runs here yet. Promote a release from staging.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Deploy", current: "page" })).toBeTruthy();
  });

  it("promotes with exactly the request it shows", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch(base, () => owner, (_url, init) => (init.method === "POST" ? json(200, stgDeploy) : undefined));
    render(<App />);
    fireEvent.click(await enabled("Promote to staging"));
    const confirm = await screen.findByRole("group", { name: "Promote to staging" });
    expect(within(confirm).getByText(/staging keeps its own overrides/)).toBeTruthy();
    const shown = within(confirm).getByText(/POST \/v1\/deployments\/d1\/promote/).textContent;
    fireEvent.click(within(confirm).getByRole("button", { name: "Promote to staging" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.init.method === "POST")!;
    expect(sent.url).toBe("/v1/deployments/d1/promote");
    expect(JSON.parse(sent.init.body as string)).toEqual({ to: "e2" });
    expect(shown).toBe(formatRequest({ method: "POST", path: sent.url, body: { to: "e2" } }));
  });

  it("rolls back to a release from history with a PUT", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch(base, () => owner, (_url, init) => (init.method === "PUT" ? json(200, devDeploy) : undefined));
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Roll back to"), { target: { value: "web-1" } });
    fireEvent.click(await enabled("Roll back"));
    const confirm = await screen.findByRole("group", { name: "Roll back development" });
    fireEvent.click(within(confirm).getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "PUT")).toBe(true));
    const sent = calls.find((c) => c.init.method === "PUT")!;
    expect(sent.url).toBe("/v1/deployments/d1");
    expect(JSON.parse(sent.init.body as string).spec.release).toBe("web-1");
    expect(JSON.parse(sent.init.body as string).metadata.resourceVersion).toBe(4);
  });

  it("undeploys through an approval", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch(base, () => owner, (url, init) =>
      init.method === "DELETE" ? json(204, undefined, { "Serendip-Approval-Id": "ap1" }) : url === "/v1/approvals/ap1" ? json(404, { title: "not found", status: 404 }) : undefined,
    );
    render(<App />);
    fireEvent.click(await enabled("Undeploy"));
    const confirm = await screen.findByRole("group", { name: "Confirm deleting web in development" });
    expect(within(confirm).getByText(/The component itself stays/)).toBeTruthy();
    await within(confirm).findByRole("table");
    fireEvent.click(within(confirm).getByRole("button", { name: "Delete deployment" }));
    await waitFor(() => expect(window.location.pathname).toBe("/approvals/ap1"));
    expect(calls.find((c) => c.init.method === "DELETE")!.url).toBe("/v1/deployments/d1");
  });

  it("shows a Reader every action disabled, with why", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch(base, () => reader);
    render(<App />);
    await screen.findByRole("list", { name: "Pipeline of shop" });
    await waitFor(() => expect(screen.getAllByText("Your roles here don't allow undeploying.").length).toBe(2));
    expect(screen.getAllByText("Your roles don't allow deploying to staging.").length).toBe(1);
    expect(screen.getAllByText("Restarting isn't available yet.").length).toBe(2);
    expect(screen.queryByRole("link", { name: "Configure" })).toBeNull();
    for (const b of screen.getAllByRole("button", { name: "Undeploy" })) expect((b as HTMLButtonElement).disabled).toBe(true);
  });

  it("says so when the application isn't in a Project", async () => {
    goTo("/applications/app1/components/c1?tab=deploy");
    mockFetch({ ...base, "/v1/applications/app1": { ...app, metadata: meta("app1", "storefront", "Application") } });
    render(<App />);
    expect(await screen.findByText("Not in a Project")).toBeTruthy();
  });
});

const schemas = {
  environment: { type: "object", properties: { replicas: { type: "integer", minimum: 1 }, logLevel: { type: "string", enum: ["debug", "info"] } } },
  traits: { autoscale: { type: "object", properties: { max: { type: "integer" } } } },
};

describe("configuration form", () => {
  const routes = { ...base, "/v1/deployments/d2": stgDeploy, "/v1/environments/e2": envs[2], "/v1/components/c1/schemas": schemas };

  it("is generated from the schemas, and refuses an invalid value without sending", async () => {
    goTo("/applications/app1/components/c1/deployments/d2/configure");
    mockFetch(routes);
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Configure staging" })).toBeTruthy();
    expect(screen.getByLabelText("logLevel (optional)").tagName).toBe("SELECT");
    expect(screen.getByLabelText("max (optional)")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("replicas (optional)"), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("At least 1.")).toBeTruthy();
    expect(calls.some((c) => c.init.method === "PUT")).toBe(false);
  });

  it("sends the overrides it shows", async () => {
    goTo("/applications/app1/components/c1/deployments/d2/configure");
    mockFetch(routes, () => owner, (_url, init) => (init.method === "PUT" ? json(200, stgDeploy) : undefined));
    render(<App />);
    fireEvent.change(await screen.findByLabelText("replicas (optional)"), { target: { value: "2" } });
    fireEvent.change(screen.getByLabelText("max (optional)"), { target: { value: "5" } });
    const shown = screen.getByText(/PUT \/v1\/deployments\/d2/).textContent;
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "PUT")).toBe(true));
    const sent = calls.find((c) => c.init.method === "PUT")!;
    const body = JSON.parse(sent.init.body as string);
    expect(body.spec.overrides).toEqual({ env: { MODE: "staging" }, config: { replicas: 2 }, traits: { autoscale: { max: 5 } } });
    expect(shown).toBe(formatRequest({ method: "PUT", path: "/v1/deployments/d2", body }));
    await waitFor(() => expect(window.location.search).toBe("?tab=deploy"));
  });
});

describe("runtime tab", () => {
  const runtime = {
    ...base,
    "/v1/deployments/d1/runtime": { objects: [{ kind: "Deployment", name: "web-dev-1a2b", health: "Healthy" }, { kind: "Pod", name: "web-dev-1a2b-x", health: "Degraded", message: "CrashLoopBackOff" }] },
    "/v1/deployments/d1/events": { events: [{ time: at, type: "Warning", reason: "BackOff", message: "Back-off restarting", objectKind: "Pod", objectName: "web-dev-1a2b-x", count: 3 }] },
    "/v1/deployments/d1/logs?tailLines=100": { lines: [{ time: at, instance: "web-dev-1a2b-x", line: "<script>alert(1)</script>" }], truncated: false },
  };

  it("shows the objects, events and logs, with log text as plain text", async () => {
    goTo("/applications/app1/components/c1?tab=runtime");
    mockFetch(runtime);
    render(<App />);
    expect(await screen.findByText("web-dev-1a2b")).toBeTruthy();
    expect(screen.getByText("Degraded")).toBeTruthy();
    expect(await screen.findByText("BackOff")).toBeTruthy();
    const log = await screen.findByRole("log", { name: "Log lines" });
    expect(within(log).getByText("<script>alert(1)</script>")).toBeTruthy();
    expect(document.querySelector("script")).toBeNull();
    expect(screen.getByLabelText("Environment")).toBeTruthy();
  });

  it("doesn't read logs without the Log Reader permission", async () => {
    goTo("/applications/app1/components/c1?tab=runtime");
    mockFetch(runtime, () => [{ actions: ["*/read"], notActions: ["serendip/components/logs/read"] }]);
    render(<App />);
    expect(await screen.findByText(/the Log Reader role/)).toBeTruthy();
    expect(await screen.findByText("web-dev-1a2b")).toBeTruthy();
    expect(calls.some((c) => c.url.includes("/logs"))).toBe(false);
  });

  it("follows the log stream and says how it ended", async () => {
    goTo("/applications/app1/components/c1?tab=runtime");
    const ndjson = [{ time: at, instance: "i1", line: "first" }, { time: at, instance: "i1", line: "second" }, { end: "idle" }].map((x) => JSON.stringify(x) + "\n").join("");
    mockFetch(runtime, () => owner, (url) =>
      url.includes("follow=true") ? new Response(new ReadableStream({ start: (c) => (c.enqueue(new TextEncoder().encode(ndjson)), c.close()) }), { status: 200, headers: { "Content-Type": "application/x-ndjson" } }) : undefined,
    );
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Follow" }));
    expect(await screen.findByText("second")).toBeTruthy();
    expect(await screen.findByText("The stream stopped after 5 minutes without a new line.")).toBeTruthy();
    const follow = calls.find((c) => c.url.includes("follow=true"))!;
    expect((follow.init.headers as Record<string, string>)["X-Serendip-Csrf"]).toBe("1");
  });
});

describe("log follow reconnects", () => {
  it("resumes after the server's maximum duration without repeating lines", async () => {
    goTo("/applications/app1/components/c1?tab=runtime");
    const stream = (items: unknown[]) =>
      new Response(new ReadableStream({ start: (c) => (c.enqueue(new TextEncoder().encode(items.map((x) => JSON.stringify(x) + "\n").join(""))), c.close()) }), {
        status: 200,
        headers: { "Content-Type": "application/x-ndjson" },
      });
    const a = { time: at, instance: "i1", line: "line a" };
    const b = { time: "2026-10-23T10:00:01Z", instance: "i1", line: "line b" };
    let n = 0;
    mockFetch(
      {
        ...base,
        "/v1/deployments/d1/runtime": { objects: [] },
        "/v1/deployments/d1/events": { events: [] },
        "/v1/deployments/d1/logs?tailLines=100": { lines: [], truncated: false },
      },
      () => owner,
      (url) => (url.includes("follow=true") ? (n++ === 0 ? stream([a, { end: "maxDuration" }]) : stream([a, b, { end: "ended" }])) : undefined),
    );
    render(<App />);
    fireEvent.click(await enabled("Follow"));
    expect(await screen.findByText("line b")).toBeTruthy();
    expect(screen.getAllByText("line a").length).toBe(1);
    expect(await screen.findByText("The instances stopped writing.")).toBeTruthy();
    const follows = calls.filter((c) => c.url.includes("follow=true")).map((c) => c.url);
    expect(follows.length).toBe(2);
    expect(follows[1]).toMatch(/sinceSeconds=\d+/);
  });
});

describe("unavailable features", () => {
  const packages = { packages: [{ provider: "openchoreo-workflow-plane", capability: "build", description: "Builds images", available: false }] };
  const withBuild = (reason: string, isReady = false) => (url: string) =>
    url === "/v1/readiness" ? json(200, { build: { ready: isReady, reason, since: at } }) : url === "/v1/packages" ? json(200, packages) : undefined;

  it.each([
    ["Unavailable", "Not available yet", /isn't available in this version/],
    ["NotInstalled", "Not installed", /which isn't installed/],
    ["Installing", "Installing", /is installing/],
    ["InstallFailed", "Not ready", /installed but not ready/],
  ])("greys out Builds when the package reads %s", async (reason, word, why) => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(base, () => owner, withBuild(reason));
    render(<App />);
    expect(await screen.findByText(word)).toBeTruthy();
    expect(screen.getByText(why).textContent).toContain("openchoreo-workflow-plane");
  });
});

describe("packages page", () => {
  const pkgs = {
    packages: [
      { provider: "openchoreo-observability-plane", capability: "observability", description: "Stored logs; heavy on a 16 GB machine", available: false },
      { provider: "test-pkg", capability: "build", description: "A test package", available: true },
    ],
  };

  it("states each package's cost and offers install only where it can work", async () => {
    goTo("/packages");
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs }, () => owner, (_url, init) =>
      init.method === "POST" ? json(201, { metadata: meta("pi1", "test-pkg", "ProviderInstance"), spec: {}, status: {} }) : undefined,
    );
    render(<App />);
    expect(await screen.findByText("Stored logs; heavy on a 16 GB machine")).toBeTruthy();
    expect(screen.getByText("Not measured yet: it can't be installed in this version.")).toBeTruthy();
    expect(screen.getByText("Not available in this version of Serendip yet.")).toBeTruthy();
    fireEvent.click(await enabled("Install package"));
    const confirm = await screen.findByRole("group", { name: "Install test-pkg" });
    fireEvent.click(within(confirm).getByRole("button", { name: "Install package" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.init.method === "POST")!;
    expect(sent.url).toBe("/v1/provider-instances");
    expect(JSON.parse(sent.init.body as string)).toEqual({ metadata: { name: "test-pkg" }, spec: { capabilityId: "build", providerId: "test-pkg" } });
  });

  it("removes an installed package through an approval, saying what's lost", async () => {
    goTo("/packages");
    const inst = { metadata: meta("pi1", "test-pkg", "ProviderInstance"), spec: { capabilityId: "build", providerId: "test-pkg" }, status: {} };
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs, "/v1/provider-instances": [inst], "/v1/provider-instances/pi1": inst });
    render(<App />);
    fireEvent.click(await enabled("Remove package"));
    const confirm = await screen.findByRole("group", { name: "Confirm deleting test-pkg" });
    expect(within(confirm).getByText(/deletes what it stored/)).toBeTruthy();
    expect(within(confirm).getByText(/DELETE \/v1\/provider-instances\/pi1/)).toBeTruthy();
  });
});

describe("platform and topology", () => {
  it("shows where workloads run, the types and traits, and each Project's pipeline", async () => {
    goTo("/platform");
    mockFetch({
      "/v1/session": base["/v1/session"],
      "/v1/platform": {
        runtimes: [{ name: "default", ready: true }],
        componentTypes: [{ name: "deployment/serendip-service", environment: schemas.environment, allowedTraits: ["autoscale"] }],
        traits: [],
      },
      "/v1/projects": [project],
      "/v1/environments": envs,
    });
    render(<App />);
    expect(await screen.findByText("deployment/serendip-service")).toBeTruthy();
    expect(screen.getByText("replicas (optional), logLevel (optional)")).toBeTruthy();
    expect(screen.getByText("No traits are installed.")).toBeTruthy();
    const rows = (await screen.findAllByRole("row")).map((r) => r.textContent);
    expect(rows).toContain("developmentstagingNot reported");
    expect(rows).toContain("productionNothing (last)Single instance");
    expect(screen.getByRole("link", { name: "Platform", current: "page" })).toBeTruthy();
  });

  it("draws an Application's declared calls, with the same facts in a table", async () => {
    goTo("/applications/app1/topology");
    mockFetch({
      ...base,
      "/v1/applications/app1/topology": {
        components: [
          { id: "c1", name: "web", ports: [] },
          { id: "c2", name: "api", ports: [{ name: "http", port: 8080 }] },
        ],
        dependencies: [{ from: "c1", to: "c2", port: "http", env: "API_URL" }],
      },
    });
    render(<App />);
    expect(await screen.findByRole("img", { name: /2 components and 1 declared calls/ })).toBeTruthy();
    expect(screen.getByText("API_URL")).toBeTruthy();
    expect(screen.getByRole("link", { name: "api" }).getAttribute("href")).toBe("/applications/app1/components/c2");
    expect(screen.getByText(/not traffic that was observed/)).toBeTruthy();
  });
});
