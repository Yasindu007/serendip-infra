package reconcile_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newSecretTestStoreAndDB(t *testing.T) (store.Store[resource.SecretSpec, resource.SecretStatus], *sql.DB) {
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
	return sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret), db
}

// fakeSecretsProvider is a test double for reconcile.SecretsProvider --
// no real OpenBao/httptest.Server needed, mirrors volume_test.go's
// fakeStorageProvider exactly.
type fakeSecretsProvider struct {
	exists    bool
	existsErr error

	writeErr  error
	readData  map[string]any
	readErr   error
	deleteErr error

	existsCalls int
	writeCalls  int
	readCalls   int
	deleteCalls int
	deleted     []resource.Secret
}

func (f *fakeSecretsProvider) Exists(ctx context.Context, sec resource.Secret) (bool, error) {
	f.existsCalls++
	return f.exists, f.existsErr
}

func (f *fakeSecretsProvider) WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error {
	f.writeCalls++
	return f.writeErr
}

func (f *fakeSecretsProvider) ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error) {
	f.readCalls++
	return f.readData, f.readErr
}

func (f *fakeSecretsProvider) Delete(ctx context.Context, sec resource.Secret) error {
	f.deleteCalls++
	if f.deleteErr == nil {
		f.deleted = append(f.deleted, sec)
	}
	return f.deleteErr
}

func TestSecretReconciler_Cases(t *testing.T) {
	tests := []struct {
		name        string
		provider    *fakeSecretsProvider
		wantStatus  resource.ConditionStatus
		wantReason  string
		wantRequeue bool
	}{
		{
			name:        "provider not ready",
			provider:    &fakeSecretsProvider{existsErr: reconcile.ErrProviderNotReady},
			wantStatus:  resource.ConditionUnknown,
			wantReason:  reconcile.ReasonProviderNotReady,
			wantRequeue: true,
		},
		{
			name:       "value exists",
			provider:   &fakeSecretsProvider{exists: true},
			wantStatus: resource.ConditionTrue,
			wantReason: "ValueExists",
		},
		{
			name:       "no value written",
			provider:   &fakeSecretsProvider{exists: false},
			wantStatus: resource.ConditionFalse,
			wantReason: "NoValueWritten",
		},
		{
			name:       "exists check fails",
			provider:   &fakeSecretsProvider{existsErr: errors.New("connection refused")},
			wantStatus: resource.ConditionUnknown,
			wantReason: "CheckFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets, _ := newSecretTestStoreAndDB(t)
			sec, err := secrets.Create(context.Background(), resource.Secret{
				Metadata: resource.Metadata{Name: "secret-under-test"},
				Spec:     resource.SecretSpec{Path: "myapp/db-password"},
			})
			if err != nil {
				t.Fatalf("Create(secret) = %v", err)
			}

			r := reconcile.NewSecretReconciler(secrets, tt.provider)
			ref := graph.ResourceRef{Kind: resource.KindSecret, ID: sec.Metadata.ID}
			checkRequeue(t, r.Reconcile(context.Background(), ref), tt.wantRequeue)

			got, err := secrets.Get(context.Background(), sec.Metadata.ID)
			if err != nil {
				t.Fatalf("Get() after reconcile = %v", err)
			}
			if len(got.Status.Conditions) != 1 {
				t.Fatalf("Conditions = %+v, want exactly 1", got.Status.Conditions)
			}
			cond := got.Status.Conditions[0]
			if cond.Type != "Reachable" {
				t.Errorf("Condition.Type = %q, want %q", cond.Type, "Reachable")
			}
			if cond.Status != tt.wantStatus {
				t.Errorf("Condition.Status = %q, want %q", cond.Status, tt.wantStatus)
			}
			if cond.Reason != tt.wantReason {
				t.Errorf("Condition.Reason = %q, want %q", cond.Reason, tt.wantReason)
			}
		})
	}
}

