# Spec 012: Gateway capability (external reachability for Components, kgateway as default provider)

Status: Draft — human review required before plan.md

## Problem

Spec 011 made Components real: a Serendip `Component` becomes a running
Pod in a per-Application data-plane namespace. But nothing outside the
cluster can reach it. Every endpoint uses OpenChoreo's default
cluster-internal visibility. Spec 010 installed both OpenChoreo charts with
`gateway.enabled=false`, and spec 011 trimmed the HTTPRoute resources out
of its `serendip-service` component type. So the most basic outcome a
platform user expects, "deploy an app, then open it", isn't possible yet.

`capability-model.md` lists Gateway/ingress as a **required** capability
with kgateway as its default. That default has no ADR yet, unlike every
other provider this project has actually deployed (OpenBao, ADR 0009;
OpenChoreo, ADR 0005). And nothing in `core/` models it yet.

**Two structural facts shape this spec, not just its details:**

1. **OpenChoreo renders routes itself.** A Workload endpoint with
   `external` visibility makes OpenChoreo's component type render an
   HTTPRoute that targets the Environment's configured external gateway.
   Serendip should not write HTTPRoutes itself; that would bypass the
   Application Platform that already owns the workload's rendering. So
   the Gateway capability's job is narrower than "do routing":
   - install the gateway implementation;
   - own the shared Gateway the platform routes through;
   - wire that Gateway into the Environment;
   - let a Component say which ports are public;
   - report honestly, in the Component's status, where each public port
     can actually be reached.

   How the new Gateway capability and the existing Application Platform
   capability divide that work, without either reaching into the other's
   provider, is a real design question (Open Questions).
2. **The node's HTTP ports are already taken.** Spec 005 deliberately left
   K3s's bundled Traefik and ServiceLB enabled, and Traefik binds ports
   80/443 on the node through ServiceLB. A kgateway Gateway exposed the
   same way would conflict. This spec has to resolve the overlap
   explicitly; silently ending up with two ingress controllers fighting
   over the same ports is not acceptable. The resolution may touch
   `agent/bootstrap` (spec 005's K3s install flags) as well as this
   provider (Open Questions).

## Scope — IN

- **ADR 0011: kgateway as the default Gateway provider.** It covers:
  - the same governance/maturity/licensing check ADR 0009 did for OpenBao
    (via oss-researcher);
  - the version pin;
  - the install mechanism, deciding explicitly whether ADR 0010's
    Helm-shellout reasoning applies here too, rather than assuming it
    carries over;
  - a resource-footprint estimate, against constitution rule 4;
  - fallback conditions, as ADR 0005/0006 carry.
- **A way for a Component to mark a port as externally reachable.** An
  addition to `ComponentSpec`'s existing `ports` (spec 011), defaulting to
  not exposed, so every existing Component keeps today's behavior. Exact
  field shape is plan.md's job.
- **Honest external-address reporting on the Component.** For every
  exposed port, `ComponentStatus` reports the address it can actually be
  reached at, or a Condition saying why not yet (gateway not ready, route
  not accepted). It never reports an address that doesn't work. A
  non-exposed port never gets one.
- **A Gateway capability interface in `core/`** (constitution rule 1:
  every capability has one), defined alongside whatever Reconciler drives
  it and using only `core/resource` types. No kgateway, Envoy, or Gateway
  API vocabulary appears in `core/`. Its shape, and whether it needs its
  own resource kind or only feeds Component status, is plan.md's job
  (Open Questions).
- **A kgateway provider under `providers/`.** It:
  - installs the kgateway controller (pinned version);
  - ensures the one shared external Gateway this edge-1 install routes
    through;
  - exposes that Gateway on the node in a way that is actually reachable
    from outside the cluster and doesn't conflict with anything else on
    the node.
- **Application Platform integration.** The OpenChoreo provider wires the
  shared Gateway into the Environment the platform deploys through, and
  restores external-endpoint routing in the `serendip-service` component
  type, which spec 011 removed only because no gateway existed. Exposure
  maps onto OpenChoreo's own `external` endpoint visibility.
- **Resolving the Traefik/ServiceLB port overlap** with a concrete,
  verified decision. It also covers what happens to already-bootstrapped
  clusters, not just new installs.
- **`cmd/serendipd` wiring**, gated on `SERENDIP_KUBECONFIG` like every
  Kubernetes-facing provider.
- **One proof-of-concept flow against a real cluster** (`kind`). It is
  configured however the plan decides gives an honest stand-in for
  "reachable from outside the node", not `kubectl port-forward`:
  1. Deploy a Component with one exposed port and one non-exposed port.
  2. Make an HTTP request **from the host machine**, outside the cluster,
     to the address the Component's status reports, and get the app's
     response.
  3. Confirm the non-exposed port has no external route.
  4. Un-expose the port and confirm the route disappears and the status
     follows.
  5. Delete the Component and confirm its route is gone.
- **One resource-footprint data point** for the added gateway components
  (controller + proxy), recorded the way spec 011 recorded its own. This
  is still not ADR 0005's T470 benchmark.

## Scope — OUT (explicitly)

- **TLS / HTTPS.** HTTP only. Real TLS needs real hostnames and a CA
  story (cert-manager is installed, but an edge-1 laptop has no public DNS
  name). Reported plainly as HTTP-only, not dressed up. A later spec's
  job.
- **Custom domains / user-supplied hostnames and DNS management.** One
  platform-chosen addressing scheme only (Open Questions).
- **`internal`/`namespace` visibility, east-west routing, service mesh.**
  Only `external` exposure is in scope. Cluster-internal reachability
  already works without a gateway.
- **API management**: authentication at the gateway, rate limiting, API
  keys, request transformation, analytics. Out, and so is the WSO2 API
  Manager alternative `capability-model.md` lists.
- **Multiple Gateways, per-Application gateways, or per-Environment gateway
  selection.** One shared external Gateway, the same single-instance shape
  as spec 010's one DataPlane and spec 011's one Environment.
- **An `external` (bring-your-own) gateway provider implementation.**
  Planned per constitution rule 1, not built here.
- **Non-HTTP exposure** (raw TCP/UDP routes). Exposed ports are served as
  HTTP routes; anything else is reported as unsupported, honestly.
- **Gateway HA, multi-replica proxies, load-balancing across nodes.**
  edge-1 is single-node (constitution rule 6: report "single instance").
- **UI/CLI/MCP surfaces, policy engine, AI-initiated exposure**: same
  standing deferrals as every prior spec.
- **Swapping K3s's CNI or anything else spec 005 left enabled** beyond
  what resolving the ingress port overlap strictly requires.

## Acceptance criteria

1. `go test ./...` green. New behavior is covered by table-driven tests
   without a live cluster:
   - the Gateway interface's Reconciler logic;
   - the kgateway provider's object construction and idempotency;
   - the OpenChoreo provider's exposure mapping (exposed port → external
     endpoint visibility; none → unchanged from spec 011);
   - external-address status reporting.
