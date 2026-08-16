package resource

import (
	"fmt"
	"regexp"
	"strings"
)

// labelNameRE matches a Kubernetes-style label key name or value:
// alphanumeric, optionally separated internally by '-', '_', or '.',
// <=63 chars total. Reused verbatim rather than inventing a new grammar
// (resource-model.md follows Kubernetes API conventions throughout).
var labelNameRE = regexp.MustCompile(`^[a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?$`)

// labelPrefixRE matches the optional DNS-subdomain prefix of a label key
// (the part before '/', e.g. "serendip.dev" in "serendip.dev/environment").
var labelPrefixRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

// Validate checks a Metadata envelope before it's persisted. It does not
// validate kind-specific Spec fields -- that's each kind's own concern,
// not the envelope's.
func Validate(m Metadata) error {
	if m.Name == "" {
		return fmt.Errorf("resource: Name is required")
	}
	for key, value := range m.Labels {
		if err := validateLabelKey(key); err != nil {
			return fmt.Errorf("resource: invalid label key %q: %w", key, err)
		}
		if value != "" && !labelNameRE.MatchString(value) {
			return fmt.Errorf("resource: invalid label value %q for key %q", value, key)
		}
	}
	for i, rel := range m.Relationships {
		if rel.Type == EdgeOwns {
			return fmt.Errorf("resource: Relationships[%d]: EdgeOwns is derived from OwnerRefs, not a declarable Relationship type", i)
		}
	}
	return nil
}

func validateLabelKey(key string) error {
	name := key
	if i := strings.LastIndex(key, "/"); i >= 0 {
		prefix, rest := key[:i], key[i+1:]
		if prefix == "" || !labelPrefixRE.MatchString(prefix) {
			return fmt.Errorf("invalid prefix %q", prefix)
		}
		name = rest
	}
	if !labelNameRE.MatchString(name) {
		return fmt.Errorf("invalid name %q", name)
	}
	return nil
}
