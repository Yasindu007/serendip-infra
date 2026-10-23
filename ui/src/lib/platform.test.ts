import { describe, expect, it } from "vitest";
import type { Environment, JSONSchema, Pipeline, TopologyView } from "../api/types";
import { layers } from "../components/TopologyDiagram";
import { availability } from "./health";
import { acceptsNewRelease, entryEnvironment, orderEnvironments, promotionTargets } from "./pipeline";
import { fromFormState, schemaFields, toFormState } from "./schemaForm";

const env = (id: string, name: string): Environment => ({
  metadata: { id, name, kind: "Environment", createdAt: "", updatedAt: "", resourceVersion: 1, ownerRefs: [{ kind: "Project", id: "p1" }] },
  spec: {},
  status: {},
});

// development promotes to staging, which promotes to production and qa.
const pipeline: Pipeline = {
  paths: [
    { from: "stg", to: ["prod", "qa"] },
    { from: "dev", to: ["stg"] },
  ],
};

describe("pipeline", () => {
  it("finds where releases enter and where each Environment promotes", () => {
    expect(entryEnvironment(pipeline)).toBe("dev");
    expect(promotionTargets(pipeline, "stg")).toEqual(["prod", "qa"]);
    expect(promotionTargets(pipeline, "prod")).toEqual([]);
    expect(acceptsNewRelease(pipeline, "dev")).toBe(true);
    expect(acceptsNewRelease(pipeline, "prod")).toBe(false);
    expect(acceptsNewRelease(undefined, "prod")).toBe(true);
  });

  it("orders Environments along the pipeline, then the rest by name", () => {
    const envs = [env("prod", "production"), env("x", "sandbox"), env("qa", "qa"), env("dev", "development"), env("stg", "staging")];
    const out = orderEnvironments(pipeline, envs).map((x) => `${x.env.metadata.name}${x.inPipeline ? "" : "*"}`);
    expect(out).toEqual(["development", "staging", "production", "qa", "sandbox*"]);
    // Without a pipeline every Environment stands alone, by name.
    expect(orderEnvironments(undefined, envs).map((x) => x.inPipeline)).toEqual([true, true, true, true, true]);
    expect(orderEnvironments(undefined, envs)[0]!.env.metadata.name).toBe("development");
  });
});

const schema: JSONSchema = {
  type: "object",
  required: ["replicas"],
  properties: {
    replicas: { type: "integer", minimum: 1, maximum: 10, description: "Running copies." },
    logLevel: { type: "string", enum: ["debug", "info"] },
    debug: { type: "boolean" },
    resources: { type: "object", properties: { limits: { type: "object", properties: { cpu: { type: "string" } } } } },
    tags: { type: "array", items: { type: "string" } },
  },
};

describe("schema forms", () => {
  it("lists a schema's fields, flattening nested objects", () => {
    expect(schemaFields(schema).map((f) => `${f.path}:${f.kind}:${f.group}`)).toEqual([
      "replicas:integer:",
      "logLevel:enum:",
      "debug:boolean:",
      "resources.limits.cpu:text:resources.limits",
      "tags:json:",
    ]);
    expect(schemaFields({ type: "string" })).toEqual([]);
  });

  it("round-trips stored values", () => {
    const stored = { replicas: 2, debug: false, resources: { limits: { cpu: "500m" } }, tags: ["a"] };
    const state = toFormState(schema, stored);
    expect(state).toMatchObject({ replicas: "2", debug: "false", "resources.limits.cpu": "500m", tags: '["a"]', logLevel: "" });
    expect(fromFormState(schema, state)).toEqual({ values: stored, errors: {} });
  });

  it("refuses what can't become a valid value, and leaves empty fields out", () => {
    expect(fromFormState(schema, { replicas: "" }).errors.replicas).toMatch(/Enter/);
    expect(fromFormState(schema, { replicas: "1.5" }).errors.replicas).toBe("Enter a whole number.");
    expect(fromFormState(schema, { replicas: "11" }).errors.replicas).toBe("At most 10.");
    expect(fromFormState(schema, { replicas: "3", tags: "[oops" }).errors.tags).toBe("Enter valid JSON.");
    expect(fromFormState(schema, { replicas: "3", logLevel: "" }).values).toEqual({ replicas: 3 });
  });
});

describe("package availability", () => {
  it("reads installed-but-not-ready as installing or not ready, never unavailable", () => {
    const r = (ready: boolean, reason: string) => ({ ready, reason, since: "" });
    expect(availability(undefined)).toBe("unavailable");
    expect(availability(r(false, "Unavailable"))).toBe("unavailable");
    expect(availability(r(false, "NotInstalled"))).toBe("notInstalled");
    expect(availability(r(false, "Installing"))).toBe("installing");
    expect(availability(r(false, "InstallFailed"))).toBe("notReady");
    expect(availability(r(true, "Ready"))).toBe("ready");
  });
});

describe("topology layout", () => {
  const node = (id: string) => ({ id, name: id, ports: [] });
  it("puts callees right of their callers, and survives a cycle", () => {
    const view: TopologyView = {
      components: [node("web"), node("api"), node("db")],
      dependencies: [
        { from: "web", to: "api", port: "http", env: "API" },
        { from: "api", to: "db", port: "pg", env: "DB" },
        { from: "web", to: "db", port: "pg", env: "DB" },
      ],
    };
    expect(Object.fromEntries(layers(view))).toEqual({ web: 0, api: 1, db: 2 });
    const cycle: TopologyView = { components: [node("a"), node("b")], dependencies: [{ from: "a", to: "b", port: "p", env: "B" }, { from: "b", to: "a", port: "p", env: "A" }] };
    for (const l of layers(cycle).values()) expect(l).toBeLessThan(2);
  });
});
