import { useState } from "react";
import { apiFetch, apiList } from "../api/client";
import type { Environment, JSONSchema, Package, PlatformComponent, PlatformView, Project, ProviderInstance, Readiness } from "../api/types";
import { ActionConfirm } from "../components/ActionConfirm";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { KeyValueList } from "../components/Data";
import { DeleteConfirm } from "../components/Delete";
import { Guarded } from "../components/Guarded";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState } from "../components/States";
import { ReadyStatus, StatusText } from "../components/Status";
import { listPackages } from "../hooks/useOptional";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { availability, availabilityWords } from "../lib/health";
import { action } from "../lib/permissions";
import { orderEnvironments, promotionTargets } from "../lib/pipeline";
import { navigate } from "../lib/router";
import { schemaFields } from "../lib/schemaForm";
import { UpdatedLine } from "./common";

const enc = encodeURIComponent;

/** A schema's settings in words: "replicas, resources.limits.cpu (optional)". */
function settings(schema: JSONSchema | undefined): string {
  const fields = schemaFields(schema);
  if (fields.length === 0) return "None";
  return fields.map((f) => `${f.path}${f.required ? "" : " (optional)"}`).join(", ");
}

function PlatformItems({ items, id, title, empty, traits }: { items: PlatformComponent[]; id: string; title: string; empty: string; traits: boolean }) {
  return (
    <section className="section" aria-labelledby={id}>
      <h2 id={id} className="section-title">
        {title}
      </h2>
      {items.length === 0 ? (
        <p className="state-body">{empty}</p>
      ) : (
        items.map((t) => (
          <div key={t.name} className="panel rows">
            <h3 className="pipeline-name mono" translate="no">
              {t.name}
            </h3>
            {t.description && <p>{t.description}</p>}
            <KeyValueList
              items={[
                ["Parameters", settings(t.parameters)],
                ["Per environment", settings(t.environment)],
                ...(traits ? [] : [["Allowed traits", (t.allowedTraits ?? []).length > 0 ? t.allowedTraits!.join(", ") : "None"] as [string, string]]),
              ]}
            />
          </div>
        ))
      )}
    </section>
  );
}

/**
 * PlatformPage: the application platform, read-only (spec 020): where
 * workloads run, the component types and traits installed and what each
 * allows, and each Project's environments and pipeline.
 */
