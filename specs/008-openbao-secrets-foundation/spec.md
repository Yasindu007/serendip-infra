# Spec 008: OpenBao deployment foundation

Status: Draft — human review required before plan.md

## Problem

Secrets & PKI (`capability-model.md:40`, `overview.md:68`) already names
OpenBao as its default provider, and ADR 0009 formalizes that choice and
its implementation constraints (standalone mode, no Helm, hand-rolled
client, documented root-token custody simplification). Nothing has been
built yet: no OpenBao deployment exists in the cluster, no client talks
to it, and no resource kind represents a Secret.

This is explicitly a **foundation** spec, not the Secrets capability
itself — mirroring how specs 005+006 were foundation work that spec 007
built the real Storage capability on top of. Spec 008 gets a real,
persistent OpenBao instance running and reachable; spec 009 (follow-on,
not part of this spec) builds the actual `Secret` resource kind,
`SecretsProvider` interface, and reconciler on top of it.

## Scope — IN

- **OpenBao Kubernetes deployment**, constructed directly via the
  existing generic `providers/kubernetes.Client` (spec 006) — no Helm:
  a StatefulSet (one replica), a Service (ClusterIP, cluster-internal
  only — no external exposure in this spec), a PVC (standalone mode's
  file storage backend needs real persistence), and a ConfigMap holding
  OpenBao's server config (listener, storage stanza — standalone/file,
  **no `disable_mlock` directive**, since OpenBao 2.7 made that a fatal
  startup error, not a no-op).
- **Init/unseal bootstrap**: on first contact with a fresh (uninitialized)
  OpenBao instance, call its init endpoint with a single key share
  (1-of-1 Shamir threshold), capture the root token + unseal key, write
  them to a root-only local file (mode 0600), then unseal. On subsequent
  `serendipd` starts against an already-initialized instance, read that
  local file and unseal if currently sealed (OpenBao re-seals on every
  pod restart — this must be handled as a normal, expected startup path,
  not an error case).
- **`providers/secrets/openbao`**: a minimal hand-rolled REST client
  (plain HTTP+JSON, `X-Vault-Token` header) covering exactly: health
  check (`/v1/sys/health`), init (`/v1/sys/init`), unseal
  (`/v1/sys/unseal`), and KV v2 read/write/delete
  (`/v1/secret/data/<path>`, `/v1/secret/metadata/<path>`) — no more
  surface than spec 008/009 actually need yet (no PKI engine, no
  dynamic DB credentials, no Kubernetes auth method).
- **`cmd/serendipd` wiring**: construct the OpenBao client, ensure the
  deployment exists (idempotent, same GET-then-Create pattern as
  `providers/storage/localpath`), run the init/unseal bootstrap, and
  register the client under a new capability name (e.g. `"secrets"`) in
  `core/provider.Registry` — gated the same way Kubernetes-facing wiring
  already is (`SERENDIP_KUBECONFIG` unset ⇒ skip, not fail).
- One proof-of-concept smoke operation through the whole stack (not the
  real Secrets capability — that's spec 009): write one KV v2 value,
  read it back, confirm it round-trips, against a real OpenBao instance
  in a real K3s cluster (disposable WSL2 clone, same verification
  discipline as specs 005-007).

## Scope — OUT (explicitly)

- **The `Secret` resource kind, `SecretsProvider` core interface, and
  any reconciler** — spec 009's job, built on this spec's foundation,
  same split as specs 006→007.
- **HA/Raft mode, multi-replica, auto-unseal** — explicitly deferred per
  ADR 0009 until a real multi-node profile justifies it.
- **Kubernetes auth method / workload identity integration** — relevant
  to a future Identity/Thunder story, not this spec.
- **PKI secrets engine, dynamic database credentials, any other OpenBao
  secrets engine beyond KV v2** — out of scope until a real consumer
  needs them.
- **External exposure of OpenBao's API** (Ingress/Gateway route) — this
  spec's Service is cluster-internal only; nothing outside the cluster
  talks to OpenBao directly.
- **Any change to `api/`** — purely `providers/` + `cmd/serendipd`
  wiring, matching spec 006's precedent.
- **Root-token custody beyond the documented local-file simplification**
  — no cloud KMS integration, no auto-unseal; ADR 0009 documents this as
  a temporary, known simplification with explicit re-evaluation triggers.

## Acceptance criteria

1. `go test ./providers/secrets/openbao/...` green: the hand-rolled
   client's request/response handling (including KV v2's double-nested
   read shape) is table-driven-tested against a fixture HTTP server, no
   live OpenBao needed in CI.
2. Against a real K3s cluster (disposable WSL2 clone, freshly bootstrapped
   per spec 005): `serendipd` starting fresh deploys OpenBao
   (StatefulSet/Service/PVC/ConfigMap all present and correct via
   `kubectl`), initializes it, writes the root token/unseal key to the
   local file, and unseals it — confirmed via OpenBao's own
   `/v1/sys/health` endpoint reporting unsealed+active.
3. Restarting `serendipd` (simulating a pod/process restart, OpenBao
   re-sealed) against the same already-initialized instance correctly
   detects "already initialized, currently sealed," reads the existing
   local credential file (does not re-init), and unseals successfully —
   proves the restart path, not just the fresh-install path.
4. One KV v2 value written through `providers/secrets/openbao`'s client
   round-trips: write, then read, byte-for-byte equal, against the real
   instance from (2).
5. `go vet ./...`, `gofmt -l .` clean; `check-imports.sh` confirms
   `core/` imports nothing under `providers/`.
6. The root-token/unseal-key local file is created with mode 0600 and
   its path/handling is documented in plan.md and
   `docs/architecture/secrets.md` (new doc, or deferred to spec 009 if
   this spec's shape isn't settled enough yet — resolve in plan.md).

## Open questions (resolve during plan review)

- **Exact local file path/format** for root token + unseal key custody
  (candidates: alongside K3s's kubeconfig under `/etc/rancher/k3s/`, or
  a Serendip-specific path like `/var/lib/serendip/openbao-init.json`)
  — resolve with a concrete decision in plan.md.
- **OpenBao version to pin** (latest researched: v2.7.1) — confirm via
  oss-researcher at plan time in case a newer patch exists.
- **StatefulSet image/config specifics** (resource requests/limits
  appropriate for the T470-class reference hardware budget, exact
  listener/storage ConfigMap contents) — resolve in plan.md, informed by
  oss-researcher if needed for exact config stanza syntax.
- **Whether `docs/architecture/secrets.md` gets written in spec 008 or
  deferred to spec 009** — this spec's shape is foundation-only; the
  doc may read better once the real capability exists too. Decide in
  plan.md.
