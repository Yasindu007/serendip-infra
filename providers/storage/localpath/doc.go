// Package localpath implements core/reconcile.StorageProvider via
// static PersistentVolume/PersistentVolumeClaim binding -- NOT by
// driving Rancher's local-path-provisioner (rancher.io/local-path)'s
// dynamic provisioning. Real-cluster testing found the provisioner's
// default per-node hostPath mode hard-requires a target node, supplied
// only via the Kubernetes scheduler's WaitForFirstConsumer flow, which
// has no consuming Pod to trigger it in this project's current scope.
// This package only reuses the provisioner's own default backing-path
// convention (/var/lib/rancher/k3s/storage) for familiarity -- see
// provider.go's doc comment for the full reasoning, and
// specs/007-storage-local-path-provider/plan.md for the design history
// (the "fix(provider)" commit that made this correction).
package localpath
