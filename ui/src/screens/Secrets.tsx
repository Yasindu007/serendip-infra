import { useState } from "react";
import { apiFetch, apiList } from "../api/client";
import type { Application, Condition, OwnerRef, Secret } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { ConditionsTable, KeyValueList } from "../components/Data";
import { DeleteConfirm } from "../components/Delete";
import { Guarded } from "../components/Guarded";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState } from "../components/States";
import { conditionKind, findCondition, StatusText } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { accessPath, action } from "../lib/permissions";
import { Link, navigate } from "../lib/router";
import { formatAge } from "../lib/time";
import { UpdatedLine } from "./common";

// Secrets (spec 019): what exists, where it belongs, and whether its value
// is stored. Values are never read or shown here: that's a separate data
// permission (serendip/secrets/value/read) and its own spec.

const secretPath = (id: string) => `/secrets/${encodeURIComponent(id)}`;

/** Whether the Secrets capability holds a value for it ("Reachable"). */
export function ValueStatus({ conditions }: { conditions: Condition[] | undefined }) {
  const c = findCondition(conditions, "Reachable");
  const kind = conditionKind(c);
  const word = !c ? "Not checked yet" : kind === "ok" ? "Value stored" : kind === "progress" ? "Checking" : kind === "error" ? "Failed" : "No value";
  return <StatusText kind={c ? kind : "unknown"}>{word}</StatusText>;
}

/** Owner links to its page when the console has one. */
function Owner({ owner, apps }: { owner: OwnerRef | undefined; apps: Application[] | undefined }) {
  if (!owner) return <>None</>;
  if (owner.kind === "Application") {
    const name = apps?.find((a) => a.metadata.id === owner.id)?.metadata.name;
    return <Link to={`/applications/${encodeURIComponent(owner.id)}`}>{name ?? owner.id}</Link>;
  }
  return <span className="mono">{`${owner.kind} ${owner.id}`}</span>;
}

export function SecretList({ ownerId }: { ownerId?: string | undefined }) {
  const now = useNow();
  const path = ownerId ? `/v1/secrets?ownerId=${encodeURIComponent(ownerId)}` : "/v1/secrets";
  const secrets = usePoll((s) => apiList<Secret>(path, s), 15_000, `secrets-${ownerId ?? ""}`);
  const apps = usePoll((s) => apiList<Application>("/v1/applications", s), 60_000, "secrets-apps");
  const ownerName = ownerId ? apps.data?.find((a) => a.metadata.id === ownerId)?.metadata.name : undefined;

  return (
    <>
      <PageHeader
        {...(ownerId ? { crumbs: [{ label: "Secrets", to: "/secrets" }] } : {})}
        title={ownerId ? `Secrets of ${ownerName ?? "an application"}` : "Secrets"}
        meta={<UpdatedLine at={secrets.updatedAt} now={now} />}
      />
      <p className="state-body">Values aren't shown here. Reading or changing a value needs its own permission, through the API.</p>
      {secrets.error ? (
        <RequestError error={secrets.error} what="secrets" />
      ) : !secrets.data ? (
        <LoadingState what="secrets" />
      ) : secrets.data.length === 0 ? (
        <EmptyState title="No secrets you can see">A Secret names a value held by the secrets store. Secrets are created through the API.</EmptyState>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Path</th>
                <th scope="col">Owner</th>
                <th scope="col">Value</th>
                <th scope="col">Created</th>
              </tr>
            </thead>
            <tbody>
              {secrets.data.map((s) => (
                <tr key={s.metadata.id}>
                  <td>
                    <Link to={secretPath(s.metadata.id)}>{s.metadata.name}</Link>
                  </td>
                  <td className="mono" translate="no">
                    {s.spec.path}
                  </td>
                  <td>
                    <Owner owner={s.metadata.ownerRefs?.[0]} apps={apps.data} />
                  </td>
                  <td>
                    <ValueStatus conditions={s.status.conditions} />
                  </td>
                  <td>
                    <time dateTime={s.metadata.createdAt} title={s.metadata.createdAt}>
                      {formatAge(s.metadata.createdAt, now)}
                    </time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ApiEquivalent request={{ method: "GET", path }} />
    </>
  );
}

export function SecretDetail({ id }: { id: string }) {
  const now = useNow();
  const secret = usePoll((s) => apiFetch<Secret>("GET", `/v1/secrets/${encodeURIComponent(id)}`, undefined, s), 10_000, `secret-${id}`);
  const apps = usePoll((s) => apiList<Application>("/v1/applications", s), 60_000, "secret-apps");
  const scope = { kind: "Secret", id };
  const canDelete = usePermissions(scope).can(action("Secret", "delete"));
  const [deleting, setDeleting] = useState(false);

  if (secret.error) return <RequestError error={secret.error} what="secret" action={<Link to="/secrets">Back to secrets</Link>} />;
  if (!secret.data) return <LoadingState what="the secret" />;
  const s = secret.data;

  return (
    <>
      <PageHeader
        crumbs={[{ label: "Secrets", to: "/secrets" }]}
        title={s.metadata.name}
        status={
          <span role="status">
            <ValueStatus conditions={s.status.conditions} />
          </span>
        }
        actions={
          <>
            <Link className="button" to={accessPath(scope)}>
              Access control (IAM)
            </Link>
            <Guarded allowed={canDelete} label="Delete secret" reason="Your roles here don't allow deleting this secret.">
              <button type="button" className="button" onClick={() => setDeleting(true)} disabled={deleting}>
                Delete secret
              </button>
            </Guarded>
          </>
        }
        meta={<UpdatedLine at={secret.updatedAt} now={now} />}
      />

      {deleting && (
        <DeleteConfirm
          target={{ kind: "Secret", id, name: s.metadata.name }}
          cascade={false}
          onDeleted={(approval) => navigate(approval ? `/approvals/${encodeURIComponent(approval)}` : "/secrets")}
          onCancel={() => setDeleting(false)}
        />
      )}

      <div className="section-row">
        <section className="section" aria-labelledby="secret-config">
          <h2 id="secret-config" className="section-title">
            Configuration
          </h2>
          <KeyValueList
            items={[
              [
                "Path",
                <span className="mono" translate="no">
                  {s.spec.path}
                </span>,
              ],
              ["Owner", <Owner owner={s.metadata.ownerRefs?.[0]} apps={apps.data} />],
              [
                "ID",
                <span className="mono" translate="no">
                  {s.metadata.id}
                </span>,
              ],
            ]}
          />
        </section>
        <section className="section" aria-labelledby="secret-conditions">
          <h2 id="secret-conditions" className="section-title">
            Conditions
          </h2>
          <ConditionsTable conditions={s.status.conditions} now={now} />
        </section>
      </div>

      <ApiEquivalent request={{ method: "GET", path: `/v1/secrets/${id}` }} />
    </>
  );
}
