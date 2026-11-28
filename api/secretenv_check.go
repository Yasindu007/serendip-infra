package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// secretEnvChecker checks the Secrets a Component's or a Deployment's
// secret variables name, where the API knows more than the resource's own
// validation (specs/026-secrets-into-components plan decisions 6 and 7):
//   - naming a Secret needs reading its value (403, audited), since the
//     platform reads it for the caller and puts it in a workload's
//     environment. The Azure analogue is an App Service Key Vault
//     reference, which resolves only with Key Vault Secrets User;
//   - the Secret exists (422);
//   - it sits in the Project or above it (422): a Secret from another
//     Project would let one Project's Components block another's deletes.
//
// Only newly named entries are checked: a new entry, or one whose Secret,
// variable or key changed (a key choice is a value choice). Unchanged and
// removed entries need nothing, so other fields stay editable.
//
// Existence and ancestry come from the workloadView the caller passes
// (spec 027), so a template's pending Application gives the Project.
type secretEnvChecker struct{}

// newlyNamed lists the entries of list that was doesn't have as they are.
func newlyNamed(list, was []resource.ComponentSecretEnv) []int {
	var out []int
	for i, e := range list {
		if !slices.Contains(was, e) {
			out = append(out, i)
		}
	}
	return out
}

// check checks list's newly named entries (field names the list in
// messages) against the Project the workload runs in, which project
// returns; it's only called when there is something to check. It returns
// the context with the value-read checks in its policy evidence.
func (s secretEnvChecker) check(ctx context.Context, v workloadView, field string, list, was []resource.ComponentSecretEnv, project func(context.Context) (string, error)) (context.Context, error) {
	fresh := newlyNamed(list, was)
	if len(fresh) == 0 {
		return ctx, nil
	}
	// Decide first, so a 422 never tells an unauthorized caller whether a
	// Secret exists or where it is (api/CLAUDE.md).
	var checked []string
	for _, i := range fresh {
		id := list[i].Secret
		if slices.Contains(checked, id) {
			continue
		}
		checked = append(checked, id)
		at := resource.Scope{Kind: resource.KindSecret, ID: id}
		var ok bool
		var err error
		ctx, ok, err = allowedNoted(ctx, secretValueRead, v.scope(at))
		if err != nil {
			return ctx, err
		}
		if !ok {
			return ctx, deployRefuse(http.StatusForbidden, "forbidden",
				"missing %s at Secret/%s: a secret variable puts the Secret's value in the workload's environment, so naming it needs reading that value", secretValueRead.Name, id)
		}
	}
	projectID, err := project(ctx)
	if err != nil {
		return ctx, err
	}
	checked = checked[:0]
	for _, i := range fresh {
		id := list[i].Secret
		if slices.Contains(checked, id) {
			continue
		}
		checked = append(checked, id)
		found, err := v.exists(ctx, resource.KindSecret, id)
		if err != nil {
			return ctx, err
		}
		if !found {
			return ctx, deployRefuse(http.StatusUnprocessableEntity, "unprocessable", "%s[%d].secret: Secret %s not found", field, i, id)
		}
		ok, err := s.inProject(ctx, v, id, projectID)
		if err != nil {
			return ctx, err
		}
		if !ok {
			return ctx, deployRefuse(http.StatusUnprocessableEntity, "unprocessable", "%s[%d].secret: the Secret must be in this Project or above it", field, i)
		}
	}
	return ctx, nil
}

// inProject says whether Secret id sits inside Project projectID, or at a
// scope on the Project's path (the root, an Organization or a Team above
// it): every owner the Secret has is then the Project or one of its
// ancestors.
func (s secretEnvChecker) inProject(ctx context.Context, v workloadView, id, projectID string) (bool, error) {
	project := graph.ResourceRef{Kind: resource.KindProject, ID: projectID}
	above, err := v.ancestors(ctx, graph.ResourceRef{Kind: resource.KindSecret, ID: id})
	if err != nil {
		return false, err
	}
	if slices.Contains(above, project) {
		return true, nil
	}
	path, err := v.ancestors(ctx, project)
	if err != nil {
		return false, err
	}
	path = append(path[:len(path):len(path)], project)
	for _, a := range above {
		if !slices.Contains(path, a) {
			return false, nil
		}
	}
	return true, nil
}
