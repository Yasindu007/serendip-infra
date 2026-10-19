package reconcile_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// approvalFixture is an Organization owning a Project owning two Secrets,
// an approval store, and a reconciler over them with a recording cleaner.
type approvalFixture struct {
	t         *testing.T
	db        *sql.DB
	orgs      *sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus]
	projects  *sqlite.Store[resource.ProjectSpec, resource.ProjectStatus]
	secrets   *sqlite.Store[resource.SecretSpec, resource.SecretStatus]
	approvals *sqlite.Store[resource.ApprovalSpec, resource.ApprovalStatus]
	org       resource.Organization
	project   resource.Project
	s1, s2    resource.Secret
	now       time.Time
	cleaned   []string         // Secret IDs the cleaner erased, in order
	cleanErr  map[string]error // ID -> the next error to return, once
	deleteErr error            // when set, the DeleteFunc fails with it
	r         *reconcile.ApprovalReconciler
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	f := &approvalFixture{t: t, db: db, now: time.Now().UTC(), cleanErr: map[string]error{}}
	f.orgs = sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	f.projects = sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)
	f.secrets = sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret)
	f.approvals = sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](db, resource.KindApproval)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.org, err = f.orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	must(err)
	f.project, err = f.projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: f.org.Metadata.ID}}}})
	must(err)
	owned := []resource.OwnerRef{{Kind: resource.KindProject, ID: f.project.Metadata.ID}}
	f.s1, err = f.secrets.Create(ctx, resource.Secret{Metadata: resource.Metadata{Name: "db", OwnerRefs: owned}, Spec: resource.SecretSpec{Path: "web/db"}})
	must(err)
	f.s2, err = f.secrets.Create(ctx, resource.Secret{Metadata: resource.Metadata{Name: "api", OwnerRefs: owned}, Spec: resource.SecretSpec{Path: "web/api"}})
	must(err)

	del := func(ctx context.Context, kind resource.Kind, id string, cascade bool) error {
		if f.deleteErr != nil {
			return f.deleteErr
		}
		return sqlite.NewStore[json.RawMessage, json.RawMessage](db, kind).Delete(ctx, id, cascade)
	}
	cleaners := map[resource.Kind]reconcile.CleanupFunc{resource.KindSecret: func(_ context.Context, ref resource.ApprovalRef) error {
		if err := f.cleanErr[ref.ID]; err != nil {
			delete(f.cleanErr, ref.ID)
			return err
		}
		var spec resource.SecretSpec
		if json.Unmarshal(ref.LastSpec, &spec) != nil || spec.Path == "" {
			t.Errorf("cleaner got %s without its recorded path", ref.ID)
		}
		f.cleaned = append(f.cleaned, ref.ID)
		return nil
	}}
	exists := func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		return sqlite.ResourceExists(ctx, db, kind, id)
	}
	f.r = reconcile.NewApprovalReconciler(f.approvals, del, cleaners, exists, func() time.Time { return f.now })
	return f
}

// approved stores an approval for deleting the Organization with cascade,
// approved by a person, recording the target and blast radius as the API
// does at request time.
func (f *approvalFixture) approved() resource.Approval {
	f.t.Helper()
	radius := []resource.ApprovalRef{
		{Kind: resource.KindOrganization, ID: f.org.Metadata.ID, Name: "acme"},
		{Kind: resource.KindProject, ID: f.project.Metadata.ID, Name: "web"},
		{Kind: resource.KindSecret, ID: f.s1.Metadata.ID, Name: "db"},
		{Kind: resource.KindSecret, ID: f.s2.Metadata.ID, Name: "api"},
	}
	return f.create(resource.ApprovalApproved, radius, time.Time{})
}

