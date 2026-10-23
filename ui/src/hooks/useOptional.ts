// An optional capability's readiness and the package that provides it
// (spec 020): what greys a feature out, and what to install.
import { apiFetch } from "../api/client";
import type { CapabilityReadiness, Package, Readiness } from "../api/types";
import { usePoll } from "./usePoll";

/** GET /v1/packages answers {"packages": [...]}. */
export async function listPackages(signal: AbortSignal): Promise<Package[]> {
  const v = await apiFetch<{ packages?: Package[] } | null>("GET", "/v1/packages", undefined, signal);
  return Array.isArray(v?.packages) ? v.packages : [];
}

export interface Optional {
  loaded: boolean;
  readiness: CapabilityReadiness | undefined;
  /** The package's name, or the capability's while the list is unknown. */
  packageName: string;
}

export function useOptional(capability: string): Optional {
  const readiness = usePoll((s) => apiFetch<Readiness>("GET", "/v1/readiness", undefined, s), 15_000, "readiness");
  const packages = usePoll((s) => listPackages(s), 60_000, "packages");
  const pkg = packages.data?.find((p) => p.capability === capability);
  return {
    loaded: readiness.data !== undefined || readiness.error !== undefined,
    readiness: readiness.data?.[capability],
    packageName: pkg?.provider ?? capability,
  };
}
