package reconcile

// InterfaceName and ActorName identify reconciler-driven mutations in
// audit_log (core/audit), set by Engine on the context passed into every
// Reconcile call -- individual Reconciler implementations should not set
// these themselves. Distinguishes "the engine changed this" from a
// human/API-driven change (api.InterfaceName = "http") or a genuinely
// unauthenticated one (audit.ActorFrom's "unknown" default is meant to
// flag that, not legitimate automated activity).
const (
	InterfaceName = "reconcile"
	ActorName     = "system:reconciler"
)
