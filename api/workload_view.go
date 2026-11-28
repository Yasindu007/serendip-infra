package api

import (
	"context"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// workloadView is what the Component and Deployment admission checks
// read (specs/027-templates-and-scaffolder plan decision 7): the
// Components, Applications and Deployments they're checked against, the
// existence and ancestry of what they name, and where a resource's
// permissions are decided. The handlers pass storedView; a template's
// create passes a pendingView, so its resources can name each other
// before any of them is stored.
type workloadView interface {
	component(ctx context.Context, id string) (resource.Component, error)
	application(ctx context.Context, id string) (resource.Application, error)
	// deployments lists every Deployment.
	deployments(ctx context.Context) ([]resource.Deployment, error)
	exists(ctx context.Context, kind resource.Kind, id string) (bool, error)
	// ancestors is graph.Ancestors: ref's owners at any depth, nearest
	// first.
	ancestors(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
	// scope is where a permission at "at" is decided: at itself, or for a
	// resource not stored yet, its nearest stored owner, whose assignments
	// it will inherit once stored.
	scope(at resource.Scope) resource.Scope
}

// storedView answers from the stores.
type storedView struct {
	comps       store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps        store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	deploys     store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	existsIn    func(ctx context.Context, kind resource.Kind, id string) (bool, error)
	ancestorsOf func(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
}

func (v storedView) component(ctx context.Context, id string) (resource.Component, error) {
	return v.comps.Get(ctx, id)
}

func (v storedView) application(ctx context.Context, id string) (resource.Application, error) {
	return v.apps.Get(ctx, id)
}

func (v storedView) deployments(ctx context.Context) ([]resource.Deployment, error) {
	return v.deploys.List(ctx, store.ListOptions{})
}

func (v storedView) exists(ctx context.Context, kind resource.Kind, id string) (bool, error) {
	return v.existsIn(ctx, kind, id)
}

func (v storedView) ancestors(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error) {
	return v.ancestorsOf(ctx, ref)
}

func (storedView) scope(at resource.Scope) resource.Scope { return at }

// pendingView answers from a batch not stored yet (a template's
// resources, with their IDs assigned), then from base. Resources are added
// in admission order, each after its own checks pass, so a check sees
// only what's been admitted before it.
type pendingView struct {
	base    workloadView
	apps    map[string]resource.Application
	comps   map[string]resource.Component
	deploys []resource.Deployment
}

func newPendingView(base workloadView) *pendingView {
	return &pendingView{base: base, apps: map[string]resource.Application{}, comps: map[string]resource.Component{}}
}

func (v *pendingView) addApplication(a resource.Application) { v.apps[a.Metadata.ID] = a }
func (v *pendingView) addComponent(c resource.Component)     { v.comps[c.Metadata.ID] = c }
func (v *pendingView) addDeployment(d resource.Deployment)   { v.deploys = append(v.deploys, d) }

func (v *pendingView) component(ctx context.Context, id string) (resource.Component, error) {
	if c, ok := v.comps[id]; ok {
		return c, nil
	}
	return v.base.component(ctx, id)
}

func (v *pendingView) application(ctx context.Context, id string) (resource.Application, error) {
	if a, ok := v.apps[id]; ok {
		return a, nil
	}
	return v.base.application(ctx, id)
}

func (v *pendingView) deployments(ctx context.Context) ([]resource.Deployment, error) {
	stored, err := v.base.deployments(ctx)
	if err != nil {
		return nil, err
	}
	return append(stored[:len(stored):len(stored)], v.deploys...), nil
}

// owners is a pending resource's owner refs, and whether it's pending.
func (v *pendingView) owners(kind resource.Kind, id string) ([]resource.OwnerRef, bool) {
	switch kind {
	case resource.KindApplication:
		if a, ok := v.apps[id]; ok {
			return a.Metadata.OwnerRefs, true
		}
	case resource.KindComponent:
		if c, ok := v.comps[id]; ok {
			return c.Metadata.OwnerRefs, true
		}
	case resource.KindDeployment:
		for _, d := range v.deploys {
			if d.Metadata.ID == id {
				return d.Metadata.OwnerRefs, true
			}
		}
	}
	return nil, false
}

func (v *pendingView) exists(ctx context.Context, kind resource.Kind, id string) (bool, error) {
	if _, ok := v.owners(kind, id); ok {
		return true, nil
	}
	return v.base.exists(ctx, kind, id)
}

// ancestors walks a pending resource's owners breadth-first, as
// graph.Ancestors does, continuing from each stored owner with base.
func (v *pendingView) ancestors(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error) {
	if _, ok := v.owners(ref.Kind, ref.ID); !ok {
		return v.base.ancestors(ctx, ref)
	}
	visited := map[graph.ResourceRef]bool{ref: true}
	var result, stored []graph.ResourceRef
	frontier := []graph.ResourceRef{ref}
	for len(frontier) > 0 {
		var next []graph.ResourceRef
		for _, node := range frontier {
			refs, _ := v.owners(node.Kind, node.ID)
			for _, o := range refs {
				or := graph.ResourceRef{Kind: o.Kind, ID: o.ID}
				if visited[or] {
					continue
				}
				visited[or] = true
				result = append(result, or)
				if _, pending := v.owners(or.Kind, or.ID); pending {
					next = append(next, or)
				} else {
					stored = append(stored, or)
				}
			}
		}
		frontier = next
	}
	for _, s := range stored {
		above, err := v.base.ancestors(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, a := range above {
			if !visited[a] {
				visited[a] = true
				result = append(result, a)
			}
		}
	}
	return result, nil
}

// scope follows a pending resource's first owner until a stored one. A
// pending resource with no owner stays as it is, so only the root's
// assignments reach it (closed, not open).
func (v *pendingView) scope(at resource.Scope) resource.Scope {
	seen := map[resource.Scope]bool{}
	for !seen[at] {
		seen[at] = true
		refs, pending := v.owners(at.Kind, at.ID)
		if !pending || len(refs) == 0 {
			return at
		}
		at = resource.Scope{Kind: refs[0].Kind, ID: refs[0].ID}
	}
	return at
}
