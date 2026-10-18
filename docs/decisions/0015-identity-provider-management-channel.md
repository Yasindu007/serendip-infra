# ADR 0015: How serendipd reaches the identity provider's management API

Status: Accepted
Date: 2026-10-19

## Context

`serendipd` runs outside the cluster. It reaches in-cluster services
through the Kubernetes API server's Service proxy, as it does for OpenBao
(ADR 0009). ThunderID's management API (users, groups, applications,
resource servers) needs a bearer token, and **the Service proxy strips
the `Authorization` header**. Spec 016 found this. Spec 018 T001 showed
the same token getting 401 through the proxy and 200 through a tunnel.

Until spec 018 that meant:
- Check access showed a person's groups as of their last sign-in.
- The principal picker only knew people who had signed in.
- Every change to the identity provider's configuration, such as the
  console's URIs or token attributes, was a manual operator step with
  `kubectl port-forward`.

Spec 018 had to settle one channel for this and for every later
identity-provider feature: live lookups now, and later agent identities,
Serendip-managed groups, automatic console URIs and credential cleanup.

Constraints:
- **No new dependency** (constitution 4): `client-go` was already
  rejected in ADR 0008.
- **Runs where `serendipd` runs:** on the cluster's node for edge-1,
  off-node in development (kind on Windows) and on future multi-node
  installs.
- **Nothing reachable from outside that wasn't before.** T001 found the
  public identity route exposed `/oauth2/introspect`,
  `/oauth2/dcr/register` and `/oauth2/par` through an `/oauth2` prefix.
  Spec 018 T003 narrowed it to an exact-path allowlist.
- **Works for every caller,** the console, API keys, the CLI and a
  future MCP server (constitution 7), not only for a person signed in to
  the console.

## Decision

The channel is a transport plus a credential:

1. **Transport: an in-process port-forward through the Kubernetes API.**
   - `providers/kubernetes` opens the pod's `portforward` subresource as
     a WebSocket, subprotocol `v4.channel.k8s.io`, the kubelet's channel
     protocol.
   - It's a minimal stdlib client: handshake checks, masking,
     fragmentation, ping/pong, close, and bounded frames.
   - It exposes a `DialContext` (`ServiceDialer`) that plugs into an
     ordinary `http.Transport`.
   - The API server passes the bytes through untouched, so the HTTP inside
     keeps its `Authorization` header.
   - The connection is authenticated with the kubeconfig `serendipd`
     already uses.
   - It was verified on kind 1.34 and K3s v1.36.4 (our pin).
2. **Credential: `serendipd`'s own administrator login.**
   - It uses the existing PKCE login through ThunderID's console client
     (scope `system`).
   - The password is read from the `serendip-identity-admin` Secret for
     each login, through the same kubeconfig, and never stored, logged or
     returned.
   - The token stays only in memory and is renewed a minute before it
     expires (3600 s on 1.0.1).
3. **Use: reads only in spec 018.**
   - `GET /users`, `/groups` and `/users/{id}/groups`, behind
     `core/identity.Directory`.
   - Serendip's RBAC decides who may see the answers. Only callers who
     can assign roles somewhere may.

## Options considered

| Option | Why not |
|---|---|
| The Service proxy | It strips `Authorization`. |
| The public Gateway route to the management paths | It would expose the management API to anyone who can reach the gateway, protected only by its token check. That's the opposite of T003. |
| A restricted Gateway route or a NodePort | Same exposure from the node's network, and a second route to keep. |
| The Service's ClusterIP from the host | It only works when `serendipd` runs on a cluster node. It fails for kind development and off-node control planes. |
| An in-cluster helper (a proxy that re-adds the header) | A new deployed component and image to build, sign and keep up (ADR 0012's bar) for a header rename. |
| Kubernetes' newer SPDY-over-WebSocket tunnel (KEP-4006) | It needs a SPDY/3.1 implementation: too heavy for stdlib-only. |
| **Act as the signed-in person** (Azure's portal calls Microsoft Graph as you) | It takes no privilege away, because `serendipd` already deploys ThunderID and holds its admin Secret. It would only work while a ThunderID administrator is signed in to the console, never for API keys or the CLI (constitution 7). |
| A narrower machine identity | ThunderID 1.0.1 has only the coarse `system` permission, so any machine identity is full admin anyway. |

## Consequences

- **The privilege held is full ThunderID administration** (`system`), the
  same as `serendipd` already had by holding the admin Secret.
  - The token never leaves the process.
  - In spec 018 it's used only for reads.
  - A finer permission, once ThunderID makes one grantable, should replace
    it.
- **Custody:** the credential stays in its existing Kubernetes Secret.
  It's reached with the root-only kubeconfig, and no second copy is made.
- **RBAC:** `serendipd`'s Kubernetes identity needs `create` on
  `pods/portforward` in `serendip-identity`, plus `get` on Services and
  `list` on Pods there.
- **Cost:**
  - about 7 ms per directory read on a warm tunnel;
  - about 39 ms when a new tunnel opens, since idle tunnels close at
    30 s;
  - about 300 ms for a login;
  - +0.2 MiB idle memory (spec 018 T010).
- **Failure:** if ThunderID or the tunnel is unavailable, lookups fall
  back to the principals seen at sign-in and say so (`source:
  signedIn`). Authentication and every other route are unaffected.
  Errors may name pod internals, so they're logged in short form and
  never returned.
- **Later identity-provider automation is just another use of this
  channel:** console URIs (`UpdateClient`), managed groups and cleanup.
  Writes will need their own audit and approval rules when they come.
- **If `serendipd` moves into the cluster** (discussed during spec 018,
  not planned), the dialer is replaced by the Service's cluster address.
  The `Directory` and everything above it stay as they are.
- **Revisit when** ThunderID offers grantable fine-grained management
  permissions, or when the API server stops passing the kubelet's
  WebSocket protocol through. It's supported since 1.31, and GA locked
  in 1.38.
