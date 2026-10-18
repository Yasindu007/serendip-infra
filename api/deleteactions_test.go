package api

import (
	"strings"
	"testing"
)

// Every DELETE route must check an action named ".../delete": that is what
// the agent guardrail recognises as a delete needing an approval
// (specs/018-agent-manager-poc plan.md decision 9). A delete under another
// verb would let an agent skip it.
func TestEveryDeleteRouteChecksADeleteAction(t *testing.T) {
	n := 0
	for pattern, ru := range routeTable() {
		if !strings.HasPrefix(pattern, "DELETE ") {
			continue
		}
		n++
		if ru.authOnly || ru.list || !strings.HasSuffix(ru.action.Name, "/delete") {
			t.Errorf("%s checks %+v, want an action ending in /delete", pattern, ru)
		}
	}
	if n == 0 {
		t.Fatal("no DELETE routes found")
	}
}