export function PlatformPage() {
  const now = useNow();
  const platform = usePoll((s) => apiFetch<PlatformView>("GET", "/v1/platform", undefined, s), 30_000, "platform");
  const projects = usePoll((s) => apiList<Project>("/v1/projects", s), 30_000, "platform-projects");
  const envs = usePoll((s) => apiList<Environment>("/v1/environments", s), 30_000, "platform-envs");

  return (
    <>
      <PageHeader title="Platform" meta={<UpdatedLine at={platform.updatedAt} now={now} />} />
      {platform.error ? (
        <RequestError error={platform.error} what="the platform" />
      ) : !platform.data ? (
        <LoadingState what="the platform" />
      ) : (
        <>
          <section className="section" aria-labelledby="pf-runtimes">
            <h2 id="pf-runtimes" className="section-title">
              Where workloads run
            </h2>
            {platform.data.runtimes.length === 0 ? (
              <p className="state-body">The platform reports nowhere to run workloads.</p>
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Name</th>
                      <th scope="col">Status</th>
                      <th scope="col">Message</th>
                    </tr>
                  </thead>
                  <tbody>
                    {platform.data.runtimes.map((r) => (
                      <tr key={r.name}>
                        <td className="mono" translate="no">
                          {r.name}
                        </td>
                        <td>{r.ready ? <StatusText kind="ok">Ready</StatusText> : <StatusText kind="warn">Not ready</StatusText>}</td>
                        <td>{r.message ?? ""}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
          <PlatformItems items={platform.data.componentTypes} id="pf-types" title="Component types" empty="No component types are installed." traits={false} />
          <PlatformItems items={platform.data.traits} id="pf-traits" title="Traits" empty="No traits are installed." traits />
        </>
      )}

      <section className="section" aria-labelledby="pf-projects">
        <h2 id="pf-projects" className="section-title">
          Projects and environments
        </h2>
        {projects.error ? (
          <RequestError error={projects.error} what="projects" />
        ) : envs.error ? (
          <RequestError error={envs.error} what="environments" />
        ) : !projects.data || !envs.data ? (
          <LoadingState what="projects" />
        ) : projects.data.length === 0 ? (
          <p className="state-body">No projects yet.</p>
        ) : (
          projects.data.map((p) => {
            const own = envs.data!.filter((e) => e.metadata.ownerRefs?.some((o) => o.kind === "Project" && o.id === p.metadata.id));
            const names = new Map(own.map((e) => [e.metadata.id, e.metadata.name]));
            return (
              <div key={p.metadata.id} className="panel rows">
                <div className="pipeline-head">
                  <h3 className="pipeline-name">{p.metadata.name}</h3>
                  <ReadyStatus conditions={p.status.conditions} />
                </div>
                {own.length === 0 ? (
                  <p className="state-body">No environments.</p>
                ) : (
                  <div className="table-wrap">
                    <table className="table">
                      <thead>
                        <tr>
                          <th scope="col">Environment</th>
                          <th scope="col">Promotes to</th>
                          <th scope="col">Failure protection</th>
                        </tr>
                      </thead>
                      <tbody>
                        {orderEnvironments(p.spec.pipeline, own).map(({ env, inPipeline }) => {
                          const to = promotionTargets(p.spec.pipeline, env.metadata.id).map((t) => names.get(t) ?? t);
                          return (
                            <tr key={env.metadata.id}>
                              <td>{env.metadata.name}</td>
                              <td>{!inPipeline ? "Not in the pipeline" : to.length > 0 ? to.join(", ") : "Nothing (last)"}</td>
                              <td>{env.status.availability === "ha" ? "High availability" : env.status.availability === "single-instance" ? "Single instance" : "Not reported"}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            );
          })
        )}
      </section>
      <ApiEquivalent request={{ method: "GET", path: "/v1/platform" }} />
    </>
  );
}

type Open = { kind: "install"; pkg: Package } | { kind: "remove"; pkg: Package; instance: ProviderInstance };

/**
 * PackagesPage: the optional packages (spec 020), whether each is
 * installed and ready, what it costs to run, and install or remove. A
 * package is a ProviderInstance; removing one is a delete, so it goes
 * through an Approval.
 */
export function PackagesPage() {
  const now = useNow();
  const packages = usePoll((s) => listPackages(s), 15_000, "packages-page");
  const readiness = usePoll((s) => apiFetch<Readiness>("GET", "/v1/readiness", undefined, s), 15_000, "readiness");
  const instances = usePoll((s) => apiList<ProviderInstance>("/v1/provider-instances", s), 15_000, "provider-instances");
  // Packages are installed platform-wide, so it's decided at "/".
  const access = usePermissions({});
  const canInstall = access.can(action("ProviderInstance", "write"));
  const canRemove = access.can(action("ProviderInstance", "delete"));
  const [open, setOpen] = useState<Open>();

  const done = () => {
    setOpen(undefined);
    instances.refresh();
    readiness.refresh();
  };

  return (
    <>
      <PageHeader title="Packages" meta={<UpdatedLine at={packages.updatedAt} now={now} />} />
      <p>Optional parts of the platform. Each is installed only when someone asks for it, and costs memory and CPU while it runs.</p>
      {packages.error ? (
        <RequestError error={packages.error} what="packages" />
      ) : !packages.data ? (
        <LoadingState what="packages" />
      ) : packages.data.length === 0 ? (
        <EmptyState title="No packages">This server offers no optional packages.</EmptyState>
      ) : (
        packages.data.map((pkg) => {
          const instance = instances.data?.find((i) => i.spec.providerId === pkg.provider);
          const state = availability(readiness.data?.[pkg.capability]);
          const { kind, word } = availabilityWords[state];
          const body = { metadata: { name: pkg.provider }, spec: { capabilityId: pkg.capability, providerId: pkg.provider } };
          return (
            <section key={pkg.provider} className={pkg.available ? "section panel" : "section unavailable"} aria-labelledby={`pkg-${pkg.provider}`}>
              <div className="pipeline-head">
                <h2 id={`pkg-${pkg.provider}`} className="section-title mono" translate="no">
                  {pkg.provider}
                </h2>
                <StatusText kind={kind}>{word}</StatusText>
              </div>
              <p>{pkg.description}</p>
              <KeyValueList
                items={[
                  ["Provides", pkg.capability],
                  ["Cost", pkg.available ? "Not stated by this server. Check the package's measured memory and CPU before installing it." : "Not measured yet: it can't be installed in this version."],
                  ...(readiness.data?.[pkg.capability]?.message ? [["Status", readiness.data[pkg.capability]!.message!] as [string, string]] : []),
                ]}
              />

              {open?.pkg.provider === pkg.provider && open.kind === "install" && (
                <ActionConfirm<ProviderInstance>
                  label={`Install ${pkg.provider}`}
                  request={{ method: "POST", path: "/v1/provider-instances", body }}
                  confirm="Install package"
                  busyLabel="Installing…"
                  onDone={done}
                  onCancel={() => setOpen(undefined)}
                >
                  <p>Install {pkg.provider} for the whole platform? It runs, and uses memory and CPU, until it's removed.</p>
                </ActionConfirm>
              )}
              {open?.pkg.provider === pkg.provider && open.kind === "remove" && (
                <DeleteConfirm
                  target={{ kind: "ProviderInstance", id: open.instance.metadata.id, name: pkg.provider }}
                  cascade={false}
                  noun="package"
                  note="Removing it uninstalls it and deletes what it stored, such as stored logs and metrics. Features that need it are greyed out again."
                  onDeleted={(approval) => (approval ? navigate(`/approvals/${enc(approval)}`) : done())}
                  onCancel={() => setOpen(undefined)}
                />
              )}

              {!open && (
                <div className="form-actions">
                  {instance ? (
                    <Guarded allowed={canRemove} label="Remove package" reason="Your roles don't allow removing packages.">
                      <button type="button" className="button" onClick={() => setOpen({ kind: "remove", pkg, instance })}>
                        Remove package
                      </button>
                    </Guarded>
                  ) : !pkg.available ? (
                    <Guarded allowed={false} label="Install package" reason="Not available in this version of Serendip yet.">
                      {null}
                    </Guarded>
                  ) : (
                    <Guarded allowed={canInstall} label="Install package" reason="Your roles don't allow installing packages.">
                      <button type="button" className="button button--primary" onClick={() => setOpen({ kind: "install", pkg })}>
                        Install package
                      </button>
                    </Guarded>
                  )}
                </div>
              )}
            </section>
          );
        })
      )}
      <ApiEquivalent request={{ method: "GET", path: "/v1/packages" }} />
    </>
  );
}
