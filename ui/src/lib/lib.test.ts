import { describe, expect, it } from "vitest";
import type { Application, Component, Condition } from "../api/types";
import { emptySource, toSpec, validate, fromSpec, type ComponentFormState } from "./componentForm";
import { componentStages } from "./stages";
import { topologySentence } from "./topology";

const cond = (type: string, status: "True" | "False", reason: string): Condition => ({ type, status, reason, lastTransitionTime: "2026-10-17T00:00:00Z" });

function comp(conditions: Condition[], expose = true, endpoints = 0): Component {
  return {
    metadata: { id: "c1", name: "web", kind: "Component", createdAt: "", updatedAt: "", resourceVersion: 3, ownerRefs: [{ kind: "Application", id: "a1" }] },
    spec: { image: "nginx:1.27", ports: [{ name: "http", port: 80, expose }] },
    status: { conditions, endpoints: Array.from({ length: endpoints }, (_, i) => ({ name: `p${i}`, url: `http://x/${i}` })) },
  };
}
const app = (conditions: Condition[]): Application => ({ metadata: { id: "a1", name: "storefront", kind: "Application", createdAt: "", updatedAt: "", resourceVersion: 1 }, spec: {}, status: { conditions } });

describe("componentStages", () => {
  it("is done end to end once released", () => {
    const s = componentStages(comp([cond("Ready", "True", "Released")], true), app([cond("Ready", "True", "Deployed")]));
    expect(s.map((x) => [x.label, x.state])).toEqual([
      ["Saved", "done"],
      ["Application ready", "done"],
      ["Released", "done"],
    ]);
  });

  it("has no Reachable stage: reachability belongs to each Deployment (spec 020)", () => {
    expect(componentStages(comp([cond("Reachable", "False", "Pending")], true), app([])).map((x) => x.label)).not.toContain("Reachable");
  });

  it("never claims progress it has no Condition for", () => {
    const s = componentStages(comp([]), app([]));
    expect(s.slice(1).every((x) => x.state === "pending")).toBe(true);
  });

  it("shows waiting, not a failure, while the application isn't ready", () => {
    const s = componentStages(comp([cond("Ready", "False", "WaitingForApplication")]), app([cond("Ready", "False", "Progressing")]));
    expect(s[1]!.state).toBe("active");
    expect(s[2]!.state).toBe("pending");
  });

  it("says when the owning application is missing", () => {
    expect(componentStages(comp([]), undefined)[1]).toMatchObject({ state: "failed", detail: "Owning application not found" });
  });
});

describe("component form", () => {
  const base: ComponentFormState = { name: "web", from: "image", image: "nginx:1.27", source: emptySource, ports: [{ key: 1, name: "http", port: "80", expose: true }], env: [] };

  it("builds the spec the API expects", () => {
    expect(toSpec({ ...base, env: [{ key: 2, name: "LOG_LEVEL", value: "info" }, { key: 3, name: "", value: "" }] })).toEqual({
      image: "nginx:1.27",
      ports: [{ name: "http", port: 80, expose: true }],
      env: { LOG_LEVEL: "info" },
    });
  });

  it("round-trips through fromSpec", () => {
    const spec = { image: "nginx", ports: [{ name: "http", port: 80, expose: true }, { name: "metrics", port: 9113 }], env: { A: "1" } };
    expect(toSpec(fromSpec("web", spec))).toEqual(spec);
  });

  it.each([
    [{ name: "Web" }, "name"],
    [{ name: "" }, "name"],
    [{ image: " " }, "image"],
    [{ ports: [{ key: 1, name: "HTTP", port: "80", expose: false }] }, "port-0-name"],
    [{ ports: [{ key: 1, name: "http", port: "70000", expose: false }] }, "port-0-port"],
    [{ ports: [{ key: 1, name: "http", port: "8.5", expose: false }] }, "port-0-port"],
    [
      {
        ports: [
          { key: 1, name: "http", port: "80", expose: false },
          { key: 2, name: "http", port: "81", expose: false },
        ],
      },
      "port-1-name",
    ],
    [{ env: [{ key: 1, name: "1BAD", value: "x" }] }, "env-0-name"],
  ] as const)("rejects %o (%s)", (patch, field) => {
    expect(validate({ ...base, ...patch } as ComponentFormState, { creating: true })[field]).toBeTruthy();
  });

  const fromGit = (patch: Partial<ComponentFormState["source"]>): ComponentFormState => ({
    ...base,
    from: "source",
    source: { ...emptySource, repository: "https://github.com/org/app.git", ...patch },
  });

  it("sends a source with image empty and defaults left to the server", () => {
    expect(toSpec(fromGit({ credentialSecret: "s1", autoBuild: true }))).toEqual({
      image: "",
      source: { repository: "https://github.com/org/app.git", credentialSecret: "s1", autoBuild: true },
      ports: [{ name: "http", port: 80, expose: true }],
    });
    const spec = { image: "", source: { repository: "git@github.com:org/app.git", branch: "dev", path: "web", dockerfile: "Containerfile" } };
    expect(toSpec(fromSpec("web", spec))).toEqual(spec);
  });

  it.each([
    [{ repository: "" }, "repository"],
    [{ repository: "https://user:pw@github.com/org/app.git" }, "repository"],
    [{ repository: "https://github.com/org/app.git?x=1" }, "repository"],
    [{ repository: "http://github.com/org/app.git" }, "repository"],
    [{ branch: "a..b" }, "branch"],
    [{ branch: "-x" }, "branch"],
    [{ path: "../up" }, "path"],
    [{ path: "/abs" }, "path"],
    [{ dockerfile: "." }, "dockerfile"],
    [{ dockerfile: "dir/" }, "dockerfile"],
  ] as const)("rejects the source %o (%s)", (patch, field) => {
    expect(validate(fromGit(patch), { creating: true })[field]).toBeTruthy();
  });

  it("accepts a valid source, without an image", () => {
    expect(validate(fromGit({ branch: "release/1.2", path: "services/web/", dockerfile: "build/Dockerfile" }), { creating: true })).toEqual({});
    expect(validate(fromGit({ repository: "ssh://git@host.example:2222/org/app.git" }), { creating: true })).toEqual({});
  });

  it("accepts a valid form, and doesn't check the name when editing", () => {
    expect(validate(base, { creating: true })).toEqual({});
    expect(validate({ ...base, name: "" }, { creating: false })).toEqual({});
  });
});

describe("topologySentence", () => {
  it("never claims high availability", () => {
    expect(topologySentence(1)).toMatch(/Single node/);
    expect(topologySentence(0)).toMatch(/Single node/);
    expect(topologySentence(3)).not.toMatch(/highly available|HA enabled/i);
  });
});
