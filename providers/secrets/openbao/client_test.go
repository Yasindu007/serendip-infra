package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServerClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, srv.Client()), srv
}

func TestClient_Health_NormalizedQueryParamsSent(t *testing.T) {
	var gotQuery string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthStatus{Initialized: true, Sealed: false})
	})

	status, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() = %v", err)
	}
	if gotQuery != "standbyok=true&uninitcode=200&sealedcode=200" {
		t.Errorf("query = %q, want normalized status-code overrides", gotQuery)
	}
	if !status.Initialized || status.Sealed {
		t.Errorf("Health() = %+v, want Initialized=true, Sealed=false", status)
	}
}

func TestClient_Health_ParsesSealedUninitialized(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthStatus{Initialized: false, Sealed: true})
	})

	status, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() = %v", err)
	}
	if status.Initialized || !status.Sealed {
		t.Errorf("Health() = %+v, want Initialized=false, Sealed=true", status)
	}
}

func TestClient_EnsureKVv2Mount_AbsentMount_EnablesIt(t *testing.T) {
	var enableCalled bool
	var gotPath string
	var gotBody map[string]any
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sys/mounts":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"sys/":       map[string]any{"type": "system"},
				"cubbyhole/": map[string]any{"type": "cubbyhole"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sys/mounts/secret":
			enableCalled = true
			gotPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	if err := client.EnsureKVv2Mount(context.Background(), "secret"); err != nil {
		t.Fatalf("EnsureKVv2Mount() = %v", err)
	}
	if !enableCalled {
		t.Fatal("mount was not enabled when absent")
	}
	if gotPath != "/v1/sys/mounts/secret" {
		t.Errorf("enable path = %q, want /v1/sys/mounts/secret", gotPath)
	}
	if gotBody["type"] != "kv" {
		t.Errorf("enable body type = %v, want \"kv\"", gotBody["type"])
	}
	opts, ok := gotBody["options"].(map[string]any)
	if !ok || opts["version"] != "2" {
		t.Errorf("enable body options = %v, want version=2", gotBody["options"])
	}
}

func TestClient_EnsureKVv2Mount_AlreadyPresent_DoesNotReEnable(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatalf("unexpected POST %s, want no enable call when the mount already exists", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sys/":    map[string]any{"type": "system"},
			"secret/": map[string]any{"type": "kv"},
		})
	})

	if err := client.EnsureKVv2Mount(context.Background(), "secret"); err != nil {
		t.Fatalf("EnsureKVv2Mount() = %v", err)
	}
}

func TestClient_Init_SendsExactShareThresholdBody(t *testing.T) {
	var gotBody initRequest
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(initResponse{Keys: []string{"unseal-key-1"}, RootToken: "root-token-1"})
	})

	keys, rootToken, err := client.Init(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("Init() = %v", err)
	}
	if gotBody.SecretShares != 1 || gotBody.SecretThreshold != 1 {
		t.Errorf("request body = %+v, want {SecretShares:1 SecretThreshold:1}", gotBody)
	}
	if len(keys) != 1 || keys[0] != "unseal-key-1" {
		t.Errorf("keys = %v, want [unseal-key-1]", keys)
	}
	if rootToken != "root-token-1" {
		t.Errorf("rootToken = %q, want %q", rootToken, "root-token-1")
	}
}

func TestClient_Unseal_SendsKeyParsesSealed(t *testing.T) {
	var gotBody unsealRequest
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(unsealResponse{Sealed: false})
	})

	sealed, err := client.Unseal(context.Background(), "unseal-key-1")
	if err != nil {
		t.Fatalf("Unseal() = %v", err)
	}
	if gotBody.Key != "unseal-key-1" {
		t.Errorf("request body Key = %q, want %q", gotBody.Key, "unseal-key-1")
	}
	if sealed {
		t.Error("Unseal() sealed = true, want false")
	}
}

func TestClient_WriteSecret_SendsDataWrapper(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody kvV2WriteRequest
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := client.WriteSecret(context.Background(), "secret", "app/db", map[string]any{"password": "hunter2"})
	if err != nil {
		t.Fatalf("WriteSecret() = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/v1/secret/data/app/db" {
		t.Errorf("request = %s %s, want PUT /v1/secret/data/app/db", gotMethod, gotPath)
	}
	if gotBody.Data["password"] != "hunter2" {
		t.Errorf("request body Data = %+v, want password=hunter2", gotBody.Data)
	}
}

func TestClient_ReadSecret_UnwrapsDoubleNestedShape(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/data/app/db" {
			t.Errorf("path = %q, want /v1/secret/data/app/db", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"data":{"password":"hunter2"},"metadata":{"version":1}}}`))
	})

	data, err := client.ReadSecret(context.Background(), "secret", "app/db")
	if err != nil {
		t.Fatalf("ReadSecret() = %v", err)
	}
	if data["password"] != "hunter2" {
		t.Errorf("ReadSecret() = %+v, want password=hunter2", data)
	}
}

func TestClient_ReadSecret_404ReturnsErrSecretNotFound(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.ReadSecret(context.Background(), "secret", "app/db")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("ReadSecret() error = %v, want errors.Is(err, ErrSecretNotFound)", err)
	}
}

func TestClient_DeleteSecret_HitsMetadataPath(t *testing.T) {
	var gotMethod, gotPath string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.DeleteSecret(context.Background(), "secret", "app/db"); err != nil {
		t.Fatalf("DeleteSecret() = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/secret/metadata/app/db" {
		t.Errorf("request = %s %s, want DELETE /v1/secret/metadata/app/db", gotMethod, gotPath)
	}
}

func TestClient_DeleteSecret_404ReturnsNilNotError(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if err := client.DeleteSecret(context.Background(), "secret", "app/db"); err != nil {
		t.Errorf("DeleteSecret() on 404 = %v, want nil", err)
	}
}

func TestClient_TokenSentAsVaultTokenHeader(t *testing.T) {
	var gotToken string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Vault-Token")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthStatus{})
	})
	client.SetToken("root-token-1")

	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() = %v", err)
	}
	if gotToken != "root-token-1" {
		t.Errorf("X-Vault-Token = %q, want %q", gotToken, "root-token-1")
	}
}
