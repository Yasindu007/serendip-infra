import { useEffect, useState } from "react";
import { apiFetch, apiList, apiPage, isShown, onSessionEnded, signOut, type ApiError, type NetworkError } from "./api/client";
import type { Approval, NodeResource, SessionInfo } from "./api/types";
import { RequestError } from "./components/RequestError";
import { Shell } from "./components/Shell";
import { Banner, LoadingState } from "./components/States";
import { usePoll } from "./hooks/usePoll";
import { match, useLocation } from "./lib/router";
import { isSingleNode, topologySentence } from "./lib/topology";
import { ApplicationDetail, ApplicationList, NewApplication } from "./screens/Applications";
import { ComponentDetail, ComponentForm, NotFound } from "./screens/Components";
import { parseScopeParam } from "./lib/permissions";
import { AccessPage } from "./screens/AccessPage";
import { ApprovalDetail, ApprovalList } from "./screens/Approvals";
import { SecretDetail, SecretList } from "./screens/Secrets";
import { Overview } from "./screens/Overview";
import { SignIn } from "./screens/SignIn";

type SessionState = { kind: "checking" } | { kind: "signedIn"; info: SessionInfo } | { kind: "signedOut"; ended: boolean } | { kind: "error"; error: ApiError | NetworkError };

export function App() {
  const { path, query } = useLocation();
  const [session, setSession] = useState<SessionState>({ kind: "checking" });

  useEffect(() => {
    const ctrl = new AbortController();
    apiFetch<SessionInfo>("GET", "/v1/session", undefined, ctrl.signal)
      .then((info) => setSession({ kind: "signedIn", info }))
      .catch((err) => {
        if (!isShown(err)) return;
        if ("status" in err && err.status === 401) setSession({ kind: "signedOut", ended: false });
        else setSession({ kind: "error", error: err });
      });
    return () => ctrl.abort();
  }, []);

  // Any request that learns the session is over sends the person to sign in,
  // keeping where they were.
  useEffect(() => onSessionEnded(() => setSession((s) => (s.kind === "signedIn" ? { kind: "signedOut", ended: true } : s))), []);

  const returnTo = path === "/signin" ? "/" : path + (query.toString() ? `?${query}` : "");

  if (path === "/signin" || session.kind === "signedOut") {
    return <SignIn returnTo={path === "/signin" ? (query.get("return") ?? "/") : returnTo} error={path === "/signin" ? query.get("error") : null} ended={session.kind === "signedOut" && session.ended} />;
  }
  if (session.kind === "checking") return <LoadingState what="your session" />;
  if (session.kind === "error") {
    return (
      <main className="signin" id="main">
        <RequestError error={session.error} what="your session" />
      </main>
    );
  }

  async function handleSignOut() {
    const next = await signOut().catch(() => "/");
    // The provider's end-session page, or the console's own root.
    const safe = next.startsWith("/") && !next.startsWith("//") ? next : /^https?:\/\//.test(next) ? next : "/";
    window.location.assign(safe);
  }

  return (
    <SignedIn path={path} actor={session.info.actor} onSignOut={handleSignOut}>
      <Routes path={path} query={query} />
    </SignedIn>
  );
}

function SignedIn({ path, actor, onSignOut, children }: { path: string; actor: string; onSignOut: () => void; children: React.ReactNode }) {
  const nodes = usePoll((s) => apiList<NodeResource>("/v1/nodes", s), 60_000, "topology");
  const banner = nodes.data && isSingleNode(nodes.data.length) ? <Banner>{topologySentence(nodes.data.length)}</Banner> : undefined;
  // Pending approvals the person can see, for the rail. A full page (100)
  // reads "100+" rather than a count it doesn't have.
  const pending = usePoll((s) => apiPage<Approval>("/v1/approvals?phase=Pending&limit=100", s), 30_000, "rail-pending");
  const waiting = pending.data ? (pending.data.next ? "100+" : String(pending.data.items.length)) : undefined;
  return (
    <Shell path={path} actor={actor} onSignOut={onSignOut} banner={banner} waiting={waiting === "0" ? undefined : waiting}>
      {children}
    </Shell>
  );
}

function Routes({ path, query }: { path: string; query: URLSearchParams }) {
  let p: Record<string, string> | null;
  if (path === "/") return <Overview />;
  if (path === "/access") {
    const scope = parseScopeParam(query.get("scope"));
    return scope ? <AccessPage scope={scope} /> : <NotFound />;
  }
  if (path === "/applications") return <ApplicationList />;
  if (path === "/applications/new") return <NewApplication />;
  if ((p = match("/applications/:app/components/new", path))) return <ComponentForm appId={p.app!} />;
  if ((p = match("/applications/:app/components/:id/edit", path))) return <ComponentForm appId={p.app!} id={p.id!} />;
  if ((p = match("/applications/:app/components/:id", path))) return <ComponentDetail appId={p.app!} id={p.id!} />;
  if ((p = match("/applications/:id", path))) return <ApplicationDetail id={p.id!} />;
  if (path === "/secrets") return <SecretList ownerId={query.get("owner") ?? undefined} />;
  if ((p = match("/secrets/:id", path))) return <SecretDetail id={p.id!} />;
  if (path === "/approvals") return <ApprovalList />;
  if ((p = match("/approvals/:id", path))) return <ApprovalDetail id={p.id!} />;
  return <NotFound />;
}
