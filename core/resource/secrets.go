package resource

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// KindSecret is a reference to a secret value held by the Secrets
// capability's provider (spec 008's OpenBao foundation; spec 009 builds
// this kind on top of it). No collision risk with the "secrets"
// capability name the way Volume/Storage had (spec 007) -- "Secret"
// (a resource) and "secrets" (the capability) read unambiguously side
// by side.
const KindSecret Kind = "Secret"

// SecretSpec deliberately holds only a reference, never the secret's
// actual value -- every mutation through core/store writes an
// unconditional, whole-resource JSON snapshot into the audit log's
// Before/After columns (core/store/sqlite/store.go), with no per-field
// redaction mechanism. If the value lived here, it would be captured
// verbatim, forever, in that audit trail -- exactly what using a
// dedicated secrets engine instead of bare Kubernetes Secrets is
// supposed to prevent (specs/009-secrets-capability/spec.md). Path is
// user-chosen (e.g. "myapp/db-password"), not derived from Metadata.ID
// the way Volume's PVC name is -- nobody wants to address secrets by
// opaque UUID, and real secrets managers universally use human-chosen
// paths. No "mount" concept here -- that's OpenBao/Vault-specific
// jargon (constitution rule 1); the KV v2 mount is a single
// deployment-wide constant (providers/secrets/openbao.DefaultKVMount),
// not a per-Secret choice.
type SecretSpec struct {
	Path string `json:"path"`
}

// MaxSecretPathLen bounds a Secret's path.
const MaxSecretPathLen = 256

// ValidateSecretSpec checks a Secret's path: slash-separated segments, none
// empty or "." or "..", and none of the characters that change a URL's
// meaning (?, #, %). The path names the value within its own Secret only:
// providers store each value under the Secret's identity, so a path can
// never reach another Secret's value (spec 017 T011).
func ValidateSecretSpec(s SecretSpec) error {
	if s.Path == "" || len(s.Path) > MaxSecretPathLen {
		return fmt.Errorf("%w: secret path must be 1 to %d characters", ErrValidation, MaxSecretPathLen)
	}
	if strings.ContainsAny(s.Path, "?#%\\") {
		return fmt.Errorf("%w: secret path %q contains ?, #, %% or a backslash", ErrValidation, s.Path)
	}
	for _, seg := range strings.Split(s.Path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: secret path %q has an empty, \".\" or \"..\" segment", ErrValidation, s.Path)
		}
		if strings.IndexFunc(seg, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: secret path %q contains a control character", ErrValidation, s.Path)
		}
	}
	return nil
}

// SecretStatus carries one Condition, Type "Reachable" -- True when a
// value currently exists at this Secret's Path in the provider, False
// when it doesn't (never written, or removed out-of-band), Unknown on
// a check error. Named "Reachable", not "Synced": there is no desired
// value in Spec to compare against, only existence to observe
// (constitution rule 6, honest capability reporting -- this Condition
// must never imply a value-matching guarantee that doesn't exist).
// LastSyncedAt is set only by a successful value write (core/reconcile.
// SecretReconciler.WriteValue) -- it exists specifically so that write
// carries real Status content to audit through the ordinary
// store.Update path, without ever needing a core/audit change.
type SecretStatus struct {
	Status
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
}

type Secret = Resource[SecretSpec, SecretStatus]
