package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeCASecretDataB64 is the base64 encoding of "fake-ca-pem-data" --
// Kubernetes Secrets always base64-encode their data map's values in
// the real API's JSON representation, and encoding/json auto-decodes
// a []byte-typed field from a base64 JSON string on Unmarshal, so
// fakes must encode it the same way a real Secret GET response would.
const fakeCASecretDataB64 = "ZmFrZS1jYS1wZW0tZGF0YQ=="

// newFakeTrustBootstrapClient builds a fake *kubernetes.Client serving
// exactly the requests ensureDataPlaneTrustsControlPlane makes: applies of
// the data-plane namespace and its cluster-gateway-ca ConfigMap, and a GET
// of the control plane's real cert-manager-issued Secret.
func newFakeTrustBootstrapClient(t *testing.T) *kubernetes.Client {
	t.Helper()
	api := newFakeAPI()
	api.put(caSecretPath, kubernetes.Object{Data: json.RawMessage(`{"ca.crt":"` + fakeCASecretDataB64 + `"}`)})
	return newTestKubeClient(t, api.handler(t))
}

var (
	caSecretPath    = "/api/v1/namespaces/" + ControlPlaneNamespace + "/secrets/" + clusterGatewayCASecretName
	caConfigMapPath = "/api/v1/namespaces/" + DataPlaneNamespace + "/configmaps/" + clusterGatewayCAConfigMapName
	dataPlaneNSPath = "/api/v1/namespaces/" + DataPlaneNamespace
)

func TestDeployer_Ensure_InstallsThreeReleasesInOrderWithExpectedFlags(t *testing.T) {
	fake := &fakeHelmRunner{}
	d := &Deployer{run: fake.run, client: newFakeTrustBootstrapClient(t)}

	if err := d.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}

	if len(fake.calls) != 3 {
		t.Fatalf("helm invocation count = %d, want 3 (cert-manager, control-plane, data-plane)", len(fake.calls))
	}

	wantRelease := []string{"cert-manager", "openchoreo-control-plane", "openchoreo-data-plane"}
	wantChart := []string{certManagerChart, controlPlaneChart, dataPlaneChart}
	wantNamespace := []string{certManagerNamespace, ControlPlaneNamespace, DataPlaneNamespace}

	for i, args := range fake.calls {
		if args[0] != "upgrade" || args[1] != "--install" {
			t.Errorf("call[%d] args[0:2] = %v, want [upgrade --install]", i, args[0:2])
		}
		if args[2] != wantRelease[i] {
			t.Errorf("call[%d] release name = %q, want %q", i, args[2], wantRelease[i])
		}
		if args[3] != wantChart[i] {
			t.Errorf("call[%d] chart = %q, want %q", i, args[3], wantChart[i])
		}
		if !containsFlag(args, "--namespace", wantNamespace[i]) {
			t.Errorf("call[%d] args = %v, want --namespace %q", i, args, wantNamespace[i])
		}
		if !containsFlag(args, "--create-namespace", "") {
			t.Errorf("call[%d] args = %v, want --create-namespace", i, args)
		}
		if !containsFlag(args, "--wait", "") {
			t.Errorf("call[%d] args = %v, want --wait", i, args)
		}
	}

	// cert-manager-specific
	if !containsFlag(fake.calls[0], "--set", "crds.enabled=true") {
		t.Errorf("cert-manager call args = %v, want --set crds.enabled=true", fake.calls[0])
	}
	// control-plane-specific: all three validation-dodging overrides
	for _, want := range []string{"security.enabled=false", "backstage.enabled=false", "gateway.enabled=false"} {
		if !containsFlag(fake.calls[1], "--set", want) {
			t.Errorf("control-plane call args = %v, want --set %q", fake.calls[1], want)
		}
	}
	// data-plane-specific: both gateway flags, confirmed necessary
	// during T006 real-cluster verification (the data plane's own
	// validateHostnames check is gated on gateway.tls.enabled alone,
	// independent of gateway.enabled -- unlike the control plane's).
	for _, want := range []string{"gateway.enabled=false", "gateway.tls.enabled=false"} {
		if !containsFlag(fake.calls[2], "--set", want) {
			t.Errorf("data-plane call args = %v, want --set %q", fake.calls[2], want)
		}
	}
	if containsFlag(fake.calls[2], "--set", "security.enabled=false") {
		t.Errorf("data-plane call args = %v, unexpectedly set security.enabled (control-plane-only flag)", fake.calls[2])
	}
}

