// Package localpath implements core/reconcile.StorageProvider against
// Rancher's local-path-provisioner (rancher.io/local-path) -- already
// running by default in any stock K3s install (confirmed via spec
// 005's own bootstrapped clusters; this is K3s's bundled
// "local-storage" component, the same provisioner, not a separate
// install step). See specs/007-storage-local-path-provider/plan.md.
package localpath
