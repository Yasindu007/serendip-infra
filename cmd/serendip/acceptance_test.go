package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// Spec 021 acceptance criteria that span the CLI, serendip mcp and the
// API together. The rest are covered where they live: 2 and 5 by
// api/catalog_parity_test.go, 1 and 3 by cli_test.go, the remote transport
// by api/mcp_handler_test.go; 10 to 13 are reviews and live runs (T010 to
// T012).

// extra adds a Component and a Secret under the world's Application.
func (w *world) extra(t *testing.T) (component, secret string) {
	t.Helper()
	ctx := context.Background()
	db := w.db
	c, err := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent).Create(ctx,
		resource.Resource[resource.ComponentSpec, resource.ComponentStatus]{Metadata: resource.Metadata{Name: "web",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: w.appID}}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret).Create(ctx,
		resource.Resource[resource.SecretSpec, resource.SecretStatus]{Metadata: resource.Metadata{Name: "db-password",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: w.appID}}}, Spec: resource.SecretSpec{Path: "shop/db"}})
	if err != nil {
		t.Fatal(err)
	}
	return c.Metadata.ID, s.Metadata.ID
}

func (w *world) audit(t *testing.T, where string, args ...any) (actor, sponsor, iface string) {
	t.Helper()
	if err := w.db.QueryRow(`SELECT actor, COALESCE(actor_sponsor,''), interface FROM audit_log WHERE `+where+` ORDER BY at DESC LIMIT 1`, args...).Scan(&actor, &sponsor, &iface); err != nil {
		t.Fatalf("audit row %s: %v", where, err)
	}
	return actor, sponsor, iface
}

// Criteria 4, 6, 7 and 9: the same delete is approved by policy for the
// person, and waits for them when their AI asks; the AI files the request,
// reads it, can't approve it, and the person approves it from the CLI.
func TestAcceptance_SameActionPersonAndAI(t *testing.T) {
	w := newWorld(t)
	w.signIn(t)
	web, _ := w.extra(t)

	// The person, through the CLI: approved by policy at once (spec 019).
	r := w.cli(nil, "", "components", "delete", web)
	if r.code != exitOK || !strings.Contains(r.stderr, "Recorded as approval") {
		t.Fatalf("the person's delete: %d %s", r.code, r.stderr)
	}
	actor, sponsor, iface := w.audit(t, "kind = 'Approval'")
	if actor != "person:owner" || sponsor != "" || iface != "cli (declared)" {
		t.Errorf("person's approval audited as actor %q sponsor %q interface %q", actor, sponsor, iface)
	}

	// Their AI, through serendip mcp, the same action on the Application:
	// it needs the person.
	_, got := w.session(t, nil, nil,
		call(1, "applications_delete", fmt.Sprintf(`{"id":%q,"cascade":true}`, w.appID)),
		call(2, "approvals_request", fmt.Sprintf(`{"kind":"Application","id":%q,"cascade":true,"reason":"shop is retired"}`, w.appID)),
	)
	if text, isErr := toolText(t, got[1]); !isErr || !strings.Contains(text, "needs") {
		t.Fatalf("the AI's delete: %v %q", isErr, text)
	}
	text, isErr := toolText(t, got[2])
	if isErr {
		t.Fatalf("the AI files the request: %q", text)
	}
	var req resource.Approval
	if err := json.Unmarshal([]byte(text), &req); err != nil || req.Status.Phase != resource.ApprovalPending || req.Spec.Reason != "shop is retired" {
		t.Fatalf("filed request = %q", text)
	}
	actor, sponsor, iface = w.audit(t, "kind = 'Approval' AND resource_id = ?", req.Metadata.ID)
	if actor != "agent:serendip-mcp/sub-owner" || sponsor != "sub-owner" || iface != "mcp (declared)" {
		t.Errorf("AI's request audited as actor %q sponsor %q interface %q", actor, sponsor, iface)
	}

	// The AI reads its state, but can't decide it, by any path.
	_, got = w.session(t, nil, nil,
		call(1, "approvals_get", fmt.Sprintf(`{"id":%q}`, req.Metadata.ID)),
		call(2, "approvals_approve", fmt.Sprintf(`{"id":%q}`, req.Metadata.ID)),
	)
	if text, isErr := toolText(t, got[1]); isErr || !strings.Contains(text, "Pending") {
		t.Errorf("the AI reads its request: %v %q", isErr, text)
	}
	if got[2]["error"] == nil {
		if _, isErr := toolText(t, got[2]); !isErr {
			t.Error("the AI approved through MCP")
		}
	}
	if r := w.cli(map[string]string{"SERENDIP_TOKEN": "tok-ai"}, "", "approvals", "approve", req.Metadata.ID); r.code != exitDenied {
		t.Errorf("the AI's token approving through the API: %d %s", r.code, r.stderr)
	}

	// The person approves from the CLI (the approval reconciler, which
	// isn't running here, then executes it; T011 shows that live).
	r = w.cli(nil, "", "approvals", "approve", req.Metadata.ID, "--comment", "agreed", "--json")
	if r.code != exitOK || !strings.Contains(r.stdout, `"Approved"`) {
		t.Errorf("the person approves: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

// Criterion 4, scripts: a script with an AI's token gets "needs approval"
// and files the request with --reason.
func TestAcceptance_ScriptDeleteFilesTheRequest(t *testing.T) {
	w := newWorld(t)
	web, _ := w.extra(t)
	r := w.cli(map[string]string{"SERENDIP_TOKEN": "tok-ai"}, "", "components", "delete", web, "--reason", "unused")
	if r.code != exitNeedsApproval || !strings.Contains(r.stderr, "filed (Pending)") {
		t.Errorf("script delete with --reason: %d %s", r.code, r.stderr)
	}
}

// Criterion 8: without the data permission, value tools are refused and
// metadata tools work. The person is Owner, which has no data actions
// (Azure's model), so neither they nor their AI read a value.
func TestAcceptance_SecretsThroughMCP(t *testing.T) {
	w := newWorld(t)
	w.signIn(t)
	_, secret := w.extra(t)
	_, got := w.session(t, nil, nil,
		call(1, "secrets_get", fmt.Sprintf(`{"id":%q}`, secret)),
		call(2, "secrets_read_value", fmt.Sprintf(`{"id":%q}`, secret)),
	)
	if text, isErr := toolText(t, got[1]); isErr || !strings.Contains(text, "db-password") {
		t.Errorf("metadata: %v %q", isErr, text)
	}
	if text, isErr := toolText(t, got[2]); !isErr || !strings.Contains(text, "HTTP 403") || !strings.Contains(text, "serendip/secrets/value/read") {
		t.Errorf("value without the data permission: %v %q", isErr, text)
	}
}
