// Component form state and validation. The rules mirror what the server
// enforces (core/reconcile/application.go validateComponentSpec), so the
// form never sends something it knows will be refused, and never refuses
// something the server would accept, apart from requiring a DNS-label name.
import type { ComponentSpec } from "../api/types";
import { dnsLabel, dnsLabelError } from "../screens/common";

export interface PortRow {
  key: number;
  name: string;
  port: string;
  expose: boolean;
}

export interface EnvRow {
  key: number;
  name: string;
  value: string;
}

export interface ComponentFormState {
  name: string;
  image: string;
  ports: PortRow[];
  env: EnvRow[];
}

export type FormErrors = Partial<Record<string, string>>;

export function toSpec(s: ComponentFormState): ComponentSpec {
  const spec: ComponentSpec = { image: s.image.trim() };
  if (s.ports.length > 0) {
    spec.ports = s.ports.map((p) => ({ name: p.name.trim(), port: Number(p.port), ...(p.expose ? { expose: true } : {}) }));
  }
  const env = s.env.filter((e) => e.name.trim() !== "");
  if (env.length > 0) spec.env = Object.fromEntries(env.map((e) => [e.name.trim(), e.value]));
  return spec;
}

export function fromSpec(name: string, spec: ComponentSpec): ComponentFormState {
  let key = 0;
  return {
    name,
    image: spec.image,
    ports: (spec.ports ?? []).map((p) => ({ key: key++, name: p.name, port: String(p.port), expose: Boolean(p.expose) })),
    env: Object.entries(spec.env ?? {}).map(([n, v]) => ({ key: key++, name: n, value: v })),
  };
}

const envName = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function validate(s: ComponentFormState, { creating }: { creating: boolean }): FormErrors {
  const errors: FormErrors = {};
  if (creating) {
    const e = dnsLabelError(s.name.trim(), "name");
    if (e) errors.name = e;
  }
  if (s.image.trim() === "") errors.image = "Enter the image to run, for example nginx:1.27.";
  const seen = new Set<string>();
  s.ports.forEach((p, i) => {
    const name = p.name.trim();
    if (!dnsLabel.test(name)) errors[`port-${i}-name`] = "Use a lowercase name like http or metrics.";
    else if (seen.has(name)) errors[`port-${i}-name`] = "Each port needs a different name.";
    seen.add(name);
    const n = Number(p.port);
    if (!Number.isInteger(n) || n < 1 || n > 65535) errors[`port-${i}-port`] = "Enter a port from 1 to 65535.";
  });
  const seenEnv = new Set<string>();
  s.env.forEach((e, i) => {
    const name = e.name.trim();
    if (name === "" && e.value === "") return;
    if (!envName.test(name)) errors[`env-${i}-name`] = "Use letters, digits and underscores, not starting with a digit.";
    else if (seenEnv.has(name)) errors[`env-${i}-name`] = "This variable is already set above.";
    seenEnv.add(name);
  });
  return errors;
}