func (f *approvalFixture) create(phase resource.ApprovalPhase, radius []resource.ApprovalRef, expires time.Time) resource.Approval {
	f.t.Helper()
	a, err := f.approvals.Create(context.Background(), resource.Approval{
		Metadata: resource.Metadata{Name: "delete acme"},
		Spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete,
			Target: resource.Scope{Kind: resource.KindOrganization, ID: f.org.Metadata.ID}, Cascade: true}, Reason: "retire"},
		Status: resource.ApprovalStatus{
			Phase:         phase,
			ExpiresAt:     expires,
			RequestedBy:   resource.ApprovalActor{Actor: "agent:am/a1", Principal: &resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am/a1"}},
			TargetVersion: f.org.Metadata.ResourceVersion,
			BlastRadius:   radius,
			Decision:      &resource.ApprovalDecision{By: &resource.ApprovalActor{Actor: "person:bob", Principal: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "bob"}}},
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f *approvalFixture) reconcile(a resource.Approval) error {
	return f.r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindApproval, ID: a.Metadata.ID})
}

func (f *approvalFixture) get(a resource.Approval) resource.Approval {
	f.t.Helper()
	got, err := f.approvals.Get(context.Background(), a.Metadata.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *approvalFixture) exists(kind resource.Kind, id string) bool {
	f.t.Helper()
	ok, err := sqlite.ResourceExists(context.Background(), f.db, kind, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return ok
}

func condition(a resource.Approval, typ string) resource.Condition {
	for _, c := range a.Status.Conditions {
		if c.Type == typ {
			return c
		}
	}
	return resource.Condition{}
}

func TestApprovalReconciler_ExecutesCleansUpAndVerifies(t *testing.T) {
	f := newApprovalFixture(t)
	a := f.approved()

	if err := f.reconcile(a); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, id := range []string{f.org.Metadata.ID, f.project.Metadata.ID, f.s1.Metadata.ID, f.s2.Metadata.ID} {
		if f.exists(resource.KindOrganization, id) || f.exists(resource.KindProject, id) || f.exists(resource.KindSecret, id) {
			t.Errorf("%s still exists after execution", id)
		}
	}
	a = f.get(a)
	if a.Status.Phase != resource.ApprovalExecuted || len(f.cleaned) != 0 {
		t.Fatalf("after execute: phase %q, cleaned %v; want Executed, nothing cleaned yet", a.Status.Phase, f.cleaned)
	}
	var decision string
	if err := f.db.QueryRow(`SELECT policy_decision FROM audit_log WHERE resource_id = ? AND after IS NULL`, f.org.Metadata.ID).Scan(&decision); err != nil ||
		decision != "approved: "+a.Metadata.ID+" by person:bob; requested by agent:am/a1" {
		t.Errorf("the delete's audited decision = %q (%v)", decision, err)
	}

	if err := f.reconcile(a); err != nil {
		t.Fatalf("finish: %v", err)
	}
	a = f.get(a)
	if len(f.cleaned) != 2 {
		t.Errorf("cleaned %v, want both Secrets", f.cleaned)
	}
	for _, ref := range a.Status.BlastRadius {
		if (ref.Kind == resource.KindSecret) != ref.CleanedUp {
			t.Errorf("%s cleanedUp = %v", ref.Scope(), ref.CleanedUp)
		}
	}
	if c := condition(a, resource.ApprovalConditionCleanedUp); c.Status != resource.ConditionTrue || c.Reason != reconcile.ReasonCleanupDone {
		t.Errorf("CleanedUp = %+v", c)
	}
	if c := condition(a, resource.ApprovalConditionVerified); c.Status != resource.ConditionTrue {
		t.Errorf("Verified = %+v", c)
	}

	// Idempotent: nothing more to do, nothing written.
	version := a.Metadata.ResourceVersion
	if err := f.reconcile(a); err != nil {
		t.Fatal(err)
	}
	if got := f.get(a); got.Metadata.ResourceVersion != version || len(f.cleaned) != 2 {
		t.Errorf("a verified approval was written again (v%d -> v%d) or cleaned again (%v)", version, got.Metadata.ResourceVersion, f.cleaned)
	}
}

func TestApprovalReconciler_CleanUpResumesWhereItStopped(t *testing.T) {
	f := newApprovalFixture(t)
	a := f.approved()
	if err := f.reconcile(a); err != nil {
		t.Fatal(err)
	}
	f.cleanErr[f.s2.Metadata.ID] = errors.New("connection refused")
	if err := f.reconcile(a); err == nil {
		t.Fatal("finish with a failing erase = nil, want the error so the engine retries")
	}
	a = f.get(a)
	if c := condition(a, resource.ApprovalConditionCleanedUp); c.Status != resource.ConditionFalse || !strings.Contains(c.Message, "1 left") || strings.Contains(c.Message, "connection refused") {
		t.Errorf("CleanedUp = %+v, want False with 1 left", c)
	}
	if c := condition(a, resource.ApprovalConditionVerified); c.Status != "" {
		t.Errorf("Verified = %+v before clean-up finished", c)
	}

	if err := f.reconcile(a); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(f.cleaned) != 2 || f.cleaned[0] != f.s1.Metadata.ID || f.cleaned[1] != f.s2.Metadata.ID {
		t.Errorf("cleaned %v, want s1 once then s2: progress is saved per ref", f.cleaned)
	}
	if c := condition(f.get(a), resource.ApprovalConditionVerified); c.Status != resource.ConditionTrue {
		t.Errorf("Verified = %+v after the retry", c)
	}
}

func TestApprovalReconciler_CleanUpWaitsForItsCapability(t *testing.T) {
	f := newApprovalFixture(t)
	a := f.approved()
	if err := f.reconcile(a); err != nil {
		t.Fatal(err)
	}
	f.cleanErr[f.s1.Metadata.ID] = reconcile.ErrProviderNotReady
	err := f.reconcile(a)
	var requeue reconcile.RequeueAfter
	if !errors.As(err, &requeue) {
		t.Fatalf("finish with the provider not ready = %v, want RequeueAfter", err)
	}
	if c := condition(f.get(a), resource.ApprovalConditionCleanedUp); c.Reason != reconcile.ReasonProviderNotReady {
		t.Errorf("CleanedUp = %+v", c)
	}
}

func TestApprovalReconciler_StaleTargetFails(t *testing.T) {
	ctx := context.Background()
	tests := map[string]func(f *approvalFixture){
		"version moved": func(f *approvalFixture) {
			f.org.Metadata.Name = "acme-renamed"
			if _, err := f.orgs.Update(ctx, f.org); err != nil {
				t.Fatal(err)
			}
		},
		"a child was added": func(f *approvalFixture) {
			if _, err := f.secrets.Create(ctx, resource.Secret{Metadata: resource.Metadata{Name: "new",
				OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: f.project.Metadata.ID}}}, Spec: resource.SecretSpec{Path: "web/new"}}); err != nil {
				t.Fatal(err)
			}
		},
		"the target is gone": func(f *approvalFixture) {
			if err := f.orgs.Delete(store.WithApproval(ctx, store.ApprovalIntent{Approval: resource.Approval{
				Spec:   resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: resource.KindOrganization, ID: f.org.Metadata.ID}, Cascade: true}},
				Status: resource.ApprovalStatus{Phase: resource.ApprovalApproved},
			}}), f.org.Metadata.ID, true); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := newApprovalFixture(t)
			a := f.approved()
			change(f)
			if err := f.reconcile(a); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			a = f.get(a)
			if c := condition(a, resource.ApprovalConditionExecuted); a.Status.Phase != resource.ApprovalFailed || c.Reason != reconcile.ReasonStaleTarget {
				t.Errorf("phase %q, Executed %+v; want Failed/StaleTarget", a.Status.Phase, c)
			}
			if name != "the target is gone" && !f.exists(resource.KindProject, f.project.Metadata.ID) {
				t.Error("a stale approval deleted something")
			}
			if len(f.cleaned) != 0 {
				t.Errorf("a failed approval cleaned up %v", f.cleaned)
			}
		})
	}
}

