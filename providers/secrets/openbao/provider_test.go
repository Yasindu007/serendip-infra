package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func newTestSecret(path string) resource.Secret {
	return resource.Secret{
		Metadata: resource.Metadata{Name: "test-secret"},
		Spec:     resource.SecretSpec{Path: path},
	}
}

func TestProvider_Exists_AbsentValue_ReturnsFalseNoError(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	p := NewProvider(client)

	exists, err := p.Exists(context.Background(), newTestSecret("myapp/db-password"))
	if err != nil {
		t.Fatalf("Exists() = %v, want nil error for a 404", err)
	}
	if exists {
		t.Error("Exists() = true, want false for an absent value")
	}
}

func TestProvider_Exists_PresentValue_ReturnsTrue(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"password": "s3cr3t"}},
		})
	})
	p := NewProvider(client)

	exists, err := p.Exists(context.Background(), newTestSecret("myapp/db-password"))
	if err != nil {
		t.Fatalf("Exists() = %v", err)
	}
	if !exists {
		t.Error("Exists() = false, want true for a present value")
	}
}

func TestProvider_WriteValue_HitsExpectedPathWithSpecData(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})
	p := NewProvider(client)

	err := p.WriteValue(context.Background(), newTestSecret("myapp/db-password"), map[string]any{"password": "s3cr3t"})
	if err != nil {
		t.Fatalf("WriteValue() = %v", err)
	}
	wantPath := "/v1/secret/data/myapp/db-password"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	data, ok := gotBody["data"].(map[string]any)
	if !ok || data["password"] != "s3cr3t" {
		t.Errorf("body = %+v, want data.password = s3cr3t", gotBody)
	}
}

func TestProvider_ReadValue_ReturnsUnwrappedData(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"password": "s3cr3t"}},
		})
	})
	p := NewProvider(client)

	got, err := p.ReadValue(context.Background(), newTestSecret("myapp/db-password"))
	if err != nil {
		t.Fatalf("ReadValue() = %v", err)
	}
	if got["password"] != "s3cr3t" {
		t.Errorf("ReadValue() = %+v, want password=s3cr3t", got)
	}
}

func TestProvider_Delete_HitsMetadataPath(t *testing.T) {
	var gotPath string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	p := NewProvider(client)

	if err := p.Delete(context.Background(), newTestSecret("myapp/db-password")); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	wantPath := "/v1/secret/metadata/myapp/db-password"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}
