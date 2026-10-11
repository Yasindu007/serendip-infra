package kgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const (
	// GatewayName names both the shared Gateway and its GatewayParameters.
	GatewayName = "serendip-external"

	// DefaultNodePort is where the gateway is exposed on every node. A
	// NodePort never contends with K3s's Traefik/ServiceLB for 80/443
	// (ADR 0011).
	DefaultNodePort int32 = 30080

	gatewayClassName = "kgateway"
	listenerName     = "http"
	listenerPort     = 80

	gatewaysPath   = "/apis/gateway.networking.k8s.io/v1/namespaces/" + Namespace + "/gateways"
	parametersPath = "/apis/gateway.kgateway.dev/v1alpha1/namespaces/" + Namespace + "/gatewayparameters"
)

// Provider implements reconcile.GatewayProvider with one shared kgateway
// Gateway.
type Provider struct {
	client      *kubernetes.Client
	host        string
	nodePort    int32
	proxyLabels map[string]string

	pollInterval time.Duration
	timeout      time.Duration
}

// NewProvider returns a Provider publishing routes under host, reachable
// on nodePort. proxyLabels are stamped on the gateway's proxy Pods: route
// renderers that lock workloads down with NetworkPolicies admit gateway
// traffic by pod label, and the composition root supplies whichever labels
// the configured renderer requires, so this provider never names it.
func NewProvider(client *kubernetes.Client, host string, nodePort int32, proxyLabels map[string]string) *Provider {
	return &Provider{client: client, host: host, nodePort: nodePort, proxyLabels: proxyLabels, pollInterval: 2 * time.Second, timeout: 2 * time.Minute}
}

var _ reconcile.GatewayProvider = (*Provider)(nil)

type servicePort struct {
	Port     int32 `json:"port"`
	NodePort int32 `json:"nodePort"`
}

type parametersSpec struct {
	Kube struct {
		Service struct {
			Type  string        `json:"type"`
			Ports []servicePort `json:"ports"`
		} `json:"service"`
	} `json:"kube"`
}

type parametersRef struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

type allowedRoutes struct {
	Namespaces struct {
		From string `json:"from"`
	} `json:"namespaces"`
}

type listener struct {
	Name          string        `json:"name"`
	Protocol      string        `json:"protocol"`
	Port          int32         `json:"port"`
	AllowedRoutes allowedRoutes `json:"allowedRoutes"`
}

// gatewaySpec models only the Gateway fields this provider owns.
type gatewaySpec struct {
	GatewayClassName string `json:"gatewayClassName"`
	Infrastructure   struct {
		Labels        map[string]string `json:"labels,omitempty"`
		ParametersRef parametersRef     `json:"parametersRef"`
	} `json:"infrastructure"`
	Listeners []listener `json:"listeners"`
}

type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

func (p *Provider) desiredParameters() parametersSpec {
	var s parametersSpec
	s.Kube.Service.Type = "NodePort"
	s.Kube.Service.Ports = []servicePort{{Port: listenerPort, NodePort: p.nodePort}}
	return s
}

func (p *Provider) desiredGateway() gatewaySpec {
	var s gatewaySpec
	s.GatewayClassName = gatewayClassName
	if len(p.proxyLabels) > 0 {
		s.Infrastructure.Labels = p.proxyLabels
	}
	s.Infrastructure.ParametersRef = parametersRef{Group: "gateway.kgateway.dev", Kind: "GatewayParameters", Name: GatewayName}
	l := listener{Name: listenerName, Protocol: "HTTP", Port: listenerPort}
	// Routes live in per-application namespaces, not the gateway's.
	l.AllowedRoutes.Namespaces.From = "All"
	s.Listeners = []listener{l}
	return s
}

