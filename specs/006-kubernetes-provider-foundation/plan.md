# Plan: Spec 006 — Kubernetes access foundation + Provider interface pattern

Status: Draft — awaiting human approval before implementation.

## Open-question resolutions

1. **Registry package: new `core/provider/` package, separate from
   `core/registry/`.** `core/registry.{Capability,Provider}Registry`
   are thin wrappers over `store.Store` — they manage *data*
   (descriptors persisted as ordinary resources, per
   `provider-model.md`'s "Registration"). What this spec needs is a
   completely different thing: a process-local, in-memory map from a
   capability name to a live Go interface implementation, populated once
   at `cmd/serendipd` startup and never persisted. Calling both
   "registry" invites confusion, so they get distinct packages:
   `core/registry` (existing, data) and `core/provider` (new, live
   code). `core/provider` holds only the registration mechanism
   (`Register`/`Lookup` by capability name) — no capability-specific
   interfaces live here yet (those arrive per-capability, in spec 007
   for Storage and beyond), keeping this package genuinely
   capability-agnostic.
2. **YAML dependency: `sigs.k8s.io/yaml`, not `gopkg.in/yaml.v3`.**
   `sigs.k8s.io/yaml` round-trips YAML through `encoding/json` (parse
   YAML → generic value → marshal to JSON → `json.Unmarshal` into the
   target struct), so the **same struct + the same `json:` tags**
   deserialize both a kubeconfig (YAML) and a Kubernetes API response
   (JSON) — one set of Go types, no parallel `yaml:`/`json:` tag
   maintenance. `gopkg.in/yaml.v3` would need separate `yaml:` tags on
   every struct this package defines, duplicating `providers/kubernetes`'s
   own JSON-tagged types for no benefit. Single new dependency,
   single-purpose, no transitive weight beyond its own small
   `gopkg.in/yaml.v2`-based internals — proportionate, unlike
   `client-go`. ADR recorded (see Files touched).
3. **Generic client shape: typed envelope, raw JSON body.** The client
   operates on a minimal `Object` envelope (`APIVersion`, `Kind`,
   `Metadata` — just enough to address/identify any object) plus
   `json.RawMessage` for everything else (`spec`/`status`/whatever the
   kind actually has). Callers (spec 007's storage provider, and future
   providers) unmarshal the raw body into their own kind-specific
   structs, the same pattern `agent/client` already established
   (define only the fields you actually touch, don't import or
   replicate the full upstream schema). This keeps `providers/kubernetes`
   genuinely reusable across every future Kubernetes object kind Serendip
   touches, rather than hardcoding PVC/PV/StorageClass knowledge into a
   package that's supposed to be capability-agnostic.

## Approach

**`providers/kubernetes/config.go`**: kubeconfig loading.

```go
type Config struct {
    Host   string // e.g. "https://127.0.0.1:6443"
    CAData []byte
    // Exactly one of the following two auth methods is populated,
    // matching what a real kubeconfig actually provides (K3s's default
    // kubeconfig uses client certs, not a bearer token).
    ClientCertData []byte
    ClientKeyData  []byte
    BearerToken    string
}

// LoadConfig parses a kubeconfig file (the standard format, the same
// file K3s's installer writes to /etc/rancher/k3s/k3s.yaml) and
// resolves its current-context's cluster+user into a flat Config ready
// for an *http.Client's TLS setup. Multi-context kubeconfigs are
// supported for parsing (current-context selects which one), but this
// spec only ever needs the one context K3s's own installer produces --
// no multi-cluster selection logic beyond that.
func LoadConfig(path string) (Config, error)
```

**`providers/kubernetes/client.go`**: the generic REST client.

```go
// Object is the minimal envelope every Kubernetes API object shares --
// enough to address, create, and identify one, with everything
// kind-specific left as raw JSON for the caller to type itself.
type Object struct {
    APIVersion string          `json:"apiVersion"`
    Kind       string          `json:"kind"`
    Metadata   ObjectMeta      `json:"metadata"`
    Spec       json.RawMessage `json:"spec,omitempty"`
    Status     json.RawMessage `json:"status,omitempty"`
}

type ObjectMeta struct {
    Name            string            `json:"name,omitempty"`
    Namespace       string            `json:"namespace,omitempty"`
    Labels          map[string]string `json:"labels,omitempty"`
    ResourceVersion string            `json:"resourceVersion,omitempty"`
}

type Client struct { /* http.Client configured from Config's TLS material */ }

func NewClient(cfg Config) (*Client, error)

// Get/Create/Delete/List operate against a REST path the caller
// supplies (e.g. "/api/v1/nodes", "/api/v1/namespaces/default/
// persistentvolumeclaims") -- this package knows nothing about which
// paths correspond to which Kubernetes kind; that mapping knowledge
// belongs to each capability-specific provider (spec 007+), keeping
// this client a thin, reusable transport, not a growing hardcoded
// registry of every Kubernetes API path Serendip will ever touch.
func (c *Client) Get(ctx context.Context, path string) (Object, error)
func (c *Client) Create(ctx context.Context, path string, obj Object) (Object, error)
func (c *Client) Delete(ctx context.Context, path string) error
func (c *Client) List(ctx context.Context, path string) ([]Object, error)
```

**`core/provider/registry.go`**: the live Go-interface registry.

```go
// Registry maps a capability name (e.g. "storage") to whatever live Go
// value its currently-active provider registered -- populated once,
// at cmd/serendipd startup, by code that imports both core/ and the
// specific providers/ package it wants active. core/ itself never
// imports a providers/ package (constitution rule 2); the only reason
// this works is that the import happens in cmd/, the composition root,
// not here.
type Registry struct { /* map[string]any under a mutex */ }

func NewRegistry() *Registry
func (r *Registry) Register(capability string, impl any)
// Lookup returns the registered implementation for capability, or
// (nil, false) if nothing is registered -- a Reconciler must treat
// that as "capability not configured yet," not panic or error loudly,
// mirroring core/reconcile.ErrNotConfigured's existing precedent for
// "nothing to do yet" states.
func (r *Registry) Lookup(capability string) (any, bool)
```

`Lookup` returns `any` rather than a capability-specific interface type
because `core/provider` doesn't know about capability-specific
interfaces (StorageProvider etc. don't exist here) -- callers (future
Reconcilers, defined per-capability) type-assert to whatever interface
their own capability expects. This is the one place genericity costs a
type assertion instead of compile-time safety; acceptable since the
alternative (a capability-specific interface living in a
capability-agnostic package) inverts the dependency the wrong way.

**`cmd/serendipd` wiring**: after constructing the existing `bus`/stores,
if `SERENDIP_KUBECONFIG` is set, `providers.kubernetes.LoadConfig` +
`NewClient`, construct `core/provider.NewRegistry()`, and (for this
spec's proof-of-concept only -- spec 007 replaces this with the real
Storage provider) register a trivial smoke-test capability that lists
Nodes once at startup and logs the count, proving the whole chain is
real. If `SERENDIP_KUBECONFIG` is unset, skip all of the above entirely
-- `serendipd` starts exactly as it does today (acceptance criterion 4).

## Interfaces / types to create

```go
// providers/kubernetes/config.go
type Config struct{ Host string; CAData, ClientCertData, ClientKeyData []byte; BearerToken string }
func LoadConfig(path string) (Config, error)

// providers/kubernetes/client.go
type ObjectMeta struct{ Name, Namespace string; Labels map[string]string; ResourceVersion string }
type Object struct{ APIVersion, Kind string; Metadata ObjectMeta; Spec, Status json.RawMessage }
type Client struct{ /* ... */ }
func NewClient(cfg Config) (*Client, error)
func (c *Client) Get(ctx context.Context, path string) (Object, error)
func (c *Client) Create(ctx context.Context, path string, obj Object) (Object, error)
func (c *Client) Delete(ctx context.Context, path string) error
func (c *Client) List(ctx context.Context, path string) ([]Object, error)

// core/provider/registry.go
type Registry struct{ /* ... */ }
func NewRegistry() *Registry
func (r *Registry) Register(capability string, impl any)
func (r *Registry) Lookup(capability string) (any, bool)

// cmd/serendipd/main.go (extended, not replaced)
func run() error // now also conditionally wires Kubernetes access
```

## Files touched

```
providers/CLAUDE.md                              (already anticipates providers/kubernetes/, no change needed -- confirm during T0 scaffold)
providers/kubernetes/config.go, config_test.go
providers/kubernetes/client.go, client_test.go
providers/kubernetes/testdata/kubeconfig-sample.yaml
core/provider/registry.go, registry_test.go
core/provider/doc.go, CLAUDE.md
cmd/serendipd/main.go                            (conditional Kubernetes wiring)
.claude/scripts/check-imports.sh                 (confirms core/provider doesn't import providers/, same as core/ generally)
docs/decisions/0008-sigs-k8s-yaml-for-kubeconfig-parsing.md  (new ADR, per CLAUDE.md's "any new dependency requires an ADR")
docs/architecture/provider-model.md              (resolve the open question this spec answers)
docs/PROGRESS.md                                 (updated on completion)
```

No file under `api/` is touched (spec.md Scope-OUT).

## Risks

- **A generic `any`-typed registry trades compile-time safety for
  genericity** — acceptable here (see Approach) since the alternative
  requires `core/provider` to know about every capability's interface
  shape, which inverts constitution rule 2's intended dependency
  direction. Each capability's own Reconciler is responsible for a
  correct type assertion and a clear error if it fails.
- **Only one real capability wiring exists in this spec (a
  proof-of-concept Node-listing smoke test)**, so the registration
  pattern is proven shallowly, not by a real production capability
  until spec 007. Accepted explicitly: spec 003 took the same approach
  (prove the Engine/Reconciler pattern with one concrete but narrow
  example — Cluster health — before a second kind's Reconciler existed).
- **`sigs.k8s.io/yaml`'s JSON round-trip has real but narrow
  limitations** (e.g. YAML-only features without a JSON equivalent
  don't survive) — irrelevant for parsing a standard, tool-generated
  kubeconfig file, which uses only JSON-representable YAML.
- **No watch support means a real capability's reconciler (spec 007)
  will need a resync interval to notice K8s-side-only changes** (e.g. a
  PVC a human deleted directly via `kubectl`) — same trade-off
  `core/reconcile.Engine`'s periodic resync already accepted for the
  Cluster/Provider case (spec 003), not a new limitation this spec
  introduces.

## Explicitly NOT done in this spec

- The `StorageProvider` Go interface or any other capability-specific
  interface.
- Any new resource kind.
- The actual Storage/local-path provider (spec 007).
- Watch/streaming support.
- Any `api/` change.
- Multi-cluster support.
