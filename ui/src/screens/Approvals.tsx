import { useState } from "react";
import { apiFetch, apiPage, ApiError, isShown, type NetworkError } from "../api/client";
import type { Approval, ApprovalActor, SessionInfo } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { ConditionsTable, KeyValueList } from "../components/Data";
import { BlastRadius } from "../components/Delete";
import { TextField } from "../components/Field";
import { Guarded } from "../components/Guarded";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { findCondition, StatusText, type StatusKind } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { action } from "../lib/permissions";
import { Link } from "../lib/router";
import { formatAge } from "../lib/time";
import { FormFailure } from "./Applications";
import { UpdatedLine } from "./common";

// Approvals (spec 019): every destructive action's record. Policy approves
// what the requester's roles allow; the rest (today, agents' deletes) waits
// here for a person allowed to do it themselves.

const approvalPath = (id: string) => `/approvals/${encodeURIComponent(id)}`;

/** An approval's state in the status vocabulary (ui/DESIGN.md). */
export function ApprovalStatusText({ a }: { a: Approval }) {
  const verified = findCondition(a.status.conditions, "Verified");
  const [kind, word]: [StatusKind, string] =
    a.status.phase === "Pending"
      ? ["progress", "Waiting for a decision"]
      : a.status.phase === "Approved"
        ? ["progress", "Approved, running"]
        : a.status.phase === "Executed"
          ? verified?.status === "True"
            ? ["ok", "Done"]
            : verified?.status === "False"
              ? ["warn", "Done, not verified"]
              : ["progress", "Done, cleaning up"]
          : a.status.phase === "Failed"
            ? ["error", "Failed"]
            : a.status.phase === "Rejected"
              ? ["unknown", "Rejected"]
              : ["unknown", "Expired"];
  return <StatusText kind={kind}>{word}</StatusText>;
}

/** "Delete component web", and "and everything it owns" for a cascade. */
export function actionText(a: Approval): string {
  const t = a.spec.action.target;
  return `Delete ${(t.kind ?? "").toLowerCase()} ${a.status.targetName ?? t.id ?? ""}${a.spec.action.cascade ? " and everything it owns" : ""}`;
}

/** An actor as people read it: "agent:am/a1" stays; "person:" is dropped. */
const who = (actor: ApprovalActor | undefined) => (actor ? actor.actor.replace(/^person:/, "") : "");