// EnsureGateway creates (or corrects) the GatewayParameters and Gateway,
// then waits, bounded, for the Gateway to be programmed.
func (p *Provider) EnsureGateway(ctx context.Context) (reconcile.GatewayEndpoint, bool, string, string, error) {
	ep := reconcile.GatewayEndpoint{Name: GatewayName, Namespace: Namespace, Host: p.host, Port: p.nodePort}

	if err := ensureObject(ctx, p.client, parametersPath, "GatewayParameters", "gateway.kgateway.dev/v1alpha1", p.desiredParameters()); err != nil {
		return ep, false, "", "", err
	}
	if err := ensureObject(ctx, p.client, gatewaysPath, "Gateway", "gateway.networking.k8s.io/v1", p.desiredGateway()); err != nil {
		return ep, false, "", "", err
	}

	ready, reason, message, err := p.waitProgrammed(ctx)
	return ep, ready, reason, message, err
}

// ensureObject creates GatewayName under collection with spec, or merge
// patches the spec when the fields this provider owns differ. spec must be
// a struct whose JSON shape is the subset of the object's spec it owns, so
// decoding the live object into the same type ignores server defaults.
func ensureObject[S any](ctx context.Context, client *kubernetes.Client, collection, kind, apiVersion string, spec S) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("kgateway: marshal %s spec: %w", kind, err)
	}
	existing, err := client.Get(ctx, collection+"/"+GatewayName)
	if errors.Is(err, kubernetes.ErrNotFound) {
		_, err := client.Create(ctx, collection, kubernetes.Object{
			APIVersion: apiVersion, Kind: kind,
			Metadata: kubernetes.ObjectMeta{Name: GatewayName, Namespace: Namespace},
			Spec:     raw,
		})
		if err != nil {
			return fmt.Errorf("kgateway: create %s %s: %w", kind, GatewayName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("kgateway: get %s %s: %w", kind, GatewayName, err)
	}
	var current S
	if err := json.Unmarshal(existing.Spec, &current); err != nil {
		return fmt.Errorf("kgateway: decode %s %s spec: %w", kind, GatewayName, err)
	}
	// Compare re-marshaled JSON rather than the decoded structs: omitempty
	// treats a nil and an empty map alike, so a server-side "{}" doesn't
	// cause a patch on every startup. Known limit: dropping a key from an
	// owned map (e.g. one fewer proxy label) leaves it on the live object,
	// since a merge patch only deletes keys sent as null.
	currentRaw, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("kgateway: marshal %s %s spec: %w", kind, GatewayName, err)
	}
	if bytes.Equal(currentRaw, raw) {
		return nil
	}
	if _, err := client.Patch(ctx, collection+"/"+GatewayName, map[string]json.RawMessage{"spec": raw}); err != nil {
		return fmt.Errorf("kgateway: patch %s %s: %w", kind, GatewayName, err)
	}
	return nil
}

// waitProgrammed polls the Gateway until its Programmed condition is True
// for the current generation, or the timeout passes. A timeout is reported
// as not-ready with the last upstream reason, not as an error.
func (p *Provider) waitProgrammed(ctx context.Context) (bool, string, string, error) {
	deadline := time.Now().Add(p.timeout)
	reason, message := "NotProgrammed", "gateway has not reported a Programmed condition yet"
	for {
		gw, err := p.client.Get(ctx, gatewaysPath+"/"+GatewayName)
		if err != nil {
			return false, "", "", fmt.Errorf("kgateway: get Gateway %s: %w", GatewayName, err)
		}
		var st struct {
			Conditions []condition `json:"conditions"`
		}
		if len(gw.Status) > 0 {
			if err := json.Unmarshal(gw.Status, &st); err != nil {
				return false, "", "", fmt.Errorf("kgateway: decode Gateway %s status: %w", GatewayName, err)
			}
		}
		for _, c := range st.Conditions {
			if c.Type != "Programmed" || (c.ObservedGeneration > 0 && c.ObservedGeneration < gw.Metadata.Generation) {
				continue
			}
			if c.Status == "True" {
				return true, "", "", nil
			}
			reason, message = c.Reason, c.Message
		}
		if time.Now().After(deadline) {
			return false, reason, message, nil
		}
		select {
		case <-ctx.Done():
			return false, "", "", ctx.Err()
		case <-time.After(p.pollInterval):
		}
	}
}
