# Plan: Spec 008 — OpenBao deployment foundation

Status: Implementing. **Correction found during T005 implementation**:
`providers/kubernetes.Object` (spec 006) only modeled `spec`/`status` as
raw JSON — correct for every kind touched so far (Node, PVC, PV all
carry a `spec`), but ConfigMap (needed for OpenBao's server config,
this spec's T005) carries its payload at a top-level `data` key
instead, with no `spec` at all. Fixed by adding a third raw-JSON field,
`Object.Data json.RawMessage \`json:"data,omitempty"\``, alongside
`Spec`/`Status` — purely additive, no existing caller affected, same
"no per-kind schema knowledge in this package" principle `Spec`/
`Status` already established. Recorded here, not reverted, per the
established "plan.md is a hypothesis, implementation can surface real
gaps" lesson (spec 007's two mid-implementation corrections).

**Second correction, found during T007 real-cluster verification**:
the originally-planned `storage "file" { path = ... }` OpenBao config
stanza (below, and ADR 0009) crashes on startup with `unknown storage
type file` — OpenBao 2.7.0 removed the `file` storage backend
entirely (confirmed via oss-researcher against OpenBao's own issue
#4077/PR #4088: their own packaged default config hit this exact crash
post-2.7.0, fixed by switching to `pebbledb`). Fixed by changing the
ConfigMap's `server.hcl` to `storage "pebbledb" { path = ... }` — the
documented non-HA, single-node durable replacement, functionally
equivalent for every decision already made (still local-disk-backed
via the same PVC, still non-HA, no Deployer/StatefulSet shape change
at all beyond the HCL string content). Also switched the StatefulSet's
container args from `-config=/openbao/config/server.hcl` to
`-config=/openbao/config` (the directory) — the official image's own
entrypoint already defaults to loading the whole directory, so passing
the file explicitly triggered a harmless but needless "ignoring
duplicate configuration found in directory" warning on every boot.
ADR 0009 updated to match.

**Third correction, found later in the same T007 pass**: the args
change above was itself wrong. Real-cluster testing (on two unrelated
clusters — a K3s cluster and an independent `kind`/Docker cluster,
ruling out any host-specific cause) showed `openbao-0` crashing
*deterministically on its very first boot*, every time, with `listen
tcp4 0.0.0.0:8200: bind: address already in use`. Root cause: the
entrypoint's own default command is already `bao server
-config=/openbao/config "$@"` — explicitly also passing
`-config=/openbao/config` as an arg made that flag appear *twice* in
the final command line (once from the entrypoint, once from `"$@"`),
and since `-config` in directory mode scans/merges every config file
in the directory, processing it twice loaded and bound the listener
twice within the same process. The "harmless... duplicate
configuration" warning this was chasing was never actually harmless
once the flag appeared via two independent sources instead of one.
Fixed: container args are now just `["server"]`, relying entirely on
the entrypoint's own default with nothing ever duplicated.

**Fourth correction, also found during T007**: neither this plan nor
ADR 0009 ever specified *enabling* the KV v2 secrets engine — only
`-dev` mode auto-mounts one; a production `Init`'d instance mounts
nothing but its built-in system backends, so `ReadSecret`/
`WriteSecret` 404'd with "no handler for route" despite matching the
documented wire protocol exactly. Fixed: `Client.EnsureKVv2Mount(ctx,
mount)` (idempotent, checks `/v1/sys/mounts` before enabling), called
unconditionally from `Bootstrap` so a prior partial failure can
self-heal on a later call.

**Fifth correction, also found during T007**: `Deployer.Ensure`
returned immediately after creating the StatefulSet, before the Pod
was actually reachable — `wireOpenBao`'s subsequent `Bootstrap` call
always raced this and crashed `serendipd` at startup with a genuine
"no endpoints available" 503. Fixed: `Ensure` now polls the
StatefulSet's `status.readyReplicas` (90s timeout) before returning.

All three of these final corrections were confirmed fixed end-to-end
against a real `kind` cluster — see `docs/PROGRESS.md`'s T007 section
for the full acceptance-criteria walkthrough.

## Open-question resolutions

1. **OpenBao version: pin `2.7.1`** (confirmed current latest via
   oss-researcher, 2026-10-10 — v2.6.x's maintenance line ended the same
   day). Image: `quay.io/openbao/openbao:2.7.1` (official primary
   registry; also mirrored to `docker.io`/`ghcr.io` identically — no
   reason to prefer a mirror). Never `latest` — no official guidance
   endorses it for anything but local experimentation.
2. **Root token / unseal key custody: a new Serendip-owned path, not
   reused from K3s's own config.** `/var/lib/serendip/secrets/
   openbao-init.json`, directory created mode `0700`, file mode `0600`,
   both root-owned. Reusing `/etc/rancher/k3s/` would conflate
   Serendip's own credential with a directory K3s itself manages;
   `serendip-agent` already establishes `/var/lib/serendip/` as
   Serendip's own state root via `SERENDIP_AGENT_STATE_DIR` (spec 004),
   so this follows that existing convention rather than inventing a new
   one. Override via `SERENDIP_OPENBAO_CREDENTIALS_PATH` env var,
   matching the existing `SERENDIP_*` convention.
3. **Resource requests/limits: conservative, explicitly flagged as
   unverified.** `requests: 128Mi/100m`, `limits: 512Mi/250m` — no
   official OpenBao minimum exists for standalone/file-backend mode
   (confirmed via oss-researcher); these numbers come from
   community/ecosystem guidance applied to Vault-era deployments, not
   an OpenBao-official SLA. Document this explicitly in
   `docs/architecture/secrets.md`, same "measure, don't assume"
   discipline ADR 0006 established for Thunder's footprint claims —
   revisit via `profiles/*/bench/` once real T470 measurement exists.
4. **`docs/architecture/secrets.md`: written now, at foundation level.**
   Describes the deployment model, init/unseal lifecycle, and credential
   custody this spec actually ships; explicitly defers the `Secret`
   resource kind / `SecretsProvider` interface / reconciler to spec 009
   (same "foundation doc now, capability section later" approach
   `storage.md` took relative to spec 007).
5. **New problem surfaced during planning, not in spec.md's original
   open questions — how does `serendipd` (a host process outside
   Kubernetes, same model since spec 002) reach OpenBao's Service,
   given spec.md scopes OUT any external exposure (no NodePort,
   no Ingress)?** Resolved: route every OpenBao REST call through the
   Kubernetes API server's **Service proxy subresource**
   (`/api/v1/namespaces/<ns>/services/<name>:<port>/proxy/<path>`), a
   stable, long-standing core Kubernetes API feature — not a new
   exposure surface, since it rides on the exact same authenticated API
   server connection `wireKubernetes` already establishes via
   `providers/kubernetes.Client`. `providers/kubernetes.Client` gains
   two small exported accessors (`HTTPClient() *http.Client`,
   `ProxyURL(namespace, service string, port int) string`) so
   `providers/secrets/openbao` can reuse the already-authenticated,
   already-TLS-configured transport instead of reconstructing
   kubeconfig-derived TLS config independently — avoids duplicating
   that logic across two provider packages. This keeps OpenBao's own
   Service genuinely cluster-internal (ClusterIP, no wider exposure)
   while still being reachable from the host process. **Flagged for
   real-cluster verification (not just unit-tested)** — the mechanism
   itself is standard and stable, but this codebase has never exercised
   a service-proxy call before.
6. **OpenBao's own persistent storage: a plain Kubernetes PVC against
   the cluster's default StorageClass (K3s's bundled `local-path`,
   dynamic provisioning, standard `WaitForFirstConsumer` binding mode,
   untouched) — NOT Serendip's own `Volume` resource/Storage
   capability (spec 007).** This is the inverse situation from spec
   007's own problem: `local-path-provisioner`'s `WaitForFirstConsumer`
   requirement needs a real consuming Pod to trigger scheduling-driven
   binding, and the OpenBao StatefulSet's Pod *is* a real consuming
   Pod — so the provisioner's normal, as-designed dynamic-provisioning
   path works here with zero workarounds (`storageClassName` simply
   omitted from the PVC so Kubernetes picks the cluster default).
   Using Serendip's own `Volume` kind instead would require building
   Application/Component-to-Volume mounting, explicitly out of spec
   007's scope and undesigned — not a reasonable thing to invent as a
   side effect of this spec. Recorded as a deliberate, temporary
   inconsistency: OpenBao's own storage doesn't yet dogfood Serendip's
   Storage capability. Worth revisiting once Volume-mounting wiring
   exists for an unrelated reason.

## Approach

**No `core/` changes in this spec** — spec.md's Scope-OUT is explicit
(`Secret` resource kind / `SecretsProvider` interface / reconciler are
spec 009's job). Everything here lives in `providers/secrets/openbao`
and `cmd/serendipd` wiring, matching specs 005+006's foundation-only
shape.

**`providers/kubernetes` (extended, not replaced)** — `client.go` gains:

```go
func (c *Client) HTTPClient() *http.Client
func (c *Client) ProxyURL(namespace, service string, port int) string
    // e.g. ProxyURL("serendip-system", "openbao", 8200) ->
    // "<baseURL>/api/v1/namespaces/serendip-system/services/openbao:8200/proxy"
```

**`providers/secrets/openbao/client.go`** — the hand-rolled REST client.
Deliberately narrow: health, init, unseal, KV v2 read/write/delete. No
PKI engine, no dynamic DB credentials, no auth-method calls — matches
spec.md's Scope-IN exactly.

```go
const DefaultKVMount = "secret"

type Client struct {
    baseURL string // the service-proxy URL from providers/kubernetes.ProxyURL
    http    *http.Client
    token   string
}

func NewClient(baseURL string, httpClient *http.Client) *Client
func (c *Client) SetToken(token string)

type HealthStatus struct {
    Initialized bool
    Sealed      bool
}
// Health always requests normalized response codes (?standbyok=true&
// uninitcode=200&sealedcode=200) so every state comes back as a plain
// 200 with a JSON body -- avoids special-casing Vault/OpenBao's
// documented varying-status-code health semantics for states this
// provider doesn't otherwise need to distinguish (standby, DR
// secondary, performance standby).
func (c *Client) Health(ctx context.Context) (HealthStatus, error)

// Init requests a Shamir-split root key with the given share/threshold
// counts. Spec 008 always calls this with (1, 1) -- single-share,
// single-threshold -- per ADR 0009's documented custody simplification.
func (c *Client) Init(ctx context.Context, shares, threshold int) (unsealKeys []string, rootToken string, err error)

// Unseal submits one key share. Returns the resulting sealed state.
func (c *Client) Unseal(ctx context.Context, key string) (sealed bool, err error)

var ErrSecretNotFound = errors.New("openbao: secret not found")

// ReadSecret GETs /v1/<mount>/data/<path>, unwraps the KV v2
// double-nested {"data": {"data": {...}, "metadata": {...}}} response
// shape down to just the inner data map. Returns ErrSecretNotFound on
// a 404 (mirrors providers/kubernetes.Client's ErrNotFound convention).
func (c *Client) ReadSecret(ctx context.Context, mount, path string) (map[string]any, error)

// WriteSecret PUTs /v1/<mount>/data/<path> with {"data": data}.
func (c *Client) WriteSecret(ctx context.Context, mount, path string, data map[string]any) error

// DeleteSecret DELETEs /v1/<mount>/metadata/<path> -- the metadata
// endpoint (not /data/<path>) so delete removes all versions, not a
// soft/recoverable delete of just the latest one; this provider has no
// use for KV v2's versioning/undelete story at this stage.
func (c *Client) DeleteSecret(ctx context.Context, mount, path string) error
```

**`providers/secrets/openbao/bootstrap.go`** — init/unseal orchestration
+ local credential file handling.

```go
type credentials struct {
    RootToken string `json:"rootToken"`
    UnsealKey string `json:"unsealKey"`
}

// Bootstrap ensures the OpenBao instance behind client is initialized
// and unsealed, persisting (fresh instance) or reusing (restart against
// an already-initialized instance) root-token/unseal-key material at
// credentialsPath. Sets the resulting root token on client via
// SetToken before returning. Safe to call on every serendipd startup --
// re-sealed-after-restart is treated as the normal, expected case, not
// an error path (OpenBao always starts sealed after a process restart
// unless auto-unseal is configured, which this spec explicitly doesn't
// use).
func Bootstrap(ctx context.Context, client *Client, credentialsPath string) error
```

Logic: `Health` first. If `!Initialized`: call `Init(ctx, 1, 1)`, write
`credentials` to `credentialsPath` (`os.MkdirAll` the parent dir mode
`0700`, `os.WriteFile` mode `0600`). If already `Initialized`: read
`credentialsPath` — **its absence here is a hard error**, not a
fallback, since there is no safe recovery path for "instance says
initialized, but this host has no record of the credentials that
produced that state" (the whole point of ADR 0009's custody model).
Either way, `SetToken(creds.RootToken)`; if `Sealed`, call
`Unseal(ctx, creds.UnsealKey)`.

**`providers/secrets/openbao/deploy.go`** — the Kubernetes deployment,
built directly via `providers/kubernetes.Client` (no Helm, same
precedent as `providers/storage/localpath`). Manifests are built as
nested `map[string]any` trees marshaled straight into
`kubernetes.Object.Spec`/`.Data`, **not** fully typed per-field Go
structs like `providers/storage/localpath`'s PV/PVC wire types — a
deliberate deviation, reasoned on instantiation count: `localpath`
builds a PV/PVC pair *per Volume* (typed structs pay for themselves
across many instantiations), whereas this is one static manifest
created once; typed structs here would be pure boilerplate with no
reuse benefit.

```go
const (
    Namespace       = "serendip-system" // shared with providers/storage/localpath; idempotent GET-then-Create either order
    StatefulSetName = "openbao"
    ServiceName     = "openbao"
    PVCName         = "openbao-data"
    ConfigMapName   = "openbao-config"
    Image           = "quay.io/openbao/openbao:2.7.1"
    ListenPort      = 8200
)

type Deployer struct { client *kubernetes.Client }

func NewDeployer(client *kubernetes.Client) *Deployer

// Ensure idempotently creates (GET-then-Create, same pattern as
// localpath.Provider.ensureNamespace) the namespace, ConfigMap (server
// config: file storage backend, tcp listener on 0.0.0.0:8200,
// tls_disable=true -- no mlock directive at all, since any
// mlock-related key is a fatal startup error on OpenBao 2.7+), PVC (no
// storageClassName set -- cluster default), Service (ClusterIP, port
// 8200), and StatefulSet (1 replica, mounts the ConfigMap and the PVC
// by name, resource requests/limits per open-question 3 above).
func (d *Deployer) Ensure(ctx context.Context) error
```

**`cmd/serendipd` wiring** — `wireKubernetes` (specs 006/007) extended
again: after building the existing `localpath` wiring, construct
`openbao.NewDeployer(client).Ensure(ctx)`, then
`openbao.NewClient(client.ProxyURL(openbao.Namespace, openbao.ServiceName, openbao.ListenPort), client.HTTPClient())`,
then `openbao.Bootstrap(ctx, obClient, credentialsPath)`
(`credentialsPath` from `SERENDIP_OPENBAO_CREDENTIALS_PATH`, defaulting
per open-question 2), then `Register("secrets", obClient)` in
`core/provider.Registry`. No Reconciler is wired this spec — there is
no `Secret` resource kind yet for one to drive (spec 009's job); the
registered capability is reachable for a smoke-test call only, same
spirit as spec 006's own `nodeLister` proof-of-concept.

## Interfaces / types to create

```go
// providers/kubernetes/client.go (extended)
func (c *Client) HTTPClient() *http.Client
func (c *Client) ProxyURL(namespace, service string, port int) string

// providers/secrets/openbao/client.go
type Client struct{ /* ... */ }
func NewClient(baseURL string, httpClient *http.Client) *Client
func (c *Client) SetToken(token string)
func (c *Client) Health(ctx context.Context) (HealthStatus, error)
func (c *Client) Init(ctx context.Context, shares, threshold int) (unsealKeys []string, rootToken string, err error)
func (c *Client) Unseal(ctx context.Context, key string) (sealed bool, err error)
func (c *Client) ReadSecret(ctx context.Context, mount, path string) (map[string]any, error)
func (c *Client) WriteSecret(ctx context.Context, mount, path string, data map[string]any) error
func (c *Client) DeleteSecret(ctx context.Context, mount, path string) error

