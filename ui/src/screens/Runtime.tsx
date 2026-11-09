import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError, apiFetch, apiList, apiStream, isShown, type NetworkError } from "../api/client";
import type { Application, Build, BuildPhase, Component, Deployment, LogLine, RuntimeEvent, RuntimeObject } from "../api/types";
import { ActionConfirm } from "../components/ActionConfirm";
import { ApiEquivalent, type ApiRequest } from "../components/ApiEquivalent";
import { AvailabilityGate } from "../components/Availability";
import { KeyValueList } from "../components/Data";
import { SelectField, TextField } from "../components/Field";
import { Guarded } from "../components/Guarded";
import { LogView } from "../components/LogView";
import { FormFailure, RequestError } from "../components/RequestError";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { findCondition, StatusText, type StatusKind } from "../components/Status";
import { usePermissions } from "../hooks/usePermissions";
import { useOptional } from "../hooks/useOptional";
import { usePoll, useNow } from "../hooks/usePoll";
import { healthKind } from "../lib/health";
import { action, readLogs, readRuntime } from "../lib/permissions";
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

      <Logs
        idPrefix="rt"
        path={`/v1/deployments/${enc(id)}/logs`}
        allowed={canLogs}
        intro="The newest 100 lines from each instance."
        endWords={endWords}
      />

      <section className="section" aria-labelledby="rt-events">
        <h2 id="rt-events" className="section-title">
          Events
        </h2>
        {canRead === false ? null : events.error ? (
          <RequestError error={events.error} what="events" />
        ) : !events.data ? (
          <LoadingState what="events" />
        ) : (
          <EventsTable events={events.data.events} now={now} />
        )}
      </section>
    </>
  );
}

