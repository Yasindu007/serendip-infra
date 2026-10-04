// Package kubernetes is a minimal, generic REST client for the
// Kubernetes API -- not client-go (ADR-level reasoning in
// specs/006-kubernetes-provider-foundation/spec.md: client-go's
// transitive weight is a real, upstream-acknowledged problem against
// this project's lightweight philosophy, for capabilities this package
// only needs a handful of REST+JSON calls to cover).
//
// This package knows nothing about which Kubernetes object kind lives
// at which API path -- that mapping knowledge belongs to each
// capability-specific provider that uses this client (providers/storage/
// local-path and beyond), keeping this package a thin, reusable
// transport layer rather than a growing hardcoded registry of every
// Kubernetes API path Serendip will ever touch.
package kubernetes
