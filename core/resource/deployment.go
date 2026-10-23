package resource

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// KindDeployment is one Component, running in one Environment
// (specs/020-openchoreo-console-features plan.md decision 2). It's owned by
// the Environment, so that Environment's scope governs who may deploy
// there, and it depends on the Component it runs. Deploying, promoting and
// rolling back are writes of a Deployment; undeploying is its delete, and
// so goes through an Approval (spec 019).
const KindDeployment Kind = "Deployment"

// DeploymentSpec is what should run in the owning Environment.
type DeploymentSpec struct {
	// Component is the ID of the Component to run.
	Component string `json:"component"`
	// Release is the release to run, as the platform names it. Empty means
	// the Component's latest release.
	Release string `json:"release,omitempty"`
	// Overrides are this Environment's own settings. Promoting a release
	// into another Environment never carries them over.
	Overrides DeploymentOverrides `json:"overrides,omitzero"`
}

// DeploymentOverrides is what differs in one Environment for the same
// release.
type DeploymentOverrides struct {
	// Env sets or replaces environment variables.
	Env map[string]string `json:"env,omitempty"`
	// Config is the per-environment configuration the Component's type
	// allows, such as replicas or resource limits. The platform's schema
	// for it decides what's valid.
	Config map[string]any `json:"config,omitempty"`
	// Traits holds per-environment settings of the Component's traits, by
	// trait instance name, each validated against its trait's schema.
	Traits map[string]map[string]any `json:"traits,omitempty"`
}

// DeploymentStatus is what the platform reports for the Deployment.
type DeploymentStatus struct {
	Status
	// Release is the release actually running.
	Release   string                    `json:"release,omitempty"`
	Endpoints []ComponentEndpointStatus `json:"endpoints,omitempty"`
	// History is the releases this Deployment ran before, newest first, at
	// most MaxDeploymentHistory: what it may roll back to.
	History []string `json:"history,omitempty"`
}

// MaxDeploymentHistory bounds DeploymentStatus.History.
const MaxDeploymentHistory = 10

// RecordRelease returns history with previous added in front (when it's a
// real change), deduplicated and bounded.
func RecordRelease(history []string, previous, current string) []string {
	if previous == "" || previous == current {
		return history
	}
	out := []string{previous}
	for _, r := range history {
		if r != previous && r != current && len(out) < MaxDeploymentHistory {
			out = append(out, r)
		}
	}
	return out
}

// ErrPipeline is a release that may not run in an Environment: it hasn't
// come along the Project's pipeline.
var ErrPipeline = fmt.Errorf("%w: pipeline", ErrValidation)

