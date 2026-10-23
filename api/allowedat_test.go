package api

import (
	"context"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// A permission check never fails open: without the authorizer's decider on
// the request, allowedAt refuses (spec 020 T006).
func TestAllowedAtFailsClosed(t *testing.T) {
	ok, err := allowedAt(context.Background(), policy.Action{Name: "serendip/components/read"}, resource.RootScope)
	if ok || err != nil {
		t.Errorf("allowedAt without a decider = %v, %v; want a refusal", ok, err)
	}
}
