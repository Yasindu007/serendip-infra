// Package identity is the Identity capability's human-login contract: a
// bearer credential presented to the API is turned into a Principal by an
// Authenticator, and the Principal becomes the audit actor (constitution
// rule 8). It covers human identity only (docs/architecture/identity.md
// section 1); platform and workload identity are separate and not modeled
// here yet. A Directory reads the provider's current people and groups,
// for picking whom to grant a role and for checking someone's access.
//
// Implementations live under providers/. The composition root wraps the
// active one in a handle that answers reconcile.ErrProviderNotReady until
// the provider has finished setting up (specs/015-identity-foundation
// plan.md decision 10).
package identity
