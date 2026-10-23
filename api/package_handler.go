package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// PackageHandler serves GET /v1/packages: the optional packages this
// server offers (specs/020 plan decision 5), and whether each is
// available in this build. Installing one is creating a ProviderInstance
// that names it; removing it is deleting that, through an Approval.
type PackageHandler struct {
	catalogue reconcile.Catalogue
}

func (h *PackageHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/packages", h.List)
}

type packageView struct {
	reconcile.Package
	Available bool `json:"available"`
}

func (h *PackageHandler) List(w http.ResponseWriter, r *http.Request) {
	out := []packageView{}
	for _, p := range h.catalogue {
		out = append(out, packageView{Package: p, Available: p.Installer != nil})
	}
	slices.SortFunc(out, func(a, b packageView) int { return strings.Compare(a.Provider, b.Provider) })
	writeJSON(w, http.StatusOK, map[string]any{"packages": out})
}

// packageValidator checks a ProviderInstance names a package this server
// offers, and that its capability has no other instance: one package per
// optional capability. Two simultaneous installs could both pass the
// check (RP-033); installing is an Owner's rare, deliberate act.
func packageValidator(c reconcile.Catalogue, s store.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus]) func(ctx context.Context, id string, spec resource.ProviderInstanceSpec) error {
	return func(ctx context.Context, id string, spec resource.ProviderInstanceSpec) error {
		if err := c.Validate(spec); err != nil {
			return err
		}
		if id != "" {
			// Which package an instance installs is fixed: changing it
			// would leave the old one installed.
			current, err := s.Get(ctx, id)
			if err != nil {
				return err
			}
			if current.Spec.ProviderID != spec.ProviderID || current.Spec.CapabilityID != spec.CapabilityID {
				return fmt.Errorf("%w: providerId and capabilityId can't change: remove this package and install the other", resource.ErrValidation)
			}
		}
		all, err := s.List(ctx, store.ListOptions{})
		if err != nil {
			return err
		}
		for _, other := range all {
			if other.Metadata.ID != id && other.Spec.CapabilityID == spec.CapabilityID {
				return fmt.Errorf("%w: capabilityId: %s already has a package (ProviderInstance %s); remove it first", resource.ErrValidation, spec.CapabilityID, other.Metadata.ID)
			}
		}
		return nil
	}
}
