// Package id generates Serendip resource identifiers (UUIDv7, see ADR 0007).
package id

import "github.com/google/uuid"

// New returns a new UUIDv7 resource identifier, suitable for
// Metadata.ID on any Serendip resource.
//
// UUIDv7 embeds a millisecond timestamp in its high bits, so IDs
// generated in quick succession within the same process tend to sort
// lexically in generation order. This is a convenience property only —
// it is not guaranteed across processes, clock adjustments, or google/uuid
// implementation versions. Metadata.CreatedAt remains the authoritative
// creation time (see docs/architecture/resource-model.md); do not parse
// or rely on a timestamp encoded in the ID itself.
func New() string {
	u, err := uuid.NewV7()
	if err != nil {
		// crypto/rand failure here means the system cannot generate
		// unique resource IDs at all -- unrecoverable for this process.
		panic("id: failed to generate UUIDv7: " + err.Error())
	}
	return u.String()
}