// A second layer under the API: an approval only a person or policy could
// have decided is never executed when it records anything else.
func TestApprovalReconciler_RefusesAnIllegitimateDecision(t *testing.T) {
	ctx := context.Background()
	for name, d := range map[string]*resource.ApprovalDecision{
		"no decision":   nil,
		"no approver":   {By: &resource.ApprovalActor{Actor: "person:bob"}},
		"an agent":      {By: &resource.ApprovalActor{Actor: "agent:am/a2", Principal: &resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am/a2"}}},
		"the requester": {By: &resource.ApprovalActor{Actor: "agent:am/a1", Principal: &resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am/a1"}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newApprovalFixture(t)
			a := f.approved()
			a.Status.Decision = d
			a, err := f.approvals.Update(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.reconcile(a); err != nil {
				t.Fatal(err)
			}
			got := f.get(a)
			if c := condition(got, resource.ApprovalConditionExecuted); got.Status.Phase != resource.ApprovalFailed || c.Reason != reconcile.ReasonDeleteFailed {
				t.Errorf("phase %q, Executed %+v; want Failed/DeleteFailed", got.Status.Phase, c)
			}
			if !f.exists(resource.KindOrganization, f.org.Metadata.ID) {
				t.Error("an illegitimately decided approval deleted its target")
			}
		})
	}
}

func TestApprovalReconciler_TransientDeleteErrorIsRetried(t *testing.T) {
	f := newApprovalFixture(t)
	a := f.approved()
	f.deleteErr = errors.New("database is locked")
	if err := f.reconcile(a); err == nil {
		t.Fatal("reconcile = nil, want the error so the engine retries")
	}
	got := f.get(a)
	if c := condition(got, resource.ApprovalConditionExecuted); got.Status.Phase != resource.ApprovalApproved || c.Reason != reconcile.ReasonDeleteFailed ||
		strings.Contains(c.Message, "locked") {
		t.Errorf("after a transient error: phase %q, Executed %+v; want still Approved, saying it retries, without the error text", got.Status.Phase, c)
	}
	version := got.Metadata.ResourceVersion
	if err := f.reconcile(got); err == nil {
		t.Fatal("second reconcile = nil, want the error again")
	}
	if again := f.get(a); again.Metadata.ResourceVersion != version {
		t.Error("the same error was written to the approval twice")
	}
	a = f.get(a)
	f.deleteErr = nil
	if err := f.reconcile(a); err != nil {
		t.Fatal(err)
	}
	if got := f.get(a); got.Status.Phase != resource.ApprovalExecuted {
		t.Errorf("phase %q after the retry, want Executed", got.Status.Phase)
	}
}

func TestApprovalReconciler_Expiry(t *testing.T) {
	f := newApprovalFixture(t)
	a := f.create(resource.ApprovalPending, nil, f.now.Add(time.Hour))

	err := f.reconcile(a)
	var requeue reconcile.RequeueAfter
	if !errors.As(err, &requeue) || requeue.Delay != time.Hour {
		t.Fatalf("a pending approval due in an hour: %v, want RequeueAfter 1h", err)
	}
	if got := f.get(a); got.Metadata.ResourceVersion != a.Metadata.ResourceVersion {
		t.Error("a pending approval not yet due was written")
	}

	f.now = f.now.Add(time.Hour)
	if err := f.reconcile(a); err != nil {
		t.Fatal(err)
	}
	got := f.get(a)
	if c := condition(got, resource.ApprovalConditionDecided); got.Status.Phase != resource.ApprovalExpired || c.Reason != "Expired" {
		t.Errorf("phase %q, Decided %+v; want Expired", got.Status.Phase, c)
	}
	if !f.exists(resource.KindOrganization, f.org.Metadata.ID) {
		t.Error("an expired approval deleted its target")
	}
}

func TestApprovalReconciler_FinishedApprovalsAreLeftAlone(t *testing.T) {
	for _, phase := range []resource.ApprovalPhase{resource.ApprovalRejected, resource.ApprovalExpired, resource.ApprovalFailed} {
		f := newApprovalFixture(t)
		a := f.create(phase, nil, time.Time{})
		if err := f.reconcile(a); err != nil {
			t.Fatal(err)
		}
		if got := f.get(a); got.Metadata.ResourceVersion != a.Metadata.ResourceVersion || !f.exists(resource.KindOrganization, f.org.Metadata.ID) {
			t.Errorf("%s: the approval was written or its target deleted", phase)
		}
	}
	f := newApprovalFixture(t)
	if err := f.r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindApproval, ID: "gone"}); err != nil {
		t.Errorf("reconcile of a missing approval = %v, want nil", err)
	}
}
