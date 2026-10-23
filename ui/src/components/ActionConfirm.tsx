import { useState, type ReactNode } from "react";
import { apiFetch, isShown, type ApiError, type NetworkError } from "../api/client";
import { ApiEquivalent, type ApiRequest } from "./ApiEquivalent";
import { FormFailure } from "./RequestError";

/**
 * ActionConfirm asks before a change that isn't a delete (deploy, promote,
 * roll back, install), saying what it does and showing the exact request
 * it sends. Deletes go through DeleteConfirm instead.
 */
export function ActionConfirm<T>({
  label,
  request,
  confirm,
  busyLabel,
  children,
  onDone,
  onCancel,
}: {
  label: string;
  request: ApiRequest;
  confirm: string;
  busyLabel: string;
  children: ReactNode;
  onDone: (result: T) => void;
  onCancel: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();

  async function run() {
    setBusy(true);
    setFailure(undefined);
    try {
      onDone(await apiFetch<T>(request.method, request.path, request.body));
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  return (
    <div className="panel rows" role="group" aria-label={label}>
      {children}
      {failure && <FormFailure error={failure} />}
      <div className="form-actions">
        <button type="button" className="button button--primary" onClick={run} disabled={busy}>
          {busy ? busyLabel : confirm}
        </button>
        <button type="button" className="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
      <ApiEquivalent request={request} />
    </div>
  );
}
