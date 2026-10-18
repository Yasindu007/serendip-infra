package policy

import (
	"strings"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// matches reports whether pattern covers action. Both are compared
// case-insensitively. A pattern holds at most one "*" (resource validation
// enforces that), which matches any run of characters, slashes included:
// "*/read" covers "serendip/components/read" and "serendip/*" covers
// everything Serendip defines, as Azure's wildcards do.
func matches(pattern, action string) bool {
	p, a := strings.ToLower(pattern), strings.ToLower(action)
	star := strings.IndexByte(p, '*')
	if star < 0 {
		return p == a
	}
	prefix, suffix := p[:star], p[star+1:]
	return len(a) >= len(prefix)+len(suffix) && strings.HasPrefix(a, prefix) && strings.HasSuffix(a, suffix)
}

func anyMatches(patterns []string, action string) bool {
	for _, p := range patterns {
		if matches(p, action) {
			return true
		}
	}
	return false
}

// grants reports whether one permission block grants the action: covered
// by actions (or dataActions) and not by notActions (or notDataActions).
// Exclusions apply only within their own block, never across roles.
func grants(p resource.Permission, a Action) bool {
	if a.Data {
		return anyMatches(p.DataActions, a.Name) && !anyMatches(p.NotDataActions, a.Name)
	}
	return anyMatches(p.Actions, a.Name) && !anyMatches(p.NotActions, a.Name)
}