2. Against a real `kind` cluster, with no manual steps beyond creating the
   cluster with whatever configuration plan.md specifies: `serendipd`
   installs the gateway controller and the shared Gateway, and both
   report ready/programmed, confirmed directly via `kubectl`.
3. A Component with one exposed port answers an HTTP request **made from
   the host machine** at the exact address its status reports. A second,
   non-exposed port on the same Component has no external route.
4. Status honesty (constitution rule 6):
   - A Component's external address appears only once the route is
     accepted and actually serving.
   - Before that, a Condition says why it isn't reachable yet.
   - Un-exposing the port removes both the route (the request now fails)
     and the address from status.
   - Nothing reports an address that doesn't answer.
5. Deleting the Component removes its route (confirmed via `kubectl`).
   Existing spec 011 behavior is unchanged: Components with no exposed
   ports deploy exactly as before.
6. Idempotency: an unchanged, already-exposed Component reconciled again
   produces no store `Update`, audit row, `ResourceVersion` bump, or
   cluster-side write.
7. The Traefik/ServiceLB overlap is resolved as plan.md decides, and the
   decision is verified on the real cluster: no two controllers contend
   for the same node ports. The handling for already-bootstrapped
   clusters is documented.
8. `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh` clean.
   `core/` imports nothing under `providers/`, and no product name
   (kgateway, Envoy, OpenChoreo, Traefik...) appears in any `core/`
   identifier, comment, or Condition reason/message.
9. ADR 0011 is written and Accepted before implementation starts. It
   records governance/maturity findings, version pin, install mechanism,
   footprint estimate, and fallback conditions.

## Open questions (resolve during plan review)

- **How the Gateway and Application Platform capabilities divide the
  work** without either reaching into the other's provider.
  - Candidates:
    - The Gateway capability only ensures a shared gateway and exposes a
      provider-neutral reference/address. The Application Platform
      provider consumes that reference to configure its Environment.
    - The Gateway capability also owns per-Component route status.
  - Related: whether this needs a new resource kind at all (a
    `Gateway`-like resource with its own status, so the gateway's health
    is visible through the API), or only `ComponentStatus` additions.
- **Gateway interface shape in `core/`.** It is a fourth data point for
  `provider-model.md`'s narrow-interface pattern. Derive the shape from
  what the capability actually does (likely ensure/delete a shared
  gateway, report its readiness and address), not from
  `StorageProvider`'s.
- **The Traefik/ServiceLB overlap.**
  - Options:
    - Disable Traefik at K3s install time (`agent/bootstrap` flag change,
      plus a one-off migration for existing clusters).
    - Expose the Gateway on non-standard ports.
    - Expose it via NodePort.
    - Keep Traefik and put kgateway behind it.
  - Each has a different honesty/UX cost.
  - On `kind` (no Traefik, no ServiceLB) exposure differs from K3s anyway.
    Decide how the verification stand-in maps onto the real K3s target so
    the proof isn't kind-specific.
- **Addressing scheme for external URLs.** OpenChoreo's rendered routes
  use path-prefix matching (`/<component>-<endpoint>`) and may also set
  hostnames.
  - Decide what Serendip reports as the address. Candidates:
    - a host plus path on the node's IP;
    - a `*.localhost`/nip.io-style hostname;
    - a configurable base domain.
  - Decide how an edge-1 user with no DNS actually reaches it.
- **Exposure field shape**: a boolean on `ComponentPort` vs. a visibility
  enum anticipating `internal` later. Constitution rule 10 says don't
  build for the deferred case, but don't make adding it a breaking change
  either.
- **Mutability of OpenChoreo's `Environment` gateway configuration.**
  Spec 011 already created `Environment/development`, and its
  `dataPlaneRef` is immutable upstream. Whether its gateway fields can be
  set after creation, or the Environment must be recreated (and what that
  does to deployed Components), must be confirmed against a real cluster,
  not assumed.
- **kgateway install mechanism and footprint.**
  - Install mechanism: Helm chart via the existing `helm` shellout
    (ADR 0010's machinery), or direct objects.
  - Footprint: what the controller + Envoy proxy cost at idle on top of a
    stack already near 1 GiB, and whether that fits edge-1's budget.
  - ADR 0011 answers both with researched numbers, not guesses.
- **Resync/latency**: route acceptance and gateway programming are
  upstream state changes, not Serendip events, so status catches up on
  the 30s resync like spec 011's readiness. Confirm that is acceptable, or
  name the case where it isn't.
