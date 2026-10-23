// A Project's pipeline (spec 020): the order releases move through its
// Environments. This only arranges what the API returned; the server
// enforces the pipeline on every deploy and promotion.
import type { Environment, Pipeline } from "../api/types";

const paths = (p: Pipeline | undefined) => p?.paths ?? [];

/** The Environment new releases enter: the one no path promotes into. */
export function entryEnvironment(p: Pipeline | undefined): string | undefined {
  const targets = new Set(paths(p).flatMap((x) => x.to));
  return paths(p).find((x) => !targets.has(x.from))?.from;
}

/** Where a release in envId may be promoted to directly. */
export function promotionTargets(p: Pipeline | undefined, envId: string): string[] {
  return paths(p).find((x) => x.from === envId)?.to ?? [];
}

/**
 * Whether a new release (the Component's latest) may be deployed to envId:
 * anywhere without a pipeline, otherwise only where releases enter.
 */
export function acceptsNewRelease(p: Pipeline | undefined, envId: string): boolean {
  return paths(p).length === 0 || entryEnvironment(p) === envId;
}

/**
 * The Environments in pipeline order, from the entry outwards (breadth
 * first), then any the pipeline doesn't name, by name. inPipeline is false
 * for those.
 */
export function orderEnvironments(p: Pipeline | undefined, envs: Environment[]): Array<{ env: Environment; inPipeline: boolean }> {
  const byId = new Map(envs.map((e) => [e.metadata.id, e]));
  const order: string[] = [];
  const first = entryEnvironment(p);
  if (first) {
    const queue = [first];
    while (queue.length > 0) {
      const id = queue.shift()!;
      if (order.includes(id)) continue;
      order.push(id);
      queue.push(...promotionTargets(p, id));
    }
  }
  const out = order.flatMap((id) => (byId.has(id) ? [{ env: byId.get(id)!, inPipeline: true }] : []));
  const rest = envs.filter((e) => !order.includes(e.metadata.id)).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
  return [...out, ...rest.map((env) => ({ env, inPipeline: paths(p).length === 0 }))];
}

/** The owner of that kind a resource has, from its owner references. */
export function ownerOf(m: { ownerRefs?: Array<{ kind: string; id: string }> }, kind: string): string | undefined {
  return m.ownerRefs?.find((o) => o.kind === kind)?.id;
}