function ApprovalRows({ items, now }: { items: Approval[]; now: Date }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Action</th>
            <th scope="col">Requested by</th>
            <th scope="col">Reason</th>
            <th scope="col">Status</th>
            <th scope="col">Requested</th>
          </tr>
        </thead>
        <tbody>
          {items.map((a) => (
            <tr key={a.metadata.id}>
              <td>
                <Link to={approvalPath(a.metadata.id)}>{actionText(a)}</Link>
              </td>
              <td>
                {who(a.status.requestedBy)}
                {a.status.requestedBy.sponsor && <span className="field-hint"> Sponsor: {a.status.requestedBy.sponsor}</span>}
              </td>
              <td>{a.spec.reason ?? ""}</td>
              <td>
                <ApprovalStatusText a={a} />
              </td>
              <td>
                <time dateTime={a.status.requestedAt} title={a.status.requestedAt}>
                  {formatAge(a.status.requestedAt, now)}
                </time>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ApprovalList() {
  const now = useNow();
  const pending = usePoll((s) => apiPage<Approval>("/v1/approvals?phase=Pending", s), 10_000, "approvals-pending");
  const recent = usePoll((s) => apiPage<Approval>("/v1/approvals?limit=50", s), 15_000, "approvals-recent");
  const [older, setOlder] = useState<{ items: Approval[]; next: string | undefined }>();
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState<ApiError | NetworkError>();
  const next = older ? older.next : recent.data?.next;
  const decided = [...(recent.data?.items ?? []), ...(older?.items ?? [])].filter((a) => a.status.phase !== "Pending");

  async function showOlder() {
    if (!next) return;
    setLoadingOlder(true);
    setOlderError(undefined);
    try {
      const page = await apiPage<Approval>(`/v1/approvals?limit=50&after=${encodeURIComponent(next)}`);
      setOlder((o) => ({ items: [...(o?.items ?? []), ...page.items], next: page.next }));
    } catch (err) {
      if (isShown(err)) setOlderError(err);
    }
    setLoadingOlder(false);
  }

  return (
    <>
      <PageHeader title="Approvals" meta={<UpdatedLine at={pending.updatedAt} now={now} />} />
      <p className="state-body">Every delete is recorded here. Your roles approve what they allow at once; requests that need a person wait below.</p>

      <section className="section" aria-labelledby="approvals-pending">
        <h2 id="approvals-pending" className="section-title">
          Waiting for a decision
        </h2>
        {pending.error ? (
          <RequestError error={pending.error} what="pending approvals" />
        ) : !pending.data ? (
          <LoadingState what="pending approvals" />
        ) : pending.data.items.length === 0 ? (
          <EmptyState title="Nothing is waiting">Requests that need a person to approve them appear here.</EmptyState>
        ) : (
          <ApprovalRows items={pending.data.items} now={now} />
        )}
      </section>

      <section className="section" aria-labelledby="approvals-recent">
        <h2 id="approvals-recent" className="section-title">
          Decided
        </h2>
        {recent.error ? (
          <RequestError error={recent.error} what="approvals" />
        ) : !recent.data ? (
          <LoadingState what="approvals" />
        ) : decided.length === 0 ? (
          <EmptyState title="No decided approvals you can see" />
        ) : (
          <ApprovalRows items={decided} now={now} />
        )}
        {olderError && <FormFailure error={olderError} />}
        {next && (
          <div className="form-actions">
            <button type="button" className="button" onClick={showOlder} disabled={loadingOlder}>
              {loadingOlder ? "Loading…" : "Show older"}
            </button>
          </div>
        )}
      </section>

      <ApiEquivalent request={{ method: "GET", path: "/v1/approvals?phase=Pending" }} />
    </>
  );
}

export function ApprovalDetail({ id }: { id: string }) {
  const now = useNow();
  const approval = usePoll((s) => apiFetch<Approval>("GET", `/v1/approvals/${encodeURIComponent(id)}`, undefined, s), 3_000, `approval-${id}`);
  const session = usePoll((s) => apiFetch<SessionInfo>("GET", "/v1/session", undefined, s), 300_000, "approval-session");
  const target = approval.data?.spec.action.target ?? {};
  const canDelete = usePermissions(target).can(action(target.kind ?? "", "delete"));
  const [deciding, setDeciding] = useState<"approve" | "reject">();

  if (approval.error) return <RequestError error={approval.error} what="approval" action={<Link to="/approvals">Back to approvals</Link>} />;
  if (!approval.data) return <LoadingState what="the approval" />;
  const a = approval.data;
  const st = a.status;
  const isRequester = session.data !== undefined && session.data.actor === st.requestedBy.actor;
  // Offered only to someone who may decide: not the requester, and allowed
  // the delete themselves (with a cascade, the server also checks below).
  const mayDecide = session.data === undefined || canDelete === undefined ? undefined : !isRequester && canDelete;
  const reason = isRequester ? "You asked for this, so someone else decides it." : "Deciding needs your roles to allow this delete yourself.";
  const d = st.decision;

  return (
    <>
      <PageHeader
        crumbs={[{ label: "Approvals", to: "/approvals" }]}
        title={actionText(a)}
        status={
          <span role="status">
            <ApprovalStatusText a={a} />
          </span>
        }
        actions={
          st.phase === "Pending" && (
            <>
              <Guarded allowed={mayDecide} label="Approve" reason={reason}>
                <button type="button" className="button button--primary" onClick={() => setDeciding("approve")} disabled={deciding !== undefined}>
                  Approve
                </button>
              </Guarded>
              <Guarded allowed={mayDecide} label="Reject" reason={reason}>
                <button type="button" className="button" onClick={() => setDeciding("reject")} disabled={deciding !== undefined}>
                  Reject
                </button>
              </Guarded>
            </>
          )
        }
        meta={<UpdatedLine at={approval.updatedAt} now={now} />}
      />

      {deciding && st.phase === "Pending" && (
        <Decide
          approval={a}
          verb={deciding}
          onDone={() => {
            setDeciding(undefined);
            approval.refresh();
          }}
          onCancel={() => setDeciding(undefined)}
        />
      )}

      <div className="section-row">
        <section className="section" aria-labelledby="approval-request">
          <h2 id="approval-request" className="section-title">
            Request
          </h2>
          <KeyValueList
            items={[
              ["Requested by", who(st.requestedBy)],
              ...(st.requestedBy.sponsor ? [["Sponsor", st.requestedBy.sponsor] as [string, React.ReactNode]] : []),
              ["Reason", a.spec.reason || "None given"],
              [
                "Requested",
                <time dateTime={st.requestedAt} title={st.requestedAt}>
                  {formatAge(st.requestedAt, now)}
                </time>,
              ],
              ...(st.expiresAt
                ? [
                    [
                      "Expires",
                      <time dateTime={st.expiresAt} title={st.expiresAt}>
                        {st.expiresAt}
                      </time>,
                    ] as [string, React.ReactNode],
                  ]
                : []),
              [
                "Target",
                <span className="mono" translate="no">
                  {`${target.kind ?? ""} ${target.id ?? ""}`}
                </span>,
              ],
            ]}
          />
        </section>
        <section className="section" aria-labelledby="approval-decision">
          <h2 id="approval-decision" className="section-title">
            Decision
          </h2>
          {!d ? (
            <p className="state-body">Not decided yet.</p>
          ) : (
            <KeyValueList
              items={[
                ["Decided by", d.byPolicy ? "Policy: the requester's roles allow it" : who(d.by)],
                [
                  "When",
                  <time dateTime={d.at} title={d.at}>
                    {formatAge(d.at, now)}
                  </time>,
                ],
                ...(d.comment ? [["Comment", d.comment] as [string, React.ReactNode]] : []),
                ...(d.evidence ? [["Evidence", <span className="mono">{d.evidence}</span>] as [string, React.ReactNode]] : []),
              ]}
            />
          )}
        </section>
      </div>

      <section className="section" aria-labelledby="approval-radius">
        <h2 id="approval-radius" className="section-title">
          {st.phase === "Executed" ? "What it removed" : "What it removes"}
        </h2>
        {(st.blastRadius?.length ?? 0) === 0 ? <p className="state-body">Nothing recorded.</p> : <BlastRadius refs={st.blastRadius ?? []} caption="Resources this approval removes" />}
      </section>

      <section className="section" aria-labelledby="approval-conditions">
        <h2 id="approval-conditions" className="section-title">
          Conditions
        </h2>
        <ConditionsTable conditions={st.conditions} now={now} />
      </section>

      <ApiEquivalent request={{ method: "GET", path: `/v1/approvals/${id}` }} />
    </>
  );
}

/** Decide confirms an approve or reject, with an optional comment, and shows the request. */
function Decide({ approval, verb, onDone, onCancel }: { approval: Approval; verb: "approve" | "reject"; onDone: () => void; onCancel: () => void }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const path = `/v1/approvals/${encodeURIComponent(approval.metadata.id)}/${verb}`;
  const body = comment.trim() ? { comment: comment.trim() } : undefined;

  async function submit() {
    setBusy(true);
    setFailure(undefined);
    try {
      await apiFetch<Approval>("POST", path, body);
      onDone();
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  return (
    <div className="panel" role="group" aria-label={verb === "approve" ? "Confirm approving" : "Confirm rejecting"}>
      <p>
        {verb === "approve"
          ? `${actionText(approval)}? Serendip carries it out at once if nothing changed since the request; if anything did, it fails and nothing is deleted.`
          : `Reject this request? Nothing is deleted, and it can't be approved later.`}
      </p>
      <TextField id="decision-comment" label="Comment" hint="Optional. Recorded with your decision." value={comment} onChange={(e) => setComment(e.target.value)} maxLength={1024} />
      {failure &&
        (failure instanceof ApiError && failure.status === 409 ? (
          <Notice tone="danger">
            <p>{failure.detail ?? failure.title}</p>
          </Notice>
        ) : (
          <FormFailure error={failure} />
        ))}
      <div className="form-actions">
        <button type="button" className="button button--primary" onClick={submit} disabled={busy}>
          {busy ? (verb === "approve" ? "Approving…" : "Rejecting…") : verb === "approve" ? "Approve" : "Reject"}
        </button>
        <button type="button" className="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
      <ApiEquivalent request={body ? { method: "POST", path, body } : { method: "POST", path }} />
    </div>
  );
}
