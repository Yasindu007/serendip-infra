import { useEffect, useState } from "react";
import { apiDelete, apiFetch, ApiError, isShown, type NetworkError } from "../api/client";
import type { ApprovalRef, GraphRef, Metadata } from "../api/types";
import { ApiEquivalent } from "./ApiEquivalent";
import { Notice } from "./States";

// Deleting goes through Approvals (constitution 5, spec 019): every delete
// records one, decided by policy when the caller's roles allow it. These
// components show what a delete removes before it happens, and the request
// it sends.

/** The API path segment of each kind the console can name. */
const kindPaths: Record<string, string> = {
  Application: "applications",
  Component: "components",
  Secret: "secrets",
  Volume: "volumes",
};

export const resourcePath = (kind: string, id: string) => `/v1/${kindPaths[kind] ?? kind.toLowerCase()}/${encodeURIComponent(id)}`;

/** BlastRadius lists what an action removes: kind, name and ID, the target first. */
export function BlastRadius({ refs, caption }: { refs: ApprovalRef[]; caption: string }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <caption className="visually-hidden">{caption}</caption>
        <thead>
          <tr>
            <th scope="col">Kind</th>
            <th scope="col">Name</th>
            <th scope="col">ID</th>
          </tr>
        </thead>
        <tbody>
          {refs.map((r) => (
            <tr key={`${r.kind}/${r.id}`}>
              <td>{r.kind}</td>
              <td>{r.name ?? ""}</td>
              <td className="mono" translate="no">
                {r.id}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * useRemoved walks what deleting target would remove: the target, and with
 * cascade everything it owns, by the graph API. Resources the caller can't
 * read aren't listed (the server hides them), and deleting them needs
 * delete there anyway, which the server checks.
 */
function useRemoved(target: ApprovalRef, cascade: boolean): { refs?: ApprovalRef[]; error?: ApiError | NetworkError } {
  const [state, setState] = useState<{ refs?: ApprovalRef[]; error?: ApiError | NetworkError }>({});
  useEffect(() => {
    const ctl = new AbortController();
    async function walk(): Promise<ApprovalRef[]> {
      const out: ApprovalRef[] = [target];
      if (!cascade) return out;
      const seen = new Set([`${target.kind}/${target.id}`]);
      for (let i = 0; i < out.length; i++) {
        const node = out[i]!;
        const children = (await apiFetch<GraphRef[] | null>("GET", `/v1/graph/${encodeURIComponent(node.kind)}/${encodeURIComponent(node.id)}/children`, undefined, ctl.signal)) ?? [];
        for (const c of children) {
          const key = `${c.Kind}/${c.ID}`;
          if (seen.has(key)) continue;
          seen.add(key);
          let name: string | undefined;
          if (kindPaths[c.Kind]) {
            const r = await apiFetch<{ metadata: Metadata }>("GET", resourcePath(c.Kind, c.ID), undefined, ctl.signal).catch(() => undefined);
            name = r?.metadata.name;
          }
          out.push(name === undefined ? { kind: c.Kind, id: c.ID } : { kind: c.Kind, id: c.ID, name });
        }
      }
      return out;
    }
    walk()
      .then((refs) => setState({ refs }))
      .catch((err) => !ctl.signal.aborted && isShown(err) && setState({ error: err }));
    return () => ctl.abort();
  }, [target.kind, target.id, target.name, cascade]);
  return state;
}

/**
 * DeleteConfirm asks before deleting, showing everything the delete
 * removes and the request it sends. The server records an Approval with
 * the delete; onDeleted gets its ID.
 */
export function DeleteConfirm({
  target,
  cascade,
  onDeleted,
  onCancel,
}: {
  target: ApprovalRef;
  cascade: boolean;
  onDeleted: (approvalId: string | undefined) => void;
  onCancel: () => void;
}) {
  const removed = useRemoved(target, cascade);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const path = resourcePath(target.kind, target.id) + (cascade ? "?cascade=true" : "");
  const noun = target.kind.toLowerCase();
  const secrets = (removed.refs ?? []).filter((r) => r.kind === "Secret").length;

  async function remove() {
    setBusy(true);
    setFailure(undefined);
    try {
      onDeleted(await apiDelete(path));
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  return (
    <div className="panel" role="group" aria-label={`Confirm deleting ${target.name ?? target.id}`}>
      <p>
        Delete {noun} {target.name ?? target.id}?{" "}
        {cascade ? "It goes, together with everything it owns, listed below." : "This removes it from Serendip."} The delete is recorded as an
        approval, decided by your roles, and can't be undone.
      </p>
      {removed.error ? (
        <Notice tone="danger">
          <p>What this would remove couldn't be listed: {removed.error.message}</p>
        </Notice>
      ) : !removed.refs ? (
        <p className="field-hint" role="status">
          Listing what this removes…
        </p>
      ) : (
        <BlastRadius refs={removed.refs} caption={`What deleting ${target.name ?? target.id} removes`} />
      )}
      {secrets > 0 && (
        <p className="field-hint">
          {secrets === 1 ? "The Secret's value is" : `All ${secrets} Secrets' values are`} erased from the secrets store shortly after the delete.
          The approval shows when that's done.
        </p>
      )}
      {failure && <DeleteFailure error={failure} />}
      <div className="form-actions">
        <button type="button" className="button button--primary" onClick={remove} disabled={busy || !removed.refs}>
          {busy ? "Deleting…" : `Delete ${noun}`}
        </button>
        <button type="button" className="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
      <ApiEquivalent request={{ method: "DELETE", path }} />
    </div>
  );
}

function DeleteFailure({ error }: { error: ApiError | NetworkError }) {
  if (!(error instanceof ApiError)) return <Notice tone="danger">{error.message} Nothing was deleted; try again.</Notice>;
  const detail =
    error.status === 409
      ? error.title === "resource has children"
        ? "It still owns other resources. Delete those first."
        : "It changed since you opened it. Reload the page and try again."
      : error.status === 403
        ? (error.detail ?? "Your roles don't allow deleting it.")
        : (error.detail ?? error.title);
  return (
    <Notice tone="danger">
      <p>{detail} Nothing was deleted.</p>
      {error.requestId && (
        <p className="request-id">
          Request ID <span className="mono">{error.requestId}</span>
        </p>
      )}
    </Notice>
  );
}
