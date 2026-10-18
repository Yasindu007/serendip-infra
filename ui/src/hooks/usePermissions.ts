// The caller's effective permissions at a scope, fetched once per page and
// refreshed every minute (spec 017 plan.md decision 11).
import { apiFetch } from "../api/client";
import type { PermissionsView, Scope } from "../api/types";
import { allows, scopeParam } from "../lib/permissions";
import { usePoll } from "./usePoll";

export interface Access {
  /** true or false once known; undefined while loading or after an error. */
  can: (action: string, data?: boolean) => boolean | undefined;
}

export function usePermissions(scope: Scope): Access {
  const param = scopeParam(scope);
  const perms = usePoll(
    (s) => apiFetch<PermissionsView>("GET", `/v1/authorization/permissions?scope=${encodeURIComponent(param)}`, undefined, s),
    60_000,
    `perms-${param}`,
  );
  const list = Array.isArray(perms.data?.permissions) ? perms.data.permissions : undefined;
  return { can: (action, data = false) => (list ? allows(list, action, data) : undefined) };
}
