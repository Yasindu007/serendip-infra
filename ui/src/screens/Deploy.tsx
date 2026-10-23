import { useEffect, useRef, useState, type FormEvent } from "react";
import { apiFetch, apiList, isShown, type ApiError, type NetworkError } from "../api/client";
import type { Application, Component, ConfigSchemas, Deployment, DeploymentOverrides, Environment, Project } from "../api/types";
import { ActionConfirm } from "../components/ActionConfirm";
import { ApiEquivalent, type ApiRequest } from "../components/ApiEquivalent";
import { KeyValueList, SafeLink } from "../components/Data";
import { DeleteConfirm } from "../components/Delete";
import { SelectField, TextField } from "../components/Field";
import { Guarded } from "../components/Guarded";
import { Icon } from "../components/Icon";
import { EnvironmentPipeline, type PipelineStage } from "../components/Pipeline";
import { FormFailure, RequestError } from "../components/RequestError";
import { SchemaForm } from "../components/SchemaForm";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { ReadyStatus, StatusText } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll } from "../hooks/usePoll";
import { action } from "../lib/permissions";
import { acceptsNewRelease, orderEnvironments, ownerOf, promotionTargets } from "../lib/pipeline";
import { Link, navigate } from "../lib/router";
import { fromFormState, schemaFields, toFormState, type SchemaFormState } from "../lib/schemaForm";

const enc = encodeURIComponent;
export const compPath = (appId: string, id: string) => `/applications/${enc(appId)}/components/${enc(id)}`;
const configurePath = (appId: string, compId: string, depId: string) => `${compPath(appId, compId)}/deployments/${enc(depId)}/configure`;

/** What one Environment runs, as the API reports it. */
export function runningRelease(d: Deployment): string | undefined {
  return d.status.release || undefined;
}

export interface DeployData {
  project: Project;
  envs: Environment[];
  /** This Component's Deployments, at most one per Environment. */
  deployments: Deployment[];
}

/** The Project, its Environments and this Component's Deployments, polled together. */
export function useDeployData(projectId: string | undefined, componentId: string) {
  return usePoll(
    async (s): Promise<DeployData | undefined> => {
      if (!projectId) return undefined;
      const [project, envs, all] = await Promise.all([
        apiFetch<Project>("GET", `/v1/projects/${enc(projectId)}`, undefined, s),
        apiList<Environment>(`/v1/environments?ownerId=${enc(projectId)}`, s),
        apiList<Deployment>("/v1/deployments", s),
      ]);
      return { project, envs, deployments: all.filter((d) => d.spec.component === componentId) };
    },
    5_000,
    `deploy-${projectId ?? ""}-${componentId}`,
  );
}

type Open =
  | { kind: "deploy"; env: string }
  | { kind: "promote"; env: string; d: Deployment; to: Environment }
  | { kind: "rollback"; env: string; d: Deployment; release: string }
  | { kind: "undeploy"; env: string; d: Deployment };

/**
 * DeployTab: the Project's environment pipeline for one Component (spec
 * 020), each Environment's release and state, and deploy, promote, roll
 * back and undeploy. The server enforces the pipeline; the console only
 * offers what it would accept.
 */
export function DeployTab({ app, comp }: { app: Application; comp: Component }) {
  const projectId = ownerOf(app.metadata, "Project");
  const data = useDeployData(projectId, comp.metadata.id);
  const [open, setOpen] = useState<Open>();

  if (!projectId) {
    return (
      <EmptyState title="Not in a Project">
        This application isn't in a Project, so it has no Environments to deploy to. Applications created in a Project deploy through its pipeline.
      </EmptyState>
    );
  }
  if (data.error) return <RequestError error={data.error} what="environments" />;
  if (!data.data) return <LoadingState what="environments" />;
  const { project, envs, deployments } = data.data;
  if (envs.length === 0) return <EmptyState title="No environments yet">The Project has no Environments to deploy to.</EmptyState>;

  const pipeline = project.spec.pipeline;
  const names = new Map(envs.map((e) => [e.metadata.id, e.metadata.name]));
  const byEnv = new Map(deployments.map((d) => [ownerOf(d.metadata, "Environment") ?? "", d]));
  const done = () => {
    setOpen(undefined);
    data.refresh();
  };

  const stages: PipelineStage[] = orderEnvironments(pipeline, envs).map(({ env }) => {
    const id = env.metadata.id;
    const d = byEnv.get(id);
    return {
      id,
      name: env.metadata.name,
      status: d ? <ReadyStatus conditions={d.status.conditions} /> : <StatusText kind="unknown">Not deployed</StatusText>,
      promotesTo: promotionTargets(pipeline, id).map((t) => names.get(t) ?? t),
      body: (
        <EnvironmentStage
          app={app}
          comp={comp}
          env={env}
          d={d}
          envs={envs}
          project={project}
          open={open?.env === id ? open : undefined}
          setOpen={setOpen}
          onDone={done}
        />
      ),
    };
  });

  return (
    <section className="section" aria-labelledby="deploy-title">
      <h2 id="deploy-title" className="section-title">
        Environments
      </h2>
      {(pipeline?.paths?.length ?? 0) === 0 && <p className="field-hint">The Project has no pipeline, so each Environment is deployed on its own.</p>}
      <EnvironmentPipeline label={`Pipeline of ${project.metadata.name}`} stages={stages} />
      <ApiEquivalent request={{ method: "GET", path: `/v1/deployments` }} />
    </section>
  );
}

