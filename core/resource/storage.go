package resource

// KindVolume is a primary storage request (resource-model.md
// "Cross-cutting infrastructure resources" calls this "Storage" --
// this spec names the resource kind "Volume" instead, deliberately, to
// avoid colliding with "Storage" already being used throughout
// capability-model.md/storage.md as the *capability* name. A resource
// kind and the capability it belongs to sharing one name would be
// confusing in API paths, logs, and error messages
// (specs/007-storage-local-path-provider/spec.md).
const KindVolume Kind = "Volume"

// VolumeSpec is deliberately minimal for this spec: one size, no
// tier/policy/replication/backup/encryption yet. storage.md's full
// storagePolicy concept (performance, replication, backup, retention,
// encryption) is real future work, not invented speculatively now
// (constitution rule 10) -- it has no consumer yet since only one
// provider (local-path, unreplicated, no backup) exists.
type VolumeSpec struct {
	SizeBytes int64 `json:"sizeBytes"`
}

// VolumeStatus carries one Condition, Type "Bound" -- True once the
// backing storage is actually bound and usable, False if provisioning
// failed or it exists but isn't bound yet, Unknown if no storage
// provider is configured at all. Mirrors ClusterStatus's single-
// Condition precedent (spec 003) rather than a separate boolean field.
type VolumeStatus struct {
	Status
}

type Volume = Resource[VolumeSpec, VolumeStatus]
