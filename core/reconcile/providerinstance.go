package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Packages are optional capabilities installed on demand (specs/020 plan
// decision 5): a ProviderInstance names one, and its reconciler installs
// it. Removing the ProviderInstance is a delete, so it goes through an
// Approval, and the approval's clean-up uninstalls it.

// Package readiness reasons, as GET /v1/readiness reports them.
const (
	ReasonNotInstalled  = "NotInstalled"
	ReasonInstalling    = "Installing"
	ReasonInstalled     = "Ready"
	ReasonInstallFailed = "InstallFailed"
	// ReasonUnavailable: the package isn't available in this build yet.
	ReasonUnavailable = "Unavailable"
	// ReasonDuplicate: another ProviderInstance already provides the
	// capability; only the oldest installs.
	ReasonDuplicate = "Duplicate"
)

// PackageState is what an install observed. Message is shown to people,
// so it's the installer's own wording, never raw error text.
type PackageState struct {
	Ready   bool
	Message string
}

// PackageInstaller installs and removes one package. Install is
// idempotent: it brings the package towards installed and reports how
// far it is. Uninstall removes what instanceID installed, and is
// idempotent too.
type PackageInstaller interface {
	Install(ctx context.Context, inst resource.ProviderInstance) (PackageState, error)
	Uninstall(ctx context.Context, instanceID string) error
}

// Package is one installable package: the provider that offers an
// optional capability.
type Package struct {
	Provider    string `json:"provider"`
	Capability  string `json:"capability"`
	Description string `json:"description"`
	// Installer is nil while the package isn't available in this build.
	Installer PackageInstaller `json:"-"`
}

// Catalogue is the packages this build knows, by provider name.
type Catalogue map[string]Package

// ReadinessFunc reports a capability's state, for GET /v1/readiness.
type ReadinessFunc func(capability string, ready bool, reason, message string)

// sorted lists the catalogue's packages by provider name, so anything
// done for each is done in the same order every time.
func (c Catalogue) sorted() []Package {
	out := make([]Package, 0, len(c))
	for _, p := range c {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Package) int { return strings.Compare(a.Provider, b.Provider) })
	return out
}

// Validate checks a ProviderInstance names a package of this catalogue
// for its capability. Packages are platform-wide, so no Environment.
func (c Catalogue) Validate(spec resource.ProviderInstanceSpec) error {
	p, ok := c[spec.ProviderID]
	switch {
	case !ok:
		return fmt.Errorf("%w: providerId: %q isn't a package this server offers", resource.ErrValidation, spec.ProviderID)
	case spec.CapabilityID != p.Capability:
		return fmt.Errorf("%w: capabilityId: package %s provides %s", resource.ErrValidation, p.Provider, p.Capability)
	case spec.EnvironmentID != "":
		return fmt.Errorf("%w: environmentId: packages are installed platform-wide", resource.ErrValidation)
	case p.Installer == nil:
		return fmt.Errorf("%w: providerId: package %s isn't available in this build yet", resource.ErrValidation, p.Provider)
	}
	return nil
}

// isCapability reports whether some package of the catalogue provides
// capability.
func (c Catalogue) isCapability(capability string) bool {
	for _, p := range c {
		if p.Capability == capability {
			return true
		}
	}
	return false
}

// installPoll is how soon an installing package is checked again.
const installPoll = 15 * time.Second

// installFailed is what people see when an install fails; the cause is
// in the server log (it can name platform internals).
const installFailed = "the install failed; the server log has the cause, and it's retried"

// ProviderInstanceReconciler installs the package each ProviderInstance
// names, writes its Ready condition, and reports its capability's
// readiness.
type ProviderInstanceReconciler struct {
	store     store.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus]
	catalogue Catalogue
	report    ReadinessFunc
}

func NewProviderInstanceReconciler(s store.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus], c Catalogue, report ReadinessFunc) *ProviderInstanceReconciler {
	return &ProviderInstanceReconciler{store: s, catalogue: c, report: report}
}

// ReportStart reports each optional capability at start-up, before any
// instance is reconciled: "starting" where an instance exists (so an
// installed package isn't shown as missing while the platform comes up),
// else not installed, or unavailable in this build.
func (r *ProviderInstanceReconciler) ReportStart(ctx context.Context) error {
	insts, err := r.store.List(ctx, store.ListOptions{})
	if err != nil {
		return err
	}
	reported := map[string]bool{}
	for _, p := range r.catalogue.sorted() {
		if reported[p.Capability] {
			continue
		}
		reported[p.Capability] = true
		switch {
		case slices.ContainsFunc(insts, func(i resource.ProviderInstance) bool { return i.Spec.CapabilityID == p.Capability }):
			r.report(p.Capability, false, ReasonInstalling, "starting")
		case p.Installer == nil:
			r.report(p.Capability, false, ReasonUnavailable, "not available in this build yet")
		default:
			r.report(p.Capability, false, ReasonNotInstalled, "install the "+p.Provider+" package to use it")
		}
	}
	return nil
}

// oldestFor is the first-created instance providing capability, besides
// except; the zero value when there's none.
func oldestFor(ctx context.Context, s store.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus], capability, except string) (resource.ProviderInstance, error) {
	insts, err := s.List(ctx, store.ListOptions{})
	if err != nil {
		return resource.ProviderInstance{}, err
	}
	var out resource.ProviderInstance
	for _, i := range insts {
		if i.Metadata.ID == except || i.Spec.CapabilityID != capability {
			continue
		}
		if out.Metadata.ID == "" || i.Metadata.CreatedAt.Before(out.Metadata.CreatedAt) ||
			(i.Metadata.CreatedAt.Equal(out.Metadata.CreatedAt) && i.Metadata.ID < out.Metadata.ID) {
			out = i
		}
	}
	return out, nil
}

