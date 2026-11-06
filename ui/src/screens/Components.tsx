import { useEffect, useRef, useState, type FormEvent } from "react";
import { apiFetch, apiList, ApiError, isShown, NetworkError } from "../api/client";
import type { Application, Component, DependenciesView, Secret } from "../api/types";
import { Tabs } from "../components/Tabs";
import { DeployTab } from "./Deploy";
import { BuildsTab, ObservabilityTab, RuntimeTab } from "./Runtime";
import { ApiEquivalent, type ApiRequest } from "../components/ApiEquivalent";
import { ConditionsTable, KeyValueList, SafeLink } from "../components/Data";
import { DeleteConfirm } from "../components/Delete";
import { SelectField, TextField } from "../components/Field";
import { Guarded } from "../components/Guarded";
import { Icon } from "../components/Icon";
import { ReconciliationPath } from "../components/ReconciliationPath";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { ReadyStatus } from "../components/Status";
import { useOptional } from "../hooks/useOptional";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { accessPath, action } from "../lib/permissions";
import { availability } from "../lib/health";
import { emptySource, fromSpec, isSSH, toSpec, validate, type ComponentFormState, type FormErrors, type SourceFields } from "../lib/componentForm";
import { Link, navigate } from "../lib/router";
import { componentStages, exposedPorts } from "../lib/stages";
import { FormFailure } from "../components/RequestError";
import { UpdatedLine } from "./common";

const appPath = (id: string) => `/applications/${encodeURIComponent(id)}`;
const compPath = (appId: string, id: string) => `${appPath(appId)}/components/${encodeURIComponent(id)}`;

const tabs = [
  { id: "overview", label: "Overview" },
  { id: "deploy", label: "Deploy" },
  { id: "runtime", label: "Runtime" },
  { id: "builds", label: "Builds" },
  { id: "observability", label: "Observability" },
];

