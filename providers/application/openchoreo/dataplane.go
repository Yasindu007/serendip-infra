package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// DefaultDataPlaneName is the name of the one DataPlane CR this spec
// registers -- a single-cluster co-located install (control plane and
// data plane are the same K3s cluster, confirmed as an officially
// supported topology, plan.md) needs exactly one.
const DefaultDataPlaneName = "default"

// clusterAgentTLSSecretName/clusterAgentTLSSecretKey are the data
// plane chart's own defaults (clusterAgent.tls.secretName,
// clusterAgent.tls.generateCerts=true's self-signed Certificate) --
// confirmed directly against the pulled chart's templates/values.yaml
// during planning, not assumed. The DataPlane CR's
// spec.clusterAgent.clientCA must reference this exact Secret for the
// control plane to trust the data plane's cluster-agent connection.
const (
	clusterAgentTLSSecretName = "cluster-agent-tls"
	clusterAgentTLSSecretKey  = "ca.crt"
)

// dataPlaneAPIPath is this CRD's REST path (group openchoreo.dev,
// version v1alpha1, namespaced, plural "dataplanes" -- confirmed
// directly against the installed CRD, crds/openchoreo.dev_dataplanes.yaml).
func dataPlaneAPIPath(namespace string) string {
	return fmt.Sprintf("/apis/openchoreo.dev/v1alpha1/namespaces/%s/dataplanes", namespace)
}

// dataPlaneSpec mirrors just the one required nested field this spec
// populates (spec.clusterAgent.clientCA.secretKeyRef) -- the CRD's
// real schema has much more (gateway listener config,
// observabilityPlaneRef, secretStoreRef), all optional and left unset
// here; this package has no reason to know about them yet.
type dataPlaneSpec struct {
	ClusterAgent dataPlaneClusterAgent `json:"clusterAgent"`
}

type dataPlaneClusterAgent struct {
	ClientCA dataPlaneClientCA `json:"clientCA"`
}

type dataPlaneClientCA struct {
	SecretKeyRef dataPlaneSecretKeyRef `json:"secretKeyRef"`
}

type dataPlaneSecretKeyRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key"`
}

// EnsureDefaultDataPlane idempotently registers the one DataPlane CR
// this single-cluster install needs, in ControlPlaneNamespace --
// nothing can actually deploy through OpenChoreo until this exists
// (plan.md's own finding, confirmed against the real chart/CRD, not
// the project's add-default-dataplane.sh script -- hand-construction
// turned out exactly as reliable once the CRD schema and the cert
// Secret's real name/namespace/key were traced directly).
func EnsureDefaultDataPlane(ctx context.Context, client *kubernetes.Client) error {
	path := dataPlaneAPIPath(ControlPlaneNamespace) + "/" + DefaultDataPlaneName
	_, err := client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("openchoreo: get DataPlane %s: %w", DefaultDataPlaneName, err)
	}

	specBytes, err := json.Marshal(dataPlaneSpec{
		ClusterAgent: dataPlaneClusterAgent{
			ClientCA: dataPlaneClientCA{
				SecretKeyRef: dataPlaneSecretKeyRef{
					Name:      clusterAgentTLSSecretName,
					Namespace: DataPlaneNamespace,
					Key:       clusterAgentTLSSecretKey,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("openchoreo: marshal DataPlane spec: %w", err)
	}

	_, err = client.Create(ctx, dataPlaneAPIPath(ControlPlaneNamespace), kubernetes.Object{
		APIVersion: "openchoreo.dev/v1alpha1",
		Kind:       "DataPlane",
		Metadata:   kubernetes.ObjectMeta{Name: DefaultDataPlaneName, Namespace: ControlPlaneNamespace},
		Spec:       specBytes,
	})
	if err != nil {
		return fmt.Errorf("openchoreo: create DataPlane %s: %w", DefaultDataPlaneName, err)
	}
	return nil
}