func TestDeployer_Ensure_CertManagerFailureStopsBeforeControlPlane(t *testing.T) {
	fake := &fakeHelmRunner{errs: []error{errors.New("cert-manager install failed")}}
	d := &Deployer{run: fake.run}

	err := d.Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure() = nil, want the simulated cert-manager failure to propagate")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("helm invocation count = %d, want exactly 1 (must not install on top of a failed prerequisite)", len(fake.calls))
	}
}

func TestDeployer_Ensure_ControlPlaneFailureStopsBeforeDataPlane(t *testing.T) {
	fake := &fakeHelmRunner{errs: []error{nil, errors.New("control plane install failed")}}
	d := &Deployer{run: fake.run}

	err := d.Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure() = nil, want the simulated control-plane failure to propagate")
	}
	if len(fake.calls) != 2 {
		t.Fatalf("helm invocation count = %d, want exactly 2 (data plane must not install after control plane failed)", len(fake.calls))
	}
}

func TestEnsureDataPlaneTrustsControlPlane_AppliesNamespaceAndConfigMapFromRealSecret(t *testing.T) {
	api := newFakeAPI()
	api.put(caSecretPath, kubernetes.Object{Data: json.RawMessage(`{"ca.crt":"` + fakeCASecretDataB64 + `"}`)})
	d := &Deployer{client: newTestKubeClient(t, api.handler(t))}

	if err := d.ensureDataPlaneTrustsControlPlane(context.Background()); err != nil {
		t.Fatalf("ensureDataPlaneTrustsControlPlane() = %v", err)
	}
	if ns := api.lastApply(t, dataPlaneNSPath); ns.Kind != "Namespace" || ns.Metadata.Name != DataPlaneNamespace {
		t.Errorf("namespace apply = %+v", ns)
	}
	cm := api.lastApply(t, caConfigMapPath)
	if cm.Kind != "ConfigMap" || cm.Metadata.Namespace != DataPlaneNamespace {
		t.Errorf("configmap apply = %+v", cm)
	}
	var gotData map[string]string
	if err := json.Unmarshal(cm.Data, &gotData); err != nil {
		t.Fatalf("unmarshal configmap data = %v", err)
	}
	if gotData["ca.crt"] != "fake-ca-pem-data" {
		t.Errorf("configmap ca.crt = %q, want the Secret's base64-decoded value %q", gotData["ca.crt"], "fake-ca-pem-data")
	}

	// Re-running stores nothing while the CA is unchanged...
	api.writes = 0
	if err := d.ensureDataPlaneTrustsControlPlane(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.writes != 0 {
		t.Errorf("repeat stored %d writes, want 0", api.writes)
	}
	// ...and carries a rotated CA over.
	api.put(caSecretPath, kubernetes.Object{Data: json.RawMessage(`{"ca.crt":"cm90YXRlZA=="}`)})
	if err := d.ensureDataPlaneTrustsControlPlane(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(api.objects[caConfigMapPath].Data), "rotated") {
		t.Errorf("configmap data = %s, want the rotated CA", api.objects[caConfigMapPath].Data)
	}
}

func TestEnsureDataPlaneTrustsControlPlane_SourceSecretMissing_PropagatesError(t *testing.T) {
	// cert-manager hasn't issued it yet, or the control-plane chart didn't
	// create the Certificate.
	d := &Deployer{client: newTestKubeClient(t, newFakeAPI().handler(t))}
	if err := d.ensureDataPlaneTrustsControlPlane(context.Background()); err == nil {
		t.Fatal("ensureDataPlaneTrustsControlPlane() = nil, want an error when the source Secret is missing")
	}
}

func TestEnsureDataPlaneTrustsControlPlane_SecretMissingCACrtKey_PropagatesError(t *testing.T) {
	// A real TLS Secret always has tls.crt/tls.key/ca.crt, but assert the
	// explicit check fires if that ever isn't true rather than panicking.
	api := newFakeAPI()
	api.put(caSecretPath, kubernetes.Object{Data: json.RawMessage(`{"tls.crt":"` + fakeCASecretDataB64 + `"}`)})
	d := &Deployer{client: newTestKubeClient(t, api.handler(t))}
	if err := d.ensureDataPlaneTrustsControlPlane(context.Background()); err == nil {
		t.Fatal("ensureDataPlaneTrustsControlPlane() = nil, want an error when the Secret has no ca.crt key")
	}
}

// containsFlag reports whether args contains flag followed by value
// (or just flag, if value is "" and the flag takes no argument).
func containsFlag(args []string, flag, value string) bool {
	for i, a := range args {
		if a != flag {
			continue
		}
		if value == "" {
			return true
		}
		if i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
