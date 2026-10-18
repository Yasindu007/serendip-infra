import type { ReactNode } from "react";

/**
 * Guarded shows an action the caller may take, or the same action disabled
 * with the reason it isn't available (spec 017 plan.md decision 11). Until
 * the caller's permissions are known it's disabled too, never offered on a
 * guess. The server still decides every request; this only saves a person
 * a refused click.
 */
export function Guarded({ allowed, label, reason, children }: { allowed: boolean | undefined; label: string; reason: string; children: ReactNode }) {
  if (allowed) return <>{children}</>;
  const id = `guard-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;
  return (
    <span className="guarded">
      <button type="button" className="button" disabled aria-describedby={id}>
        {label}
      </button>
      <span id={id} className="field-hint">
        {allowed === undefined ? "Checking your access…" : reason}
      </span>
    </span>
  );
}
