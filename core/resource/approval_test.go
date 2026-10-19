package resource_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestApproval_KindConstant(t *testing.T) {
	if string(resource.KindApproval) != "Approval" {
		t.Errorf("KindApproval = %q, want %q", resource.KindApproval, "Approval")
	}
}

func TestApprovalTransition(t *testing.T) {
	phases := []resource.ApprovalPhase{
		resource.ApprovalPending, resource.ApprovalApproved, resource.ApprovalRejected,
		resource.ApprovalExpired, resource.ApprovalExecuted, resource.ApprovalFailed,
	}
	legal := map[[2]resource.ApprovalPhase]bool{
		{resource.ApprovalPending, resource.ApprovalApproved}:  true,
		{resource.ApprovalPending, resource.ApprovalRejected}:  true,
		{resource.ApprovalPending, resource.ApprovalExpired}:   true,
		{resource.ApprovalApproved, resource.ApprovalExecuted}: true,
		{resource.ApprovalApproved, resource.ApprovalFailed}:   true,
	}
	for _, from := range phases {
		for _, to := range phases {
			err := resource.ApprovalTransition(from, to)
			if want := legal[[2]resource.ApprovalPhase{from, to}]; want != (err == nil) {
				t.Errorf("ApprovalTransition(%s, %s) = %v, want legal=%v", from, to, err, want)
			}
			if err != nil && !errors.Is(err, resource.ErrValidation) {
				t.Errorf("ApprovalTransition(%s, %s) error doesn't wrap ErrValidation: %v", from, to, err)
			}
		}
	}
	if err := resource.ApprovalTransition("", resource.ApprovalExecuted); err == nil {
		t.Error("ApprovalTransition from no phase to Executed = nil, want an error")
	}
}

func TestApprovalPhase_Terminal(t *testing.T) {
	tests := map[resource.ApprovalPhase]bool{
		resource.ApprovalPending:  false,
		resource.ApprovalApproved: false,
		resource.ApprovalRejected: true,
		resource.ApprovalExpired:  true,
		resource.ApprovalExecuted: true,
		resource.ApprovalFailed:   true,
		"":                        false,
	}
	for p, want := range tests {
		if got := p.Terminal(); got != want {
			t.Errorf("%q.Terminal() = %v, want %v", p, got, want)
		}
	}
}

func TestValidateApprovalSpec(t *testing.T) {
	target := resource.Scope{Kind: resource.KindComponent, ID: "c-1"}
	tests := []struct {
		name    string
		spec    resource.ApprovalSpec
		wantErr string
	}{
		{name: "delete with reason", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: target}, Reason: "retired"}},
		{name: "cascade without reason", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: target, Cascade: true}}},
		{name: "multi-line reason", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: target}, Reason: "line one\nline two"}},
		{name: "other verb", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "scale", Target: target}}, wantErr: "action.verb"},
		{name: "no verb", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Target: target}}, wantErr: "action.verb"},
		{name: "root target", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete"}}, wantErr: "action.target"},
		{name: "no kind", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: resource.Scope{ID: "c-1"}}}, wantErr: "action.target"},
		{name: "no id", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: resource.Scope{Kind: resource.KindComponent}}}, wantErr: "action.target"},
		{name: "path in id", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: resource.Scope{Kind: resource.KindComponent, ID: "a/b"}}}, wantErr: "action.target.id"},
		{name: "dot-dot id", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: resource.Scope{Kind: resource.KindComponent, ID: ".."}}}, wantErr: "action.target.id"},
		{name: "reason too long", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: target}, Reason: strings.Repeat("x", resource.MaxApprovalReasonLen+1)}, wantErr: "reason"},
		{name: "control character in reason", spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: "delete", Target: target}, Reason: "bad\x1b[31m"}, wantErr: "reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.ValidateApprovalSpec(tt.spec)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateApprovalSpec() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateApprovalSpec() = %v, want an error mentioning %q", err, tt.wantErr)
			}
			if !errors.Is(err, resource.ErrValidation) {
				t.Errorf("error doesn't wrap ErrValidation: %v", err)
			}
		})
	}
}

func TestValidateApprovalComment(t *testing.T) {
	if err := resource.ValidateApprovalComment("ok, checked with the team"); err != nil {
		t.Errorf("ValidateApprovalComment(ordinary) = %v, want nil", err)
	}
	if err := resource.ValidateApprovalComment(strings.Repeat("x", resource.MaxApprovalCommentLen+1)); !errors.Is(err, resource.ErrValidation) {
		t.Errorf("ValidateApprovalComment(too long) = %v, want ErrValidation", err)
	}
}

// The wire shape is what the console and every client read; pin the
// field names so a rename is a deliberate, visible change.
func TestApproval_JSONShape(t *testing.T) {
	a := resource.Approval{
		Metadata: resource.Metadata{ID: "ap-1", Name: "delete Component/c-1", Kind: resource.KindApproval},
		Spec: resource.ApprovalSpec{
			Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: resource.KindComponent, ID: "c-1"}, Cascade: true},
			Reason: "retired",
		},
		Status: resource.ApprovalStatus{
			Phase:       resource.ApprovalExecuted,
			RequestedBy: resource.ApprovalActor{Actor: "agent:am/a1", Principal: &resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am/a1"}, Sponsor: "person:alice"},
			BlastRadius: []resource.ApprovalRef{{Kind: resource.KindSecret, ID: "s-1", Name: "db", LastSpec: json.RawMessage(`{"path":"app/db"}`), CleanedUp: true}},
			Decision:    &resource.ApprovalDecision{ByPolicy: true, Evidence: "allow: Contributor"},
		},
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"action":{"verb":"delete","target":{"kind":"Component","id":"c-1"},"cascade":true}`,
		`"reason":"retired"`,
		`"phase":"Executed"`,
		`"requestedBy":{"actor":"agent:am/a1","principal":{"type":"agent","id":"am/a1"},"sponsor":"person:alice"}`,
		`"blastRadius":[{"kind":"Secret","id":"s-1","name":"db","lastSpec":{"path":"app/db"},"cleanedUp":true}]`,
		`"decision":{"byPolicy":true,"at":`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %s\n got %s", want, raw)
		}
	}
	for _, unwanted := range []string{`"expiresAt"`, `"executedAt"`} {
		if strings.Contains(string(raw), unwanted) {
			t.Errorf("JSON has zero-valued %s; want it omitted\n got %s", unwanted, raw)
		}
	}
}