// providers/secrets/openbao/bootstrap.go
func Bootstrap(ctx context.Context, client *Client, credentialsPath string) error

// providers/secrets/openbao/deploy.go
type Deployer struct{ /* ... */ }
func NewDeployer(client *kubernetes.Client) *Deployer
func (d *Deployer) Ensure(ctx context.Context) error

// cmd/serendipd/main.go (extended)
// wireKubernetes gains the openbao deploy+bootstrap+register sequence above.
```

## Files touched

```
providers/kubernetes/client.go, client_test.go       (HTTPClient/ProxyURL accessors)
providers/secrets/openbao/doc.go
providers/secrets/openbao/client.go, client_test.go
providers/secrets/openbao/bootstrap.go, bootstrap_test.go
providers/secrets/openbao/deploy.go, deploy_test.go
cmd/serendipd/main.go                                (extends wireKubernetes)
docs/architecture/secrets.md                         (new)
docs/decisions/0009-openbao-as-secrets-provider.md   (already written/Accepted)
docs/PROGRESS.md                                     (updated on completion)
```

## Risks

- **Service-proxy reachability is the single biggest unverified
  assumption** — the mechanism is standard, stable Kubernetes, but
  nothing in this codebase has exercised it before. If it doesn't work
  as expected against the real K3s API server, the fallback is a
  NodePort Service (a real, if less clean, alternative) — this would be
  a `propose_shortcuts_before_switching`-tier call if it comes to that,
  not a silent swap.
- **Undocumented resource minimums** (open-question 3) — the
  128Mi/512Mi guess could be wrong in either direction; real-cluster
  verification should watch actual RSS, not just "it started."
- **Credential-file-missing-but-instance-initialized is an
  unrecoverable state by design** (`Bootstrap`'s hard-error branch) —
  correct per ADR 0009's custody model, but worth naming plainly: there
  is deliberately no "reset and re-init" escape hatch in this spec. A
  human operator would need to wipe the PVC and ConfigMap-driven
  storage by hand to recover, same tier of manual-recovery acceptance
  as spec 007's "a human manually deletes the StorageClass" risk.
- **No Reconciler, no resource kind — the registered `"secrets"`
  capability has no real consumer yet**, same shape as spec 006's
  `nodeLister` proof-of-concept had before spec 007 gave it one. This is
  intentional (spec.md's Scope-OUT), not an oversight.

## Explicitly NOT done in this spec

- `Secret` resource kind, `SecretsProvider` core interface, any
  reconciler — spec 009.
- HA/Raft mode, multi-replica, auto-unseal.
- Kubernetes auth method / workload identity integration.
- PKI secrets engine, dynamic database credentials, any OpenBao engine
  beyond KV v2.
- External exposure of OpenBao's API (Ingress/Gateway/NodePort).
- Any change to `api/`.
- OpenBao's own storage going through Serendip's `Volume`/Storage
  capability (open-question 6) — plain PVC against the cluster default
  StorageClass instead.
