import type { Condition } from "../api/types";
import { Icon, type IconName } from "./Icon";

/** The console's whole status vocabulary (ui/DESIGN.md "Status"). */
export type StatusKind = "ok" | "progress" | "warn" | "error" | "unknown";

const icons: Record<StatusKind, IconName> = {
  ok: "check",
  progress: "progress",
  warn: "alert",
  error: "error",
  unknown: "unknown",
};

/**
 * StatusText always shows an icon AND a word, in the status colour: colour
 * is never the only signal.
 */
export function StatusText({ kind, children }: { kind: StatusKind; children: string }) {
  return (
    <span className={`status status--${kind}`}>
      <Icon name={icons[kind]} className={kind === "progress" ? "spin" : undefined} />
      {children}
    </span>
  );
}

// Reasons the reconcilers set (core/reconcile/application.go). Anything else
// is shown as-is with an "unknown" treatment rather than guessed at.
const progressReasons = new Set(["Progressing", "Pending", "WaitingForApplication", "ProviderNotReady"]);
const errorReasons = new Set(["DeployFailed", "Unsupported", "InvalidSpec", "EnsureFailed", "RouteRejected"]);

export function conditionKind(c: Condition | undefined): StatusKind {
  if (!c) return "unknown";
  if (c.status === "True") return "ok";
  if (c.reason && errorReasons.has(c.reason)) return "error";
  if (c.reason && progressReasons.has(c.reason)) return "progress";
  return c.status === "False" ? "warn" : "unknown";
}

export function findCondition(conditions: Condition[] | undefined, type: string): Condition | undefined {
  return conditions?.find((c) => c.type === type);
}

/** A resource's overall state, from its Ready condition. */
export function ReadyStatus({ conditions }: { conditions: Condition[] | undefined }) {
  const ready = findCondition(conditions, "Ready");
  const kind = conditionKind(ready);
  const word = !ready
    ? "Not reconciled yet"
    : kind === "ok"
      ? "Ready"
      : kind === "progress"
        ? "In progress"
        : kind === "error"
          ? "Failed"
          : "Not ready";
  return <StatusText kind={ready ? kind : "unknown"}>{word}</StatusText>;
}
