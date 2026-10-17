import type { ReactNode } from "react";
import { Icon } from "./Icon";

/** A full-width notice under the page edge: topology, provider outages. */
export function Banner({ tone = "warn", children }: { tone?: "warn" | "danger"; children: ReactNode }) {
  return (
    <p className={tone === "danger" ? "banner banner--danger" : "banner"} role={tone === "danger" ? "alert" : undefined}>
      <Icon name={tone === "danger" ? "error" : "alert"} />
      <span>{children}</span>
    </p>
  );
}

/** An inline notice inside a page or form. */
export function Notice({ tone = "warn", children }: { tone?: "warn" | "danger"; children: ReactNode }) {
  return (
    <div className={tone === "danger" ? "notice notice--danger" : "notice"} role={tone === "danger" ? "alert" : undefined}>
      <Icon name={tone === "danger" ? "error" : "alert"} />
      <div>{children}</div>
    </div>
  );
}

export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode | undefined }) {
  return (
    <div className="state">
      <h2 className="state-title">{title}</h2>
      {children && <p className="state-body">{children}</p>}
      {action}
    </div>
  );
}

export function LoadingState({ what }: { what: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <p className="state-body">Loading {what}…</p>
    </div>
  );
}

/**
 * ErrorState shows what went wrong and what to do, plus the request ID to
 * quote when asking for help. Never a stack trace or a raw body.
 */
export function ErrorState({ title, detail, requestId, action }: { title: string; detail?: string | undefined; requestId?: string | undefined; action?: ReactNode }) {
  return (
    <div className="state" role="alert">
      <h2 className="state-title">{title}</h2>
      {detail && <p className="state-body">{detail}</p>}
      {requestId && <p className="request-id">Request ID <span className="mono">{requestId}</span></p>}
      {action}
    </div>
  );
}
