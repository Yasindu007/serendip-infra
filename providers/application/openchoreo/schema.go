package openchoreo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

var _ reconcile.ConfigSchemaProvider = (*Provider)(nil)

// ConfigSchemas reads what can be configured on comp from the platform's
// own component type: its per-environment schema
// (spec.environmentConfigs.openAPIV3Schema). Read live, so a type changed
// by an upgrade is what's checked. Serendip's Components have no trait
// instances, so there are no trait schemas.
func (p *Provider) ConfigSchemas(ctx context.Context, comp resource.Component) (reconcile.ConfigSchemas, error) {
	ct, err := p.client.Get(ctx, clusterPath("clustercomponenttypes")+"/"+componentTypeName)
	if err != nil {
		return reconcile.ConfigSchemas{}, fmt.Errorf("get the component type %s: %w", componentTypeName, err)
	}
	var spec struct {
		EnvironmentConfigs struct {
			OpenAPIV3Schema json.RawMessage `json:"openAPIV3Schema"`
		} `json:"environmentConfigs"`
	}
	if err := json.Unmarshal(ct.Spec, &spec); err != nil {
		return reconcile.ConfigSchemas{}, fmt.Errorf("decode the component type %s: %w", componentTypeName, err)
	}
	return reconcile.ConfigSchemas{Environment: spec.EnvironmentConfigs.OpenAPIV3Schema}, nil
}
