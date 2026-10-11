package reconcile_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

type appStores struct {
	db         *sql.DB
	apps       store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	components store.Store[resource.ComponentSpec, resource.ComponentStatus]
}

func newAppStores(t *testing.T) appStores {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	return appStores{
		db:         db,
		apps:       sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication),
		components: sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent),
	}
}

func countAuditRowsDB(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE resource_id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count audit_log rows: %v", err)
	}
	return n
}

type ensureResult struct {
	ready   bool
	reason  string
	message string
	err     error
}

type fakeAppPlatform struct {
	app  ensureResult
	comp ensureResult

	deleteAppErr  error
	deleteCompErr error

	ensureAppCalls, ensureCompCalls int
	deletedAppIDs, deletedCompIDs   []string
	lastEnsureCompApp               resource.Application
}

func (f *fakeAppPlatform) EnsureApplication(ctx context.Context, app resource.Application) (bool, string, string, error) {
	f.ensureAppCalls++
	return f.app.ready, f.app.reason, f.app.message, f.app.err
}

func (f *fakeAppPlatform) DeleteApplication(ctx context.Context, app resource.Application) error {
	f.deletedAppIDs = append(f.deletedAppIDs, app.Metadata.ID)
	return f.deleteAppErr
}

func (f *fakeAppPlatform) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (bool, string, string, error) {
	f.ensureCompCalls++
	f.lastEnsureCompApp = app
	return f.comp.ready, f.comp.reason, f.comp.message, f.comp.err
}

func (f *fakeAppPlatform) DeleteComponent(ctx context.Context, comp resource.Component) error {
	f.deletedCompIDs = append(f.deletedCompIDs, comp.Metadata.ID)
	return f.deleteCompErr
}

func readyCondition(t *testing.T, conds []resource.Condition) resource.Condition {
	t.Helper()
	if len(conds) != 1 || conds[0].Type != "Ready" {
		t.Fatalf("Conditions = %+v, want exactly one Ready condition", conds)
	}
	return conds[0]
}

func createApp(t *testing.T, s appStores, readyStatus resource.ConditionStatus) resource.Application {
	t.Helper()
	app := resource.Application{Metadata: resource.Metadata{Name: "shop"}}
	if readyStatus != "" {
		app.Status.Conditions = []resource.Condition{{Type: "Ready", Status: readyStatus, Reason: "x"}}
	}
	created, err := s.apps.Create(context.Background(), app)
	if err != nil {
		t.Fatalf("Create(app) = %v", err)
	}
	return created
}

func TestApplicationReconciler_Cases(t *testing.T) {
	tests := []struct {
		name       string
		result     ensureResult
		wantStatus resource.ConditionStatus
		wantReason string
	}{
		{"deployed", ensureResult{ready: true, reason: reconcile.ReasonDeployed}, resource.ConditionTrue, reconcile.ReasonDeployed},
		{"progressing", ensureResult{reason: reconcile.ReasonProgressing, message: "namespace pending"}, resource.ConditionFalse, reconcile.ReasonProgressing},
		{"failed", ensureResult{reason: reconcile.ReasonDeployFailed}, resource.ConditionFalse, reconcile.ReasonDeployFailed},
		{"provider error", ensureResult{err: errors.New("connection refused")}, resource.ConditionFalse, reconcile.ReasonEnsureFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAppStores(t)
			app := createApp(t, s, "")
			p := &fakeAppPlatform{app: tt.result}
			r := reconcile.NewApplicationReconciler(s.apps, p)

			if err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindApplication, ID: app.Metadata.ID}); err != nil {
				t.Fatalf("Reconcile() = %v", err)
			}
			got, err := s.apps.Get(context.Background(), app.Metadata.ID)
			if err != nil {
				t.Fatalf("Get() = %v", err)
			}
			c := readyCondition(t, got.Status.Conditions)
			if c.Status != tt.wantStatus || c.Reason != tt.wantReason {
				t.Errorf("Ready = {%s, %s}, want {%s, %s}", c.Status, c.Reason, tt.wantStatus, tt.wantReason)
			}
			if tt.result.err != nil && c.Message != tt.result.err.Error() {
				t.Errorf("Message = %q, want provider error text", c.Message)
			}
		})
	}
}

