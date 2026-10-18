package policy_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// world is a store with an ownership tree and an engine over it:
//
//	/ ── org ── projA ── prod ── app ── web
//	                   └ dev  ── devapp
//	       └─── projB ── appB
//	   secret (owned by app)
type world struct {
	t       testing.TB
	ctx     context.Context
	engine  *policy.Engine
	assign  *sqlite.Store[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus]
	defs    *sqlite.Store[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus]
	counter int
}

var (
	org    = resource.Scope{Kind: resource.KindOrganization, ID: "org"}
	projA  = resource.Scope{Kind: resource.KindProject, ID: "projA"}
	projB  = resource.Scope{Kind: resource.KindProject, ID: "projB"}
	prod   = resource.Scope{Kind: resource.KindEnvironment, ID: "prod"}
	dev    = resource.Scope{Kind: resource.KindEnvironment, ID: "dev"}
	app    = resource.Scope{Kind: resource.KindApplication, ID: "app"}
	devapp = resource.Scope{Kind: resource.KindApplication, ID: "devapp"}
	appB   = resource.Scope{Kind: resource.KindApplication, ID: "appB"}
	web    = resource.Scope{Kind: resource.KindComponent, ID: "web"}
	secret = resource.Scope{Kind: resource.KindSecret, ID: "db-password"}

	alice = policy.Principal{Type: resource.PrincipalPerson, ID: "sub-alice"}
	bob   = policy.Principal{Type: resource.PrincipalPerson, ID: "sub-bob"}
)

