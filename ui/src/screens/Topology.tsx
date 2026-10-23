import { apiFetch } from "../api/client";
import type { Application, TopologyView } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { AvailabilityGate } from "../components/Availability";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState } from "../components/States";
import { TopologyDiagram } from "../components/TopologyDiagram";
import { useOptional } from "../hooks/useOptional";
import { usePoll, useNow } from "../hooks/usePoll";
import { Link } from "../lib/router";
import { UpdatedLine } from "./common";

const enc = encodeURIComponent;

/**
 * ApplicationTopology: an Application's Components and the calls they
 * declare to each other (spec 020). It's what's declared, not live traffic,
 * and says so.
 */
export function ApplicationTopology({ id }: { id: string }) {
  const now = useNow();
  const app = usePoll((s) => apiFetch<Application>("GET", `/v1/applications/${enc(id)}`, undefined, s), 15_000, `topo-app-${id}`);
  const topo = usePoll((s) => apiFetch<TopologyView>("GET", `/v1/applications/${enc(id)}/topology`, undefined, s), 15_000, `topo-${id}`);
  const traffic = useOptional("observability");
  const appPath = `/applications/${enc(id)}`;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: "Applications", to: "/applications" },
          { label: app.data?.metadata.name ?? "Application", to: appPath },
        ]}
        title="Topology"
        meta={<UpdatedLine at={topo.updatedAt} now={now} />}
      />
      <p>The calls each component declares to others in this application. This is what's declared, not traffic that was observed.</p>
      {topo.error ? (
        <RequestError error={topo.error} what="topology" />
      ) : !topo.data ? (
        <LoadingState what="the topology" />
      ) : topo.data.components.length === 0 ? (
        <EmptyState title="No components yet">Add components to the application to see how they call each other.</EmptyState>
      ) : (
        <>
          <TopologyDiagram view={topo.data} label={`${topo.data.components.length} components and ${topo.data.dependencies.length} declared calls, listed in the table below`} />
          <section className="section" aria-labelledby="topo-calls">
            <h2 id="topo-calls" className="section-title">
              Declared calls
            </h2>
            {topo.data.dependencies.length === 0 ? (
              <p className="state-body">No component declares a call to another.</p>
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">From</th>
                      <th scope="col">To</th>
                      <th scope="col">Port</th>
                      <th scope="col">Address in</th>
                    </tr>
                  </thead>
                  <tbody>
                    {topo.data.dependencies.map((d) => {
                      const name = (cid: string) => topo.data!.components.find((c) => c.id === cid)?.name ?? cid;
                      return (
                        <tr key={`${d.from}-${d.to}-${d.port}`}>
                          <td>
                            <Link to={`${appPath}/components/${enc(d.from)}`}>{name(d.from)}</Link>
                          </td>
                          <td>
                            <Link to={`${appPath}/components/${enc(d.to)}`}>{name(d.to)}</Link>
                          </td>
                          <td>{d.port}</td>
                          <td className="mono">{d.env}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
      {traffic.loaded && <AvailabilityGate title="Live traffic" id="topo-traffic" readiness={traffic.readiness} packageName={traffic.packageName} what="live traffic between components" />}
      <ApiEquivalent request={{ method: "GET", path: `/v1/applications/${id}/topology` }} />
    </>
  );
}
