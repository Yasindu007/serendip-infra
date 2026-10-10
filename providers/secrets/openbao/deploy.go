package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Namespace is shared with providers/storage/localpath -- whichever
// provider's Ensure/NewProvider runs first creates it (idempotent
// GET-then-Create, safe either order). A dedicated namespace keeps
// every Serendip-managed object visibly separate from anything else a
// user might put in the cluster.
const Namespace = "serendip-system"

const (
	StatefulSetName = "openbao"
	ServiceName     = "openbao"
	PVCName         = "openbao-data"
	ConfigMapName   = "openbao-config"
	Image           = "quay.io/openbao/openbao:2.7.1"
	ListenPort      = 8200
)

// serverConfigHCL is OpenBao's server config -- file storage backend
// (standalone mode, ADR 0009), a plain tcp listener with TLS disabled
// (fine since this stays cluster-internal, reached only via the
// Kubernetes API server's Service-proxy subresource -- see doc.go).
// Deliberately carries NO mlock-related directive at all: OpenBao 2.7+
// removed mlock entirely, and any disable_mlock key (even "false") is
// now a fatal startup error, not a no-op (confirmed via oss-researcher
// against OpenBao's own current docs).
const serverConfigHCL = `storage "file" {
  path = "/openbao/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = "true"
}
`

// Deployer constructs the OpenBao Kubernetes deployment directly via
// providers/kubernetes.Client -- no Helm (ADR 0009), same precedent
// providers/storage/localpath set for bypassing Helm/dynamic-
// provisioning machinery in favor of direct API object creation.
type Deployer struct {
	client *kubernetes.Client
}

// NewDeployer builds a Deployer. Call Ensure to actually create the
// deployment -- construction alone performs no API calls.
func NewDeployer(client *kubernetes.Client) *Deployer {
	return &Deployer{client: client}
}

// Ensure idempotently creates (GET-then-Create) the namespace,
// ConfigMap, PVC, Service, and StatefulSet, in that dependency order --
// each later object references an earlier one by name (the
// StatefulSet mounts the ConfigMap and the PVC; the Service selects
// the StatefulSet's Pods), so creating them any other order would let
// a reference dangle, even though Kubernetes itself doesn't enforce
// creation ordering by reference.
func (d *Deployer) Ensure(ctx context.Context) error {
	if err := d.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("openbao: ensure namespace %s: %w", Namespace, err)
	}
	if err := d.ensureConfigMap(ctx); err != nil {
		return fmt.Errorf("openbao: ensure ConfigMap %s: %w", ConfigMapName, err)
	}
	if err := d.ensurePVC(ctx); err != nil {
		return fmt.Errorf("openbao: ensure PVC %s: %w", PVCName, err)
	}
	if err := d.ensureService(ctx); err != nil {
		return fmt.Errorf("openbao: ensure Service %s: %w", ServiceName, err)
	}
	if err := d.ensureStatefulSet(ctx); err != nil {
		return fmt.Errorf("openbao: ensure StatefulSet %s: %w", StatefulSetName, err)
	}
	return nil
}

func (d *Deployer) ensureNamespace(ctx context.Context) error {
	_, err := d.client.Get(ctx, "/api/v1/namespaces/"+Namespace)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	_, err = d.client.Create(ctx, "/api/v1/namespaces", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Namespace",
		Metadata:   kubernetes.ObjectMeta{Name: Namespace},
	})
	return err
}

func (d *Deployer) ensureConfigMap(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", Namespace, ConfigMapName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	dataBytes, err := json.Marshal(map[string]string{"server.hcl": serverConfigHCL})
	if err != nil {
		return fmt.Errorf("marshal ConfigMap data: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/configmaps", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   kubernetes.ObjectMeta{Name: ConfigMapName, Namespace: Namespace},
		Data:       dataBytes,
	})
	return err
}

// ensurePVC deliberately omits storageClassName so Kubernetes picks
// the cluster's default StorageClass (K3s's bundled local-path,
// dynamic provisioning, standard WaitForFirstConsumer binding --
// untouched, not bypassed). This differs from providers/storage/
// localpath's own static PV/PVC approach: that provider had no
// consuming Pod to trigger WaitForFirstConsumer's scheduling-driven
// binding, but the StatefulSet's Pod here IS a real consuming Pod, so
// the provisioner's normal, as-designed dynamic-provisioning path
// works with zero workarounds -- see plan.md's open-question 6.
func (d *Deployer) ensurePVC(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", Namespace, PVCName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	specBytes, err := json.Marshal(map[string]any{
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]string{"storage": "1Gi"},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal PVC spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Metadata:   kubernetes.ObjectMeta{Name: PVCName, Namespace: Namespace},
		Spec:       specBytes,
	})
	return err
}

func (d *Deployer) ensureService(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/%s", Namespace, ServiceName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	specBytes, err := json.Marshal(map[string]any{
		"selector": map[string]string{"app": "openbao"},
		"ports": []map[string]any{
			{"port": ListenPort, "targetPort": ListenPort},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal Service spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/services", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Service",
		Metadata:   kubernetes.ObjectMeta{Name: ServiceName, Namespace: Namespace},
		Spec:       specBytes,
	})
	return err
}

// ensureStatefulSet's container args explicitly override the image's
// default entrypoint command rather than relying on it -- a
// deliberate, documented assumption (plan.md) to verify against the
// real image in T007: if the official entrypoint script requires a
// different invocation, this is the one line to change, not a design
// rework.
func (d *Deployer) ensureStatefulSet(ctx context.Context) error {
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", Namespace, StatefulSetName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	labels := map[string]string{"app": "openbao"}
	specBytes, err := json.Marshal(map[string]any{
		"serviceName": ServiceName,
		"replicas":    1,
		"selector":    map[string]any{"matchLabels": labels},
		"template": map[string]any{
			"metadata": map[string]any{"labels": labels},
			"spec": map[string]any{
				"containers": []map[string]any{
					{
						"name":  "openbao",
						"image": Image,
						"args":  []string{"server", "-config=/openbao/config/server.hcl"},
						"ports": []map[string]any{
							{"containerPort": ListenPort},
						},
						"volumeMounts": []map[string]any{
							{"name": "config", "mountPath": "/openbao/config"},
							{"name": "data", "mountPath": "/openbao/data"},
						},
						"resources": map[string]any{
							"requests": map[string]string{"cpu": "100m", "memory": "128Mi"},
							"limits":   map[string]string{"cpu": "250m", "memory": "512Mi"},
						},
					},
				},
				"volumes": []map[string]any{
					{"name": "config", "configMap": map[string]any{"name": ConfigMapName}},
					{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": PVCName}},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal StatefulSet spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets", Namespace), kubernetes.Object{
		APIVersion: "apps/v1",
		Kind:       "StatefulSet",
		Metadata:   kubernetes.ObjectMeta{Name: StatefulSetName, Namespace: Namespace, Labels: labels},
		Spec:       specBytes,
	})
	return err
}