func newWorld(t testing.TB) *world {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	for child, owner := range map[resource.Scope]resource.Scope{
		projA: org, projB: org, prod: projA, dev: projA, app: prod, devapp: dev, appB: projB, web: app, secret: app,
	} {
		if err := graph.SyncEdges(ctx, db, graph.ResourceRef{Kind: child.Kind, ID: child.ID},
			[]resource.OwnerRef{{Kind: owner.Kind, ID: owner.ID}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	bus := event.NewInProcessBus()
	w := &world{t: t, ctx: ctx,
		assign: sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).SetBus(bus),
		defs:   sqlite.NewStore[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus](db, resource.KindRoleDefinition).SetBus(bus),
	}
	g := graph.NewSQLiteGraph(db)
	w.engine = policy.NewEngine(w.assign, w.defs, g.Ancestors)
	stop := w.engine.Watch(bus)
	t.Cleanup(stop)
	return w
}

func (w *world) grant(p policy.Principal, roleID string, at resource.Scope, delegation *resource.Delegation) string {
	w.t.Helper()
	w.counter++
	pt, id := p.Type, p.ID
	created, err := w.assign.Create(w.ctx, resource.RoleAssignment{
		Metadata: resource.Metadata{Name: fmt.Sprintf("ra-%d", w.counter)},
		Spec:     resource.RoleAssignmentSpec{Principal: resource.PrincipalRef{Type: pt, ID: id}, RoleDefinitionID: roleID, Scope: at, Delegation: delegation},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return created.Metadata.ID
}

func (w *world) grantGroup(group, roleID string, at resource.Scope) {
	w.t.Helper()
	w.grant(policy.Principal{Type: resource.PrincipalGroup, ID: group}, roleID, at, nil)
}

func (w *world) customRole(spec resource.RoleDefinitionSpec) string {
	w.t.Helper()
	created, err := w.defs.Create(w.ctx, resource.RoleDefinition{Metadata: resource.Metadata{Name: spec.RoleName}, Spec: spec})
	if err != nil {
		w.t.Fatal(err)
	}
	return created.Metadata.ID
}

func (w *world) allowed(p policy.Principal, action string, data bool, at resource.Scope) bool {
	w.t.Helper()
	d, err := w.engine.Decide(w.ctx, p, policy.Action{Name: action, Data: data}, at)
	if err != nil {
		w.t.Fatal(err)
	}
	return d.Effect == policy.Allow
}

type check struct {
	who    policy.Principal
	action string
	data   bool
	at     resource.Scope
	want   bool
}

func (w *world) expect(checks ...check) {
	w.t.Helper()
	for _, c := range checks {
		if got := w.allowed(c.who, c.action, c.data, c.at); got != c.want {
			w.t.Errorf("%s %s (data=%v) at %s: allowed=%v, want %v", c.who.ID, c.action, c.data, c.at, got, c.want)
		}
	}
}

func TestReaderOnAProject(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.ReaderRoleID, projA, nil)
	w.expect(
		check{alice, "serendip/components/read", false, web, true},
		check{alice, "serendip/applications/read", false, app, true},
		check{alice, "serendip/secrets/read", false, secret, true},
		check{alice, "serendip/secrets/value/read", true, secret, false},
		check{alice, "serendip/components/write", false, web, false},
		check{alice, "serendip/components/delete", false, web, false},
		check{alice, "serendip/applications/read", false, appB, false},
		check{alice, "serendip/applications/read", false, resource.RootScope, false},
		check{bob, "serendip/components/read", false, web, false},
	)
}

func TestContributorOnAnEnvironment(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.ContributorRoleID, prod, nil)
	w.expect(
		check{alice, "serendip/components/delete", false, web, true},
		check{alice, "serendip/components/write", false, web, true},
		check{alice, "serendip/applications/delete", false, devapp, false},
		check{alice, policy.ActionRoleAssignmentsWrite, false, prod, false},
		check{alice, policy.ActionRoleAssignmentsDelete, false, web, false},
	)
}

func TestDataPlaneNeedsASecretsRole(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	w.expect(
		check{alice, "serendip/secrets/delete", false, secret, true},
		check{alice, "serendip/secrets/value/read", true, secret, false},
		check{alice, "serendip/secrets/value/write", true, secret, false},
	)
	w.grant(bob, policy.SecretsUserRoleID, secret, nil)
	w.expect(
		check{bob, "serendip/secrets/value/read", true, secret, true},
		check{bob, "serendip/secrets/read", false, secret, true},
		check{bob, "serendip/secrets/value/write", true, secret, false},
		check{bob, "serendip/secrets/delete", false, secret, false},
	)
	w.grant(alice, policy.SecretsOfficerRoleID, app, nil)
	w.expect(
		check{alice, "serendip/secrets/value/read", true, secret, true},
		check{alice, "serendip/secrets/value/write", true, secret, true},
	)
}

func TestInheritanceIsAdditive(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.ContributorRoleID, projA, nil)
	w.grant(alice, policy.ReaderRoleID, app, nil)
	// A lower Reader never removes the inherited Contributor.
	w.expect(check{alice, "serendip/components/delete", false, web, true})
}

func TestNotActionsSubtractOnlyWithinTheirRole(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.ContributorRoleID, projA, nil)
	w.expect(check{alice, policy.ActionRoleAssignmentsWrite, false, projA, false})
	w.grant(alice, policy.RBACAdministratorRoleID, projA, nil)
	// Contributor's notActions don't deny what another role grants.
	w.expect(check{alice, policy.ActionRoleAssignmentsWrite, false, app, true})
}

func TestGroups(t *testing.T) {
	w := newWorld(t)
	w.grantGroup("platform", policy.ContributorRoleID, projA)
	member := policy.Principal{Type: resource.PrincipalPerson, ID: "sub-carol", Groups: []string{"eng", "platform"}}
	outsider := policy.Principal{Type: resource.PrincipalPerson, ID: "sub-dave", Groups: []string{"eng"}}
	w.expect(
		check{member, "serendip/components/write", false, web, true},
		check{outsider, "serendip/components/write", false, web, false},
	)
	// A person whose ID happens to equal the group name isn't a member.
	impostor := policy.Principal{Type: resource.PrincipalPerson, ID: "platform"}
	w.expect(check{impostor, "serendip/components/write", false, web, false})
}

