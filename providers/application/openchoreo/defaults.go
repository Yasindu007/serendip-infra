package openchoreo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const apiVersion = "openchoreo.dev/v1alpha1"

// controlPlaneNamespaceLabel marks a namespace as an OpenChoreo org
// namespace: the boundary Projects/Components/Environments live in.
// OpenChoreo 1.3.1 has no Organization CRD.
const controlPlaneNamespaceLabel = "openchoreo.dev/control-plane"

// Names of the platform-level objects EnsurePlatformDefaults creates. The
// cluster-scoped types are serendip-prefixed so they can't collide with
// OpenChoreo's own getting-started sample (default/service), which an
// operator may also have applied.
const (
	projectTypeName       = "serendip-default"
	componentTypeName     = "serendip-service"
	deploymentPipeline    = "serendip"
	componentTypeWorkload = "deployment"
)

func clusterPath(plural string) string {
	return "/apis/openchoreo.dev/v1alpha1/" + plural
}

func namespacedPath(plural string) string {
	return fmt.Sprintf("/apis/openchoreo.dev/v1alpha1/namespaces/%s/%s", ControlPlaneNamespace, plural)
}

// apply server-side-applies obj into collection (its item path is
// collection/name) under kubernetes.FieldManager and returns the result.
func apply(ctx context.Context, client *kubernetes.Client, collection string, obj kubernetes.Object) (kubernetes.Object, error) {
	applied, err := client.Apply(ctx, collection+"/"+obj.Metadata.Name, obj, kubernetes.FieldManager)
	if err != nil {
		return kubernetes.Object{}, fmt.Errorf("apply %s %s: %w", obj.Kind, obj.Metadata.Name, err)
	}
	return applied, nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("openchoreo: marshal static object: %v", err))
	}
	return b
}

// EnsurePlatformDefaults server-side-applies the platform-level objects
// every Serendip Application/Component deploys through, none of which the
// OpenChoreo charts seed themselves: a project type, a component type, the
// single Environment, a single-environment DeploymentPipeline, and the
// NetworkPolicy that closes the OpenChoreo API to every pod (apiDenyPolicy;
// risk register RP-018). It also labels ControlPlaneNamespace as the org namespace (reused because the
// default DataPlane already lives there). Safe to call repeatedly -- cmd's
// foundations loop does, so a default deleted or edited by hand is
// restored, and a field a newer Serendip stops setting is removed. A
// no-drift pass stores nothing.
func EnsurePlatformDefaults(ctx context.Context, client *kubernetes.Client) error {
	if err := ensureNamespaceLabel(ctx, client); err != nil {
		return fmt.Errorf("openchoreo: label namespace %s: %w", ControlPlaneNamespace, err)
	}
	for _, d := range platformDefaults() {
		if _, err := apply(ctx, client, d.collection, d.obj); err != nil {
			return fmt.Errorf("openchoreo: platform defaults: %w", err)
		}
	}
	return nil
}

// ensureNamespaceLabel stays a merge patch, not an apply: Helm created this
// namespace, and Serendip owns exactly one label on it.
func ensureNamespaceLabel(ctx context.Context, client *kubernetes.Client) error {
	path := "/api/v1/namespaces/" + ControlPlaneNamespace
	ns, err := client.Get(ctx, path)
	if err != nil {
		return err
	}
	if ns.Metadata.Labels[controlPlaneNamespaceLabel] == "true" {
		return nil
	}
	_, err = client.Patch(ctx, path, map[string]any{
		"metadata": map[string]any{"labels": map[string]string{controlPlaneNamespaceLabel: "true"}},
	})
	return err
}

type platformDefault struct {
	collection string
	obj        kubernetes.Object
}