// AdmitRelease decides whether release may run in env, for one Component,
// under the Project's pipeline (plan 020 decision 2; the platform doesn't
// enforce it). running is the release each Environment runs now (by
// Environment ID); history is what env itself ran before.
//   - Without a pipeline, anything goes.
//   - In the first Environment, anything goes: releases enter there.
//   - Elsewhere, "latest" (an empty release) is refused, and a release must
//     run now in an Environment that promotes to env, or be one env ran
//     before (a rollback).
func AdmitRelease(p Pipeline, env, release string, running map[string]string, history []string) error {
	if len(p.Paths) == 0 {
		return nil
	}
	if !slices.Contains(p.Environments(), env) {
		return fmt.Errorf("%w: this Environment isn't in the Project's pipeline", ErrPipeline)
	}
	if env == p.First() {
		return nil
	}
	if release == "" {
		return fmt.Errorf("%w: a new release enters at the pipeline's first Environment (%s); name the release to promote", ErrPipeline, p.First())
	}
	if slices.Contains(history, release) {
		return nil
	}
	var from []string
	for _, path := range p.Paths {
		if slices.Contains(path.To, env) {
			from = append(from, path.From)
			if running[path.From] == release {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: release %s isn't running in an Environment that promotes here (%s), and this Environment hasn't run it before", ErrPipeline, release, strings.Join(from, ", "))
}

type Deployment = Resource[DeploymentSpec, DeploymentStatus]

// DeploymentRelationships is the depends-on edge a Deployment declares to
// its Component, derived from its spec so the graph's dependents and blast
// radius include it.
func DeploymentRelationships(spec DeploymentSpec) []Relationship {
	return []Relationship{{Type: EdgeDependsOn, TargetKind: KindComponent, TargetID: spec.Component}}
}

// Limits on a Deployment's overrides.
const (
	maxReleaseNameLen  = 253
	maxEnvVars         = 128
	maxEnvValueLen     = 32 << 10
	maxOverridesJSON   = 64 << 10
	maxTraitInstanceLn = 63
)

var (
	// releaseNameRE is a DNS-subdomain-style name, which is how platforms
	// name releases.
	releaseNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	envNameRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dnsLabelRE    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// ValidateDeploymentSpec checks a Deployment's own fields. Whether the
// Component exists, may run in the Environment, and whether Config and
// Traits fit their schemas is decided where those are known.
func ValidateDeploymentSpec(s DeploymentSpec) error {
	if s.Component == "" || !idRE.MatchString(s.Component) || s.Component == "." || s.Component == ".." {
		return fmt.Errorf("%w: component: a Component ID is required", ErrValidation)
	}
	if s.Release != "" && (len(s.Release) > maxReleaseNameLen || !releaseNameRE.MatchString(s.Release)) {
		return fmt.Errorf("%w: release %q: lower-case letters, digits, '-' and '.', at most %d", ErrValidation, s.Release, maxReleaseNameLen)
	}
	o := s.Overrides
	if len(o.Env) > maxEnvVars {
		return fmt.Errorf("%w: overrides.env: at most %d variables", ErrValidation, maxEnvVars)
	}
	for k, v := range o.Env {
		if !envNameRE.MatchString(k) {
			return fmt.Errorf("%w: overrides.env: %q isn't a variable name", ErrValidation, k)
		}
		if len(v) > maxEnvValueLen {
			return fmt.Errorf("%w: overrides.env.%s: at most %d bytes", ErrValidation, k, maxEnvValueLen)
		}
	}
	for name := range o.Traits {
		if len(name) > maxTraitInstanceLn || !dnsLabelRE.MatchString(name) {
			return fmt.Errorf("%w: overrides.traits: %q isn't a trait instance name", ErrValidation, name)
		}
	}
	if b, err := json.Marshal(o); err != nil || len(b) > maxOverridesJSON {
		return fmt.Errorf("%w: overrides: at most %d bytes of JSON", ErrValidation, maxOverridesJSON)
	}
	return nil
}

// Pipeline is the order a Project's releases move through its
// Environments: each path promotes from one Environment to others. A
// Project without one has a single stage. The platform does no such check,
// so Serendip enforces it (plan.md decision 2).
type Pipeline struct {
	Paths []PromotionPath `json:"paths,omitempty"`
}

// PromotionPath allows promoting from one Environment to each of To, by
// Environment ID.
type PromotionPath struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}

// Environments lists every Environment the pipeline names, in first-seen
// order.
func (p Pipeline) Environments() []string {
	var out []string
	add := func(id string) {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, path := range p.Paths {
		add(path.From)
		for _, t := range path.To {
			add(t)
		}
	}
	return out
}

// First is the Environment a new release enters, the one no path promotes
// into; "" for an empty pipeline. ValidatePipeline ensures there's exactly
// one.
func (p Pipeline) First() string {
	for _, id := range p.Environments() {
		if !p.targeted(id) {
			return id
		}
	}
	return ""
}

func (p Pipeline) targeted(id string) bool {
	for _, path := range p.Paths {
		if slices.Contains(path.To, id) {
			return true
		}
	}
	return false
}

// Allows reports whether a release may be promoted from one Environment
// directly to another.
func (p Pipeline) Allows(from, to string) bool {
	for _, path := range p.Paths {
		if path.From == from && slices.Contains(path.To, to) {
			return true
		}
	}
	return false
}

// ValidatePipeline checks a pipeline's shape: valid IDs, no path from an
// Environment to itself, no duplicates, one first Environment, and no
// cycles. Whether the IDs are this Project's Environments is checked where
// the Project's children are known.
func ValidatePipeline(p Pipeline) error {
	if len(p.Paths) == 0 {
		return nil
	}
	froms := map[string]bool{}
	for i, path := range p.Paths {
		if !validRefID(path.From) {
			return fmt.Errorf("%w: pipeline.paths[%d].from: an Environment ID is required", ErrValidation, i)
		}
		if froms[path.From] {
			return fmt.Errorf("%w: pipeline: %s has more than one path; list every target in one", ErrValidation, path.From)
		}
		froms[path.From] = true
		if len(path.To) == 0 {
			return fmt.Errorf("%w: pipeline.paths[%d].to: at least one Environment", ErrValidation, i)
		}
		seen := map[string]bool{}
		for _, t := range path.To {
			switch {
			case !validRefID(t):
				return fmt.Errorf("%w: pipeline.paths[%d].to: %q isn't an Environment ID", ErrValidation, i, t)
			case t == path.From:
				return fmt.Errorf("%w: pipeline: %s promotes to itself", ErrValidation, t)
			case seen[t]:
				return fmt.Errorf("%w: pipeline.paths[%d].to: %s is listed twice", ErrValidation, i, t)
			}
			seen[t] = true
		}
	}
	var firsts []string
	for _, id := range p.Environments() {
		if !p.targeted(id) {
			firsts = append(firsts, id)
		}
	}
	if len(firsts) != 1 {
		return fmt.Errorf("%w: pipeline: exactly one Environment must start it (no path into it), found %d", ErrValidation, len(firsts))
	}
	// Every Environment is reachable from the first, or something loops.
	reached := map[string]bool{firsts[0]: true}
	queue := []string{firsts[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, path := range p.Paths {
			if path.From != cur {
				continue
			}
			for _, t := range path.To {
				if !reached[t] {
					reached[t] = true
					queue = append(queue, t)
				}
			}
		}
	}
	if len(reached) != len(p.Environments()) {
		return fmt.Errorf("%w: pipeline: some Environments can't be reached from %s (a cycle)", ErrValidation, firsts[0])
	}
	// A path back to an Environment already passed is a cycle too.
	if cyclic(p, firsts[0]) {
		return fmt.Errorf("%w: pipeline: promotion paths form a cycle", ErrValidation)
	}
	return nil
}

func cyclic(p Pipeline, start string) bool {
	const (
		unseen = iota
		onPath
		done
	)
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		state[id] = onPath
		for _, path := range p.Paths {
			if path.From != id {
				continue
			}
			for _, t := range path.To {
				switch state[t] {
				case onPath:
					return true
				case unseen:
					if visit(t) {
						return true
					}
				}
			}
		}
		state[id] = done
		return false
	}
	return visit(start)
}

func validRefID(id string) bool {
	return id != "" && idRE.MatchString(id) && id != "." && id != ".."
}