func TestSecretReconciler_NoDrift_IsIdempotent_NoAdditionalUpdate(t *testing.T) {
	secrets, _ := newSecretTestStoreAndDB(t)
	sec, err := secrets.Create(context.Background(), resource.Secret{
		Metadata: resource.Metadata{Name: "secret-under-test"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	if err != nil {
		t.Fatalf("Create(secret) = %v", err)
	}

	provider := &fakeSecretsProvider{exists: true}
	r := reconcile.NewSecretReconciler(secrets, provider)
	ref := graph.ResourceRef{Kind: resource.KindSecret, ID: sec.Metadata.ID}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (first) = %v", err)
	}
	afterFirst, err := secrets.Get(context.Background(), sec.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after first reconcile = %v", err)
	}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (second) = %v", err)
	}
	afterSecond, err := secrets.Get(context.Background(), sec.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after second reconcile = %v", err)
	}

	if afterSecond.Metadata.ResourceVersion != afterFirst.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion after second no-drift reconcile = %d, want unchanged %d",
			afterSecond.Metadata.ResourceVersion, afterFirst.Metadata.ResourceVersion)
	}
	if provider.existsCalls != 2 {
		t.Errorf("provider.Exists call count = %d, want 2 (both reconciles call Exists; only the store write is skipped on no-drift)", provider.existsCalls)
	}
}

func TestSecretReconciler_DeletedSecretSinceEvent_NoProviderCall(t *testing.T) {
	secrets, _ := newSecretTestStoreAndDB(t)
	provider := &fakeSecretsProvider{}
	r := reconcile.NewSecretReconciler(secrets, provider)

	err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindSecret, ID: "does-not-exist"})
	if err != nil {
		t.Errorf("Reconcile(nonexistent secret) = %v, want nil", err)
	}
	if provider.existsCalls != 0 {
		t.Errorf("provider.Exists call count = %d, want 0", provider.existsCalls)
	}
	// Deliberately unlike VolumeReconciler: no provider.Delete call
	// here either -- a Secret's Path is gone once the record is gone, so
	// the value is erased from the delete's Approval (EraseValue), not
	// here.
	if provider.deleteCalls != 0 {
		t.Errorf("provider.Delete call count = %d, want 0 (erasure happens in EraseValue, not Reconcile)", provider.deleteCalls)
	}
}