export function ComponentDetail({ appId, id, tab = "overview" }: { appId: string; id: string; tab?: string }) {
  const now = useNow();
  const comp = usePoll((s) => apiFetch<Component>("GET", `/v1/components/${encodeURIComponent(id)}`, undefined, s), 5_000, `comp-${id}`);
  const app = usePoll((s) => apiFetch<Application>("GET", `/v1/applications/${encodeURIComponent(appId)}`, undefined, s), 5_000, `comp-app-${appId}`);
  const scope = { kind: "Component", id };
  const access = usePermissions(scope);
  const canEdit = access.can(action("Component", "write"));
  const canDelete = access.can(action("Component", "delete"));
  const [deleting, setDeleting] = useState(false);

  if (comp.error) return <RequestError error={comp.error} what="component" action={<Link to={appPath(appId)}>Back to the application</Link>} />;
  if (!comp.data) return <LoadingState what="the component" />;
  const c = comp.data;
  const endpoint = c.status.endpoints?.[0];
  const ports = c.spec.ports ?? [];
  const env = Object.entries(c.spec.env ?? {});

  return (
    <>
      <PageHeader
        crumbs={[
          { label: "Applications", to: "/applications" },
          { label: app.data?.metadata.name ?? "Application", to: appPath(appId) },
        ]}
        title={c.metadata.name}
        status={
          <span role="status">
            <ReadyStatus conditions={c.status.conditions} />
          </span>
        }
        actions={
          <>
            {endpoint && (
              <SafeLink href={endpoint.url}>
                <span className="button">
                  Open endpoint <Icon name="external" />
                </span>
              </SafeLink>
            )}
            <Link className="button" to={accessPath(scope)}>
              Access control (IAM)
            </Link>
            <Guarded allowed={canDelete} label="Delete component" reason="Your roles here don't allow deleting this component.">
              <button type="button" className="button" onClick={() => setDeleting(true)} disabled={deleting}>
                Delete component
              </button>
            </Guarded>
            <Guarded allowed={canEdit} label="Edit component" reason="Your roles here don't allow changing this component.">
              <Link className="button button--primary" to={`${compPath(appId, id)}/edit`}>
                Edit component
              </Link>
            </Guarded>
          </>
        }
        meta={<UpdatedLine at={comp.updatedAt} now={now} />}
      />

      {deleting && (
        <DeleteConfirm
          target={{ kind: "Component", id, name: c.metadata.name }}
          cascade={false}
          onDeleted={(approval) => navigate(approval ? `/approvals/${encodeURIComponent(approval)}` : appPath(appId))}
          onCancel={() => setDeleting(false)}
        />
      )}

      <Tabs label="Component sections" base={compPath(appId, id)} current={tabs.some((t) => t.id === tab) ? tab : "overview"} tabs={tabs} />

      {tab === "deploy" && app.data && <DeployTab app={app.data} comp={c} />}
      {tab === "runtime" && app.data && <RuntimeTab app={app.data} comp={c} />}
      {tab === "builds" && <BuildsTab comp={c} />}
      {tab === "observability" && <ObservabilityTab />}
      {(tab === "deploy" || tab === "runtime") && !app.data && (app.error ? <RequestError error={app.error} what="application" /> : <LoadingState what="the application" />)}
      {!tabs.slice(1).some((t) => t.id === tab) && (
        <>
          <section className="section panel" aria-labelledby="comp-path">
            <h2 id="comp-path" className="section-title">
              Reconciliation
            </h2>
            <ReconciliationPath label="Reconciliation stages" stages={componentStages(c, app.data)} />
          </section>

          <div className="section-row">
            <section className="section" aria-labelledby="comp-config">
              <h2 id="comp-config" className="section-title">
                Configuration
              </h2>
              <KeyValueList
                items={[
                  ...sourceItems(c),
                  ...ports.map((p): [string, React.ReactNode] => [`Port ${p.name}`, `${p.port}, ${p.expose ? "exposed" : "inside the cluster only"}`]),
                  ["Environment", env.length === 0 ? "None" : `${env.length} variable${env.length === 1 ? "" : "s"}`],
                  ...(c.status.endpoints ?? []).map((e): [string, React.ReactNode] => [`Endpoint ${e.name}`, <SafeLink href={e.url} />]),
                ]}
              />
              {exposedPorts(c).length > 0 && <p className="field-hint">Each Environment's endpoints are on the Deploy tab.</p>}
            </section>

            <section className="section" aria-labelledby="comp-conditions">
              <h2 id="comp-conditions" className="section-title">
                Conditions
              </h2>
              <ConditionsTable conditions={c.status.conditions} now={now} />
            </section>
          </div>

          {(c.metadata.relationships?.length ?? 0) > 0 && (
            <section className="section" aria-labelledby="comp-rel">
              <h2 id="comp-rel" className="section-title">
                Relationships
              </h2>
              <KeyValueList items={(c.metadata.relationships ?? []).map((r): [string, React.ReactNode] => [r.type, <span className="mono">{`${r.targetKind} ${r.targetId}`}</span>])} />
            </section>
          )}

          <Dependencies appId={appId} id={id} />

          <ApiEquivalent request={{ method: "GET", path: `/v1/components/${id}` }} />
        </>
      )}
    </>
  );
}

/** Where the image comes from: the image itself, or the source and what it last built. */
function sourceItems(c: Component): Array<[string, React.ReactNode]> {
  const src = c.spec.source;
  const mono = (v: string) => (
    <span className="mono" translate="no">
      {v}
    </span>
  );
  if (!src) return [["Image", mono(c.spec.image)]];
  return [
    ["Built from", mono(src.repository)],
    ["Branch", mono(src.branch || "main")],
    ["Path", mono(src.path || ".")],
    ["Dockerfile", mono(src.dockerfile || "Dockerfile")],
    ["Credential", src.credentialSecret ? <>Secret {mono(src.credentialSecret)}</> : "None (a public repository)"],
    ["Auto-build", src.autoBuild ? "On" : "Off"],
    ["Running image", c.status.image ? mono(c.status.image) : "None yet: no build of this source has succeeded"],
  ];
}

/** What a Component calls, and what in its Application calls it: declared, not observed. */
function Dependencies({ appId, id }: { appId: string; id: string }) {
  const deps = usePoll(
    (s) =>
      apiFetch<Partial<DependenciesView>>("GET", `/v1/components/${encodeURIComponent(id)}/dependencies`, undefined, s).then((v): DependenciesView => ({
        calls: v?.calls ?? [],
        calledBy: v?.calledBy ?? [],
      })),
    15_000,
    `deps-${id}`,
  );
  const comps = usePoll((s) => apiList<Component>(`/v1/components?ownerId=${encodeURIComponent(appId)}`, s), 15_000, `deps-comps-${appId}`);
  const name = (cid: string) => comps.data?.find((c) => c.metadata.id === cid)?.metadata.name ?? cid;
  const link = (cid: string) => <Link to={compPath(appId, cid)}>{name(cid)}</Link>;
  return (
    <section className="section" aria-labelledby="comp-deps">
      <h2 id="comp-deps" className="section-title">
        Dependencies
      </h2>
      {deps.error ? (
        <RequestError error={deps.error} what="dependencies" />
      ) : !deps.data ? (
        <LoadingState what="dependencies" />
      ) : deps.data.calls.length === 0 && deps.data.calledBy.length === 0 ? (
        <p className="state-body">This component declares no calls to others in its application, and none declare calls to it.</p>
      ) : (
        <KeyValueList
          items={[
            ...deps.data.calls.map((l): [string, React.ReactNode] => [
              `Calls ${name(l.to)}`,
              <>
                {link(l.to)} on port {l.port}, address in <span className="mono">{l.env}</span>
              </>,
            ]),
            ...deps.data.calledBy.map((l): [string, React.ReactNode] => [`Called by ${name(l.from)}`, <>{link(l.from)} on port {l.port}</>]),
          ]}
        />
      )}
      <p>
        <Link to={`${appPath(appId)}/topology`}>See the application's topology</Link>
      </p>
    </section>
  );
}

let rowKey = 1000;
const nextKey = () => rowKey++;

export function ComponentForm({ appId, id }: { appId: string; id?: string }) {
  const creating = id === undefined;
  const [state, setState] = useState<ComponentFormState>({ name: "", from: "image", image: "", source: emptySource, ports: [{ key: nextKey(), name: "http", port: "80", expose: true }], env: [] });
  const [existing, setExisting] = useState<Component>();
  const [loadError, setLoadError] = useState<ApiError | NetworkError>();
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const errorSummary = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (creating) return;
    const ctrl = new AbortController();
    apiFetch<Component>("GET", `/v1/components/${encodeURIComponent(id)}`, undefined, ctrl.signal)
      .then((c) => {
        setExisting(c);
        setState(fromSpec(c.metadata.name, c.spec));
      })
      .catch((err) => isShown(err) && setLoadError(err));
    return () => ctrl.abort();
  }, [creating, id]);

  const errors: FormErrors = submitted ? validate(state, { creating }) : {};
  const spec = toSpec(state);
  const request: ApiRequest = creating
    ? { method: "POST", path: "/v1/components", body: { metadata: { name: state.name.trim(), ownerRefs: [{ kind: "Application", id: appId }] }, spec } }
    : { method: "PUT", path: `/v1/components/${id}`, body: existing ? { ...existing, spec } : undefined };

  if (loadError) return <RequestError error={loadError} what="component" />;
  if (!creating && !existing) return <LoadingState what="the component" />;

  function update(patch: Partial<ComponentFormState>) {
    setState((s) => ({ ...s, ...patch }));
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (Object.keys(validate(state, { creating })).length > 0) {
      requestAnimationFrame(() => errorSummary.current?.focus());
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      const saved = await apiFetch<Component>(request.method as "POST" | "PUT", request.path, request.body);
      navigate(compPath(appId, saved.metadata.id));
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
          { label: "Application", to: appPath(appId) },
        ]}
        title={creating ? "Create component" : `Edit ${existing?.metadata.name ?? "component"}`}
      />
      <form className="form" onSubmit={submit} noValidate>
        {errorCount > 0 && (
          <div ref={errorSummary} tabIndex={-1}>
            <Notice tone="danger">
              {errorCount === 1 ? "One field needs attention." : `${errorCount} fields need attention.`} Nothing was sent.
            </Notice>
          </div>
        )}

        {creating && (
          <TextField
            id="comp-name"
            label="Name"
            hint="Lowercase letters, digits and hyphens, for example web."
            value={state.name}
            onChange={(e) => update({ name: e.target.value })}
            {...(errors.name ? { error: errors.name } : {})}
            required
          />
        )}
        <fieldset>
          <legend>Image</legend>
          <div className="rows">
            <label className="checkbox">
              <input type="radio" name="comp-from" checked={state.from === "image"} onChange={() => update({ from: "image" })} />
              A ready-made image
            </label>
            <label className="checkbox">
              <input type="radio" name="comp-from" checked={state.from === "source"} onChange={() => update({ from: "source" })} />
              Built from source in Git
            </label>
            {state.from === "image" ? (
              <TextField
                id="comp-image"
                label="Image"
                hint="An image that's already built and pullable, for example nginx:1.27."
                mono
                value={state.image}
                onChange={(e) => update({ image: e.target.value })}
                {...(errors.image ? { error: errors.image } : {})}
                required
              />
            ) : (
              <SourceForm source={state.source} errors={errors} unchanged={!creating && sameSource(existing, state.source)} onChange={(patch) => update({ source: { ...state.source, ...patch } })} />
            )}
          </div>
        </fieldset>

        <fieldset>
          <legend>Ports</legend>
          <div className="rows">
            {state.ports.length === 0 && <p className="field-hint">No ports. The component runs but nothing can connect to it.</p>}
            {state.ports.map((p, i) => (
              <div className="row" key={p.key}>
                <TextField
                  id={`port-${p.key}-name`}
                  label="Port name"
                  value={p.name}
                  onChange={(e) => update({ ports: state.ports.map((x) => (x.key === p.key ? { ...x, name: e.target.value } : x)) })}
                  {...(errors[`port-${i}-name`] ? { error: errors[`port-${i}-name`] } : {})}
                />
                <TextField
                  id={`port-${p.key}-port`}
                  label="Port"
                  inputMode="numeric"
                  value={p.port}
                  onChange={(e) => update({ ports: state.ports.map((x) => (x.key === p.key ? { ...x, port: e.target.value } : x)) })}
                  {...(errors[`port-${i}-port`] ? { error: errors[`port-${i}-port`] } : {})}
                />
                <label className="checkbox">
                  <input type="checkbox" checked={p.expose} onChange={(e) => update({ ports: state.ports.map((x) => (x.key === p.key ? { ...x, expose: e.target.checked } : x)) })} />
                  Expose outside the cluster
                </label>
                <button type="button" className="button" aria-label={`Remove port ${p.name || i + 1} from this form`} onClick={() => update({ ports: state.ports.filter((x) => x.key !== p.key) })}>
                  Remove
                </button>
              </div>
            ))}
            <div>
              <button type="button" className="button" onClick={() => update({ ports: [...state.ports, { key: nextKey(), name: "", port: "", expose: false }] })}>
                <Icon name="plus" /> Add port
              </button>
            </div>
          </div>
        </fieldset>

        <fieldset>
          <legend>Environment variables</legend>
          <p className="field-hint">Values are stored and shown in plain text. Don't put secrets here: secret injection isn't available yet.</p>
          <div className="rows">
            {state.env.map((v, i) => (
              <div className="row" key={v.key}>
                <TextField
                  id={`env-${v.key}-name`}
                  label="Name"
                  mono
                  value={v.name}
                  onChange={(e) => update({ env: state.env.map((x) => (x.key === v.key ? { ...x, name: e.target.value } : x)) })}
                  {...(errors[`env-${i}-name`] ? { error: errors[`env-${i}-name`] } : {})}
                />
                <TextField id={`env-${v.key}-value`} label="Value" mono value={v.value} onChange={(e) => update({ env: state.env.map((x) => (x.key === v.key ? { ...x, value: e.target.value } : x)) })} />
                <button type="button" className="button" aria-label={`Remove variable ${v.name || i + 1} from this form`} onClick={() => update({ env: state.env.filter((x) => x.key !== v.key) })}>
                  Remove
                </button>
              </div>
            ))}
            <div>
              <button type="button" className="button" onClick={() => update({ env: [...state.env, { key: nextKey(), name: "", value: "" }] })}>
                <Icon name="plus" /> Add variable
              </button>
            </div>
          </div>
        </fieldset>

        {failure && <FormFailure error={failure} />}
        <div className="form-actions">
          <button className="button button--primary" type="submit" disabled={busy}>
            {busy ? (creating ? "Creating…" : "Saving…") : creating ? "Create component" : "Save changes"}
          </button>
          <Link className="button" to={creating ? appPath(appId) : compPath(appId, id)}>
            Cancel
          </Link>
        </div>
        <ApiEquivalent request={request} />
      </form>
    </>
  );
}

