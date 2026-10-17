import { describe, expect, it } from "vitest";
import type { Application, Component, Condition } from "../api/types";
import { toSpec, validate, fromSpec, type ComponentFormState } from "./componentForm";
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
  it("is done end to end when everything is serving", () => {
    const s = componentStages(comp([cond("Ready", "True", "Deployed"), cond("Reachable", "True", "Serving")], true, 1), app([cond("Ready", "True", "Deployed")]));
    expect(s.map((x) => [x.label, x.state])).toEqual([
      ["Saved", "done"],
      ["Application ready", "done"],
      ["Deployed", "done"],
      ["Reachable", "done"],
    ]);
    expect(s[3]!.detail).toBe("1 endpoint serving");
  });

  it("has no Reachable stage when nothing is exposed", () => {
    expect(componentStages(comp([], false), app([])).map((x) => x.label)).not.toContain("Reachable");
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

  it("marks a rejected route as failed", () => {
    const s = componentStages(comp([cond("Ready", "True", "Deployed"), cond("Reachable", "False", "RouteRejected")]), app([cond("Ready", "True", "Deployed")]));
    expect(s[3]!.state).toBe("failed");
  });

  it("says when the owning application is missing", () => {
    expect(componentStages(comp([]), undefined)[1]).toMatchObject({ state: "failed", detail: "Owning application not found" });
  });
});

describe("component form", () => {
  const base: ComponentFormState = { name: "web", image: "nginx:1.27", ports: [{ key: 1, name: "http", port: "80", expose: true }], env: [] };

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
