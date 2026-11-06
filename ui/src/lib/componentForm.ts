// Component form state and validation. The rules mirror what the server
// enforces (core/reconcile/application.go validateComponentSpec, and
// core/resource ValidateComponentSource for a source), so the
// form never sends something it knows will be refused, and never refuses
// something the server would accept, apart from requiring a DNS-label name.
import type { ComponentSource, ComponentSpec } from "../api/types";
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

/** Where the image comes from: ready-made, or built from Git (spec 022). */
export type ImageFrom = "image" | "source";

export interface SourceFields {
  repository: string;
  branch: string;
  path: string;
  dockerfile: string;
  credentialSecret: string;
  autoBuild: boolean;
}

export const emptySource: SourceFields = { repository: "", branch: "", path: "", dockerfile: "", credentialSecret: "", autoBuild: false };

export interface ComponentFormState {
  name: string;
  from: ImageFrom;
  image: string;
  source: SourceFields;
  ports: PortRow[];
  env: EnvRow[];
}

export type FormErrors = Partial<Record<string, string>>;

/** A source as sent: empty fields are left out, so the server's defaults apply. */
export function toSource(f: SourceFields): ComponentSource {
  const src: ComponentSource = { repository: f.repository.trim() };
  const branch = f.branch.trim();
  const path = f.path.trim();
  const dockerfile = f.dockerfile.trim();
  if (branch) src.branch = branch;
  if (path) src.path = path;
  if (dockerfile) src.dockerfile = dockerfile;
  if (f.credentialSecret) src.credentialSecret = f.credentialSecret;
  if (f.autoBuild) src.autoBuild = true;
  return src;
}

export function toSpec(s: ComponentFormState): ComponentSpec {
  // Exactly one of image and source: a source Component sends image "".
  const spec: ComponentSpec = s.from === "source" ? { image: "", source: toSource(s.source) } : { image: s.image.trim() };
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
    from: spec.source ? "source" : "image",
    image: spec.image,
    source: spec.source
      ? {
          repository: spec.source.repository,
          branch: spec.source.branch ?? "",
          path: spec.source.path ?? "",
          dockerfile: spec.source.dockerfile ?? "",
          credentialSecret: spec.source.credentialSecret ?? "",
          autoBuild: Boolean(spec.source.autoBuild),
        }
      : emptySource,
    ports: (spec.ports ?? []).map((p) => ({ key: key++, name: p.name, port: String(p.port), expose: Boolean(p.expose) })),
    env: Object.entries(spec.env ?? {}).map(([n, v]) => ({ key: key++, name: n, value: v })),
  };
}

const envName = /^[A-Za-z_][A-Za-z0-9_]*$/;

// core/resource/build.go's grammar: no credentials, query or fragment in
// the URL, and nothing a shell would act on.
const httpsRepo = /^https:\/\/[A-Za-z0-9.-]+(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?$/;
const sshRepo = /^ssh:\/\/([A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?$/;
const scpLike = /^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9.-]+:[A-Za-z0-9._~%+-]+(\/[A-Za-z0-9._~%+-]+)*\/?$/;

/** An HTTPS or SSH repository URL the server accepts. */
export function repositoryOK(r: string): boolean {
  return r.length <= 2048 && (httpsRepo.test(r) || sshRepo.test(r) || scpLike.test(r));
}

/** Watching for new commits works over HTTPS only (auto-build reads the head over smart HTTP). */
export const isSSH = (r: string) => r !== "" && !r.trim().startsWith("https://");

export function branchOK(b: string): boolean {
  if (!/^[A-Za-z0-9._/-]{1,255}$/.test(b) || b.startsWith("-") || b.endsWith(".") || b.includes("..")) return false;
  return b.split("/").every((part) => part !== "" && !part.startsWith(".") && !part.endsWith(".lock"));
}

/** A relative path inside the repository: "." only for a directory, never leaving it. */
export function repoPathOK(p: string, dir: boolean): boolean {
  if (p === ".") return dir;
  if (p.length > 1024 || !/^[A-Za-z0-9._/-]+$/.test(p) || p.startsWith("/") || p.startsWith("-")) return false;
  const q = dir && p.endsWith("/") ? p.slice(0, -1) : p;
  return q.split("/").every((part) => part !== "" && part !== "." && part !== "..");
}

export function validate(s: ComponentFormState, { creating }: { creating: boolean }): FormErrors {
  const errors: FormErrors = {};
  if (creating) {
    const e = dnsLabelError(s.name.trim(), "name");
    if (e) errors.name = e;
  }
  if (s.from === "image") {
    if (s.image.trim() === "") errors.image = "Enter the image to run, for example nginx:1.27.";
  } else {
    const r = s.source.repository.trim();
    if (r === "") errors.repository = "Enter the Git repository's URL, for example https://github.com/org/app.git.";
    else if (!repositoryOK(r)) errors.repository = "Use an https:// or SSH URL, without a user name and password, a query or a fragment. Put a credential in a Secret instead.";
    const b = s.source.branch.trim();
    if (b !== "" && !branchOK(b)) errors.branch = "That isn't a branch name Git allows.";
    const p = s.source.path.trim();
    if (p !== "" && !repoPathOK(p, true)) errors.path = "Use a relative path inside the repository, for example services/web.";
    const d = s.source.dockerfile.trim();
    if (d !== "" && !repoPathOK(d, false)) errors.dockerfile = "Use a relative path to a file, for example Dockerfile.";
  }
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