/** Whether the form's source is the one stored: the server doesn't recheck it then. */
function sameSource(c: Component | undefined, f: SourceFields): boolean {
  const s = c?.spec.source;
  if (!s) return false;
  return (
    s.repository === f.repository.trim() &&
    (s.branch ?? "") === f.branch.trim() &&
    (s.path ?? "") === f.path.trim() &&
    (s.dockerfile ?? "") === f.dockerfile.trim() &&
    (s.credentialSecret ?? "") === f.credentialSecret &&
    Boolean(s.autoBuild) === f.autoBuild
  );
}

/**
 * SourceForm: the Git repository a Component is built from (spec 022), a
 * credential Secret for a private one, and auto-build. A new or changed
 * source needs the builds package ready; the server refuses it otherwise,
 * so the form says so first.
 */
function SourceForm({ source, errors, unchanged, onChange }: { source: SourceFields; errors: FormErrors; unchanged: boolean; onChange: (patch: Partial<SourceFields>) => void }) {
  const opt = useOptional("build");
  const state = availability(opt.readiness);
  const secrets = usePoll((s) => apiList<Secret>("/v1/secrets", s), 60_000, "source-secrets");
  const known = secrets.data ?? [];
  // A Secret named already but not listed (not readable, or deleted) stays
  // selectable, so saving the form doesn't silently drop it.
  const options = [
    { value: "", label: "None: a public repository" },
    ...known.map((x) => ({ value: x.metadata.id, label: x.metadata.name })),
    ...(source.credentialSecret && !known.some((x) => x.metadata.id === source.credentialSecret) ? [{ value: source.credentialSecret, label: `Secret ${source.credentialSecret}` }] : []),
  ];
  return (
    <>
      {opt.loaded && state !== "ready" && !unchanged && (
        <Notice>
          Building from source needs the {opt.packageName} package, which {state === "installing" ? "is still installing" : state === "notReady" ? "isn't ready" : "isn't installed"}. Serendip refuses a new or changed source until it's ready. <Link to="/packages">See packages</Link>
        </Notice>
      )}
      <TextField
        id="src-repository"
        label="Repository"
        hint="The Git repository's https:// or SSH URL, for example https://github.com/org/app.git."
        mono
        value={source.repository}
        onChange={(e) => onChange({ repository: e.target.value })}
        {...(errors.repository ? { error: errors.repository } : {})}
        required
      />
      <TextField id="src-branch" label="Branch (optional)" hint="Defaults to main." mono value={source.branch} onChange={(e) => onChange({ branch: e.target.value })} {...(errors.branch ? { error: errors.branch } : {})} />
      <TextField
        id="src-path"
        label="Path (optional)"
        hint="The folder to build, inside the repository. Defaults to its root."
        mono
        value={source.path}
        onChange={(e) => onChange({ path: e.target.value })}
        {...(errors.path ? { error: errors.path } : {})}
      />
      <TextField
        id="src-dockerfile"
        label="Dockerfile (optional)"
        hint="Relative to the path. Defaults to Dockerfile."
        mono
        value={source.dockerfile}
        onChange={(e) => onChange({ dockerfile: e.target.value })}
        {...(errors.dockerfile ? { error: errors.dockerfile } : {})}
      />
      <SelectField
        id="src-credential"
        label="Credential"
        hint="For a private repository: a Secret holding the user name and password or token. It's read only for the build and never shown back. Naming one needs permission to read its value."
        value={source.credentialSecret}
        onChange={(e) => onChange({ credentialSecret: e.target.value })}
        options={options}
      />
      {secrets.error && <p className="field-hint">The list of Secrets couldn't be read, so only "None" and the Secret already named are offered.</p>}
      <label className="checkbox">
        <input type="checkbox" checked={source.autoBuild} onChange={(e) => onChange({ autoBuild: e.target.checked })} />
        Auto-build: build each new commit on the branch
      </label>
      <p className="field-hint">
        {source.autoBuild && isSSH(source.repository)
          ? "Auto-build can't see new commits on an SSH URL: use the https:// URL, or trigger builds yourself."
          : "Serendip checks the branch for new commits every 5 minutes; no webhook is needed. If it can't reach the repository, the component says so."}
      </p>
    </>
  );
}

export function NotFound() {
  return <EmptyState title="Page not found" action={<Link to="/">Go to the overview</Link>}>There's nothing at this address in the console.</EmptyState>;
}