func (r *ProviderInstanceReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	inst, err := r.store.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // removed: the approval's clean-up uninstalls it
	}
	if err != nil {
		return err
	}
	capability := inst.Spec.CapabilityID
	if err := r.catalogue.Validate(inst.Spec); err != nil {
		// Reported for the capability only when nothing else provides it:
		// an invalid record mustn't make an installed package look gone.
		if r.catalogue.isCapability(capability) {
			other, oerr := oldestFor(ctx, r.store, capability, inst.Metadata.ID)
			if oerr != nil {
				return oerr
			}
			if other.Metadata.ID == "" {
				r.report(capability, false, ReasonInvalidSpec, err.Error())
			}
		}
		return r.setReady(ctx, inst, resource.ConditionFalse, ReasonInvalidSpec, err.Error())
	}
	// One package per capability: should two exist (written past the
	// API, or created at the same moment, RP-033), only the oldest
	// installs, and this one doesn't touch the capability's readiness.
	first, err := oldestFor(ctx, r.store, capability, "")
	if err != nil {
		return err
	}
	if first.Metadata.ID != inst.Metadata.ID {
		return r.setReady(ctx, inst, resource.ConditionFalse, ReasonDuplicate,
			fmt.Sprintf("ProviderInstance %s already provides %s; remove this one", first.Metadata.ID, capability))
	}
	p := r.catalogue[inst.Spec.ProviderID]
	state, err := p.Installer.Install(ctx, inst)
	if errors.Is(err, ErrProviderNotReady) {
		r.report(capability, false, ReasonInstalling, "waiting for the platform")
		if err := r.setReady(ctx, inst, resource.ConditionFalse, ReasonProviderNotReady, "waiting for the platform"); err != nil {
			return err
		}
		return RequeueAfter{Delay: installPoll}
	}
	if err != nil {
		log.Printf("reconcile: install %s for ProviderInstance %s: %v", p.Provider, inst.Metadata.ID, err)
		r.report(capability, false, ReasonInstallFailed, installFailed)
		if serr := r.setReady(ctx, inst, resource.ConditionFalse, ReasonInstallFailed, installFailed); serr != nil {
			return serr
		}
		return err // retried with back-off
	}
	if !state.Ready {
		r.report(capability, false, ReasonInstalling, state.Message)
		if err := r.setReady(ctx, inst, resource.ConditionFalse, ReasonInstalling, state.Message); err != nil {
			return err
		}
		return RequeueAfter{Delay: installPoll}
	}
	r.report(capability, true, ReasonInstalled, state.Message)
	return r.setReady(ctx, inst, resource.ConditionTrue, ReasonInstalled, state.Message)
}

// setReady writes the Ready condition only when it changes. An install
// can take a while; if the instance was edited meanwhile, the write
// conflicts, and the edit's own reconcile writes the condition instead.
func (r *ProviderInstanceReconciler) setReady(ctx context.Context, inst resource.ProviderInstance, status resource.ConditionStatus, reason, message string) error {
	for _, c := range inst.Status.Conditions {
		if c.Type == "Ready" && c.Status == status && c.Reason == reason && c.Message == message {
			return nil
		}
	}
	ready := resource.Condition{Type: "Ready", Status: status, Reason: reason, Message: message, LastTransitionTime: time.Now()}
	inst.Status.Conditions = upsertCondition(inst.Status.Conditions, keepTransitionTime(inst.Status.Conditions, ready))
	_, err := r.store.Update(ctx, inst)
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// UninstallCleanup removes a removed ProviderInstance's package once its
// Approval is executed, and reports the capability not installed, unless
// another instance now provides it (re-created straight away, or a
// duplicate), whose own reconcile reports it. A damaged LastSpec can't
// strand it: Uninstall is per instance and idempotent, so every package
// is asked to remove what this instance might have installed.
func UninstallCleanup(s store.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus], c Catalogue, report ReadinessFunc) CleanupFunc {
	return func(ctx context.Context, ref resource.ApprovalRef) error {
		var spec resource.ProviderInstanceSpec
		if len(ref.LastSpec) == 0 || json.Unmarshal(ref.LastSpec, &spec) != nil {
			for _, p := range c.sorted() {
				if p.Installer != nil {
					if err := p.Installer.Uninstall(ctx, ref.ID); err != nil {
						return err
					}
				}
			}
			// Which capability it provided is unknown, so each one nothing
			// provides any more is reported not installed.
			for _, p := range c.sorted() {
				if p.Installer == nil {
					continue
				}
				other, err := oldestFor(ctx, s, p.Capability, ref.ID)
				if err != nil {
					return err
				}
				if other.Metadata.ID == "" {
					report(p.Capability, false, ReasonNotInstalled, "install the "+p.Provider+" package to use it")
				}
			}
			return nil
		}
		p, ok := c[spec.ProviderID]
		if !ok || p.Installer == nil {
			return nil // nothing this build installed
		}
		if err := p.Installer.Uninstall(ctx, ref.ID); err != nil {
			return err
		}
		other, err := oldestFor(ctx, s, p.Capability, ref.ID)
		if err != nil {
			return err
		}
		if other.Metadata.ID == "" {
			report(p.Capability, false, ReasonNotInstalled, "install the "+p.Provider+" package to use it")
		}
		return nil
	}
}