/** Events, newest first, with the rest on request. */
function EventsTable({ events, now }: { events: RuntimeEvent[]; now: Date }) {
  const [allEvents, setAllEvents] = useState(false);
  if (events.length === 0) return <p className="state-body">No recent events.</p>;
  return (
    <>
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
            {(allEvents ? events : events.slice(0, EVENTS_SHOWN)).map((e, i) => (
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
      {events.length > EVENTS_SHOWN && (
        <div>
          <button type="button" className="button" onClick={() => setAllEvents(!allEvents)}>
            {allEvents ? `Show the newest ${EVENTS_SHOWN}` : `Show all ${events.length} events`}
          </button>
        </div>
      )}
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

/**
 * Logs: recent lines, and followed live with reconnect (spec 020), for a
 * Deployment or a build. path is the logs route without its query.
 * explain turns an error the reader should see in words (an expired
 * build) into text; anything else is shown as a request error.
 */
function Logs({
  idPrefix,
  path: base,
  allowed,
  intro,
  endWords,
  canFollow = true,
  explain,
  reloadKey,
}: {
  idPrefix: string;
  path: string;
  allowed: boolean | undefined;
  intro: string;
  endWords: Record<string, string>;
  canFollow?: boolean;
  explain?: (err: ApiError | NetworkError) => ReactNode | undefined;
  /** When it changes (a build's phase), the recent lines are read again, unless they're being followed. */
  reloadKey?: string | undefined;
}) {
  const [lines, setLines] = useState<LogLine[]>();
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState<ApiError | NetworkError>();
  const [following, setFollowing] = useState(false);
  const [ended, setEnded] = useState<string>();
  const stream = useRef<AbortController | undefined>(undefined);
  // The recent-lines read in flight, cancelled when following starts so a
  // late answer can't replace the streamed lines.
  const recent = useRef<AbortController | undefined>(undefined);
  const recentPath = `${base}?tailLines=100`;
  const followPath = `${base}?follow=true&tailLines=100`;

  async function loadRecent() {
    recent.current?.abort();
    const ctrl = new AbortController();
    recent.current = ctrl;
    setError(undefined);
    try {
      const r = await apiFetch<{ lines: LogLine[]; truncated: boolean }>("GET", recentPath, undefined, ctrl.signal);
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
  }, [base, allowed]);

  // A build that moved on has new lines (spec 022 T014 U1). Lines a
  // follow showed, even one that has ended, already include them and can
  // be more than a recent read's, so they're left alone.
  const firstKey = useRef(reloadKey);
  const followed = useRef(false);
  useEffect(() => {
    if (!allowed || reloadKey === firstKey.current) return;
    firstKey.current = reloadKey;
    if (!followed.current) void loadRecent();
  }, [reloadKey]);

  async function follow() {
    followed.current = true;
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
    let path = followPath;
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
        path = `${base}?follow=true&sinceSeconds=${since}`;
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

  const explained = error && explain?.(error);
  return (
    <section className="section" aria-labelledby={`${idPrefix}-logs`}>
      <h2 id={`${idPrefix}-logs`} className="section-title">
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
                {canFollow && (
                  <button type="button" className="button button--primary" onClick={() => void follow()}>
                    Follow
                  </button>
                )}
                <button type="button" className="button" onClick={() => void loadRecent()}>
                  Refresh
                </button>
              </>
            )}
          </div>
          <p className="field-hint" role="status">
            {following ? "Following: new lines appear as they're written." : (ended ?? intro)}
          </p>
          {explained ? <p className="state-body">{explained}</p> : error ? <RequestError error={error} what="logs" /> : !lines ? <LoadingState what="logs" /> : <LogView lines={lines} label="Log lines" following={following} />}
          {truncated && <Notice>Only the newest lines are shown.</Notice>}
          <ApiEquivalent request={{ method: "GET", path: following ? followPath : recentPath }} />
        </>
      )}
    </section>
  );
}

/**
 * BuildsTab: a source Component's builds (spec 022): its runs, newest
 * first; start one at the branch's head or a commit; cancel a running one;
 * and a run's logs, followed live, and events. Greyed out until the builds
 * package is ready.
 */
export function BuildsTab({ comp }: { comp: Component }) {
  const opt = useOptional("build");
  if (!opt.loaded) return <LoadingState what="package availability" />;
  return (
    <AvailabilityGate title="Builds" id="builds-title" readiness={opt.readiness} packageName={opt.packageName} what="builds from source">
      {comp.spec.source ? <Builds comp={comp} /> : <p className="state-body">This component runs a ready-made image, so it has no builds. Edit it to build from source in Git instead.</p>}
    </AvailabilityGate>
  );
}

const phaseKind: Record<BuildPhase, StatusKind> = { Queued: "progress", Running: "progress", Succeeded: "ok", Failed: "error", Cancelled: "unknown" };

/** A build's phase in words; a Build the reconciler hasn't seen yet is queued. */
function PhaseText({ phase }: { phase: BuildPhase | undefined }) {
  const p = phase ?? "Queued";
  return <StatusText kind={phaseKind[p] ?? "unknown"}>{p}</StatusText>;
}

const done = (b: Build) => b.status.phase === "Succeeded" || b.status.phase === "Failed" || b.status.phase === "Cancelled";

/** "1 min 20 s"; while running, up to now. Empty before it starts. */
export function buildDuration(b: Build, now: Date): string {
  if (!b.status.startedAt) return "";
  const start = Date.parse(b.status.startedAt);
  const end = b.status.finishedAt ? Date.parse(b.status.finishedAt) : now.getTime();
  if (Number.isNaN(start) || Number.isNaN(end)) return "";
  const s = Math.max(0, Math.round((end - start) / 1000));
  return s < 60 ? `${s} s` : `${Math.floor(s / 60)} min ${s % 60} s`;
}

const shortCommit = (c: string | undefined) => (c ? c.slice(0, 12) : "");
const commitRE = /^([0-9a-f]{40}|[0-9a-f]{64})$/;

function Builds({ comp }: { comp: Component }) {
  const now = useNow();
  const id = comp.metadata.id;
  const access = usePermissions({ kind: "Component", id });
  const canWrite = access.can(action("Build", "write"));
  const builds = usePoll((s) => apiList<Build>(`/v1/components/${enc(id)}/builds`, s), 5_000, `builds-${id}`);
  const [chosen, setChosen] = useState<string>();
  const auto = findCondition(comp.status.conditions, "AutoBuild");
  const list = builds.data ?? [];
  const current = list.find((b) => b.metadata.id === chosen) ?? list[0];

  return (
    <>
      {comp.spec.source?.autoBuild && auto && (
        <p>
          <StatusText kind={auto.status === "True" ? "ok" : "warn"}>{auto.status === "True" ? "Auto-build on" : "Auto-build can't see new commits"}</StatusText> {auto.message ?? ""}
        </p>
      )}
      {comp.spec.source && !comp.spec.source.autoBuild && <p className="field-hint">Auto-build is off: builds start only when someone starts one. Edit the component to turn it on.</p>}

      <Trigger compId={id} allowed={canWrite} onStarted={(b) => (setChosen(b.metadata.id), builds.refresh())} />

      {builds.error ? (
        <RequestError error={builds.error} what="builds" />
      ) : !builds.data ? (
        <LoadingState what="builds" />
      ) : list.length === 0 ? (
        <p className="state-body">No builds yet.</p>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <caption className="visually-hidden">Builds, newest first</caption>
            <thead>
              <tr>
                <th scope="col">Build</th>
                <th scope="col">State</th>
                <th scope="col">Commit</th>
                <th scope="col">Started</th>
                <th scope="col">Took</th>
              </tr>
            </thead>
            <tbody>
              {list.map((b) => (
                <tr key={b.metadata.id} aria-current={b.metadata.id === current?.metadata.id ? "true" : undefined}>
                  <td>
                    <button type="button" className="link-button mono" onClick={() => setChosen(b.metadata.id)} aria-pressed={b.metadata.id === current?.metadata.id}>
                      {b.metadata.name}
                    </button>
                  </td>
                  <td>
                    <PhaseText phase={b.status.phase} />
                  </td>
                  <td className="mono" translate="no">
                    {shortCommit(b.status.commit ?? b.spec.commit) || "Branch head"}
                  </td>
                  <td>
                    {b.status.startedAt ? (
                      <time dateTime={b.status.startedAt} title={b.status.startedAt}>
                        {formatAge(b.status.startedAt, now)}
                      </time>
                    ) : (
                      "Not yet"
                    )}
                  </td>
                  <td>{buildDuration(b, now)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ApiEquivalent request={{ method: "GET", path: `/v1/components/${id}/builds` }} />

      {current && <BuildDetail key={current.metadata.id} b={current} compId={id} canWrite={canWrite} onChanged={builds.refresh} />}
    </>
  );
}

/** Starts a build: at the branch's head, or at a full commit ID. */
function Trigger({ compId, allowed, onStarted }: { compId: string; allowed: boolean | undefined; onStarted: (b: Build) => void }) {
  const [commit, setCommit] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const c = commit.trim();
  const invalid = c !== "" && !commitRE.test(c);
  const request: ApiRequest = { method: "POST", path: `/v1/components/${compId}/builds`, ...(c ? { body: { commit: c } } : {}) };

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (invalid) return;
    setBusy(true);
    setFailure(undefined);
    try {
      onStarted(await apiFetch<Build>("POST", `/v1/components/${enc(compId)}/builds`, c ? { commit: c } : undefined));
      setCommit("");
    } catch (err) {
      if (isShown(err)) setFailure(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form" onSubmit={submit} noValidate aria-label="Start a build">
      <TextField
        id="build-commit"
        label="Commit (optional)"
        hint="A full commit ID, 40 or 64 hex digits. Leave it empty to build the branch's newest commit."
        mono
        value={commit}
        onChange={(e) => setCommit(e.target.value)}
        {...(invalid ? { error: "Enter the full commit ID: 40 or 64 lower-case hex digits." } : {})}
      />
      {failure && <FormFailure error={failure} />}
      <div className="form-actions">
        <Guarded allowed={allowed} label="Start build" reason="Your roles here don't allow starting builds.">
          <button type="submit" className="button button--primary" disabled={busy}>
            {busy ? "Starting…" : "Start build"}
          </button>
        </Guarded>
      </div>
      <p className="field-hint">A build waits its turn behind one already running. When it succeeds, its image becomes the component's new release.</p>
      <ApiEquivalent request={request} />
    </form>
  );
}

const buildEndWords: Record<string, string> = {
  idle: "The stream stopped after 5 minutes without a new line.",
  ended: "The build finished.",
  error: "The platform failed part-way through, so some lines may be missing.",
};

/** A 410 is an expired run, or one that never ran: words, not an error. */
function explainBuildRead(err: ApiError | NetworkError): string | undefined {
  if (!(err instanceof ApiError)) return undefined;
  if (err.status === 410) return err.title === "logs expired" ? "Logs expired: a build's logs and events are kept for an hour after it ends. Its outcome stays here." : "This build never ran, so it has no logs or events.";
  if (err.status === 409) return "The build hasn't started yet. Its logs appear once it's running.";
  return undefined;
}

function BuildDetail({ b, compId, canWrite, onChanged }: { b: Build; compId: string; canWrite: boolean | undefined; onChanged: () => void }) {
  const now = useNow();
  const id = b.metadata.id;
  const access = usePermissions({ kind: "Component", id: compId });
  const canLogs = access.can(readLogs);
  const [cancelling, setCancelling] = useState(false);
  const started = Boolean(b.status.started);
  const events = usePoll(
    (s) => (started ? apiFetch<{ events: RuntimeEvent[] }>("GET", `/v1/builds/${enc(id)}/events`, undefined, s) : Promise.resolve(undefined)),
    done(b) ? 60_000 : 10_000,
    `build-events-${id}-${started}`,
  );
  const steps = b.status.steps ?? [];
  const eventsExplained = events.error && explainBuildRead(events.error);

  return (
    <>
      <section className="section panel" aria-labelledby="build-detail">
        <div className="pipeline-head">
          <h2 id="build-detail" className="section-title mono" translate="no">
            {b.metadata.name}
          </h2>
          <PhaseText phase={b.status.phase} />
        </div>
        <KeyValueList
          items={[
            ["Commit", <span className="mono">{b.status.commit ?? b.spec.commit ?? "The branch's head, read when it starts"}</span>],
            ...(b.status.image ? [["Image", <span className="mono">{b.status.image}</span>] as [string, ReactNode]] : []),
            ...(b.status.source ? [["Built from", <span className="mono">{`${b.status.source.repository} ${b.status.source.branch ?? "main"}`}</span>] as [string, ReactNode]] : []),
            ["Took", buildDuration(b, now) || "Not started"],
            ...(b.spec.cancel && !done(b) ? [["Cancel", "Asked for; the build stops shortly"] as [string, ReactNode]] : []),
          ]}
        />
        {steps.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Step</th>
                  <th scope="col">State</th>
                  <th scope="col">Message</th>
                </tr>
              </thead>
              <tbody>
                {steps.map((st) => (
                  <tr key={st.name}>
                    <td className="mono">{st.name}</td>
                    <td>
                      <PhaseText phase={st.phase} />
                    </td>
                    <td>{st.message ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {cancelling ? (
          <ActionConfirm<Build>
            label={`Cancel ${b.metadata.name}`}
            request={{ method: "POST", path: `/v1/builds/${id}/cancel` }}
            confirm="Cancel build"
            busyLabel="Cancelling…"
            onDone={() => (setCancelling(false), onChanged())}
            onCancel={() => setCancelling(false)}
          >
            <p>Stop this build? What's running now is unchanged.</p>
          </ActionConfirm>
        ) : (
          !done(b) &&
          !b.spec.cancel && (
            <div className="form-actions">
              <Guarded allowed={canWrite} label="Cancel build" reason="Your roles here don't allow cancelling builds.">
                <button type="button" className="button" onClick={() => setCancelling(true)}>
                  Cancel build
                </button>
              </Guarded>
            </div>
          )
        )}
        <ApiEquivalent request={{ method: "GET", path: `/v1/builds/${id}` }} />
      </section>

      {!started ? (
        <section className="section" aria-labelledby="bd-logs">
          <h2 id="bd-logs" className="section-title">
            Logs
          </h2>
          <p className="state-body">{done(b) ? "This build never ran, so it has no logs or events." : "The build hasn't started yet. Its logs appear once it's running."}</p>
        </section>
      ) : (
        <Logs
          idPrefix="bd"
          path={`/v1/builds/${enc(id)}/logs`}
          allowed={canLogs}
          intro="The newest 100 lines from each step."
          endWords={buildEndWords}
          canFollow={!done(b)}
          explain={explainBuildRead}
          reloadKey={b.status.phase}
        />
      )}

      {started && (
        <section className="section" aria-labelledby="bd-events">
          <h2 id="bd-events" className="section-title">
            Events
          </h2>
          {eventsExplained ? (
            <p className="state-body">{eventsExplained}</p>
          ) : events.error ? (
            <RequestError error={events.error} what="events" />
          ) : !events.data ? (
            <LoadingState what="events" />
          ) : (
            <EventsTable events={events.data.events} now={now} />
          )}
          <ApiEquivalent request={{ method: "GET", path: `/v1/builds/${id}/events` }} />
        </section>
      )}
    </>
  );
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
