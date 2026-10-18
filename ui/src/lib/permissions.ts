// What the console offers depends on the caller's permissions at a scope
// (spec 017 plan.md decision 11). This mirrors core/policy's matching so
// controls can be hidden or disabled ahead of time; it is a convenience
// only. The server decides every request.
import type { Permission, Scope } from "../api/types";

/** A pattern covers an action, case-insensitively; one "*" matches any run, "/" included. */
export function matches(pattern: string, action: string): boolean {
  const p = pattern.toLowerCase();
  const a = action.toLowerCase();
  const star = p.indexOf("*");
  if (star < 0) return p === a;
  const prefix = p.slice(0, star);
  const suffix = p.slice(star + 1);
  return a.length >= prefix.length + suffix.length && a.startsWith(prefix) && a.endsWith(suffix);
}

const anyMatches = (patterns: string[] | undefined, action: string) => (patterns ?? []).some((p) => matches(p, action));

/** One permission block grants an action: covered and not excluded, within the block. */
export function grants(p: Permission, action: string, data = false): boolean {
  if (data) return anyMatches(p.dataActions, action) && !anyMatches(p.notDataActions, action);
  return anyMatches(p.actions, action) && !anyMatches(p.notActions, action);
}

/** Any block grants it: roles add up, as in Azure. */
export function allows(perms: Permission[], action: string, data = false): boolean {
  return perms.some((p) => grants(p, action, data));
}

/** The action namespace of each kind the console works with. */
const paths: Record<string, string> = {
  Application: "applications",
  Component: "components",
};

export function action(kind: string, verb: "read" | "write" | "delete"): string {
  return `serendip/${paths[kind] ?? kind.toLowerCase()}/${verb}`;
}

export const assignRoles = "serendip/authorization/roleAssignments/write";

/** "/" or "Kind/id", the form the API takes in ?scope=. */
export function scopeParam(s: Scope): string {
  return s.kind && s.id ? `${s.kind}/${s.id}` : "/";
}

export function parseScopeParam(v: string | null): Scope | undefined {
  if (v === null || v === "/") return {};
  const i = v.indexOf("/");
  if (i <= 0 || i === v.length - 1 || v.indexOf("/", i + 1) >= 0) return undefined;
  return { kind: v.slice(0, i), id: v.slice(i + 1) };
}

export const sameScope = (a: Scope, b: Scope) => (a.kind ?? "") === (b.kind ?? "") && (a.id ?? "") === (b.id ?? "");

/** The console's Access control page for a scope. */
export const accessPath = (s: Scope) => `/access?scope=${encodeURIComponent(scopeParam(s))}`;
