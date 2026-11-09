import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

  it("shows the newest events first, and the rest on request", async () => {
    goTo("/applications/app1/components/c1?tab=runtime");
    const many = Array.from({ length: 14 }, (_, i) => ({ time: at, type: "Normal", reason: `Reason${i}`, message: "m", objectKind: "Pod", objectName: "p" }));
    mockFetch({ ...runtime, "/v1/deployments/d1/events": { events: many } });
    render(<App />);
    expect(await screen.findByText("Reason9")).toBeTruthy();
    expect(screen.queryByText("Reason10")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show all 14 events" }));
    expect(await screen.findByText("Reason13")).toBeTruthy();
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

describe("builds tab", () => {
  const source = { repository: "https://github.com/org/web.git", autoBuild: true };
  const srcComp = {
    ...comp,
    spec: { image: "", source },
    status: { conditions: [...ready, { type: "AutoBuild", status: "False", reason: "Unreachable", message: "auto-build can't reach the repository; retrying", lastTransitionTime: at }] },
  };
  const commit = "a".repeat(40);
  const running = {
    metadata: meta("b2", "build-20261023-100500", "Build", [{ kind: "Component", id: "c1" }]),
    spec: {},
    status: { phase: "Running", started: true, commit, startedAt: at, steps: [{ name: "checkout", phase: "Succeeded" }, { name: "build", phase: "Running" }] },
  };
  const old = {
    metadata: meta("b1", "build-20261023-090000", "Build", [{ kind: "Component", id: "c1" }]),
    spec: {},
    status: { phase: "Succeeded", started: true, commit: "b".repeat(40), startedAt: at, finishedAt: "2026-10-23T10:01:20Z", image: "localhost:5000/serendip/cmp-x@sha256:1" },
  };
  const readyBuild = (url: string) => (url === "/v1/readiness" ? json(200, { build: { ready: true, reason: "Ready", since: at } }) : undefined);
  const routes = (builds: unknown[]) => ({
    ...base,
    "/v1/components/c1": srcComp,
    "/v1/components/c1/builds": builds,
    "/v1/builds/b2/events": { events: [{ time: at, type: "Normal", reason: "Started", objectKind: "Pod", objectName: "build-x", message: "Started container" }] },
    "/v1/builds/b2/logs?tailLines=100": { lines: [{ time: at, instance: "build", line: "STEP 1/3: FROM alpine" }], truncated: false },
  });

  it("lists runs newest first with their state, commit and duration, and the newest's logs and events", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(routes([running, old]), () => owner, readyBuild);
    render(<App />);
    expect(await screen.findByText("STEP 1/3: FROM alpine")).toBeTruthy();
    const rows = screen.getAllByRole("row").filter((r) => r.textContent?.includes("build-2026"));
    expect(rows[0]!.textContent).toContain("Running");
    expect(rows[0]!.textContent).toContain(commit.slice(0, 12));
    expect(rows[1]!.textContent).toContain("Succeeded");
    expect(rows[1]!.textContent).toContain("1 min 20 s");
    expect(await screen.findByText("Started container")).toBeTruthy();
    expect(screen.getByText(/auto-build can't reach the repository/)).toBeTruthy();
  });

  it("reads the logs again when the build's phase changes", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      goTo("/applications/app1/components/c1?tab=builds");
      const r: Record<string, unknown> = routes([running]);
      mockFetch(r, () => owner, readyBuild);
      render(<App />);
      expect(await screen.findByText("STEP 1/3: FROM alpine")).toBeTruthy();
      r["/v1/components/c1/builds"] = [{ ...running, status: { ...running.status, phase: "Succeeded", finishedAt: at } }];
      r["/v1/builds/b2/logs?tailLines=100"] = { lines: [{ time: at, instance: "build", line: "pushed the image" }], truncated: false };
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_500);
      });
      expect(await screen.findByText("pushed the image")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("starts a build at a commit with exactly the request it shows, and refuses a short one", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(routes([]), () => owner, (url, init) => (init.method === "POST" ? json(201, { ...running, spec: { commit } }) : readyBuild(url)));
    render(<App />);
    const field = await screen.findByLabelText("Commit (optional)");
    fireEvent.change(field, { target: { value: "abc123" } });
    expect(screen.getByText(/Enter the full commit ID/)).toBeTruthy();
    fireEvent.click(await enabled("Start build"));
    expect(calls.some((c) => c.init.method === "POST")).toBe(false);
    fireEvent.change(field, { target: { value: commit } });
    expect(screen.getAllByText(`POST /v1/components/c1/builds`, { exact: false }).length).toBeGreaterThan(0);
    fireEvent.click(await enabled("Start build"));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.init.method === "POST")!;
    expect(sent.url).toBe("/v1/components/c1/builds");
    expect(JSON.parse(sent.init.body as string)).toEqual({ commit });
  });

  it("cancels a running build", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(routes([running]), () => owner, (url, init) => (init.method === "POST" ? json(200, { ...running, spec: { cancel: true } }) : readyBuild(url)));
    render(<App />);
    fireEvent.click(await enabled("Cancel build"));
    const confirm = await screen.findByRole("group", { name: `Cancel ${running.metadata.name}` });
    fireEvent.click(within(confirm).getByRole("button", { name: "Cancel build" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST" && c.url === "/v1/builds/b2/cancel")).toBe(true));
  });

  it("says logs expired for an old run, rather than an error", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(routes([old]), () => owner, (url) =>
      url.startsWith("/v1/builds/b1/") ? json(410, { title: "logs expired", detail: "expired", status: 410 }) : readyBuild(url),
    );
    render(<App />);
    expect((await screen.findAllByText(/Logs expired: a build's logs and events are kept for an hour/)).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Follow" })).toBeNull();
  });

  it("shows runs to a Reader but not their logs", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(routes([running]), () => [{ actions: ["*/read"], notActions: ["serendip/components/logs/read"] }], readyBuild);
    render(<App />);
    expect(await screen.findByText(/the Log Reader role/)).toBeTruthy();
    expect(screen.getAllByText("build-20261023-100500").length).toBeGreaterThan(0);
    expect(calls.some((c) => c.url.includes("/logs"))).toBe(false);
  });

  it("says an image component has no builds", async () => {
    goTo("/applications/app1/components/c1?tab=builds");
    mockFetch(base, () => owner, readyBuild);
    render(<App />);
    expect(await screen.findByText(/runs a ready-made image, so it has no builds/)).toBeTruthy();
  });
});

describe("component source form", () => {
  it("sends a source Component with image empty, and warns while the package isn't ready", async () => {
    goTo("/applications/app1/components/new");
    const secret = { metadata: meta("s1", "git-token", "Secret"), spec: { path: "git" }, status: {} };
    mockFetch({ ...base, "/v1/secrets": [secret] }, () => owner, (url, init) =>
      init.method === "POST" ? json(201, comp) : url === "/v1/readiness" ? json(200, { build: { ready: false, reason: "NotInstalled", since: at } }) : undefined,
    );
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "web" } });
    fireEvent.click(screen.getByLabelText("Built from source in Git"));
    expect(await screen.findByText(/Serendip refuses a new or changed source until it's ready/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/org/web.git" } });
    await screen.findByRole("option", { name: "git-token" });
    fireEvent.change(screen.getByLabelText("Credential"), { target: { value: "s1" } });
    fireEvent.click(screen.getByLabelText(/Auto-build/));
    fireEvent.click(screen.getByRole("button", { name: "Create component" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = JSON.parse(calls.find((c) => c.init.method === "POST")!.init.body as string);
    expect(sent.spec.image).toBe("");
    expect(sent.spec.source).toEqual({ repository: "https://github.com/org/web.git", credentialSecret: "s1", autoBuild: true });
  });
});

describe("packages page", () => {
  const requirements = {
    idleMemoryMiB: 46,
    idleCPUMillicores: 20,
    peakMemoryMiB: 900,
    peakCPUMillicores: 1800,
    measuredOn: "simulated T470 (2 cores, 16 GB), 2026-10-30",
    results: "profiles/edge-1/bench/results/2026-10-30-spec022-simulated-t470",
    note: "Fits beside edge-1; a build slows everything else for its duration.",
  };
  const removal = "Build history stays, but run logs and the stored images built from source are deleted.";
  const pkgs = {
    packages: [
      { provider: "openchoreo-observability-plane", capability: "observability", description: "Stored logs; heavy on a 16 GB machine", available: false, requirements: null, installable: false, notInstallable: "package openchoreo-observability-plane isn't available in this build yet" },
      { provider: "test-pkg", capability: "build", description: "A test package", available: true, requirements, removal, installable: true },
    ],
  };

  it("states the measured cost, and 'not measured' with no number otherwise", async () => {
    goTo("/packages");
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs });
    render(<App />);
    const measured = (await screen.findByText("A test package")).closest("section")!;
    expect(within(measured).getByText("Idle: 46 MiB of memory and 0.02 CPU cores.")).toBeTruthy();
    expect(within(measured).getByText("While a build runs: up to 900 MiB of memory and 1.8 CPU cores.")).toBeTruthy();
    expect(within(measured).getByText(/Measured on simulated T470 .*2026-10-30-spec022-simulated-t470/)).toBeTruthy();
    expect(within(measured).getByText(requirements.note)).toBeTruthy();
    const unmeasured = screen.getByText("Stored logs; heavy on a 16 GB machine").closest("section")!;
    expect(within(unmeasured).getByText(/^Not measured\./)).toBeTruthy();
    // No number for an unmeasured package: no MiB, no cores, no millicores.
    expect(unmeasured.textContent).not.toMatch(/\d+\s*(MiB|GiB|CPU cores|millicores|m CPU)/);
    expect(within(unmeasured).queryByText(/^Idle:/)).toBeNull();
  });

  it("offers install only where it can work, restating the cost before the request", async () => {
    goTo("/packages");
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs }, () => owner, (_url, init) =>
      init.method === "POST" ? json(201, { metadata: meta("pi1", "test-pkg", "ProviderInstance"), spec: {}, status: {} }) : undefined,
    );
    render(<App />);
    expect(await screen.findByText("Stored logs; heavy on a 16 GB machine")).toBeTruthy();
    expect(screen.getByText("Not available in this version of Serendip yet.")).toBeTruthy();
    fireEvent.click(await enabled("Install package"));
    const confirm = await screen.findByRole("group", { name: "Install test-pkg" });
    expect(within(confirm).getByText("Idle: 46 MiB of memory and 0.02 CPU cores.")).toBeTruthy();
    fireEvent.click(within(confirm).getByRole("button", { name: "Install package" }));
    await waitFor(() => expect(calls.some((c) => c.init.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.init.method === "POST")!;
    expect(sent.url).toBe("/v1/provider-instances");
    expect(JSON.parse(sent.init.body as string)).toEqual({ metadata: { name: "test-pkg" }, spec: { capabilityId: "build", providerId: "test-pkg" } });
  });

  it("refuses install, saying why, for an available package whose cost isn't measured", async () => {
    goTo("/packages");
    const unmeasured = {
      packages: [{ provider: "test-pkg", capability: "build", description: "A test package", available: true, requirements: null, installable: false, notInstallable: "package test-pkg can't be installed until its resource cost is measured" }],
    };
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": unmeasured });
    render(<App />);
    expect(await screen.findByText("Package test-pkg can't be installed until its resource cost is measured.")).toBeTruthy();
    expect(screen.queryAllByRole("button", { name: "Install package" }).every((b) => (b as HTMLButtonElement).disabled)).toBe(true);
  });

  it("doesn't offer installing to someone who may not", async () => {
    goTo("/packages");
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs }, () => reader);
    render(<App />);
    expect(await screen.findByText("A test package")).toBeTruthy();
    await waitFor(() => expect(calls.some((c) => c.url.startsWith("/v1/authorization/permissions"))).toBe(true));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryAllByRole("button", { name: "Install package" }).filter((b) => !(b as HTMLButtonElement).disabled)).toHaveLength(0);
  });

  it("removes an installed package through an approval, saying what's lost", async () => {
    goTo("/packages");
    const inst = { metadata: meta("pi1", "test-pkg", "ProviderInstance"), spec: { capabilityId: "build", providerId: "test-pkg" }, status: {} };
    mockFetch({ "/v1/session": base["/v1/session"], "/v1/packages": pkgs, "/v1/provider-instances": [inst], "/v1/provider-instances/pi1": inst });
    render(<App />);
    fireEvent.click(await enabled("Remove package"));
    const confirm = await screen.findByRole("group", { name: "Confirm deleting test-pkg" });
    expect(within(confirm).getByText(removal)).toBeTruthy();
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
