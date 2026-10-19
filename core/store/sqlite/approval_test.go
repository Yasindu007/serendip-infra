package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/core/store/storetest"
)

// approvalWorld is an Organization owning a Project owning a Secret: a
// cascade of three kinds, with a Secret whose spec clean-up needs.
type approvalWorld struct {
	db        *sql.DB
	orgs      *sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus]
	projects  *sqlite.Store[resource.ProjectSpec, resource.ProjectStatus]
	secrets   *sqlite.Store[resource.SecretSpec, resource.SecretStatus]
	approvals *sqlite.Store[resource.ApprovalSpec, resource.ApprovalStatus]
	org       resource.Organization
	project   resource.Project
	secret    resource.Secret
}

func newApprovalWorld(t *testing.T) *approvalWorld {
	t.Helper()
	ctx := context.Background()
	w := &approvalWorld{db: newCascadeTestDB(t)}
	w.orgs = sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](w.db, resource.KindOrganization)
	w.projects = sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](w.db, resource.KindProject)
	w.secrets = sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](w.db, resource.KindSecret)
	w.approvals = sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](w.db, resource.KindApproval)
	var err error
	if w.org, err = w.orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}}); err != nil {
		t.Fatal(err)
	}
	if w.project, err = w.projects.Create(ctx, resource.Project{Metadata: resource.Metadata{
		Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: w.org.Metadata.ID}},
	}}); err != nil {
		t.Fatal(err)
	}
	if w.secret, err = w.secrets.Create(ctx, resource.Secret{
		Metadata: resource.Metadata{Name: "db", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: w.project.Metadata.ID}}},
		Spec:     resource.SecretSpec{Path: "web/db"},
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *approvalWorld) listApprovals(t *testing.T) []resource.Approval {
	t.Helper()
	all, err := w.approvals.List(context.Background(), store.ListOptions{Kind: resource.KindApproval})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func (w *approvalWorld) exists(t *testing.T, kind resource.Kind, id string) bool {
	t.Helper()
	ok, err := sqlite.ResourceExists(context.Background(), w.db, kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// intent is a policy-decided approval for deleting target.
func intent(target resource.Scope, cascade bool) store.ApprovalIntent {
	return store.ApprovalIntent{Approval: resource.Approval{
		Spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: target, Cascade: cascade}},
		Status: resource.ApprovalStatus{
			Phase:       resource.ApprovalApproved,
			RequestedBy: resource.ApprovalActor{Actor: "alice"},
			Decision:    &resource.ApprovalDecision{ByPolicy: true, Evidence: "allow: Owner"},
		},
	}}
}

func TestDelete_RefusesWithoutAnApproval(t *testing.T) {
	w := newApprovalWorld(t)
	err := w.secrets.Delete(context.Background(), w.secret.Metadata.ID, false)
	if !errors.Is(err, store.ErrNoApproval) {
		t.Fatalf("Delete() without an approval = %v, want ErrNoApproval", err)
	}
	if !w.exists(t, resource.KindSecret, w.secret.Metadata.ID) {
		t.Error("the Secret is gone after a refused delete")
	}
}

func TestDelete_RefusesAnApprovalForAnotherDelete(t *testing.T) {
	w := newApprovalWorld(t)
	secretRef := resource.Scope{Kind: resource.KindSecret, ID: w.secret.Metadata.ID}
	executed := intent(secretRef, false)
	executed.Approval.Status.Phase = resource.ApprovalExecuted
	tests := map[string]store.ApprovalIntent{
		"another target":   intent(resource.Scope{Kind: resource.KindSecret, ID: "other"}, false),
		"another kind":     intent(resource.Scope{Kind: resource.KindProject, ID: w.secret.Metadata.ID}, false),
		"cascade mismatch": intent(secretRef, true),
		"not approved":     executed,
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			err := w.secrets.Delete(store.WithApproval(context.Background(), in), w.secret.Metadata.ID, false)
			if !errors.Is(err, store.ErrNoApproval) {
				t.Fatalf("Delete() = %v, want ErrNoApproval", err)
			}
			if !w.exists(t, resource.KindSecret, w.secret.Metadata.ID) || len(w.listApprovals(t)) != 0 {
				t.Error("a refused delete changed something")
			}
		})
	}
}