function EnvironmentStage({
  app,
  comp,
  env,
  d,
  envs,
  project,
  open,
  setOpen,
  onDone,
}: {
  app: Application;
  comp: Component;
  env: Environment;
  d: Deployment | undefined;
  envs: Environment[];
  project: Project;
  open: Open | undefined;
  setOpen: (o: Open | undefined) => void;
  onDone: () => void;
}) {
  const id = env.metadata.id;
  const access = usePermissions({ kind: "Environment", id });
  const canWrite = access.can(action("Deployment", "write"));
  const canDelete = access.can(action("Deployment", "delete"));
  const pipeline = project.spec.pipeline;
  const [rollTo, setRollTo] = useState("");

  if (!d) {
    const sources = (pipeline?.paths ?? []).filter((p) => p.to.includes(id)).map((p) => envs.find((e) => e.metadata.id === p.from)?.metadata.name ?? p.from);
    if (!acceptsNewRelease(pipeline, id)) return <p className="field-hint">Nothing runs here yet. Promote a release from {sources.join(" or ")}.</p>;
    const body = { metadata: { name: comp.metadata.name, ownerRefs: [{ kind: "Environment", id }] }, spec: { component: comp.metadata.id } };
    return open?.kind === "deploy" ? (
      <ActionConfirm<Deployment>
        label={`Deploy to ${env.metadata.name}`}
        request={{ method: "POST", path: "/v1/deployments", body }}
        confirm="Deploy"
        busyLabel="Deploying…"
        onDone={onDone}
        onCancel={() => setOpen(undefined)}
      >
        <p>
          Deploy the latest release of {comp.metadata.name} to {env.metadata.name}?
        </p>
      </ActionConfirm>
    ) : canWrite ? (
      // Deploying creates a Deployment, so it's offered only to those who
      // may (ui/DESIGN.md "Permission-aware controls").
      <div className="form-actions">
        <button type="button" className="button button--primary" onClick={() => setOpen({ kind: "deploy", env: id })}>
          Deploy
        </button>
      </div>
    ) : (
      <p className="field-hint">Nothing runs here yet.</p>
    );
  }

  const running = runningRelease(d);
  const wanted = d.spec.release;
  const o = d.spec.overrides ?? {};
  const counts = [
    Object.keys(o.env ?? {}).length > 0 ? plural(Object.keys(o.env ?? {}).length, "variable") : "",
    Object.keys(o.config ?? {}).length > 0 ? plural(Object.keys(o.config ?? {}).length, "setting") : "",
    Object.keys(o.traits ?? {}).length > 0 ? plural(Object.keys(o.traits ?? {}).length, "trait") : "",
  ].filter(Boolean);
  const history = d.status.history ?? [];

  return (
    <>
      <KeyValueList
        items={[
          [
            "Release",
            running ? (
              <span className="mono" translate="no">
                {running}
              </span>
            ) : (
              "None running yet"
            ),
          ],
          ...(wanted && wanted !== running
            ? [
                [
                  "Moving to",
                  <span className="mono" translate="no">
                    {wanted}
                  </span>,
                ] as [string, React.ReactNode],
              ]
            : []),
          ["Overrides", counts.length > 0 ? counts.join(", ") : "None"],
          ...(d.status.endpoints ?? []).map((e): [string, React.ReactNode] => [`Endpoint ${e.name}`, <SafeLink href={e.url} />]),
        ]}
      />

      {open?.kind === "promote" && (
        <ActionConfirm<Deployment>
          label={`Promote to ${open.to.metadata.name}`}
          request={{ method: "POST", path: `/v1/deployments/${d.metadata.id}/promote`, body: { to: open.to.metadata.id } }}
          confirm={`Promote to ${open.to.metadata.name}`}
          busyLabel="Promoting…"
          onDone={onDone}
          onCancel={() => setOpen(undefined)}
        >
          <p>
            Run release <span className="mono">{running}</span> in {open.to.metadata.name}? {open.to.metadata.name} keeps its own overrides.
          </p>
        </ActionConfirm>
      )}
      {open?.kind === "rollback" && (
        <ActionConfirm<Deployment>
          label={`Roll back ${env.metadata.name}`}
          request={{ method: "PUT", path: `/v1/deployments/${d.metadata.id}`, body: { ...d, spec: { ...d.spec, release: open.release } } }}
          confirm="Roll back"
          busyLabel="Rolling back…"
          onDone={onDone}
          onCancel={() => setOpen(undefined)}
        >
          <p>
            Run release <span className="mono">{open.release}</span> in {env.metadata.name} again? Its overrides stay as they are.
          </p>
        </ActionConfirm>
      )}
      {open?.kind === "undeploy" && (
        <DeleteConfirm
          target={{ kind: "Deployment", id: d.metadata.id, name: `${comp.metadata.name} in ${env.metadata.name}` }}
          cascade={false}
          noun="deployment"
          note={`${comp.metadata.name} stops running in ${env.metadata.name}. The component itself stays.`}
          onDeleted={(approval) => (approval ? navigate(`/approvals/${enc(approval)}`) : onDone())}
          onCancel={() => setOpen(undefined)}
        />
      )}

      {!open && (
        <div className="rows">
          <div className="form-actions">
            {promotionTargets(pipeline, id).map((t) => {
              const to = envs.find((e) => e.metadata.id === t);
              return to ? <PromoteButton key={t} to={to} disabled={!running} onClick={() => setOpen({ kind: "promote", env: id, d, to })} /> : null;
            })}
            <Guarded allowed={canWrite} label="Configure" reason="Your roles here don't allow changing this deployment.">
              <Link className="button" to={configurePath(app.metadata.id, comp.metadata.id, d.metadata.id)}>
                Configure
              </Link>
            </Guarded>
            <Guarded allowed={false} label="Restart" reason="Restarting isn't available yet.">
              {null}
            </Guarded>
            <Guarded allowed={canDelete} label="Undeploy" reason="Your roles here don't allow undeploying.">
              <button type="button" className="button" onClick={() => setOpen({ kind: "undeploy", env: id, d })}>
                Undeploy
              </button>
            </Guarded>
          </div>
          {history.length > 0 && (
            <div className="row">
              <SelectField
                id={`rollback-${id}`}
                label="Roll back to"
                value={rollTo}
                onChange={(e) => setRollTo(e.target.value)}
                options={[{ value: "", label: "Choose a release" }, ...history.map((r) => ({ value: r, label: r }))]}
              />
              <Guarded allowed={canWrite} label="Roll back" reason="Your roles here don't allow changing this deployment.">
                <button type="button" className="button" disabled={rollTo === ""} onClick={() => setOpen({ kind: "rollback", env: id, d, release: rollTo })}>
                  Roll back
                </button>
              </Guarded>
            </div>
          )}
        </div>
      )}
    </>
  );
}

