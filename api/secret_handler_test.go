package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// fakeSecretsProvider is an api_test-local double for
// reconcile.SecretsProvider -- this layer's job is routing/encoding,
// not wire protocol, so no httptest.Server is needed here (that's
// providers/secrets/openbao/provider_test.go's job).
type fakeSecretsProvider struct {
	values      map[string]map[string]any
	deleteCalls int
}

func newFakeSecretsProvider() *fakeSecretsProvider {
	return &fakeSecretsProvider{values: map[string]map[string]any{}}
}

func (f *fakeSecretsProvider) Exists(ctx context.Context, sec resource.Secret) (bool, error) {
	_, ok := f.values[sec.Spec.Path]
	return ok, nil
}

func (f *fakeSecretsProvider) WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error {
	f.values[sec.Spec.Path] = value
	return nil
}

func (f *fakeSecretsProvider) ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error) {
	return f.values[sec.Spec.Path], nil
}

func (f *fakeSecretsProvider) Delete(ctx context.Context, sec resource.Secret) error {
	f.deleteCalls++
	delete(f.values, sec.Spec.Path)
	return nil
}

func newTestSecretMux(t *testing.T) (*http.ServeMux, *fakeSecretsProvider) {
	t.Helper()
	db := newTestDB(t)
	s := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret)
	provider := newFakeSecretsProvider()
	r := reconcile.NewSecretReconciler(s, provider)
	mux := http.NewServeMux()
	api.NewSecretHandler(s, r).Register(mux)
	return mux, provider
}

func TestSecretHandler_Create_NoValueAnywhereInResponse(t *testing.T) {
	mux, _ := newTestSecretMux(t)

	w := doRequest(mux, http.MethodPost, "/v1/secrets", resource.Secret{
		Metadata: resource.Metadata{Name: "db-password"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body)
	}
	if strings.Contains(w.Body.String(), "s3cr3t") {
		t.Errorf("Create response unexpectedly contains a value-like string: %s", w.Body)
	}
}

func TestSecretHandler_WriteThenReadValue_RoundTrips(t *testing.T) {
	mux, _ := newTestSecretMux(t)

	created := doRequest(mux, http.MethodPost, "/v1/secrets", resource.Secret{
		Metadata: resource.Metadata{Name: "db-password"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	id := decodeID(t, created.Body.Bytes())

	write := doRequest(mux, http.MethodPost, "/v1/secrets/"+id+"/value", map[string]any{
		"data": map[string]any{"password": "s3cr3t-value"},
	})
	if write.Code != http.StatusOK {
		t.Fatalf("write value status = %d, want %d, body: %s", write.Code, http.StatusOK, write.Body)
	}

	read := doRequest(mux, http.MethodGet, "/v1/secrets/"+id+"/value", nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read value status = %d, want %d, body: %s", read.Code, http.StatusOK, read.Body)
	}
	if !strings.Contains(read.Body.String(), "s3cr3t-value") {
		t.Errorf("read value response = %s, want it to contain the written value", read.Body)
	}
}

func TestSecretHandler_Delete_CallsProviderDeleteBeforeRecordGone(t *testing.T) {
	mux, provider := newTestSecretMux(t)

	created := doRequest(mux, http.MethodPost, "/v1/secrets", resource.Secret{
		Metadata: resource.Metadata{Name: "db-password"},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	})
	id := decodeID(t, created.Body.Bytes())

	doRequest(mux, http.MethodPost, "/v1/secrets/"+id+"/value", map[string]any{
		"data": map[string]any{"password": "s3cr3t-value"},
	})

	del := doRequest(mux, http.MethodDelete, "/v1/secrets/"+id, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body: %s", del.Code, http.StatusNoContent, del.Body)
	}
	if provider.deleteCalls != 1 {
		t.Errorf("provider delete calls = %d, want 1", provider.deleteCalls)
	}

	get := doRequest(mux, http.MethodGet, "/v1/secrets/"+id, nil)
	if get.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want %d", get.Code, http.StatusNotFound)
	}
}

func decodeID(t *testing.T, body []byte) string {
	t.Helper()
	var sec resource.Secret
	if err := json.Unmarshal(body, &sec); err != nil {
		t.Fatalf("decode secret = %v, body: %s", err, body)
	}
	if sec.Metadata.ID == "" {
		t.Fatalf("decoded secret has empty ID, body: %s", body)
	}
	return sec.Metadata.ID
}
