# ADR 0008: sigs.k8s.io/yaml for kubeconfig parsing

Status: Accepted
Date: 2026-10

## Context

Spec 006 (`providers/kubernetes`) needs to parse a standard kubeconfig
file (the format K3s's installer writes to `/etc/rancher/k3s/k3s.yaml`,
spec 005) to extract the cluster server URL, CA certificate, and
client/bearer auth material. Go's standard library has no YAML parser
(constitution rule 4: standard library first, any new dependency needs
an ADR stating what it buys us and its transitive weight).

Two realistic candidates: `gopkg.in/yaml.v3` (the most widely used
general-purpose Go YAML library) and `sigs.k8s.io/yaml` (a thin wrapper
that round-trips YAML through `encoding/json`: parse YAML → generic
value → marshal to JSON → `json.Unmarshal` into the target struct).

A third option — hand-rolling a kubeconfig-specific parser without a
YAML dependency at all — was considered and rejected: kubeconfig is a
real, non-trivial YAML document (nested maps/lists, multi-document
structure for multi-context files), and reinventing a YAML parser for
one file format has no payoff over taking one small, well-established
dependency, the same reasoning ADR 0007 used to reject hand-rolling a
sortable ID.

## Decision

Use `sigs.k8s.io/yaml`. The deciding factor: `providers/kubernetes`
also needs Go structs for Kubernetes API objects (JSON, not YAML) in
the same package (`client.go`'s `Object`/`ObjectMeta`). Because
`sigs.k8s.io/yaml` parses YAML by converting to JSON first, **the same
struct and the same `json:` tags work for both** the kubeconfig (YAML)
and the Kubernetes API (JSON) — one set of Go types, no parallel
`yaml:`/`json:` tag maintenance on every struct this package defines.
`gopkg.in/yaml.v3` would require separate `yaml:` tags throughout,
duplicating the JSON-tagged types this package already needs.

Transitive weight, verified directly (not estimated) by building a
throwaway module against it:

```
go build + go list -deps: only one additional module actually linked
into the build: go.yaml.in/yaml/v2.
```

`go list -m all` additionally shows `github.com/google/go-cmp`,
`go.yaml.in/yaml/v3`, `gopkg.in/check.v1`, and `sigs.k8s.io/randfill`
in the module graph — these are test-only dependencies of
`sigs.k8s.io/yaml`'s own test suite, never imported by or linked into
code that uses the package, confirmed via `go list -deps` on an actual
importing program. Real added weight: one small module.

## Consequences

- One new direct dependency: `sigs.k8s.io/yaml` (one real transitive
  dependency, `go.yaml.in/yaml/v2`).
- `providers/kubernetes/client.go`'s `Object`/`ObjectMeta` structs use
  `json:` tags only; `providers/kubernetes/config.go`'s kubeconfig
  structs do the same, and both parse correctly through
  `sigs.k8s.io/yaml.Unmarshal` despite the source being YAML.
- Any future `providers/` package that needs to parse another
  Kubernetes-adjacent YAML file (a Helm-values-shaped config, a profile
  manifest) should default to this same library for consistency, rather
  than introducing `gopkg.in/yaml.v3` as a second YAML dependency,
  unless a real need for YAML-only features (anchors, multi-document
  streaming) that `sigs.k8s.io/yaml`'s JSON round-trip can't represent
  actually arises.
