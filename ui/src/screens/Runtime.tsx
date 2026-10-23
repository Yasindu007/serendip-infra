import { useEffect, useRef, useState } from "react";
import { apiFetch, apiStream, isShown, type ApiError, type NetworkError } from "../api/client";
import type { Application, Component, Deployment, LogLine, RuntimeEvent, RuntimeObject } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { AvailabilityGate } from "../components/Availability";
import { SelectField } from "../components/Field";
import { LogView } from "../components/LogView";
import { RequestError } from "../components/RequestError";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { StatusText } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { useOptional } from "../hooks/useOptional";
import { usePoll, useNow } from "../hooks/usePoll";
import { healthKind } from "../lib/health";
import { readLogs, readRuntime } from "../lib/permissions";
import { orderEnvironments, ownerOf } from "../lib/pipeline";
import { formatAge } from "../lib/time";
import { useDeployData } from "./Deploy";

const enc = encodeURIComponent;

/**
 * RuntimeTab: what one of the Component's Deployments runs as on the
 * platform (spec 020): its objects and their health, recent events, and
 * logs, read live and followed on request.
 */
export function RuntimeTab({ app, comp }: { app: Application; comp: Component }) {
  const projectId = ownerOf(app.metadata, "Project");
  const data = useDeployData(projectId, comp.metadata.id);
  const [chosen, setChosen] = useState<string>();

  if (data.error) return <RequestError error={data.error} what="deployments" />;
  if (projectId && !data.data) return <LoadingState what="deployments" />;
  const ordered = data.data ? orderEnvironments(data.data.project.spec.pipeline, data.data.envs) : [];
  const deployed = ordered.flatMap(({ env }) => {
    const d = data.data?.deployments.find((x) => ownerOf(x.metadata, "Environment") === env.metadata.id);
    return d ? [{ env: env.metadata.name, d }] : [];
  });
  if (deployed.length === 0) return <EmptyState title="Not deployed anywhere">Deploy the component to an Environment to see what it runs as.</EmptyState>;
  const current = deployed.find((x) => x.d.metadata.id === chosen) ?? deployed[0]!;

  return (
    <>
      {deployed.length > 1 && (
        <SelectField
          id="runtime-env"
          label="Environment"
          value={current.d.metadata.id}
          onChange={(e) => setChosen(e.target.value)}
          options={deployed.map((x) => ({ value: x.d.metadata.id, label: x.env }))}
        />
      )}
      <DeploymentRuntime key={current.d.metadata.id} d={current.d} envName={current.env} />
    </>
  );
}