/** Promoting needs deployments write at the target Environment, so it's checked there. */
function PromoteButton({ to, disabled, onClick }: { to: Environment; disabled: boolean; onClick: () => void }) {
  const allowed = usePermissions({ kind: "Environment", id: to.metadata.id }).can(action("Deployment", "write"));
  const label = `Promote to ${to.metadata.name}`;
  return (
    <Guarded allowed={allowed} label={label} reason={`Your roles don't allow deploying to ${to.metadata.name}.`}>
      <button type="button" className="button button--primary" disabled={disabled} title={disabled ? "Nothing runs here yet to promote." : undefined} onClick={onClick}>
        {label}
      </button>
    </Guarded>
  );
}

const plural = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

let rowKey = 5000;
const nextKey = () => rowKey++;
const envNameRE = /^[A-Za-z_][A-Za-z0-9_]*$/;

interface EnvRow {
  key: number;
  name: string;
  value: string;
}

/** Stored keys a schema form doesn't describe are kept, not dropped on save. */
function keepUndescribed(stored: Record<string, unknown> | undefined, schema: ConfigSchemas["environment"], values: Record<string, unknown>) {
  const described = new Set(schemaFields(schema).map((f) => f.path.split(".")[0]));
  const out = { ...values };
  for (const [k, v] of Object.entries(stored ?? {})) if (!described.has(k) && !(k in out)) out[k] = v;
  return out;
}

