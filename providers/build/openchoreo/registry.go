package openchoreo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// The package's registry (plan decision 2, T001 item 2): a registry:2 on
// the node's network, so workloads pull built images as localhost:5000/...
// (containerd uses plain HTTP for localhost with no node configuration),
// while builds push over its Service. That's single-node only.
const (
	registryName  = "serendip-registry"
	registryImage = "registry:2.8.3"
	registryPort  = 5000
	// registryServiceName is the Service builds push through. T001 pushed
	// to registry.serendip-builds.svc.cluster.local:5000.
	registryServiceName = "registry"
	registryStorage     = "10Gi"
)

// pushRegistry is where builds push, and pullRegistry what workloads pull
// from, both without a scheme.
var (
	pushRegistry = fmt.Sprintf("%s.%s.svc.cluster.local:%d", registryServiceName, BuildsNamespace, registryPort)
	pullRegistry = fmt.Sprintf("localhost:%d", registryPort)
)

func registryLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/name": registryName, "app.kubernetes.io/managed-by": "serendip"}
}

func deploymentsPath(namespace string) string {
	return fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments", namespace)
}

func servicesPath(namespace string) string {
	return fmt.Sprintf("/api/v1/namespaces/%s/services", namespace)
}

func pvcsPath(namespace string) string {
	return fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", namespace)
}

// registryObjects returns the registry's PVC, Deployment and Service, in
// the order they're applied. The PVC names no storage class, so it gets
// the cluster's default (local-path on K3s), which is the Storage
// capability's default class.
func registryObjects() []kubernetes.Object {
	labels := registryLabels()
	pvc := kubernetes.Object{
		APIVersion: "v1", Kind: "PersistentVolumeClaim",
		Metadata: kubernetes.ObjectMeta{Name: registryName, Namespace: BuildsNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"accessModes": []string{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]string{"storage": registryStorage}},
		}),
	}
	deployment := kubernetes.Object{
		APIVersion: "apps/v1", Kind: "Deployment",
		Metadata: kubernetes.ObjectMeta{Name: registryName, Namespace: BuildsNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": labels},
			// Recreate: a second pod couldn't take the host port, or the
			// ReadWriteOnce volume, while the old one runs.
			"strategy": map[string]any{"type": "Recreate"},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"hostNetwork": true,
					"dnsPolicy":   "ClusterFirstWithHostNet",
					"containers": []any{map[string]any{
						"name":  "registry",
						"image": registryImage,
						"ports": []any{map[string]any{"name": "http", "containerPort": registryPort}},
						"env": []any{
							map[string]any{"name": "REGISTRY_HTTP_ADDR", "value": fmt.Sprintf(":%d", registryPort)},
						},
						"readinessProbe": map[string]any{
							"httpGet": map[string]any{"path": "/v2/", "port": registryPort},
						},
						// T001 measured ~10 MB idle.
						"resources": map[string]any{
							"requests": map[string]string{"memory": "32Mi", "cpu": "10m"},
							"limits":   map[string]string{"memory": "256Mi"},
						},
						"volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/var/lib/registry"}},
					}},
					"volumes": []any{map[string]any{
						"name":                  "data",
						"persistentVolumeClaim": map[string]string{"claimName": registryName},
					}},
				},
			},
		}),
	}
	service := kubernetes.Object{
		APIVersion: "v1", Kind: "Service",
		Metadata: kubernetes.ObjectMeta{Name: registryServiceName, Namespace: BuildsNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"selector": labels,
			"ports":    []any{map[string]any{"name": "http", "port": registryPort, "targetPort": registryPort}},
		}),
	}
	return []kubernetes.Object{pvc, deployment, service}
}

// collectionPath returns the collection an object is applied under.
func collectionPath(obj kubernetes.Object) string {
	switch obj.Kind {
	case "PersistentVolumeClaim":
		return pvcsPath(obj.Metadata.Namespace)
	case "Deployment":
		return deploymentsPath(obj.Metadata.Namespace)
	case "Service":
		return servicesPath(obj.Metadata.Namespace)
	}
	panic("openchoreo builds: no collection for " + obj.Kind)
}

// ensureRegistry applies the registry's objects.
func ensureRegistry(ctx context.Context, client *kubernetes.Client) error {
	for _, obj := range registryObjects() {
		if _, err := apply(ctx, client, collectionPath(obj), obj); err != nil {
			return err
		}
	}
	return nil
}

// removeRegistry deletes the registry's Service, Deployment and PVC, with
// the images it stored. Already gone is fine.
func removeRegistry(ctx context.Context, client *kubernetes.Client) error {
	objs := registryObjects()
	for i := len(objs) - 1; i >= 0; i-- {
		obj := objs[i]
		if err := deleteIfExists(ctx, client, collectionPath(obj)+"/"+obj.Metadata.Name); err != nil {
			return err
		}
	}
	return nil
}

// podPorts is the subset of a Pod this package reads to find who holds a
// host port.
type podPorts struct {
	Metadata kubernetes.ObjectMeta `json:"metadata"`
	Spec     struct {
		HostNetwork bool `json:"hostNetwork"`
		Containers  []struct {
			Ports []struct {
				ContainerPort int `json:"containerPort"`
				HostPort      int `json:"hostPort"`
			} `json:"ports"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// registryPortHolder names a running pod, other than the registry, that
// holds the registry's port on the node, or "" if none does (T001 item 2:
// the port is a host port, and a clash must fail clearly). Only pods are
// seen: a process outside Kubernetes holding the port shows up as the
// registry never becoming ready.
func registryPortHolder(ctx context.Context, client *kubernetes.Client) (string, error) {
	raw, err := client.GetRaw(ctx, "/api/v1/pods?"+url.Values{"fieldSelector": {"status.phase!=Succeeded,status.phase!=Failed"}}.Encode())
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}
	var list struct {
		Items []podPorts `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", fmt.Errorf("decode pods: %w", err)
	}
	for _, p := range list.Items {
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		if p.Metadata.Namespace == BuildsNamespace && p.Metadata.Labels["app.kubernetes.io/name"] == registryName {
			continue
		}
		for _, c := range p.Spec.Containers {
			for _, port := range c.Ports {
				if port.HostPort == registryPort || (p.Spec.HostNetwork && port.ContainerPort == registryPort) {
					return p.Metadata.Namespace + "/" + p.Metadata.Name, nil
				}
			}
		}
	}
	return "", nil
}
