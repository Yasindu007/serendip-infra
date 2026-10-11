package openchoreo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const testCRDManifest = `# header comment
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    api-approved.kubernetes.io: https://example.invalid/pull/1
  name: gateways.gateway.networking.k8s.io
spec:
  group: gateway.networking.k8s.io
status:
  storedVersions: []
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: httproutes.gateway.networking.k8s.io
spec:
  group: gateway.networking.k8s.io
`

func TestEnsureCRDsFromManifest_CreatesMissingAndPreservesAnnotations(t *testing.T) {
	api := newFakeAPI()
	api.put(crdCollectionPath+"/httproutes.gateway.networking.k8s.io", kubernetes.Object{Kind: "CustomResourceDefinition"})
	client := newTestKubeClient(t, api.handler(t))

	if err := ensureCRDsFromManifest(context.Background(), client, []byte(testCRDManifest)); err != nil {
		t.Fatalf("ensureCRDsFromManifest() = %v", err)
	}
	if n := api.count(http.MethodPost); n != 1 {
		t.Fatalf("POST calls = %d (%v), want 1 (only the missing CRD)", n, api.calls)
	}
	gw, ok := api.objects[crdCollectionPath+"/gateways.gateway.networking.k8s.io"]
	if !ok {
		t.Fatal("gateways CRD not created")
	}
	if gw.Metadata.Annotations["api-approved.kubernetes.io"] == "" {
		t.Errorf("annotations = %v, want api-approved.kubernetes.io preserved (required for *.k8s.io groups)", gw.Metadata.Annotations)
	}
	if len(gw.Status) != 0 {
		t.Errorf("status sent on create = %s, want none", gw.Status)
	}
	if !strings.Contains(string(gw.Spec), "gateway.networking.k8s.io") {
		t.Errorf("spec = %s, want CRD spec carried through", gw.Spec)
	}
}

func TestEnsureCRDsFromManifest_RejectsNonCRDObjects(t *testing.T) {
	client := newTestKubeClient(t, newFakeAPI().handler(t))
	manifest := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n"
	if err := ensureCRDsFromManifest(context.Background(), client, []byte(manifest)); err == nil {
		t.Error("ensureCRDsFromManifest() = nil, want an error for a non-CRD object")
	}
}

func TestFetchManifest_VerifiesChecksum(t *testing.T) {
	body := []byte(testCRDManifest)
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()

	got, err := fetchManifest(context.Background(), srv.Client(), srv.URL, good)
	if err != nil || string(got) != testCRDManifest {
		t.Fatalf("fetchManifest(good sha) = (%d bytes, %v), want body and nil", len(got), err)
	}
	if _, err := fetchManifest(context.Background(), srv.Client(), srv.URL, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("fetchManifest(bad sha) = %v, want checksum mismatch error", err)
	}
}

func TestFetchManifest_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	if _, err := fetchManifest(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Error("fetchManifest() = nil, want error on 404")
	}
}

func TestSplitYAMLDocuments(t *testing.T) {
	docs := splitYAMLDocuments([]byte("a: 1\n---\nb: 2\r\n---\r\nc: 3"))
	if len(docs) != 3 || strings.TrimSpace(string(docs[2])) != "c: 3" {
		t.Errorf("splitYAMLDocuments = %q, want 3 docs", docs)
	}
}

func TestAllCRDsPresent(t *testing.T) {
	api := newFakeAPI()
	client := newTestKubeClient(t, api.handler(t))
	names := []string{"a.example.io", "b.example.io"}

	if ok, err := allCRDsPresent(context.Background(), client, names); err != nil || ok {
		t.Errorf("none present = (%v, %v), want (false, nil)", ok, err)
	}
	api.put(crdCollectionPath+"/a.example.io", kubernetes.Object{})
	if ok, _ := allCRDsPresent(context.Background(), client, names); ok {
		t.Error("one of two present reported as all present")
	}
	api.put(crdCollectionPath+"/b.example.io", kubernetes.Object{})
	if ok, err := allCRDsPresent(context.Background(), client, names); err != nil || !ok {
		t.Errorf("all present = (%v, %v), want (true, nil)", ok, err)
	}
	api.failPath = crdCollectionPath
	if _, err := allCRDsPresent(context.Background(), client, names); err == nil {
		t.Error("API failure not propagated")
	}
}

func TestGatewayAPICRDNamesMatchManifestKinds(t *testing.T) {
	if len(gatewayAPICRDNames) != 5 {
		t.Errorf("gatewayAPICRDNames has %d entries, want the 5 CRDs in the %s standard manifest", len(gatewayAPICRDNames), gatewayAPIVersion)
	}
}

func TestFetchManifest_TooLarge(t *testing.T) {
	big := make([]byte, maxManifestBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(big) }))
	defer srv.Close()
	if _, err := fetchManifest(context.Background(), srv.Client(), srv.URL, ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("fetchManifest(oversized) = %v, want size error", err)
	}
}

func TestSplitYAMLDocuments_SeparatorVariants(t *testing.T) {
	docs := splitYAMLDocuments([]byte("a: 1\n--- # next\nb: 2\n---   \nc: 3\n---x: not a separator\n"))
	if len(docs) != 3 {
		t.Fatalf("splitYAMLDocuments = %q, want 3 docs", docs)
	}
	if !strings.Contains(string(docs[2]), "---x") {
		t.Errorf("'---x' line treated as separator: %q", docs)
	}
}
