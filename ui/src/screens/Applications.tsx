import { useState, type FormEvent } from "react";
import { apiFetch, apiList, ApiError, isShown, NetworkError } from "../api/client";
import type { Application, Component } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { ConditionsTable, SafeLink } from "../components/Data";
import { DeleteConfirm } from "../components/Delete";
import { Guarded } from "../components/Guarded";
import { TextField } from "../components/Field";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { ReadyStatus } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { accessPath, action } from "../lib/permissions";
import { Link, navigate } from "../lib/router";
import { formatAge } from "../lib/time";
import { dnsLabelError, UpdatedLine } from "./common";

export function ApplicationList() {
  const now = useNow();
  const apps = usePoll((s) => apiList<Application>("/v1/applications", s), 15_000, "applications");
  // An application has no owner, so creating one is decided at "/".
  const canCreate = usePermissions({}).can(action("Application", "write"));
  return (
    <>
      <PageHeader
        title="Applications"
        actions={
          canCreate && (
            <Link className="button button--primary" to="/applications/new">
              Create application
            </Link>
          )
        }
        meta={<UpdatedLine at={apps.updatedAt} now={now} />}
      />
      {apps.error ? (
        <RequestError error={apps.error} what="applications" />
      ) : !apps.data ? (
        <LoadingState what="applications" />
      ) : apps.data.length === 0 ? (
        <EmptyState title="No applications yet">An application groups the components you deploy together. Create one to start.</EmptyState>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Status</th>
                <th scope="col">Created</th>
              </tr>
            </thead>
            <tbody>
              {apps.data.map((a) => (
                <tr key={a.metadata.id}>
                  <td>
                    <Link to={`/applications/${encodeURIComponent(a.metadata.id)}`}>{a.metadata.name}</Link>
                  </td>
                  <td>
                    <ReadyStatus conditions={a.status.conditions} />
                  </td>
                  <td>
                    <time dateTime={a.metadata.createdAt} title={a.metadata.createdAt}>
                      {formatAge(a.metadata.createdAt, now)}
                    </time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function NewApplication() {
  const [name, setName] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const nameError = touched ? dnsLabelError(name, "name") : undefined;
  const body = { metadata: { name }, spec: {} };

  async function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (dnsLabelError(name, "name")) return;
    setBusy(true);
    setFailure(undefined);
    try {
      const created = await apiFetch<Application>("POST", "/v1/applications", body);
      navigate(`/applications/${encodeURIComponent(created.metadata.id)}`);
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader crumbs={[{ label: "Applications", to: "/applications" }]} title="Create application" />
      <form className="form" onSubmit={submit} noValidate>
        <TextField
          id="app-name"
          name="name"
          label="Name"
          hint="Lowercase letters, digits and hyphens, for example storefront."
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => setTouched(true)}
          {...(nameError ? { error: nameError } : {})}
          required
        />
        {failure && <FormFailure error={failure} />}
        <div className="form-actions">
          <button className="button button--primary" type="submit" disabled={busy}>
            {busy ? "Creating…" : "Create application"}
          </button>
          <Link className="button" to="/applications">
            Cancel
          </Link>
        </div>
        <ApiEquivalent request={{ method: "POST", path: "/v1/applications", body }} />
      </form>
    </>
  );
}

export function FormFailure({ error }: { error: ApiError | NetworkError }) {
  if (error instanceof NetworkError) return <Notice tone="danger">{error.message} Nothing was saved; try again.</Notice>;
  const detail =
    error.status === 409
      ? "Someone changed this since you opened it. Reload the page to see their change, then edit again."
      : error.status === 413
        ? "That's more data than Serendip accepts in one request (1 MiB)."
        : (error.detail ?? error.title);
  return (
    <Notice tone="danger">
      <p>{detail}</p>
      {error.requestId && (
        <p className="request-id">
          Request ID <span className="mono">{error.requestId}</span>
        </p>
      )}
    </Notice>
  );
}

export function ApplicationDetail({ id }: { id: string }) {
  const now = useNow();
  const app = usePoll((s) => apiFetch<Application>("GET", `/v1/applications/${encodeURIComponent(id)}`, undefined, s), 5_000, `app-${id}`);
  const comps = usePoll((s) => apiList<Component>(`/v1/components?ownerId=${encodeURIComponent(id)}`, s), 5_000, `app-comps-${id}`);
  const scope = { kind: "Application", id };
  const access = usePermissions(scope);
  const canCreate = access.can(action("Component", "write"));
  // Deleting an application takes everything it owns with it, so the
  // server also checks delete on each of those; this is only the first gate.
  const canDelete = access.can(action("Application", "delete"));
  const [deleting, setDeleting] = useState(false);

  if (app.error) return <RequestError error={app.error} what="application" action={<Link to="/applications">Back to applications</Link>} />;
  if (!app.data) return <LoadingState what="the application" />;
  const a = app.data;

  return (
    <>
      <PageHeader
        crumbs={[{ label: "Applications", to: "/applications" }]}
        title={a.metadata.name}
        status={
          <span role="status">
            <ReadyStatus conditions={a.status.conditions} />
          </span>
        }
        actions={
          <>
            <Link className="button" to={accessPath(scope)}>
              Access control (IAM)
            </Link>
            <Link className="button" to={`/secrets?owner=${encodeURIComponent(id)}`}>
              Secrets
            </Link>
            <Guarded allowed={canDelete} label="Delete application" reason="Your roles here don't allow deleting this application.">
              <button type="button" className="button" onClick={() => setDeleting(true)} disabled={deleting}>
                Delete application
              </button>
            </Guarded>
            {canCreate && (
              <Link className="button button--primary" to={`/applications/${encodeURIComponent(id)}/components/new`}>
                Create component
              </Link>
            )}
          </>
        }
        meta={<UpdatedLine at={app.updatedAt} now={now} />}
      />

      {deleting && (
        <DeleteConfirm
          target={{ kind: "Application", id, name: a.metadata.name }}
          cascade
          onDeleted={(approval) => navigate(approval ? `/approvals/${encodeURIComponent(approval)}` : "/applications")}
          onCancel={() => setDeleting(false)}
        />
      )}

      <section className="section" aria-labelledby="app-components">
        <h2 id="app-components" className="section-title">
          Components
        </h2>
        {comps.error ? (
          <RequestError error={comps.error} what="components" />
        ) : !comps.data ? (
          <LoadingState what="components" />
        ) : comps.data.length === 0 ? (
          <EmptyState title="No components yet">A component runs one container image. Add one to deploy something.</EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Image</th>
                  <th scope="col">Status</th>
                  <th scope="col">Endpoint</th>
                </tr>
              </thead>
              <tbody>
                {comps.data.map((c) => (
                  <tr key={c.metadata.id}>
                    <td>
                      <Link to={`/applications/${encodeURIComponent(id)}/components/${encodeURIComponent(c.metadata.id)}`}>{c.metadata.name}</Link>
                    </td>
                    <td className="mono" translate="no">
                      {c.spec.image}
                    </td>
                    <td>
                      <ReadyStatus conditions={c.status.conditions} />
                    </td>
                    <td>{c.status.endpoints?.[0] ? <SafeLink href={c.status.endpoints[0].url} /> : "None"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="section" aria-labelledby="app-conditions">
        <h2 id="app-conditions" className="section-title">
          Conditions
        </h2>
        <ConditionsTable conditions={a.status.conditions} now={now} />
      </section>

      <ApiEquivalent request={{ method: "GET", path: `/v1/applications/${id}` }} />
    </>
  );
}
