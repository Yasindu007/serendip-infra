package api

import (
	"strings"
	"testing"
)

// policyApproval names a DELETE's target from its rule. A DELETE route
// whose target it can't name would be refused by the store (the approval
// isn't for that delete), so this test says which route needs teaching.
func TestEveryDeleteRouteCanNameItsApprovalTarget(t *testing.T) {
	for pattern, ru := range routeTable() {
		if !strings.HasPrefix(pattern, "DELETE ") {
			continue
		}
		switch ru.target {
		case targetResource, targetAssignment, targetRole:
		default:
			t.Errorf("%s: target %d isn't one policyApproval names", pattern, ru.target)
		}
		if ru.kind == "" || !strings.HasSuffix(pattern, "/{id}") {
			t.Errorf("%s: want a kind and an {id}, got kind %q", pattern, ru.kind)
		}
	}
}