function DeploymentRuntime({ d, envName }: { d: Deployment; envName: string }) {
  const now = useNow();
  const id = d.metadata.id;
  const access = usePermissions({ kind: "Deployment", id });
  const canRead = access.can(readRuntime);
  const canLogs = access.can(readLogs);
  const [allEvents, setAllEvents] = useState(false);
  const objects = usePoll(
    (s) => (canRead ? apiFetch<{ objects: RuntimeObject[] }>("GET", `/v1/deployments/${enc(id)}/runtime`, undefined, s) : Promise.resolve(undefined)),
    10_000,
    `runtime-${id}-${canRead}`,
  );
  const events = usePoll(
    (s) => (canRead ? apiFetch<{ events: RuntimeEvent[] }>("GET", `/v1/deployments/${enc(id)}/events`, undefined, s) : Promise.resolve(undefined)),
    10_000,
    `events-${id}-${canRead}`,
  );

  return (
    <>
      <section className="section" aria-labelledby="rt-objects">
        <h2 id="rt-objects" className="section-title">
          Running in {envName}
        </h2>
        {canRead === false ? (
          <p className="state-body">Your roles here don't allow reading what this deployment runs as.</p>
        ) : objects.error ? (
          <RequestError error={objects.error} what="runtime objects" />
        ) : !objects.data ? (
          <LoadingState what="runtime objects" />
        ) : objects.data.objects.length === 0 ? (
          <p className="state-body">The platform reports nothing running for this deployment yet.</p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Kind</th>
                  <th scope="col">Name</th>
                  <th scope="col">Health</th>
                  <th scope="col">Message</th>
                </tr>
              </thead>
              <tbody>
                {objects.data.objects.map((o) => (
                  <tr key={`${o.kind}/${o.name}`}>
                    <td>{o.kind}</td>
                    <td className="mono" translate="no">
                      {o.name}
                    </td>
                    <td>
                      <StatusText kind={healthKind(o.health)}>{o.health || "Unknown"}</StatusText>
                    </td>
                    <td>{o.message ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <ApiEquivalent request={{ method: "GET", path: `/v1/deployments/${id}/runtime` }} />
      </section>

      <Logs id={id} allowed={canLogs} />

      <section className="section" aria-labelledby="rt-events">
        <h2 id="rt-events" className="section-title">
          Events
        </h2>
        {canRead === false ? null : events.error ? (
          <RequestError error={events.error} what="events" />
        ) : !events.data ? (
          <LoadingState what="events" />
        ) : events.data.events.length === 0 ? (
          <p className="state-body">No recent events.</p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">When</th>
                  <th scope="col">Type</th>
                  <th scope="col">Reason</th>
                  <th scope="col">Object</th>
                  <th scope="col">Message</th>
                </tr>
              </thead>
              <tbody>
                {(allEvents ? events.data.events : events.data.events.slice(0, EVENTS_SHOWN)).map((e, i) => (
                  <tr key={i}>
                    <td>
                      <time dateTime={e.time} title={e.time}>
                        {formatAge(e.time, now)}
                      </time>
                      {(e.count ?? 0) > 1 && ` (${e.count} times)`}
                    </td>
                    <td>{e.type === "Warning" ? <StatusText kind="warn">Warning</StatusText> : e.type}</td>
                    <td className="mono">{e.reason}</td>
                    <td className="mono" translate="no">{`${e.objectKind} ${e.objectName}`}</td>
                    <td>{e.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {(events.data?.events.length ?? 0) > EVENTS_SHOWN && (
          <div>
            <button type="button" className="button" onClick={() => setAllEvents(!allEvents)}>
              {allEvents ? `Show the newest ${EVENTS_SHOWN}` : `Show all ${events.data!.events.length} events`}
            </button>
          </div>
        )}
      </section>
    </>
  );
}

/** Events shown before "Show all": the newest, which is what explains the state now. */
const EVENTS_SHOWN = 10;

/** At most this many lines are kept on screen while following. */
const MAX_LINES = 5_000;

const endWords: Record<string, string> = {
  idle: "The stream stopped after 5 minutes without a new line.",
  ended: "The instances stopped writing.",
  error: "The platform failed part-way through, so some lines may be missing.",
};

function Logs({ id, allowed }: { id: string; allowed: boolean | undefined }) {
  const [lines, setLines] = useState<LogLine[]>();
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState<ApiError | NetworkError>();
  const [following, setFollowing] = useState(false);
  const [ended, setEnded] = useState<string>();
  const stream = useRef<AbortController | undefined>(undefined);
  // The recent-lines read in flight, cancelled when following starts so a
  // late answer can't replace the streamed lines.
  const recent = useRef<AbortController | undefined>(undefined);
  const recentPath = `/v1/deployments/${id}/logs?tailLines=100`;
  const followPath = `/v1/deployments/${id}/logs?follow=true&tailLines=100`;

  async function loadRecent() {
    recent.current?.abort();
    const ctrl = new AbortController();
    recent.current = ctrl;
    setError(undefined);
    try {
      const r = await apiFetch<{ lines: LogLine[]; truncated: boolean }>("GET", `/v1/deployments/${enc(id)}/logs?tailLines=100`, undefined, ctrl.signal);
      setLines(r.lines ?? []);
      setTruncated(r.truncated);
    } catch (err) {
      if (isShown(err)) setError(err);
    }
  }

  useEffect(() => {
    if (!allowed) return;
    void loadRecent();
    return () => {
      recent.current?.abort();
      stream.current?.abort();
    };
  }, [id, allowed]);

  async function follow() {
    recent.current?.abort();
    stream.current?.abort();
    const ctrl = new AbortController();
    stream.current = ctrl;
    setFollowing(true);
    setEnded(undefined);
    setError(undefined);
    setTruncated(false);
    setLines([]);
    // The server ends a stream after 10 minutes; reconnecting is the
    // console's job. A reconnect asks only for lines since the last one
    // seen, and drops what it already shows.
    let last: LogLine | undefined;
    // Set on a reconnect: lines before the last one seen were shown already.
    let resumeAt: LogLine | undefined;
    let path = `/v1/deployments/${enc(id)}/logs?follow=true&tailLines=100`;
    try {
      for (;;) {
        const end = await apiStream<LogLine>(
          path,
          (l) => {
            if (resumeAt) {
              const r = resumeAt;
              if (Date.parse(l.time) < Date.parse(r.time) || (l.time === r.time && l.instance === r.instance && l.line === r.line)) return;
            }
            last = l;
            setLines((prev) => {
              const next = [...(prev ?? []), l];
              return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
            });
          },
          ctrl.signal,
        );
        if (end !== "maxDuration") {
          setEnded(end === undefined ? "The connection closed, so some lines may be missing." : (endWords[end] ?? `The stream ended (${end}).`));
          break;
        }
        resumeAt = last;
        const since = last ? Math.max(1, Math.ceil((Date.now() - Date.parse(last.time)) / 1000) + 1) : 1;
        path = `/v1/deployments/${enc(id)}/logs?follow=true&sinceSeconds=${since}`;
      }
    } catch (err) {
      if (!ctrl.signal.aborted && isShown(err)) setError(err);
    } finally {
      if (stream.current === ctrl) {
        stream.current = undefined;
        setFollowing(false);
      }
    }
  }

  function stop() {
    stream.current?.abort();
    stream.current = undefined;
    setFollowing(false);
    setEnded("You stopped following.");
  }

  return (
    <section className="section" aria-labelledby="rt-logs">
      <h2 id="rt-logs" className="section-title">
        Logs
      </h2>
      {allowed === false ? (
        <p className="state-body">Logs can hold sensitive data, so reading them needs its own permission (the Log Reader role), which your roles here don't include.</p>
      ) : allowed === undefined ? (
        <LoadingState what="your access" />
      ) : (
        <>
          <div className="form-actions">
            {following ? (
              <button type="button" className="button" onClick={stop}>
                Stop following
              </button>
            ) : (
              <>
                <button type="button" className="button button--primary" onClick={() => void follow()}>
                  Follow
                </button>
                <button type="button" className="button" onClick={() => void loadRecent()}>
                  Refresh
                </button>
              </>
            )}
          </div>
          <p className="field-hint" role="status">
            {following ? "Following: new lines appear as they're written." : (ended ?? "The newest 100 lines from each instance.")}
          </p>
          {error ? <RequestError error={error} what="logs" /> : !lines ? <LoadingState what="logs" /> : <LogView lines={lines} label="Log lines" following={following} />}
          {truncated && <Notice>Only the newest lines are shown.</Notice>}
          <ApiEquivalent request={{ method: "GET", path: following ? followPath : recentPath }} />
        </>
      )}
    </section>
  );
}

/** BuildsTab: builds need the build package; greyed out until it's ready. */
export function BuildsTab() {
  const opt = useOptional("build");
  if (!opt.loaded) return <LoadingState what="package availability" />;
  return <AvailabilityGate title="Builds" id="builds-title" readiness={opt.readiness} packageName={opt.packageName} what="builds from source" />;
}

/** ObservabilityTab: stored logs, metrics, traces and alerts need the observability package. */
export function ObservabilityTab() {
  const opt = useOptional("observability");
  if (!opt.loaded) return <LoadingState what="package availability" />;
  return (
    <>
      <AvailabilityGate title="Metrics, traces and alerts" id="obs-title" readiness={opt.readiness} packageName={opt.packageName} what="metrics, traces, alerts and log search" />
      <p className="field-hint">Recent logs and events don't need this package: they're on the Runtime tab.</p>
    </>
  );
}
