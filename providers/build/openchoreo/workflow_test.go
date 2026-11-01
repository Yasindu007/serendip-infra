package openchoreo

import (
	"encoding/json"
	"strings"
	"testing"
)

// The workflow is T001's, so these check the parts that broke or mattered
// in the spike rather than every field.
func TestClusterWorkflow_MatchesTheSpike(t *testing.T) {
	obj := clusterWorkflow()
	if obj.APIVersion != "openchoreo.dev/v1alpha1" || obj.Kind != "ClusterWorkflow" || obj.Metadata.Name != "serendip-dockerfile" || obj.Metadata.Namespace != "" {
		t.Errorf("envelope = %+v", obj)
	}
	spec := string(obj.Spec)
	for _, want := range []string{
		`"workflowPlaneRef":{"kind":"ClusterWorkflowPlane","name":"default"}`,
		`"ttlAfterCompletion":"1h"`,
		`{"name":"git-secret","value":"${metadata.workflowRunName}-git"}`,
		`"secret":{"optional":true,"secretName":"{{workflow.parameters.git-secret}}"}`,
		`"securityContext":{"privileged":true}`,
		`"pushRegistry":{"default":"registry.serendip-builds.svc.cluster.local:5000","type":"string"}`,
		`"pullRegistry":{"default":"localhost:5000","type":"string"}`,
		`"value":"{{steps.checkout.outputs.parameters.commit}}"`,
		`"image":"alpine/git:v2.52.0"`,
		`"image":"ghcr.io/openchoreo/podman-runner:v1.2"`,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec lacks %s", want)
		}
	}
	// No user namespaces on kind (RP-038), and no upstream steps.
	for _, unwanted := range []string{"hostUsers", "generate-workload", "ExternalSecret"} {
		if strings.Contains(spec, unwanted) {
			t.Errorf("spec has %s", unwanted)
		}
	}
}

func TestClusterWorkflow_OutputsAreOnTheirSteps(t *testing.T) {
	var spec struct {
		RunTemplate struct {
			Spec struct {
				Templates []struct {
					Name    string `json:"name"`
					Outputs struct {
						Parameters []struct {
							Name string `json:"name"`
						} `json:"parameters"`
					} `json:"outputs"`
				} `json:"templates"`
			} `json:"spec"`
		} `json:"runTemplate"`
	}
	if err := json.Unmarshal(clusterWorkflow().Spec, &spec); err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{}
	for _, tpl := range spec.RunTemplate.Spec.Templates {
		for _, p := range tpl.Outputs.Parameters {
			outputs[p.Name] = tpl.Name
		}
	}
	if outputs[CommitOutput] != CheckoutStep || outputs[ImageOutput] != BuildPushStep {
		t.Errorf("outputs = %v", outputs)
	}
}

// Argo would try to expand a literal "{{" in a script (T001 item 3).
func TestScripts_HaveNoArgoBraces(t *testing.T) {
	for name, s := range map[string]string{"checkout": checkoutScript, "build-push": buildPushScript} {
		if strings.Contains(s, "{{") || strings.Contains(s, "${") {
			t.Errorf("%s script has template braces", name)
		}
	}
}

func TestRegistryObjects(t *testing.T) {
	objs := registryObjects()
	if len(objs) != 3 || objs[0].Kind != "PersistentVolumeClaim" || objs[1].Kind != "Deployment" || objs[2].Kind != "Service" {
		t.Fatalf("objects = %v", objs)
	}
	dep := string(objs[1].Spec)
	for _, want := range []string{`"hostNetwork":true`, `"image":"registry:2.8.3"`, `"type":"Recreate"`, `"claimName":"serendip-registry"`} {
		if !strings.Contains(dep, want) {
			t.Errorf("deployment lacks %s", want)
		}
	}
	if strings.Contains(string(objs[0].Spec), "storageClassName") {
		t.Error("the PVC should take the default storage class")
	}
}
