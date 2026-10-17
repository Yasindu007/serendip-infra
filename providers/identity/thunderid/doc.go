// Package thunderid is the Identity capability's default provider
// foundation: it installs ThunderID (ADR 0006; github.com/thunder-id/thunderid)
// on the cluster and exposes its login-facing endpoints through the shared
// gateway (specs/015-identity-foundation plan.md).
//
// What it owns:
//   - Deployer installs the official Helm chart (one replica, SQLite on the
//     chart's PVCs, plain HTTP), with a fixed public OIDC client declared
//     through the chart's first-install bootstrap files (decisions 1, 4).
//   - EnsureAdminSecret creates the initial administrator's password once
//     and never changes it (decision 3).
//   - EnsureRoute applies the HTTPRoute that exposes only the paths a login
//     needs (decision 6).
//   - CheckReady and JWKSProxyURL reach the server through the Kubernetes
//     API's Service proxy, the same way providers/secrets/openbao does --
//     serendipd runs outside the cluster.
//
// Verifying tokens is not this package's job: providers/identity/oidc does
// that against any issuer, and the composition root points it at this
// one.
package thunderid
