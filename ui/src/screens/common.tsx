import { formatAge } from "../lib/time";

/** "Updated 12 seconds ago", the honest freshness of polled data. */
export function UpdatedLine({ at, now }: { at: Date | undefined; now: Date }) {
  if (!at) return null;
  const iso = at.toISOString();
  return (
    <>
      Updated <time dateTime={iso}>{formatAge(iso, now)}</time>
    </>
  );
}

/** DNS label: what Serendip resource and port names must be. */
export const dnsLabel = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;

export function dnsLabelError(value: string, what: string): string | undefined {
  if (value === "") return `Enter a ${what}.`;
  if (!dnsLabel.test(value)) return `Use lowercase letters, digits and hyphens, starting and ending with a letter or digit (at most 63).`;
  return undefined;
}
