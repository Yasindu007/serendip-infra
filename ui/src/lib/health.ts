// Mapping the platform's and the registry's own words onto the console's
// five states (ui/DESIGN.md "Status vocabulary"). Unrecognised words are
// shown as-is, as a warning, never guessed at.
import type { CapabilityReadiness } from "../api/types";
import type { StatusKind } from "../components/Status";

/** A runtime object's health: Healthy, Progressing, Degraded, Suspended or Unknown. */
export function healthKind(health: string): StatusKind {
  switch (health) {
    case "Healthy":
      return "ok";
    case "Progressing":
      return "progress";
    case "Degraded":
      return "error";
    case "Unknown":
    case "":
      return "unknown";
    default:
      return "warn";
  }
}

/** Whether an optional capability (a package) can be used, and how to say it. */
export type Availability = "ready" | "installing" | "notReady" | "notInstalled" | "unavailable";

/**
 * An optional capability's state from GET /v1/readiness (spec 020
 * acceptance criterion 10). Missing means this server doesn't offer it.
 */
export function availability(r: CapabilityReadiness | undefined): Availability {
  if (!r) return "unavailable";
  if (r.ready) return "ready";
  switch (r.reason) {
    case "Installing":
      return "installing";
    case "NotInstalled":
      return "notInstalled";
    case "Unavailable":
      return "unavailable";
    default:
      return "notReady";
  }
}

export const availabilityWords: Record<Availability, { kind: StatusKind; word: string }> = {
  ready: { kind: "ok", word: "Ready" },
  installing: { kind: "progress", word: "Installing" },
  notReady: { kind: "error", word: "Not ready" },
  notInstalled: { kind: "unknown", word: "Not installed" },
  unavailable: { kind: "unknown", word: "Not available yet" },
};
