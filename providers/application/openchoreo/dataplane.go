package openchoreo

import (
	"context"
	"fmt"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
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

// dataPlaneSpec mirrors just the fields this package owns on the DataPlane:
// the cluster agent's client CA, and the external gateway routes attach
// to. The CRD's real schema has more (observabilityPlaneRef,
// secretStoreRef, ...), all optional and left unset here.
type dataPlaneSpec struct {
	ClusterAgent dataPlaneClusterAgent `json:"clusterAgent"`
	Gateway      dataPlaneGateway      `json:"gateway"`
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

type dataPlaneGateway struct {
	Ingress dataPlaneIngress `json:"ingress"`
}

type dataPlaneIngress struct {
	External gatewayEndpointSpec `json:"external"`
}

// EnsureDataPlane server-side-applies the one DataPlane CR this
// single-cluster install needs, in ControlPlaneNamespace: nothing can
// deploy through OpenChoreo until it exists. It trusts the data plane
// chart's cluster-agent certificate, and points external ingress at the
// shared gateway ep, so external Workload endpoints render HTTPRoutes
// attached to it. The gateway is configured on the DataPlane rather than
// the Environment because the release controller watches DataPlanes and
// re-renders on change. Safe to call repeatedly (cmd's foundations loop
// does); a no-drift apply stores nothing.
func EnsureDataPlane(ctx context.Context, client *kubernetes.Client, ep reconcile.GatewayEndpoint) error {
	_, err := apply(ctx, client, dataPlaneAPIPath(ControlPlaneNamespace), kubernetes.Object{
		APIVersion: apiVersion,
		Kind:       "DataPlane",
		Metadata:   kubernetes.ObjectMeta{Name: DefaultDataPlaneName, Namespace: ControlPlaneNamespace},
		Spec: mustJSON(dataPlaneSpec{
			ClusterAgent: dataPlaneClusterAgent{
				ClientCA: dataPlaneClientCA{
					SecretKeyRef: dataPlaneSecretKeyRef{
						Name:      clusterAgentTLSSecretName,
						Namespace: DataPlaneNamespace,
						Key:       clusterAgentTLSSecretKey,
					},
				},
			},
			Gateway: dataPlaneGateway{Ingress: dataPlaneIngress{External: gatewayEndpointSpec{
				Name: ep.Name, Namespace: ep.Namespace, HTTP: gatewayListener{Port: ep.Port, Host: ep.Host},
			}}},
		}),
	})
	if err != nil {
		return fmt.Errorf("openchoreo: %w", err)
	}
	return nil
}

// GatewayProxyLabels are the pod labels the shared gateway's proxies must
// carry. Every component OpenChoreo renders gets a NetworkPolicy admitting
// traffic on its exposed ports only from same-namespace pods or from pods
// labeled openchoreo.dev/system-component -- the label OpenChoreo's own
// data-plane chart stamps on its gateway proxies via
// spec.infrastructure.labels. Without it the route attaches but every
// request times out at the proxy (found in spec 012's real-cluster run).
func GatewayProxyLabels() map[string]string {
	return map[string]string{"openchoreo.dev/system-component": "gateway"}
}

type gatewayListener struct {
	Port int32  `json:"port"`
	Host string `json:"host"`
}

type gatewayEndpointSpec struct {
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	HTTP      gatewayListener `json:"http"`
}