func TestCustomRoleWildcards(t *testing.T) {
	w := newWorld(t)
	roleID := w.customRole(resource.RoleDefinitionSpec{
		RoleName:         "Component operator",
		Permissions:      []resource.Permission{{Actions: []string{"serendip/components/*"}, NotActions: []string{"serendip/components/delete"}}},
		AssignableScopes: []resource.Scope{projA},
	})
	w.grant(alice, roleID, app, nil)
	w.expect(
		check{alice, "serendip/components/write", false, web, true},
		check{alice, "SERENDIP/Components/Read", false, web, true},
		check{alice, "serendip/components/delete", false, web, false},
		check{alice, "serendip/applications/write", false, app, false},
	)
}

func TestEvidenceIsTheNearestGrant(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	nearest := w.grant(alice, policy.ReaderRoleID, app, nil)
	d, err := w.engine.Decide(w.ctx, alice, policy.Action{Name: "serendip/components/read"}, web)
	if err != nil || d.Effect != policy.Allow || d.Evidence.AssignmentID != nearest || d.Evidence.RoleName != "Reader" {
		t.Fatalf("decision = %+v, %v; want Reader at the application", d, err)
	}
	if got := d.String(); got != fmt.Sprintf("allow: Reader (%s) at Application/app", nearest) {
		t.Errorf("String() = %q", got)
	}
	denied := policy.Decision{Effect: policy.Deny}
	if got := denied.Describe(policy.Action{Name: "serendip/components/delete"}, web); got != "deny: missing serendip/components/delete at Component/web" {
		t.Errorf("Describe() = %q", got)
	}
}

func TestDanglingAssignmentGrantsNothing(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, "no-such-role", resource.RootScope, nil)
	w.expect(check{alice, "serendip/components/read", false, web, false})
}

func TestNewAssignmentsTakeEffectImmediately(t *testing.T) {
	w := newWorld(t)
	w.expect(check{alice, "serendip/components/read", false, web, false})
	id := w.grant(alice, policy.ReaderRoleID, web, nil)
	w.expect(check{alice, "serendip/components/read", false, web, true})
	if err := w.assign.Delete(w.ctx, id, false); err != nil {
		t.Fatal(err)
	}
	w.expect(check{alice, "serendip/components/read", false, web, false})
}

func TestConstrainedDelegation(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.RBACAdministratorRoleID, projA, &resource.Delegation{
		RoleDefinitionIDs: []string{policy.ReaderRoleID, policy.ContributorRoleID},
		PrincipalTypes:    []resource.PrincipalType{resource.PrincipalPerson},
	})
	assign := func(caller policy.Principal, to resource.PrincipalRef, role string, at resource.Scope) error {
		return w.engine.CheckAssign(w.ctx, caller, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: role, Scope: at})
	}
	bobRef := resource.PrincipalRef{Type: resource.PrincipalPerson, ID: bob.ID}
	aliceRef := resource.PrincipalRef{Type: resource.PrincipalPerson, ID: alice.ID}
	cases := []struct {
		name string
		err  error
	}{
		{"Reader for another person", assign(alice, bobRef, policy.ReaderRoleID, app)},
		{"Owner is not on the list", assign(alice, bobRef, policy.OwnerRoleID, app)},
		{"a group is not on the list", assign(alice, resource.PrincipalRef{Type: resource.PrincipalGroup, ID: "eng"}, policy.ReaderRoleID, app)},
		{"assigning to herself", assign(alice, aliceRef, policy.ContributorRoleID, app)},
		{"outside her scope", assign(alice, bobRef, policy.ReaderRoleID, appB)},
	}
	want := []bool{true, false, false, false, false}
	for i, c := range cases {
		if ok := c.err == nil; ok != want[i] {
			t.Errorf("%s: err = %v, want ok=%v", c.name, c.err, want[i])
		}
		if c.err != nil && !errors.Is(c.err, policy.ErrForbidden) {
			t.Errorf("%s: err %v doesn't wrap ErrForbidden", c.name, c.err)
		}
	}

	// An unconstrained Owner may assign anything, to anyone, including itself.
	w.grant(bob, policy.OwnerRoleID, resource.RootScope, nil)
	if err := assign(bob, bobRef, policy.OwnerRoleID, projB); err != nil {
		t.Errorf("unconstrained Owner: %v", err)
	}
}