func TestApplicationReconciler_DeletedRecordCallsDeleteApplication(t *testing.T) {
	s := newAppStores(t)
	p := &fakeAppPlatform{}
	r := reconcile.NewApplicationReconciler(s.apps, p)

	if err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindApplication, ID: "gone-id"}); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(p.deletedAppIDs) != 1 || p.deletedAppIDs[0] != "gone-id" {
		t.Errorf("DeleteApplication IDs = %v, want [gone-id]", p.deletedAppIDs)
	}
	if p.ensureAppCalls != 0 {
		t.Errorf("EnsureApplication calls = %d, want 0", p.ensureAppCalls)
	}
}

func TestApplicationReconciler_DeleteErrorPropagates(t *testing.T) {
	s := newAppStores(t)
	p := &fakeAppPlatform{deleteAppErr: errors.New("boom")}
	r := reconcile.NewApplicationReconciler(s.apps, p)
	if err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindApplication, ID: "gone-id"}); err == nil {
		t.Error("Reconcile() = nil, want the DeleteApplication error")
	}
}

func TestApplicationReconciler_Idempotent(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, "")
	p := &fakeAppPlatform{app: ensureResult{ready: true, reason: reconcile.ReasonDeployed}}
	r := reconcile.NewApplicationReconciler(s.apps, p)
	ref := graph.ResourceRef{Kind: resource.KindApplication, ID: app.Metadata.ID}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("first Reconcile() = %v", err)
	}
	first, _ := s.apps.Get(context.Background(), app.Metadata.ID)
	rows := countAuditRowsDB(t, s.db, app.Metadata.ID)

	// Message churn alone must not cause a write.
	p.app.message = "different upstream text"
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("second Reconcile() = %v", err)
	}
	second, _ := s.apps.Get(context.Background(), app.Metadata.ID)
	if second.Metadata.ResourceVersion != first.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion %d -> %d, want unchanged on no-drift reconcile", first.Metadata.ResourceVersion, second.Metadata.ResourceVersion)
	}
	if got := countAuditRowsDB(t, s.db, app.Metadata.ID); got != rows {
		t.Errorf("audit rows %d -> %d, want unchanged", rows, got)
	}
}

