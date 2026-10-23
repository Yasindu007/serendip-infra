import type { ReactNode } from "react";
import type { CapabilityReadiness } from "../api/types";
import { availability, availabilityWords } from "../lib/health";
import { Link } from "../lib/router";
import { StatusText } from "./Status";

/**
 * AvailabilityGate shows a feature that needs an optional package (spec 020
 * acceptance criterion 10): the feature itself once the package is ready,
 * otherwise the section greyed out, saying why from GET /v1/readiness and
 * naming the package. Installed but not ready reads "installing" or "not
 * ready", never "unavailable". Nothing is mocked in its place.
 */
export function AvailabilityGate({
  title,
  id,
  readiness,
  packageName,
  what,
  children,
}: {
  title: string;
  id: string;
  readiness: CapabilityReadiness | undefined;
  packageName: string;
  what: string;
  children?: ReactNode;
}) {
  const state = availability(readiness);
  if (state === "ready" && children) {
    return (
      <section className="section" aria-labelledby={id}>
        <h2 id={id} className="section-title">
          {title}
        </h2>
        {children}
      </section>
    );
  }
  const { kind, word } = availabilityWords[state];
  const why =
    state === "ready"
      ? `The ${packageName} package is ready, but this console doesn't show ${what} yet.`
      : state === "installing"
        ? `The ${packageName} package is installing. This section works once it's ready.`
        : state === "notReady"
          ? `The ${packageName} package is installed but not ready${readiness?.message ? `: ${readiness.message}` : "."}`
          : state === "notInstalled"
            ? `This needs the ${packageName} package, which isn't installed.`
            : `This needs the ${packageName} package, which isn't available in this version of Serendip yet.`;
  return (
    <section className="section unavailable" aria-labelledby={id}>
      <div className="pipeline-head">
        <h2 id={id} className="section-title">
          {title}
        </h2>
        <StatusText kind={kind}>{word}</StatusText>
      </div>
      <p>{why}</p>
      {(state === "notInstalled" || state === "unavailable") && <Link to="/packages">See packages</Link>}
    </section>
  );
}