func TestAssignableScopesAndUnknownRoles(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	custom := w.customRole(resource.RoleDefinitionSpec{
		RoleName: "App reader", Permissions: []resource.Permission{{Actions: []string{"*/read"}}}, AssignableScopes: []resource.Scope{app},
	})
	to := resource.PrincipalRef{Type: resource.PrincipalPerson, ID: bob.ID}
	if err := w.engine.CheckAssign(w.ctx, alice, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: custom, Scope: web}); err != nil {
		t.Errorf("below an assignable scope: %v", err)
	}
	if err := w.engine.CheckAssign(w.ctx, alice, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: custom, Scope: appB}); !errors.Is(err, policy.ErrNotAssignable) {
		t.Errorf("outside the assignable scopes: err = %v", err)
	}
	if err := w.engine.CheckAssign(w.ctx, alice, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: "nope", Scope: web}); !errors.Is(err, policy.ErrUnknownRole) {
		t.Errorf("unknown role: err = %v", err)
	}
	w.grant(bob, custom, web, nil)
	if inUse, err := w.engine.RoleInUse(w.ctx, custom); err != nil || !inUse {
		t.Errorf("RoleInUse = %v, %v; want true", inUse, err)
	}
}

func TestLastOwnerAtRoot(t *testing.T) {
	w := newWorld(t)
	first := w.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	if err := w.engine.CheckUnassign(w.ctx, alice, first); !errors.Is(err, policy.ErrLastOwner) {
		t.Fatalf("removing the only Owner: err = %v, want ErrLastOwner", err)
	}
	second := w.grant(bob, policy.OwnerRoleID, resource.RootScope, nil)
	if err := w.engine.CheckUnassign(w.ctx, alice, second); err != nil {
		t.Errorf("removing one of two Owners: %v", err)
	}
	// An Owner assignment below "/" is never the protected one.
	lower := w.grant(bob, policy.OwnerRoleID, projA, nil)
	if last, err := w.engine.IsLastRootOwner(w.ctx, lower); err != nil || last {
		t.Errorf("an Owner at a project counted as the last root Owner (%v, %v)", last, err)
	}
	// Someone without permission learns they lack it, not that it's the
	// last Owner.
	carol := policy.Principal{Type: resource.PrincipalPerson, ID: "sub-carol"}
	only := newWorld(t)
	id := only.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	if err := only.engine.CheckUnassign(only.ctx, carol, id); err == nil || errors.Is(err, policy.ErrLastOwner) {
		t.Errorf("unauthorised removal: err = %v, want a missing-permission error", err)
	}
}

func TestDelegateCantGrantToTheirOwnGroup(t *testing.T) {
	w := newWorld(t)
	delegate := policy.Principal{Type: resource.PrincipalPerson, ID: "sub-erin", Groups: []string{"ops"}}
	w.grant(delegate, policy.RBACAdministratorRoleID, projA, &resource.Delegation{
		RoleDefinitionIDs: []string{policy.ContributorRoleID},
		PrincipalTypes:    []resource.PrincipalType{resource.PrincipalGroup},
	})
	own := resource.RoleAssignmentSpec{Principal: resource.PrincipalRef{Type: resource.PrincipalGroup, ID: "ops"}, RoleDefinitionID: policy.ContributorRoleID, Scope: app}
	if err := w.engine.CheckAssign(w.ctx, delegate, own); !errors.Is(err, policy.ErrForbidden) {
		t.Errorf("granting to her own group: err = %v, want ErrForbidden", err)
	}
	other := own
	other.Principal.ID = "qa"
	if err := w.engine.CheckAssign(w.ctx, delegate, other); err != nil {
		t.Errorf("granting to another group: %v", err)
	}
}

func TestRunPicksUpOutOfBandChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "oob.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	// No bus: like the break-glass command writing from another process.
	assign := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	defs := sqlite.NewStore[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus](db, resource.KindRoleDefinition)
	engine := policy.NewEngine(assign, defs, graph.NewSQLiteGraph(db).Ancestors)
	read := policy.Action{Name: "serendip/components/read"}
	if d, _ := engine.Decide(ctx, alice, read, web); d.Effect != policy.Deny {
		t.Fatal("allowed before any assignment")
	}
	if _, err := assign.Create(ctx, resource.RoleAssignment{Metadata: resource.Metadata{Name: "oob"},
		Spec: resource.RoleAssignmentSpec{Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: alice.ID}, RoleDefinitionID: policy.ReaderRoleID, Scope: resource.RootScope}}); err != nil {
		t.Fatal(err)
	}
	go engine.Run(ctx, time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d, _ := engine.Decide(ctx, alice, read, web); d.Effect == policy.Allow {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("an assignment written without an event was never picked up")
}

func TestBuiltInRolesAreValid(t *testing.T) {
	for id, r := range policy.BuiltInRoles() {
		if err := resource.ValidateRoleDefinition(r, false); err != nil {
			t.Errorf("built-in %s (%s): %v", r.RoleName, id, err)
		}
	}
	if len(policy.BuiltInRoles()) != 7 {
		t.Errorf("%d built-in roles, want 7", len(policy.BuiltInRoles()))
	}
}

func BenchmarkDecide(b *testing.B) {
	w := newWorld(b)
	ctx := policy.WithAncestorCache(w.ctx)
	for i := range 100 {
		p := policy.Principal{Type: resource.PrincipalPerson, ID: fmt.Sprintf("sub-%d", i)}
		w.grant(p, policy.ReaderRoleID, projB, nil)
	}
	w.grant(alice, policy.ContributorRoleID, projA, nil)
	a := policy.Action{Name: "serendip/components/write"}
	b.ReportAllocs()
	for b.Loop() {
		if d, err := w.engine.Decide(ctx, alice, a, web); err != nil || d.Effect != policy.Allow {
			b.Fatal(d, err)
		}
	}
}

// A constrained delegate who may grant a role that can itself assign roles
// must pass on a delegation no wider than their own, or they could mint an
// unconstrained administrator.
func TestDelegateCantMintAWiderAdministrator(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.RBACAdministratorRoleID, projA, &resource.Delegation{
		RoleDefinitionIDs: []string{policy.ReaderRoleID, policy.RBACAdministratorRoleID},
		PrincipalTypes:    []resource.PrincipalType{resource.PrincipalPerson},
	})
	to := resource.PrincipalRef{Type: resource.PrincipalPerson, ID: bob.ID}
	try := func(d *resource.Delegation) error {
		return w.engine.CheckAssign(w.ctx, alice, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: policy.RBACAdministratorRoleID, Scope: app, Delegation: d})
	}
	if err := try(nil); !errors.Is(err, policy.ErrForbidden) {
		t.Errorf("unconstrained administrator: err = %v, want ErrForbidden", err)
	}
	if err := try(&resource.Delegation{RoleDefinitionIDs: []string{policy.OwnerRoleID}, PrincipalTypes: []resource.PrincipalType{resource.PrincipalPerson}}); !errors.Is(err, policy.ErrForbidden) {
		t.Errorf("delegation wider than hers: err = %v, want ErrForbidden", err)
	}
	if err := try(&resource.Delegation{RoleDefinitionIDs: []string{policy.ReaderRoleID}, PrincipalTypes: []resource.PrincipalType{resource.PrincipalPerson}}); err != nil {
		t.Errorf("delegation within hers: %v", err)
	}
	// Roles that can't assign need no delegation.
	if err := w.engine.CheckAssign(w.ctx, alice, resource.RoleAssignmentSpec{Principal: to, RoleDefinitionID: policy.ReaderRoleID, Scope: app}); err != nil {
		t.Errorf("plain Reader: %v", err)
	}
}
