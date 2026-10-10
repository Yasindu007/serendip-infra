// Package openbao implements the Secrets & PKI capability's deployment
// foundation against OpenBao (the Linux Foundation/OpenSSF-governed
// Vault fork) -- see docs/decisions/0009-openbao-as-secrets-provider.md
// for why, and specs/008-openbao-secrets-foundation/ for scope.
//
// This package deliberately does NOT depend on
// github.com/openbao/openbao/api/v2, OpenBao's official Go client --
// ADR 0009 rejects it on the same transitive-dependency-weight grounds
// providers/kubernetes rejected client-go (ADR 0008's sibling
// reasoning). Client is a minimal, hand-rolled REST client covering
// only health, init, unseal, and KV v2 read/write/delete -- the exact
// surface this spec and spec 009 need, nothing more.
//
// serendipd runs outside Kubernetes (same model as providers/
// kubernetes since spec 002), so it never talks to OpenBao's
// ClusterIP Service directly -- every call in this package is made
// against a baseURL built from providers/kubernetes.Client.ProxyURL,
// riding the Kubernetes API server's own Service-proxy subresource
// instead of requiring any new network exposure.
//
// This spec (008) is foundation only: deployment (Deployer), init/
// unseal lifecycle (Bootstrap), and the REST client (Client). The
// real Secret resource kind, SecretsProvider core interface, and
// reconciler are spec 009's job, built on top of what this package
// provides.
package openbao
