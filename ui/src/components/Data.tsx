import type { ReactNode } from "react";
import type { Condition } from "../api/types";
import { formatAge } from "../lib/time";
import { conditionKind, StatusText } from "./Status";

/** Conditions exactly as the API reports them: type, status, reason, message, since. */
export function ConditionsTable({ conditions, now }: { conditions: Condition[] | undefined; now: Date }) {
  if (!conditions || conditions.length === 0) {
    return <p className="state-body">No conditions reported yet. The reconciler hasn't processed this resource.</p>;
  }
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Type</th>
            <th scope="col">Status</th>
            <th scope="col">Reason</th>
            <th scope="col">Message</th>
            <th scope="col">Since</th>
          </tr>
        </thead>
        <tbody>
          {conditions.map((c) => (
            <tr key={c.type}>
              <td>{c.type}</td>
              <td>
                <StatusText kind={conditionKind(c)}>{c.status}</StatusText>
              </td>
              <td className="mono">{c.reason ?? ""}</td>
              <td>{c.message ?? ""}</td>
              <td>
                <time dateTime={c.lastTransitionTime} title={c.lastTransitionTime}>
                  {formatAge(c.lastTransitionTime, now)}
                </time>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function KeyValueList({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="kv">
      {items.map(([k, v]) => (
        <div key={k} className="kv-row">
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * SafeLink renders a link only for an http(s) URL. API data is never
 * trusted as a URL scheme: anything else (javascript:, data:, a relative
 * path) is shown as plain text.
 */
export function SafeLink({ href, children }: { href: string; children?: ReactNode }) {
  let ok = false;
  try {
    const u = new URL(href);
    ok = u.protocol === "http:" || u.protocol === "https:";
  } catch {
    ok = false;
  }
  if (!ok) return <span className="mono">{children ?? href}</span>;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {children ?? href}
    </a>
  );
}
