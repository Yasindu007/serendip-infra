package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

var _ reconcile.PlatformViewProvider = (*Provider)(nil)

// descriptionAnnotation is where the platform's objects carry a human
// description.
const descriptionAnnotation = "openchoreo.dev/description"

// Platform describes the platform read-only (spec 020): its data planes,
// and the component types and traits installed, cluster-wide and in the
// control-plane namespace. Environments and the pipeline are Serendip's
// own records, served by their own routes.
func (p *Provider) Platform(ctx context.Context) (reconcile.PlatformView, error) {
	var view reconcile.PlatformView
	for _, path := range []string{namespacedPath("dataplanes"), clusterPath("clusterdataplanes")} {
		objs, err := p.listIfServed(ctx, path)
		if err != nil {
			return reconcile.PlatformView{}, err
		}
		for _, o := range objs {
			ready, message := planeReady(o)
			view.Runtimes = append(view.Runtimes, reconcile.PlatformRuntime{Name: o.Metadata.Name, Ready: ready, Message: message})
		}
	}
	var err error
	if view.ComponentTypes, err = p.platformKinds(ctx, "clustercomponenttypes", "componenttypes"); err != nil {
		return reconcile.PlatformView{}, err
	}
	if view.Traits, err = p.platformKinds(ctx, "clustertraits", "traits"); err != nil {
		return reconcile.PlatformView{}, err
	}
	return view, nil
}

// listIfServed lists path, treating a kind the platform doesn't serve
// (404) as none.
func (p *Provider) listIfServed(ctx context.Context, path string) ([]kubernetes.Object, error) {
	objs, err := p.client.List(ctx, path)
	var status *kubernetes.StatusError
	if errors.Is(err, kubernetes.ErrNotFound) || (errors.As(err, &status) && status.Code == 404) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", path, err)
	}
	return objs, nil
}

// planeReady reads a data plane's conditions: ready when Ready (or, on
// planes that report only that, Created) is True.
func planeReady(o kubernetes.Object) (bool, string) {
	var st struct {
		Conditions []struct {
			Type, Status, Message string
		} `json:"conditions"`
	}
	if len(o.Status) > 0 && json.Unmarshal(o.Status, &st) != nil {
		return false, "its status can't be read"
	}
	byType := map[string]string{}
	message := ""
	for _, c := range st.Conditions {
		byType[c.Type] = c.Status
		if c.Status == "False" && message == "" {
			message = c.Message
		}
	}
	if s, ok := byType["Ready"]; ok {
		return s == "True", message
	}
	return byType["Created"] == "True", message
}

// platformKinds lists one kind of type or trait, cluster-wide then in the
// control-plane namespace, by name.
func (p *Provider) platformKinds(ctx context.Context, cluster, namespaced string) ([]reconcile.PlatformComponent, error) {
	var out []reconcile.PlatformComponent
	for _, path := range []string{clusterPath(cluster), namespacedPath(namespaced)} {
		objs, err := p.listIfServed(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			var spec struct {
				Parameters struct {
					Schema json.RawMessage `json:"openAPIV3Schema"`
				} `json:"parameters"`
				EnvironmentConfigs struct {
					Schema json.RawMessage `json:"openAPIV3Schema"`
				} `json:"environmentConfigs"`
				AllowedTraits []struct {
					Name string `json:"name"`
				} `json:"allowedTraits"`
			}
			if len(o.Spec) > 0 {
				if err := json.Unmarshal(o.Spec, &spec); err != nil {
					return nil, fmt.Errorf("decode %s/%s: %w", path, o.Metadata.Name, err)
				}
			}
			c := reconcile.PlatformComponent{Name: o.Metadata.Name, Description: o.Metadata.Annotations[descriptionAnnotation],
				Parameters: spec.Parameters.Schema, Environment: spec.EnvironmentConfigs.Schema}
			for _, t := range spec.AllowedTraits {
				c.AllowedTraits = append(c.AllowedTraits, t.Name)
			}
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