func TestDelete_RecordsANewApprovalInTheSameTransaction(t *testing.T) {
	w := newApprovalWorld(t)
	ctx := audit.WithActor(context.Background(), "alice")
	in := intent(resource.Scope{Kind: resource.KindSecret, ID: w.secret.Metadata.ID}, false)
	in.Approval.Metadata.ID = "ap-1"
	if err := w.secrets.Delete(store.WithApproval(ctx, in), w.secret.Metadata.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if w.exists(t, resource.KindSecret, w.secret.Metadata.ID) {
		t.Fatal("the Secret still exists")
	}
	got, err := w.approvals.Get(ctx, "ap-1")
	if err != nil {
		t.Fatalf("Get(approval) = %v", err)
	}
	st := got.Status
	if st.Phase != resource.ApprovalExecuted || st.ExecutedAt.IsZero() || st.RequestedAt.IsZero() {
		t.Errorf("phase %q, executedAt %v, requestedAt %v; want Executed with both times", st.Phase, st.ExecutedAt, st.RequestedAt)
	}
	if st.TargetName != "db" || st.TargetVersion != w.secret.Metadata.ResourceVersion {
		t.Errorf("target %q v%d, want db v%d", st.TargetName, st.TargetVersion, w.secret.Metadata.ResourceVersion)
	}
	if len(st.BlastRadius) != 1 || st.BlastRadius[0].ID != w.secret.Metadata.ID || st.BlastRadius[0].Name != "db" ||
		string(st.BlastRadius[0].LastSpec) != `{"path":"web/db"}` {
		t.Errorf("blast radius = %+v, want the Secret with its name and spec", st.BlastRadius)
	}
	if want := []resource.Scope{{Kind: resource.KindProject, ID: w.project.Metadata.ID}, {Kind: resource.KindOrganization, ID: w.org.Metadata.ID}}; len(st.OwnerChain) != 2 || st.OwnerChain[0] != want[0] || st.OwnerChain[1] != want[1] {
		t.Errorf("ownerChain = %v, want %v (nearest first, read before the delete)", st.OwnerChain, want)
	}
	if len(got.Metadata.OwnerRefs) != 0 {
		t.Errorf("ownerRefs = %v, want none: a cascade must never delete an approval", got.Metadata.OwnerRefs)
	}
	if c := findCondition(st.Conditions, resource.ApprovalConditionExecuted); c == nil || c.Status != resource.ConditionTrue || c.Reason != "Deleted" {
		t.Errorf("Executed condition = %+v, want True/Deleted", c)
	}
	if got := countAuditRecords(t, w.db, "ap-1"); got != 1 {
		t.Errorf("approval audit rows = %d, want 1", got)
	}
	if rec := lastAuditRecord(t, w.db, "ap-1"); rec.Actor != "alice" || rec.Before != nil || rec.After == nil {
		t.Errorf("approval audit = actor %q before %s after %s, want alice's create", rec.Actor, rec.Before, rec.After)
	}
}

func TestDelete_CompletesAnExistingApproval(t *testing.T) {
	w := newApprovalWorld(t)
	ctx := context.Background()
	target := resource.Scope{Kind: resource.KindSecret, ID: w.secret.Metadata.ID}
	pending := intent(target, false).Approval
	pending.Metadata.Name = "delete db"
	stored, err := w.approvals.Create(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}

	unchecked := store.ApprovalIntent{Approval: stored, Existing: true}
	if err := w.secrets.Delete(store.WithApproval(ctx, unchecked), target.ID, false); !errors.Is(err, store.ErrNoApproval) {
		t.Fatalf("Delete() with a stored approval but no reviewed version or radius = %v, want ErrNoApproval", err)
	}
	reviewed := func(a resource.Approval) store.ApprovalIntent {
		return store.ApprovalIntent{Approval: a, Existing: true, ExpectedVersion: w.secret.Metadata.ResourceVersion, ExpectedRadius: []resource.Scope{target}}
	}

	stale := stored
	stale.Metadata.ResourceVersion = 99
	err = w.secrets.Delete(store.WithApproval(ctx, reviewed(stale)), target.ID, false)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Delete() with an outdated approval = %v, want ErrConflict", err)
	}
	if !w.exists(t, resource.KindSecret, target.ID) {
		t.Fatal("the Secret is gone after a conflicting approval")
	}

	if err := w.secrets.Delete(store.WithApproval(ctx, reviewed(stored)), target.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	got, err := w.approvals.Get(ctx, stored.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != resource.ApprovalExecuted || got.Metadata.ResourceVersion != 2 || len(got.Status.BlastRadius) != 1 {
		t.Errorf("approval = phase %q v%d radius %d, want Executed v2 with 1 ref", got.Status.Phase, got.Metadata.ResourceVersion, len(got.Status.BlastRadius))
	}
	if !got.Metadata.CreatedAt.Equal(stored.Metadata.CreatedAt) {
		t.Errorf("createdAt changed from %v to %v", stored.Metadata.CreatedAt, got.Metadata.CreatedAt)
	}
	if n := len(w.listApprovals(t)); n != 1 {
		t.Errorf("approvals = %d, want the one updated, not a second", n)
	}
}

func TestDelete_RefusesAStaleTarget(t *testing.T) {
	w := newApprovalWorld(t)
	ctx := context.Background()
	orgRef := resource.Scope{Kind: resource.KindOrganization, ID: w.org.Metadata.ID}
	projectRef := resource.Scope{Kind: resource.KindProject, ID: w.project.Metadata.ID}
	secretRef := resource.Scope{Kind: resource.KindSecret, ID: w.secret.Metadata.ID}

	moved := intent(orgRef, true)
	moved.ExpectedVersion = w.org.Metadata.ResourceVersion + 1
	missingChild := intent(orgRef, true)
	missingChild.ExpectedRadius = []resource.Scope{orgRef, projectRef}
	extraChild := intent(orgRef, true)
	extraChild.ExpectedRadius = []resource.Scope{orgRef, projectRef, secretRef, {Kind: resource.KindSecret, ID: "gone"}}
	swapped := intent(orgRef, true)
	swapped.ExpectedRadius = []resource.Scope{orgRef, projectRef, {Kind: resource.KindSecret, ID: "other"}}

	for name, in := range map[string]store.ApprovalIntent{
		"version moved":       moved,
		"a child was added":   missingChild,
		"a child was removed": extraChild,
		"a child was swapped": swapped,
	} {
		t.Run(name, func(t *testing.T) {
			err := w.orgs.Delete(store.WithApproval(ctx, in), orgRef.ID, true)
			if !errors.Is(err, store.ErrStale) {
				t.Fatalf("Delete() = %v, want ErrStale", err)
			}
			for _, s := range []resource.Scope{orgRef, projectRef, secretRef} {
				if !w.exists(t, s.Kind, s.ID) {
					t.Errorf("%s is gone after a stale delete", s)
				}
			}
			if n := len(w.listApprovals(t)); n != 0 {
				t.Errorf("approvals = %d after a stale delete, want 0", n)
			}
		})
	}

	exact := intent(orgRef, true)
	exact.ExpectedVersion = w.org.Metadata.ResourceVersion
	exact.ExpectedRadius = []resource.Scope{secretRef, orgRef, projectRef}
	if err := w.orgs.Delete(store.WithApproval(ctx, exact), orgRef.ID, true); err != nil {
		t.Fatalf("Delete() with the reviewed version and radius = %v", err)
	}
}

func TestDelete_FailedDeleteLeavesNoApproval(t *testing.T) {
	w := newApprovalWorld(t)
	ctx := context.Background()
	if err := w.secrets.Delete(storetest.Approved(ctx, resource.KindSecret, "nope", false), "nope", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Delete(missing) = %v, want ErrNotFound", err)
	}
	if err := w.orgs.Delete(storetest.Approved(ctx, resource.KindOrganization, w.org.Metadata.ID, false), w.org.Metadata.ID, false); !errors.Is(err, store.ErrHasChildren) {
		t.Fatalf("Delete(has children) = %v, want ErrHasChildren", err)
	}
	if n := len(w.listApprovals(t)); n != 0 {
		t.Errorf("approvals = %d after two failed deletes, want 0", n)
	}
}

func TestDelete_CascadeBlastRadiusListsEveryRemovedRow(t *testing.T) {
	w := newApprovalWorld(t)
	ctx := context.Background()
	if err := w.orgs.Delete(storetest.Approved(ctx, resource.KindOrganization, w.org.Metadata.ID, true), w.org.Metadata.ID, true); err != nil {
		t.Fatalf("Delete(cascade) = %v", err)
	}
	all := w.listApprovals(t)
	if len(all) != 1 {
		t.Fatalf("approvals = %d, want 1", len(all))
	}
	radius := all[0].Status.BlastRadius
	if len(radius) != 3 || radius[0].Kind != resource.KindOrganization || radius[0].Name != "acme" {
		t.Fatalf("blast radius = %+v, want the Organization first, then 2 more", radius)
	}
	byKind := map[resource.Kind]resource.ApprovalRef{}
	for _, r := range radius {
		byKind[r.Kind] = r
	}
	if p := byKind[resource.KindProject]; p.ID != w.project.Metadata.ID || p.Name != "web" {
		t.Errorf("project ref = %+v", p)
	}
	if s := byKind[resource.KindSecret]; s.ID != w.secret.Metadata.ID || s.Name != "db" || string(s.LastSpec) != `{"path":"web/db"}` {
		t.Errorf("secret ref = %+v, want its name and its spec for clean-up", s)
	}
}

func findCondition(cs []resource.Condition, typ string) *resource.Condition {
	for i := range cs {
		if cs[i].Type == typ {
			return &cs[i]
		}
	}
	return nil
}