func platformDefaults() []platformDefault {
	return []platformDefault{
		{clusterPath("clusterprojecttypes"), kubernetes.Object{
			APIVersion: apiVersion, Kind: "ClusterProjectType",
			Metadata: kubernetes.ObjectMeta{Name: projectTypeName},
			Spec:     mustJSON(projectTypeSpec()),
		}},
		{clusterPath("clustercomponenttypes"), kubernetes.Object{
			APIVersion: apiVersion, Kind: "ClusterComponentType",
			Metadata: kubernetes.ObjectMeta{Name: componentTypeName},
			Spec:     mustJSON(componentTypeSpec()),
		}},
		{namespacedPath("environments"), kubernetes.Object{
			APIVersion: apiVersion, Kind: "Environment",
			Metadata: kubernetes.ObjectMeta{Name: environmentName, Namespace: ControlPlaneNamespace},
			Spec: mustJSON(map[string]any{
				"dataPlaneRef": map[string]string{"kind": "DataPlane", "name": DefaultDataPlaneName},
				"isProduction": false,
			}),
		}},
		{namespacedPath("deploymentpipelines"), kubernetes.Object{
			APIVersion: apiVersion, Kind: "DeploymentPipeline",
			Metadata: kubernetes.ObjectMeta{Name: deploymentPipeline, Namespace: ControlPlaneNamespace},
			Spec: mustJSON(map[string]any{
				"promotionPaths": []any{map[string]any{
					"sourceEnvironmentRef":  map[string]string{"name": environmentName},
					"targetEnvironmentRefs": []any{},
				}},
			}),
		}},
		{"/apis/networking.k8s.io/v1/namespaces/" + ControlPlaneNamespace + "/networkpolicies", apiDenyPolicy()},
	}
}

// projectTypeSpec is OpenChoreo v1.3.1's getting-started ClusterProjectType
// "default": it provisions only the per-environment data-plane cell
// namespace.
func projectTypeSpec() map[string]any {
	stringMap := map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "string"},
		"default":              map[string]any{},
	}
	return map[string]any{
		"environmentConfigs": map[string]any{"openAPIV3Schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespaceLabels":      stringMap,
				"namespaceAnnotations": stringMap,
			},
		}},
		"resources": []any{map[string]any{
			"id": "cell-namespace",
			"template": map[string]any{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]any{
					"name":        "${metadata.namespace}",
					"labels":      "${oc_merge(metadata.labels, environmentConfigs.namespaceLabels)}",
					"annotations": "${environmentConfigs.namespaceAnnotations}",
				},
			},
		}},
	}
}

// componentTypeSpec is a trimmed copy of OpenChoreo v1.3.1's
// getting-started ClusterComponentType "service": the Deployment, the
// ClusterIP Service, and the ConfigMap that carries the Workload's
// plain env vars (consumed via envFrom). Dropped from the sample: the
// HTTPRoute resources and their visibility validations (gateway ingress
// is disabled), ExternalSecret resources (external-secrets isn't
// installed; secret injection is out of scope), file-mount config,
// command/args, and the workflow/trait allow-lists.
func componentTypeSpec() map[string]any {
	quantity := map[string]any{
		"type":    "object",
		"default": map[string]any{},
		"properties": map[string]any{
			"cpu":    map[string]any{"type": "string", "default": "100m"},
			"memory": map[string]any{"type": "string", "default": "256Mi"},
		},
	}
	return map[string]any{
		"workloadType": componentTypeWorkload,
		"validations": []any{map[string]any{
			"rule":    "${size(workload.endpoints) > 0}",
			"message": "component must have at least one endpoint",
		}},
		"environmentConfigs": map[string]any{"openAPIV3Schema": map[string]any{
			"type": "object",
			"$defs": map[string]any{
				"ResourceQuantity": quantity,
				"ResourceRequirements": map[string]any{
					"type":    "object",
					"default": map[string]any{},
					"properties": map[string]any{
						"requests": map[string]any{"$ref": "#/$defs/ResourceQuantity"},
						"limits":   map[string]any{"$ref": "#/$defs/ResourceQuantity"},
					},
				},
			},
			"properties": map[string]any{
				"replicas":        map[string]any{"type": "integer", "default": 1, "minimum": 0},
				"resources":       map[string]any{"$ref": "#/$defs/ResourceRequirements"},
				"imagePullPolicy": map[string]any{"type": "string", "default": "IfNotPresent", "enum": []string{"Always", "IfNotPresent", "Never"}},
			},
		}},
		"resources": []any{
			map[string]any{"id": "deployment", "template": deploymentTemplate()},
			map[string]any{
				"id":          "service",
				"includeWhen": "${size(workload.endpoints) > 0}",
				"template": map[string]any{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]any{
						"name":      "${metadata.componentName}",
						"namespace": "${metadata.namespace}",
						"labels":    "${metadata.labels}",
					},
					"spec": map[string]any{
						"type":     "ClusterIP",
						"selector": "${metadata.podSelectors}",
						"ports":    "${workload.toServicePorts()}",
					},
				},
			},
			map[string]any{
				"id":       "httproute-external",
				"forEach":  `${workload.endpoints.transformMap(name, ep, has(ep.visibility) && "external" in ep.visibility && ep.type == "HTTP", ep)}`,
				"var":      "endpoint",
				"template": httpRouteExternalTemplate(),
			},
			map[string]any{
				"id":      "env-config",
				"forEach": "${configurations.toConfigEnvsByContainer()}",
				"var":     "envConfig",
				"template": map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name":      "${envConfig.resourceName}",
						"namespace": "${metadata.namespace}",
					},
					"data": "${envConfig.envs.transformMapEntry(index, env, {env.name: env.value})}",
				},
			},
		},
	}
}

