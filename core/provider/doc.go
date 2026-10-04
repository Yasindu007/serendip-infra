// Package provider is the in-process registry of live Go capability
// implementations -- distinct from core/registry's CapabilityRegistry/
// ProviderRegistry, which manage persisted descriptor *data*, not live
// code. See core/provider/CLAUDE.md for the distinction and
// specs/006-kubernetes-provider-foundation/plan.md for the design.
package provider
