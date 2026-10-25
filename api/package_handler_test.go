package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

type nopInstaller struct{}

func (nopInstaller) Install(context.Context, resource.ProviderInstance) (reconcile.PackageState, error) {
	return reconcile.PackageState{Ready: true}, nil
}
func (nopInstaller) Uninstall(context.Context, string) error { return nil }

// measured is a stated cost, which a package needs to be installable
// (plan 022 decision 4).
var measured = &provider.Requirements{IdleMemoryMiB: 50, PeakMemoryMiB: 300, PeakCPUMillicores: 1500,
	MeasuredOn: "test", Results: "testdata"}

func testCatalogue() reconcile.Catalogue {
	return reconcile.Catalogue{
		"test-builder":  {Provider: "test-builder", Capability: "build", Description: "builds", Installer: nopInstaller{}, Requirements: measured},
		"other-builder": {Provider: "other-builder", Capability: "build", Installer: nopInstaller{}, Requirements: measured},
		"later":         {Provider: "later", Capability: "observability"},
	}
}

func install(provider, capability string) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": provider}, "spec": map[string]any{"capabilityId": capability, "providerId": provider}}
}

func TestPackages(t *testing.T) {
	d := newDeployWorld(t, api.WithPackages(testCatalogue()))

	got := d.do("tok-owner", http.MethodGet, "/v1/packages", nil)
	var list struct {
		Packages []struct {
			Provider, Capability string
			Available            bool
		} `json:"packages"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &list); err != nil || len(list.Packages) != 3 || list.Packages[0].Provider != "later" || list.Packages[0].Available {
		t.Errorf("packages: %d %s", got.Code, got.Body)
	}

	// Only what's offered, available, and one per capability.
	for _, body := range []map[string]any{install("unknown", "build"), install("later", "observability"), install("test-builder", "observability")} {
		if got := d.do("tok-owner", http.MethodPost, "/v1/provider-instances", body); got.Code != http.StatusBadRequest {
			t.Errorf("%v: %d %s", body["spec"], got.Code, got.Body)
		}
	}
	// A Project's Contributor can't install: packages are platform-wide.
	if got := d.do("tok-devops", http.MethodPost, "/v1/provider-instances", install("test-builder", "build")); got.Code != http.StatusForbidden {
		t.Errorf("a Project's Contributor installs: %d", got.Code)
	}
	// Not even by naming their Project as its owner, and nobody may give a
	// package an owner (T016 security review).
	owned := install("test-builder", "build")
	owned["metadata"].(map[string]any)["ownerRefs"] = []any{map[string]any{"kind": "Project", "id": d.proj}}
	if got := d.do("tok-devops", http.MethodPost, "/v1/provider-instances", owned); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "serendip/provider-instances/write at /") {
		t.Errorf("a Project's Contributor installs under their Project: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-owner", http.MethodPost, "/v1/provider-instances", owned); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "platform-wide") {
		t.Errorf("an owned package: %d %s", got.Code, got.Body)
	}
	got = d.do("tok-owner", http.MethodPost, "/v1/provider-instances", install("test-builder", "build"))
	if got.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", got.Code, got.Body)
	}
	var inst resource.ProviderInstance
	json.Unmarshal(got.Body.Bytes(), &inst)
	if got := d.do("tok-owner", http.MethodPost, "/v1/provider-instances", install("other-builder", "build")); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "already has a package") {
		t.Errorf("a second build package: %d %s", got.Code, got.Body)
	}
	// Updating the one there is fine.
	if got := d.do("tok-owner", http.MethodPut, "/v1/provider-instances/"+inst.Metadata.ID, inst); got.Code != http.StatusOK {
		t.Errorf("update: %d %s", got.Code, got.Body)
	}
	// But not into another package: the first would stay installed.
	got = d.do("tok-owner", http.MethodGet, "/v1/provider-instances/"+inst.Metadata.ID, nil)
	json.Unmarshal(got.Body.Bytes(), &inst)
	inst.Spec.ProviderID = "other-builder"
	if got := d.do("tok-owner", http.MethodPut, "/v1/provider-instances/"+inst.Metadata.ID, inst); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "can't change") {
		t.Errorf("switching package: %d %s", got.Code, got.Body)
	}

	// Removing is a delete: an agent's waits for a person, the Owner's is
	// approved by policy and recorded.
	if got := d.do("tok-owner-ai", http.MethodDelete, "/v1/provider-instances/"+inst.Metadata.ID, nil); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "needs-approval") {
		t.Errorf("the Owner's assistant removes: %d %s", got.Code, got.Body)
	}
	got = d.do("tok-owner", http.MethodDelete, "/v1/provider-instances/"+inst.Metadata.ID, nil)
	if got.Code != http.StatusNoContent || got.Header().Get("Serendip-Approval-Id") == "" {
		t.Errorf("the Owner removes: %d %s", got.Code, got.Body)
	}
}

// Installs racing for one capability: exactly one is created, and every
// other gets 409, whether the API's check or the store's unique key
// refused it (RP-033).
func TestPackages_RacingInstalls(t *testing.T) {
	d := newDeployWorld(t, api.WithPackages(testCatalogue()))
	const n = 20
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := []string{"test-builder", "other-builder"}[i%2]
			codes[i] = d.do("tok-owner", http.MethodPost, "/v1/provider-instances", install(p, "build")).Code
		}()
	}
	wg.Wait()
	count := map[int]int{}
	for _, c := range codes {
		count[c]++
	}
	if count[http.StatusCreated] != 1 || count[http.StatusConflict] != n-1 {
		t.Errorf("status counts = %v, want one 201 and %d 409s", count, n-1)
	}
}