/**
 * DeploymentConfigure edits one Environment's overrides (spec 020):
 * environment variables, and the per-environment configuration and trait
 * settings as forms generated from the platform's schemas. The server
 * checks every value against the same schemas.
 */
export function DeploymentConfigure({ appId, compId, id }: { appId: string; compId: string; id: string }) {
  const [d, setD] = useState<Deployment>();
  const [envName, setEnvName] = useState<string>();
  const [names, setNames] = useState<{ app?: string; comp?: string }>({});
  const [schemas, setSchemas] = useState<ConfigSchemas>();
  const [schemaError, setSchemaError] = useState<ApiError | NetworkError>();
  const [loadError, setLoadError] = useState<ApiError | NetworkError>();
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [config, setConfig] = useState<SchemaFormState>({});
  const [traits, setTraits] = useState<Record<string, SchemaFormState>>({});
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const summary = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    (async () => {
      const dep = await apiFetch<Deployment>("GET", `/v1/deployments/${enc(id)}`, undefined, ctrl.signal);
      const envId = ownerOf(dep.metadata, "Environment");
      const [e, s, app, comp] = await Promise.all([
        envId ? apiFetch<Environment>("GET", `/v1/environments/${enc(envId)}`, undefined, ctrl.signal).catch(() => undefined) : undefined,
        apiFetch<ConfigSchemas>("GET", `/v1/components/${enc(dep.spec.component)}/schemas`, undefined, ctrl.signal).catch((err) => {
          if (isShown(err)) setSchemaError(err);
          return undefined;
        }),
        apiFetch<Application>("GET", `/v1/applications/${enc(appId)}`, undefined, ctrl.signal).catch(() => undefined),
        apiFetch<Component>("GET", `/v1/components/${enc(dep.spec.component)}`, undefined, ctrl.signal).catch(() => undefined),
      ]);
      setNames({ ...(app ? { app: app.metadata.name } : {}), ...(comp ? { comp: comp.metadata.name } : {}) });
      const o = dep.spec.overrides ?? {};
      setD(dep);
      setEnvName(e?.metadata.name);
      setSchemas(s);
      setEnv(Object.entries(o.env ?? {}).map(([name, value]) => ({ key: nextKey(), name, value })));
      setConfig(toFormState(s?.environment, o.config));
      setTraits(Object.fromEntries(Object.entries(s?.traits ?? {}).map(([t, schema]) => [t, toFormState(schema, o.traits?.[t])])));
    })().catch((err) => isShown(err) && setLoadError(err));
    return () => ctrl.abort();
  }, [id, appId]);

  if (loadError) return <RequestError error={loadError} what="deployment" />;
  if (!d) return <LoadingState what="the deployment" />;

  const o = d.spec.overrides ?? {};
  const cfg = fromFormState(schemas?.environment, config);
  const traitResults = Object.fromEntries(Object.entries(schemas?.traits ?? {}).map(([t, schema]) => [t, fromFormState(schema, traits[t] ?? {})]));
  const errors: Record<string, string> = {};
  if (submitted) {
    const seen = new Set<string>();
    env.forEach((r, i) => {
      if (!envNameRE.test(r.name)) errors[`env-${i}`] = "Letters, digits and underscores, not starting with a digit.";
      else if (seen.has(r.name)) errors[`env-${i}`] = "Already set above.";
      seen.add(r.name);
    });
    for (const [p, e] of Object.entries(cfg.errors)) errors[`config.${p}`] = e;
    for (const [t, r] of Object.entries(traitResults)) for (const [p, e] of Object.entries(r.errors)) errors[`trait.${t}.${p}`] = e;
  }

  // Without the schemas, what's stored is sent back unchanged: the server
  // re-checks only configuration that changed.
  const overrides: DeploymentOverrides = {};
  if (env.length > 0) overrides.env = Object.fromEntries(env.map((r) => [r.name, r.value]));
  const config_ = schemas ? keepUndescribed(o.config, schemas.environment, cfg.values) : o.config;
  if (config_ && Object.keys(config_).length > 0) overrides.config = config_;
  const traits_ = schemas
    ? Object.fromEntries(Object.entries(traitResults).flatMap(([t, r]) => (Object.keys(r.values).length > 0 ? [[t, r.values]] : [])))
    : o.traits;
  if (traits_ && Object.keys(traits_).length > 0) overrides.traits = traits_;
  const request: ApiRequest = { method: "PUT", path: `/v1/deployments/${id}`, body: { ...d, spec: { ...d.spec, overrides } } };
  const back = `${compPath(appId, compId)}?tab=deploy`;
  const where = envName ?? "this environment";

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    const invalid =
      env.some((r, i) => !envNameRE.test(r.name) || env.findIndex((x) => x.name === r.name) !== i) ||
      Object.keys(cfg.errors).length > 0 ||
      Object.values(traitResults).some((r) => Object.keys(r.errors).length > 0);
    if (invalid) {
      requestAnimationFrame(() => summary.current?.focus());
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      await apiFetch<Deployment>("PUT", request.path, request.body);
      navigate(back);
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  const errorCount = Object.keys(errors).length;
  return (
    <>
      <PageHeader
        crumbs={[
          { label: "Applications", to: "/applications" },
          { label: names.app ?? "Application", to: `/applications/${enc(appId)}` },
          { label: names.comp ?? "Component", to: back },
        ]}
        title={`Configure ${where}`}
      />
      <form className="form" onSubmit={submit} noValidate>
        <p>
          These settings apply in {where} only. Promoting a release here keeps them, and promoting from here doesn't carry them on.
        </p>
        {errorCount > 0 && (
          <div ref={summary} tabIndex={-1}>
            <Notice tone="danger">
              {errorCount === 1 ? "One field needs attention." : `${errorCount} fields need attention.`} Nothing was sent.
            </Notice>
          </div>
        )}

        <fieldset>
          <legend>Environment variables</legend>
          <p className="field-hint">Set or replace the component's own variables here. Values are stored and shown in plain text.</p>
          <div className="rows">
            {env.map((r, i) => (
              <div className="row" key={r.key}>
                <TextField
                  id={`ovr-${r.key}-name`}
                  label="Name"
                  mono
                  value={r.name}
                  onChange={(e) => setEnv(env.map((x) => (x.key === r.key ? { ...x, name: e.target.value } : x)))}
                  {...(errors[`env-${i}`] ? { error: errors[`env-${i}`] } : {})}
                />
                <TextField id={`ovr-${r.key}-value`} label="Value" mono value={r.value} onChange={(e) => setEnv(env.map((x) => (x.key === r.key ? { ...x, value: e.target.value } : x)))} />
                <button type="button" className="button" aria-label={`Remove variable ${r.name || i + 1} from this form`} onClick={() => setEnv(env.filter((x) => x.key !== r.key))}>
                  Remove
                </button>
              </div>
            ))}
            <div>
              <button type="button" className="button" onClick={() => setEnv([...env, { key: nextKey(), name: "", value: "" }])}>
                <Icon name="plus" /> Add variable
              </button>
            </div>
          </div>
        </fieldset>

        {schemaError ? (
          <Notice>
            <p>
              The platform's settings couldn't be read ({schemaError.message}), so only environment variables can be changed now. Other settings are kept as
              they are.
            </p>
          </Notice>
        ) : (
          <>
            <fieldset>
              <legend>Settings</legend>
              <SchemaForm
                idPrefix="cfg"
                schema={schemas?.environment}
                state={config}
                errors={Object.fromEntries(Object.entries(errors).flatMap(([k, v]) => (k.startsWith("config.") ? [[k.slice(7), v]] : [])))}
                onChange={(p, v) => setConfig({ ...config, [p]: v })}
              />
            </fieldset>
            {Object.entries(schemas?.traits ?? {}).map(([t, schema]) => (
              <fieldset key={t}>
                <legend>Trait {t}</legend>
                <SchemaForm
                  idPrefix={`trait-${t}`}
                  schema={schema}
                  state={traits[t] ?? {}}
                  errors={Object.fromEntries(Object.entries(errors).flatMap(([k, v]) => (k.startsWith(`trait.${t}.`) ? [[k.slice(t.length + 7), v]] : [])))}
                  onChange={(p, v) => setTraits({ ...traits, [t]: { ...(traits[t] ?? {}), [p]: v } })}
                />
              </fieldset>
            ))}
          </>
        )}

        {failure && <FormFailure error={failure} />}
        <div className="form-actions">
          <button className="button button--primary" type="submit" disabled={busy}>
            {busy ? "Saving…" : "Save changes"}
          </button>
          <Link className="button" to={back}>
            Cancel
          </Link>
        </div>
        <ApiEquivalent request={request} />
      </form>
    </>
  );
}
