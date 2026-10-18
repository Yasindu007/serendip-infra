import { apiFetch, apiList } from "../api/client";
import type { NodeResource, Readiness } from "../api/types";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState } from "../components/States";
import { StatusText } from "../components/Status";
import { usePoll, useNow } from "../hooks/usePoll";
import { accessPath } from "../lib/permissions";
import { Link } from "../lib/router";
import { formatAge } from "../lib/time";
import { nodeCountSentence } from "../lib/topology";
import { UpdatedLine } from "./common";

export function Overview() {
  const now = useNow();
  const readiness = usePoll((s) => apiFetch<Readiness>("GET", "/v1/readiness", undefined, s), 15_000, "readiness");
  const nodes = usePoll((s) => apiList<NodeResource>("/v1/nodes", s), 15_000, "nodes");

  return (
    <>
      <PageHeader
        title="Overview"
        actions={
          <Link className="button" to={accessPath({})}>
            Access control
          </Link>
        }
        meta={<UpdatedLine at={readiness.updatedAt} now={now} />}
      />

      <section className="section" aria-labelledby="ov-topology">
        <h2 id="ov-topology" className="section-title">
          Topology
        </h2>
        {nodes.error ? <RequestError error={nodes.error} what="nodes" /> : nodes.data ? <p>{nodeCountSentence(nodes.data.length)}</p> : <LoadingState what="nodes" />}
      </section>

      <section className="section" aria-labelledby="ov-capabilities">
        <h2 id="ov-capabilities" className="section-title">
          Capabilities
        </h2>
        {readiness.error ? (
          <RequestError error={readiness.error} what="capability readiness" />
        ) : !readiness.data ? (
          <LoadingState what="capability readiness" />
        ) : Object.keys(readiness.data).length === 0 ? (
          <EmptyState title="No capabilities registered">
            serendipd is running without a cluster, so only the API itself is available. Set SERENDIP_KUBECONFIG to manage applications.
          </EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Capability</th>
                  <th scope="col">Status</th>
                  <th scope="col">Reason</th>
                  <th scope="col">Message</th>
                  <th scope="col">Since</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(readiness.data).map(([name, r]) => (
                  <tr key={name}>
                    <td>{name}</td>
                    <td>
                      <StatusText kind={r.ready ? "ok" : r.reason === "SetupFailed" ? "error" : "progress"}>{r.ready ? "Ready" : "Not ready"}</StatusText>
                    </td>
                    <td className="mono">{r.reason}</td>
                    <td>{r.message ?? ""}</td>
                    <td>
                      <time dateTime={r.since} title={r.since}>
                        {formatAge(r.since, now)}
                      </time>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