// httpRouteExternalTemplate is OpenChoreo v1.3.1's sample "service" type's
// external HTTPRoute, minus its CORS filter (CORS on HTTPRoute is outside
// the standard-channel Gateway API installed here). It attaches to the
// gateway named in the DataPlane's gateway.ingress.external, under host
// <environment>-<org namespace>.<gateway host>, path /<component>-<endpoint>.
// The labels are what the release controller uses to report the
// endpoint's invoke URL.
func httpRouteExternalTemplate() map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      "${oc_generate_name(metadata.componentName, endpoint.key)}",
			"namespace": "${metadata.namespace}",
			"labels":    `${oc_merge(metadata.labels, {"openchoreo.dev/endpoint-name": endpoint.key, "openchoreo.dev/endpoint-visibility": "external"})}`,
		},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{
				"name":      "${gateway.ingress.external.name}",
				"namespace": "${gateway.ingress.external.namespace}",
			}},
			"hostnames": `${[gateway.ingress.external.?http, gateway.ingress.external.?https].filter(g, g.hasValue()).map(g, g.value().host).distinct().map(h, metadata.environmentName + "-" + metadata.componentNamespace + "." + h)}`,
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{
					"path": map[string]any{"type": "PathPrefix", "value": "/${metadata.componentName}-${endpoint.key}"},
				}},
				"filters": []any{map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{"path": map[string]any{
						"type":               "ReplacePrefixMatch",
						"replacePrefixMatch": `${endpoint.value.?basePath.orValue("/")}`,
					}},
				}},
				"backendRefs": []any{map[string]any{
					"name": "${metadata.componentName}",
					"port": "${endpoint.value.port}",
				}},
			}},
		},
	}
}

func deploymentTemplate() map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "${metadata.name}",
			"namespace": "${metadata.namespace}",
			"labels":    "${metadata.labels}",
		},
		"spec": map[string]any{
			"replicas": "${environmentConfigs.replicas}",
			"selector": map[string]any{"matchLabels": "${metadata.podSelectors}"},
			"template": map[string]any{
				"metadata": map[string]any{"labels": "${metadata.podSelectors}"},
				"spec": map[string]any{
					"containers": []any{map[string]any{
						"name":            "main",
						"image":           "${workload.container.image}",
						"imagePullPolicy": "${environmentConfigs.imagePullPolicy}",
						"resources": map[string]any{
							"requests": map[string]any{
								"cpu":    "${environmentConfigs.resources.requests.cpu}",
								"memory": "${environmentConfigs.resources.requests.memory}",
							},
							"limits": map[string]any{
								"cpu":    "${environmentConfigs.resources.limits.cpu}",
								"memory": "${environmentConfigs.resources.limits.memory}",
							},
						},
						"env":     "${dependencies.toContainerEnvs()}",
						"envFrom": "${configurations.toContainerEnvFrom()}",
					}},
				},
			},
		},
	}
}

// apiDenyPolicyName names the NetworkPolicy that closes the OpenChoreo API.
const apiDenyPolicyName = "serendip-deny-openchoreo-api"

// apiDenyPolicy denies all ingress to the OpenChoreo API server's pods
// (risk register RP-018). The control-plane chart is installed with
// security.enabled=false (see deploy.go), which makes that API accept
// every request with no authentication and no authorization, and its
// ClusterIP Service is otherwise reachable from any pod: a workload could
// create or delete OpenChoreo resources outside Serendip's RBAC, approvals
// and audit. Serendip never calls that API (it reads and writes the CRs
// through the Kubernetes API), and no other OpenChoreo component does
// either (verified on 1.3.1: only the kubelet's probes reach it, which
// NetworkPolicy doesn't block). An empty ingress list is deny-all.
func apiDenyPolicy() kubernetes.Object {
	return kubernetes.Object{
		APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy",
		Metadata: kubernetes.ObjectMeta{Name: apiDenyPolicyName, Namespace: ControlPlaneNamespace},
		Spec: mustJSON(map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/component": "api-server"}},
			"policyTypes": []string{"Ingress"},
			"ingress":     []any{},
		}),
	}
}
