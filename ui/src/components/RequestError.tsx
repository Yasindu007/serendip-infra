import type { ReactNode } from "react";
import { ApiError, NetworkError } from "../api/client";
import { ErrorState, Notice } from "./States";

/**
 * RequestError turns a failed request into what happened and what to do
 * (spec 016 plan.md decision 14). A 401 never gets here: the session gate
 * shows the sign-in page instead.
 */
export function RequestError({ error, what, action }: { error: ApiError | NetworkError; what: string; action?: ReactNode }) {
  if (error instanceof NetworkError) {
    return <ErrorState title="Can't reach Serendip" detail={`${error.message} The page keeps retrying.`} action={action} />;
  }
  const common = { requestId: error.requestId, action };
  switch (error.status) {
    case 403:
      return <ErrorState title="Not allowed" detail={error.detail ?? `You can't view ${what}.`} {...common} />;
    case 404:
      return <ErrorState title={`No such ${what}`} detail="It may have been removed, or the link is wrong." {...common} />;
    case 429:
      return <ErrorState title="Too many requests" detail={`Retrying in ${error.retryAfterSeconds ?? 60} seconds.`} {...common} />;
    case 503:
      return <ErrorState title={capitalise(error.title)} detail={`${error.detail ?? ""} The page keeps retrying.`.trim()} {...common} />;
    default:
      return <ErrorState title={`Couldn't load ${what}`} detail={error.detail ?? capitalise(error.title)} {...common} />;
  }
}

/** Why a form or an action failed, inside the form: nothing was saved. */
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

function capitalise(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