func TestSecretReconciler_WriteValue_WritesThroughAndUpdatesStatus(t *testing.T) {
	secrets, db := newSecretTestStoreAndDB(t)
	sec, err := secrets.Create(context.Background(), resource.Secret{
		Metadata: resource.Metadata{Name: "secret-under-test"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	if err != nil {
		t.Fatalf("Create(secret) = %v", err)
	}

	provider := &fakeSecretsProvider{}
	r := reconcile.NewSecretReconciler(secrets, provider)

	value := map[string]any{"password": "s3cr3t-value"}
	updated, err := r.WriteValue(context.Background(), sec.Metadata.ID, value)
	if err != nil {
		t.Fatalf("WriteValue() = %v", err)
	}
	if provider.writeCalls != 1 {
		t.Errorf("provider.WriteValue call count = %d, want 1", provider.writeCalls)
	}
	if updated.Status.LastSyncedAt == nil {
		t.Error("Status.LastSyncedAt is nil, want set after a successful write")
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Status != resource.ConditionTrue {
		t.Errorf("Conditions = %+v, want exactly one True Reachable Condition", updated.Status.Conditions)
	}

	// Security property, empirical half: the audit row this Update
	// produced must not contain the plaintext value anywhere in its
	// Before/After JSON.
	rows, err := db.QueryContext(context.Background(), `SELECT before, after FROM audit_log WHERE resource_id = ?`, sec.Metadata.ID)
	if err != nil {
		t.Fatalf("query audit_log = %v", err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var before, after []byte
		if err := rows.Scan(&before, &after); err != nil {
			t.Fatalf("scan audit_log row = %v", err)
		}
		found++
		for _, blob := range [][]byte{before, after} {
			if blob == nil {
				continue
			}
			var asMap map[string]any
			if err := json.Unmarshal(blob, &asMap); err != nil {
				t.Fatalf("unmarshal audit snapshot = %v", err)
			}
			if containsValue(asMap, "s3cr3t-value") {
				t.Errorf("audit snapshot contains the plaintext secret value: %s", blob)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log rows = %v", err)
	}
	if found == 0 {
		t.Fatal("no audit_log rows found for this Secret -- WriteValue's Update must still be audited")
	}
}

// containsValue recursively searches a decoded JSON value for want
// appearing anywhere, string or nested.
func containsValue(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case map[string]any:
		for _, val := range x {
			if containsValue(val, want) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if containsValue(val, want) {
				return true
			}
		}
	}
	return false
}

func TestSecretReconciler_ReadValue_ReturnsProviderDataNoStoreWrite(t *testing.T) {
	secrets, _ := newSecretTestStoreAndDB(t)
	sec, err := secrets.Create(context.Background(), resource.Secret{
		Metadata: resource.Metadata{Name: "secret-under-test"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	if err != nil {
		t.Fatalf("Create(secret) = %v", err)
	}

	provider := &fakeSecretsProvider{readData: map[string]any{"password": "s3cr3t-value"}}
	r := reconcile.NewSecretReconciler(secrets, provider)

	got, err := r.ReadValue(context.Background(), sec.Metadata.ID)
	if err != nil {
		t.Fatalf("ReadValue() = %v", err)
	}
	if got["password"] != "s3cr3t-value" {
		t.Errorf("ReadValue() = %+v, want password=s3cr3t-value", got)
	}

	after, err := secrets.Get(context.Background(), sec.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after ReadValue = %v", err)
	}
	if after.Metadata.ResourceVersion != sec.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion changed after ReadValue (%d -> %d), want unchanged -- reads must not bump it",
			sec.Metadata.ResourceVersion, after.Metadata.ResourceVersion)
	}
}

func TestSecretReconciler_EraseValue(t *testing.T) {
	secrets, _ := newSecretTestStoreAndDB(t)
	ref := resource.ApprovalRef{Kind: resource.KindSecret, ID: "s-1", Name: "db", LastSpec: json.RawMessage(`{"path":"myapp/db-password"}`)}

	provider := &fakeSecretsProvider{}
	r := reconcile.NewSecretReconciler(secrets, provider)
	for i := range 2 { // a retry after a crash erases again; that must succeed
		if err := r.EraseValue(context.Background(), ref); err != nil {
			t.Fatalf("EraseValue() #%d = %v", i+1, err)
		}
	}
	if len(provider.deleted) != 2 {
		t.Fatalf("provider.Delete calls = %d, want 2", len(provider.deleted))
	}
	got := provider.deleted[0]
	if got.Metadata.ID != "s-1" || got.Metadata.Kind != resource.KindSecret || got.Spec.Path != "myapp/db-password" {
		t.Errorf("provider.Delete got %+v, want Secret s-1 at myapp/db-password: values are stored under ID and path", got)
	}
}

func TestSecretReconciler_EraseValue_Errors(t *testing.T) {
	secrets, _ := newSecretTestStoreAndDB(t)
	tests := []struct {
		name     string
		ref      resource.ApprovalRef
		provider *fakeSecretsProvider
		want     error
	}{
		{name: "provider not ready", ref: resource.ApprovalRef{Kind: resource.KindSecret, ID: "s-1", LastSpec: json.RawMessage(`{"path":"a/b"}`)},
			provider: &fakeSecretsProvider{deleteErr: reconcile.ErrProviderNotReady}, want: reconcile.ErrProviderNotReady},
		{name: "provider failure", ref: resource.ApprovalRef{Kind: resource.KindSecret, ID: "s-1", LastSpec: json.RawMessage(`{"path":"a/b"}`)},
			provider: &fakeSecretsProvider{deleteErr: errors.New("connection refused")}},
		{name: "not a Secret", ref: resource.ApprovalRef{Kind: resource.KindVolume, ID: "v-1", LastSpec: json.RawMessage(`{"path":"a/b"}`)},
			provider: &fakeSecretsProvider{}},
		{name: "no recorded spec", ref: resource.ApprovalRef{Kind: resource.KindSecret, ID: "s-1"}, provider: &fakeSecretsProvider{}},
		{name: "no recorded path", ref: resource.ApprovalRef{Kind: resource.KindSecret, ID: "s-1", LastSpec: json.RawMessage(`{}`)}, provider: &fakeSecretsProvider{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := reconcile.NewSecretReconciler(secrets, tt.provider).EraseValue(context.Background(), tt.ref)
			if err == nil {
				t.Fatal("EraseValue() = nil, want an error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("EraseValue() = %v, want %v", err, tt.want)
			}
			if len(tt.provider.deleted) != 0 {
				t.Errorf("provider erased %v despite the error", tt.provider.deleted)
			}
		})
	}
}
