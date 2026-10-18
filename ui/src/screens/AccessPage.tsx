import { apiFetch } from "../api/client";
import type { Application, Component, Scope } from "../api/types";
import { usePoll } from "../hooks/usePoll";
import { AccessControl } from "./Access";

/**
 * AccessPage is /access?scope=…: the Access control page for "/" (from
 * Overview), an Application or a Component, with the resource's name and a
 * way back to it.
 */
export function AccessPage({ scope }: { scope: Scope }) {
  const kind = scope.kind ?? "";
  const id = scope.id ?? "";
  const path = kind === "Application" ? "applications" : kind === "Component" ? "components" : "";
  const target = usePoll(
    (s) => (path ? apiFetch<Application | Component>("GET", `/v1/${path}/${encodeURIComponent(id)}`, undefined, s) : Promise.resolve(undefined)),
    60_000,
    `access-target-${kind}-${id}`,
  );
  const name = target.data?.metadata.name;

  if (!kind) return <AccessControl scope={scope} crumbs={[{ label: "Overview", to: "/" }]} />;
  if (kind === "Application") {
    return (
      <AccessControl
        scope={scope}
        {...(name ? { name } : {})}
        crumbs={[
          { label: "Applications", to: "/applications" },
          { label: name ?? "Application", to: `/applications/${encodeURIComponent(id)}` },
        ]}
      />
    );
  }
  if (kind === "Component") {
    const appId = target.data?.metadata.ownerRefs?.find((o) => o.kind === "Application")?.id;
    const crumbs = [{ label: "Applications", to: "/applications" }];
    if (appId) crumbs.push({ label: name ?? "Component", to: `/applications/${encodeURIComponent(appId)}/components/${encodeURIComponent(id)}` });
    return <AccessControl scope={scope} {...(name ? { name } : {})} crumbs={crumbs} />;
  }
  return <AccessControl scope={scope} name={`${kind} ${id}`} />;
}