func TestComponentReconciler_Cases(t *testing.T) {
	validSpec := resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 80}}}

	tests := []struct {
		name string
		// appReady "" means: create no Application at all.
		appReady       resource.ConditionStatus
		noOwner        bool
		twoOwners      bool
		spec           resource.ComponentSpec
		result         ensureResult
		wantStatus     resource.ConditionStatus
		wantReason     string
		wantEnsureCall bool
	}{
		{name: "deployed", appReady: resource.ConditionTrue, spec: validSpec,
			result: ensureResult{ready: true, reason: reconcile.ReasonDeployed}, wantStatus: resource.ConditionTrue, wantReason: reconcile.ReasonDeployed, wantEnsureCall: true},
		{name: "progressing", appReady: resource.ConditionTrue, spec: validSpec,
			result: ensureResult{reason: reconcile.ReasonProgressing}, wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonProgressing, wantEnsureCall: true},
		{name: "unsupported", appReady: resource.ConditionTrue, spec: resource.ComponentSpec{Image: "worker"},
			result: ensureResult{reason: reconcile.ReasonUnsupported}, wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonUnsupported, wantEnsureCall: true},
		{name: "provider error", appReady: resource.ConditionTrue, spec: validSpec,
			result: ensureResult{err: errors.New("timeout")}, wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonEnsureFailed, wantEnsureCall: true},
		{name: "empty image", appReady: resource.ConditionTrue, spec: resource.ComponentSpec{},
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "port name not a DNS label", appReady: resource.ConditionTrue,
			spec:       resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "HTTP_Main", Port: 80}}},
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "port out of range", appReady: resource.ConditionTrue,
			spec:       resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 0}}},
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "duplicate port names", appReady: resource.ConditionTrue,
			spec:       resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 80}, {Name: "http", Port: 8080}}},
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "no owner", appReady: resource.ConditionTrue, noOwner: true, spec: validSpec,
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "two application owners", appReady: resource.ConditionTrue, twoOwners: true, spec: validSpec,
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonInvalidSpec},
		{name: "application missing", spec: validSpec,
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonWaitingForApplication},
		{name: "application not ready", appReady: resource.ConditionFalse, spec: validSpec,
			wantStatus: resource.ConditionFalse, wantReason: reconcile.ReasonWaitingForApplication},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAppStores(t)
			appID := "missing-app"
			if tt.appReady != "" {
				appID = createApp(t, s, tt.appReady).Metadata.ID
			}
			var owners []resource.OwnerRef
			switch {
			case tt.noOwner:
			case tt.twoOwners:
				other := createApp(t, s, resource.ConditionTrue)
				owners = []resource.OwnerRef{{Kind: resource.KindApplication, ID: appID}, {Kind: resource.KindApplication, ID: other.Metadata.ID}}
			default:
				owners = []resource.OwnerRef{{Kind: resource.KindApplication, ID: appID}}
			}
			comp, err := s.components.Create(context.Background(), resource.Component{
				Metadata: resource.Metadata{Name: "web", OwnerRefs: owners},
				Spec:     tt.spec,
			})
			if err != nil {
				t.Fatalf("Create(component) = %v", err)
			}

			p := &fakeAppPlatform{comp: tt.result}
			r := reconcile.NewComponentReconciler(s.components, s.apps, p)
			if err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}); err != nil {
				t.Fatalf("Reconcile() = %v", err)
			}

			got, err := s.components.Get(context.Background(), comp.Metadata.ID)
			if err != nil {
				t.Fatalf("Get() = %v", err)
			}
			c := readyCondition(t, got.Status.Conditions)
			if c.Status != tt.wantStatus || c.Reason != tt.wantReason {
				t.Errorf("Ready = {%s, %s}, want {%s, %s}", c.Status, c.Reason, tt.wantStatus, tt.wantReason)
			}
			if gotCall := p.ensureCompCalls > 0; gotCall != tt.wantEnsureCall {
				t.Errorf("EnsureComponent called = %v, want %v", gotCall, tt.wantEnsureCall)
			}
			if tt.wantEnsureCall && p.lastEnsureCompApp.Metadata.ID != appID {
				t.Errorf("EnsureComponent got app %q, want owning app %q", p.lastEnsureCompApp.Metadata.ID, appID)
			}
		})
	}
}

func TestComponentReconciler_DeletedRecordCallsDeleteComponent(t *testing.T) {
	s := newAppStores(t)
	p := &fakeAppPlatform{}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)

	if err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindComponent, ID: "gone-comp"}); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(p.deletedCompIDs) != 1 || p.deletedCompIDs[0] != "gone-comp" {
		t.Errorf("DeleteComponent IDs = %v, want [gone-comp]", p.deletedCompIDs)
	}
}

func TestComponentReconciler_Idempotent(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, resource.ConditionTrue)
	comp, err := s.components.Create(context.Background(), resource.Component{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
		Spec:     resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 80}}},
	})
	if err != nil {
		t.Fatalf("Create(component) = %v", err)
	}
	p := &fakeAppPlatform{comp: ensureResult{ready: true, reason: reconcile.ReasonDeployed}}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)
	ref := graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("first Reconcile() = %v", err)
	}
	first, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	rows := countAuditRowsDB(t, s.db, comp.Metadata.ID)

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("second Reconcile() = %v", err)
	}
	second, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if second.Metadata.ResourceVersion != first.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion %d -> %d, want unchanged", first.Metadata.ResourceVersion, second.Metadata.ResourceVersion)
	}
	if got := countAuditRowsDB(t, s.db, comp.Metadata.ID); got != rows {
		t.Errorf("audit rows %d -> %d, want unchanged", rows, got)
	}
}
