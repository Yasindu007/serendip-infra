package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Provider implements reconcile.ApplicationPlatformProvider on
// OpenChoreo: a Serendip Application becomes a Project plus its one
// ProjectReleaseBinding; a Component becomes a Component (autoDeploy) plus
// its Workload. OpenChoreo's own controllers create the ComponentRelease
// and ReleaseBinding, and render the real Deployment into the Project's
// data-plane cell namespace. Everything lives in ControlPlaneNamespace,
// labeled as the org namespace by EnsurePlatformDefaults.
type Provider struct {
	client  *kubernetes.Client
	gateway reconcile.GatewayEndpoint
}

// NewProvider returns a Provider whose external routes attach to gateway.
func NewProvider(client *kubernetes.Client, gateway reconcile.GatewayEndpoint) *Provider {
	return &Provider{client: client, gateway: gateway}
}

var _ reconcile.ApplicationPlatformProvider = (*Provider)(nil)

// endpointFor maps a Component port to a Workload endpoint. Exposed ports
// become HTTP endpoints with external visibility, which is what makes the
// component type render a route on the shared gateway. Everything else
// stays TCP with the default cluster-internal visibility: TCP is the honest
// generic choice when nothing routes by HTTP.
func endpointFor(port resource.ComponentPort) workloadEndpoint {
	if port.Expose {
		return workloadEndpoint{Type: "HTTP", Port: port.Port, Visibility: []string{"external"}}
	}
	return workloadEndpoint{Type: "TCP", Port: port.Port}
}

type ownerRef struct {
	ProjectName   string `json:"projectName"`
	ComponentName string `json:"componentName,omitempty"`
}

type envVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type workloadEndpoint struct {
	Type       string   `json:"type"`
	Port       int32    `json:"port"`
	Visibility []string `json:"visibility,omitempty"`
}

type workloadContainer struct {
	Image string   `json:"image"`
	Env   []envVar `json:"env,omitempty"`
}

// workloadSpec models only the Workload fields this provider owns.
type workloadSpec struct {
	Owner     ownerRef                    `json:"owner"`
	Container workloadContainer           `json:"container"`
	Endpoints map[string]workloadEndpoint `json:"endpoints,omitempty"`
}

type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type objectStatus struct {
	Conditions []condition `json:"conditions"`
}

func (p *Provider) EnsureApplication(ctx context.Context, app resource.Application) (bool, string, string, error) {
	project := projectName(app.Metadata.ID)
	labels := serendipLabels(app.Metadata.ID, app.Metadata.Name)

	if _, err := apply(ctx, p.client, namespacedPath("projects"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Project",
		Metadata: kubernetes.ObjectMeta{Name: project, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"deploymentPipelineRef": map[string]string{"name": deploymentPipeline},
			"type":                  map[string]string{"kind": "ClusterProjectType", "name": projectTypeName},
		}),
	}); err != nil {
		return false, "", "", err
	}

	binding, err := apply(ctx, p.client, namespacedPath("projectreleasebindings"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "ProjectReleaseBinding",
		Metadata: kubernetes.ObjectMeta{Name: projectReleaseBindingName(app.Metadata.ID), Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"owner":       ownerRef{ProjectName: project},
			"environment": environmentName,
		}),
	})
	if err != nil {
		return false, "", "", err
	}
	ready, reason, message := mapReady(binding)
	return ready, reason, message, nil
}

func (p *Provider) DeleteApplication(ctx context.Context, app resource.Application) error {
	// The Project's own cleanup finalizer removes its Components,
	// ProjectReleaseBindings, and data-plane cell namespaces.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("projects")+"/"+projectName(app.Metadata.ID))
}

func (p *Provider) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (reconcile.ComponentObservation, error) {
	w, err := p.ensureWorkloadReady(ctx, app, comp)
	if err != nil {
		return reconcile.ComponentObservation{}, err
	}
	obs := reconcile.ComponentObservation{Ready: w.ready, Reason: w.reason, Message: w.message}

	exposed := exposedPorts(comp.Spec)
	if len(exposed) == 0 {
		return obs, nil
	}
	if !w.ready {
		obs.Reachable = &reconcile.Reachability{Reason: reconcile.ReasonPending, Message: "workload is not deployed"}
		return obs, nil
	}
	reach, err := p.observeReachability(ctx, app, comp, w.binding, exposed)
	if err != nil {
		// A failed read about routes says nothing about the workload: keep
		// its Ready result and report reachability as unknown-yet.
		reach = reconcile.Reachability{Reason: reconcile.ReasonPending, Message: "could not observe routes: " + err.Error()}
	}
	obs.Reachable = &reach
	return obs, nil
}

// workloadState is the workload half of an observation, plus the
// ReleaseBinding it was read from (zero when none exists yet).
type workloadState struct {
	ready   bool
	reason  string
	message string
	binding kubernetes.Object
}

func progressing(message string) workloadState {
	return workloadState{reason: reconcile.ReasonProgressing, message: message}
}

func (p *Provider) ensureWorkloadReady(ctx context.Context, app resource.Application, comp resource.Component) (workloadState, error) {
	if len(comp.Spec.Ports) == 0 {
		return workloadState{reason: reconcile.ReasonUnsupported,
			message: "components without ports are not supported by this provider yet"}, nil
	}

	project := projectName(app.Metadata.ID)
	name := componentName(comp.Metadata.ID)
	labels := serendipLabels(comp.Metadata.ID, comp.Metadata.Name)

	// owner is immutable upstream, so check it before applying: moving a
	// Component to another Application would need delete-and-recreate, and
	// an apply would just be rejected.
	existing, err := p.client.Get(ctx, namespacedPath("components")+"/"+name)
	switch {
	case errors.Is(err, kubernetes.ErrNotFound):
	case err != nil:
		return workloadState{}, fmt.Errorf("get Component %s: %w", name, err)
	default:
		var compSpec struct {
			Owner ownerRef `json:"owner"`
		}
		if len(existing.Spec) == 0 {
			// Nothing to compare yet; the apply below sets the owner.
		} else if err := json.Unmarshal(existing.Spec, &compSpec); err != nil {
			return workloadState{}, fmt.Errorf("decode Component %s spec: %w", name, err)
		}
		if compSpec.Owner.ProjectName != "" && compSpec.Owner.ProjectName != project {
			return workloadState{reason: reconcile.ReasonUnsupported,
				message: "moving a component to a different application is not supported; delete it and create a new component under the target application"}, nil
		}
	}
	if _, err := apply(ctx, p.client, namespacedPath("components"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Component",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"owner":         ownerRef{ProjectName: project},
			"autoDeploy":    true,
			"componentType": map[string]string{"kind": "ClusterComponentType", "name": componentTypeWorkload + "/" + componentTypeName},
		}),
	}); err != nil {
		return workloadState{}, err
	}

	patched, err := p.ensureWorkload(ctx, project, name, labels, comp.Spec)
	if err != nil {
		return workloadState{}, err
	}
	if patched {
		// The ReleaseBinding still describes the previous release at this
		// point; reading it now would report the old version as Deployed.
		return progressing("rolling out updated workload"), nil
	}

	binding, err := p.client.Get(ctx, namespacedPath("releasebindings")+"/"+releaseBindingName(comp.Metadata.ID))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return progressing("waiting for the platform to create the release binding"), nil
	}
	if err != nil {
		return workloadState{}, fmt.Errorf("get ReleaseBinding %s: %w", releaseBindingName(comp.Metadata.ID), err)
	}

	current, err := p.bindingReleasesDesired(ctx, binding, desiredWorkload(project, name, comp.Spec))
	if err != nil {
		return workloadState{}, err
	}
	if !current {
		return progressing("waiting for a release of the current spec to be bound"), nil
	}
	ready, reason, message := mapReady(binding)
	return workloadState{ready: ready, reason: reason, message: message, binding: binding}, nil
}

func exposedPorts(spec resource.ComponentSpec) []string {
	var names []string
	for _, p := range spec.Ports {
		if p.Expose {
			names = append(names, p.Name)
		}
	}
	return names
}

type routeParentStatus struct {
	ParentRef struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"parentRef"`
	Conditions []condition `json:"conditions"`
}

// observeReachability reports whether every exposed port has a route that
// the shared gateway accepted, and the platform has published an invoke URL
// for it. URLs come from the platform's own report, never assembled here,
// so status shows exactly the address the route really matches.
func (p *Provider) observeReachability(ctx context.Context, app resource.Application, comp resource.Component, binding kubernetes.Object, exposed []string) (reconcile.Reachability, error) {
	pending := func(format string, args ...any) reconcile.Reachability {
		return reconcile.Reachability{Reason: reconcile.ReasonPending, Message: fmt.Sprintf(format, args...)}
	}

	prb, err := p.client.Get(ctx, namespacedPath("projectreleasebindings")+"/"+projectReleaseBindingName(app.Metadata.ID))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return pending("application binding not found"), nil
	}
	if err != nil {
		return reconcile.Reachability{}, fmt.Errorf("get ProjectReleaseBinding %s: %w", projectReleaseBindingName(app.Metadata.ID), err)
	}
	var prbStatus struct {
		Namespace string `json:"namespace"`
	}
	if len(prb.Status) > 0 {
		if err := json.Unmarshal(prb.Status, &prbStatus); err != nil {
			return reconcile.Reachability{}, fmt.Errorf("decode ProjectReleaseBinding status: %w", err)
		}
	}
	if prbStatus.Namespace == "" {
		return pending("data-plane namespace not reported yet"), nil
	}

	name := componentName(comp.Metadata.ID)
	selector := url.QueryEscape("openchoreo.dev/component=" + name + ",openchoreo.dev/endpoint-visibility=external")
	routes, err := p.client.List(ctx, "/apis/gateway.networking.k8s.io/v1/namespaces/"+url.PathEscape(prbStatus.Namespace)+"/httproutes?labelSelector="+selector)
	if err != nil {
		return reconcile.Reachability{}, fmt.Errorf("list HTTPRoutes for %s: %w", name, err)
	}
	byEndpoint := make(map[string]kubernetes.Object, len(routes))
	for _, r := range routes {
		byEndpoint[r.Metadata.Labels["openchoreo.dev/endpoint-name"]] = r
	}

	var bindingStatus struct {
		Endpoints []struct {
			Name         string `json:"name"`
			InvokeURL    string `json:"invokeURL"`
			ExternalURLs struct {
				HTTP *endpointURL `json:"http"`
			} `json:"externalURLs"`
		} `json:"endpoints"`
	}
	if len(binding.Status) > 0 {
		if err := json.Unmarshal(binding.Status, &bindingStatus); err != nil {
			return reconcile.Reachability{}, fmt.Errorf("decode ReleaseBinding status: %w", err)
		}
	}
	urls := make(map[string]string, len(bindingStatus.Endpoints))
	for _, e := range bindingStatus.Endpoints {
		switch {
		case e.InvokeURL != "":
			urls[e.Name] = e.InvokeURL
		case e.ExternalURLs.HTTP != nil:
			urls[e.Name] = e.ExternalURLs.HTTP.String()
		}
	}

	var endpoints []resource.ComponentEndpointStatus
	for _, port := range exposed {
		route, ok := byEndpoint[port]
		if !ok {
			return pending("route for port %q not rendered yet", port), nil
		}
		parent, ok, err := p.gatewayParent(route)
		if err != nil {
			return reconcile.Reachability{}, err
		}
		if !ok {
			return pending("route for port %q not yet attached to the gateway", port), nil
		}
		for _, typ := range []string{"Accepted", "ResolvedRefs"} {
			c := findCondition(parent.Conditions, typ)
			stale := c != nil && c.ObservedGeneration > 0 && c.ObservedGeneration < route.Metadata.Generation
			if c == nil || c.Status == "Unknown" || stale {
				return pending("route for port %q: %s not reported yet", port, typ), nil
			}
			if c.Status != "True" {
				return reconcile.Reachability{Reason: reconcile.ReasonRouteRejected,
					Message: fmt.Sprintf("route for port %q: %s: %s", port, c.Reason, c.Message)}, nil
			}
		}
		u := urls[port]
		if u == "" {
			return pending("waiting for the external URL of port %q", port), nil
		}
		endpoints = append(endpoints, resource.ComponentEndpointStatus{Name: port, URL: u})
	}
	return reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: endpoints}, nil
}

// endpointURL is the platform's structured report of one endpoint
// address. OpenChoreo 1.3.1 fills externalURLs rather than invokeURL for
// HTTP endpoints, so the URL is assembled from these reported parts.
type endpointURL struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int32  `json:"port"`
	Path   string `json:"path"`
}

func (u endpointURL) String() string {
	if u.Host == "" {
		return ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	host := u.Host
	if u.Port != 0 && !(scheme == "http" && u.Port == 80) && !(scheme == "https" && u.Port == 443) {
		host = net.JoinHostPort(u.Host, strconv.Itoa(int(u.Port)))
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: u.Path}).String()
}

// gatewayParent finds the route's status entry for the shared gateway. An
// empty parentRef namespace means the route's own namespace, which is never
// the gateway's here, so only an explicit match counts.
func (p *Provider) gatewayParent(route kubernetes.Object) (routeParentStatus, bool, error) {
	var st struct {
		Parents []routeParentStatus `json:"parents"`
	}
	if len(route.Status) > 0 {
		if err := json.Unmarshal(route.Status, &st); err != nil {
			return routeParentStatus{}, false, fmt.Errorf("decode HTTPRoute %s status: %w", route.Metadata.Name, err)
		}
	}
	for _, ps := range st.Parents {
		if ps.ParentRef.Name == p.gateway.Name && ps.ParentRef.Namespace == p.gateway.Namespace {
			return ps, true, nil
		}
	}
	return routeParentStatus{}, false, nil
}

func findCondition(conds []condition, typ string) *condition {
	for i := range conds {
		if conds[i].Type == typ {
			return &conds[i]
		}
	}
	return nil
}

// bindingReleasesDesired reports whether the ComponentRelease the binding
// points at was cut from the desired workload. Right after a Workload
// change the binding can still be Ready for the previous release; the
// Component's own reconcile may run again before the platform reacts, so
// "did I just patch" alone isn't enough to avoid reporting the old
// version as Deployed.
func (p *Provider) bindingReleasesDesired(ctx context.Context, binding kubernetes.Object, desired workloadSpec) (bool, error) {
	if len(binding.Spec) == 0 {
		return false, nil
	}
	var bindingSpec struct {
		ReleaseName string `json:"releaseName"`
	}
	if err := json.Unmarshal(binding.Spec, &bindingSpec); err != nil {
		return false, fmt.Errorf("decode ReleaseBinding %s spec: %w", binding.Metadata.Name, err)
	}
	if bindingSpec.ReleaseName == "" {
		return false, nil
	}
	release, err := p.client.Get(ctx, namespacedPath("componentreleases")+"/"+bindingSpec.ReleaseName)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get ComponentRelease %s: %w", bindingSpec.ReleaseName, err)
	}
	var releaseSpec struct {
		Workload workloadSpec `json:"workload"`
	}
	if err := json.Unmarshal(release.Spec, &releaseSpec); err != nil {
		return false, fmt.Errorf("decode ComponentRelease %s spec: %w", bindingSpec.ReleaseName, err)
	}
	return workloadsEqual(releaseSpec.Workload, desired), nil
}

func (p *Provider) DeleteComponent(ctx context.Context, comp resource.Component) error {
	// The Component's own cleanup finalizer removes its Workload, releases,
	// and live data-plane resources.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("components")+"/"+componentName(comp.Metadata.ID))
}

// ensureWorkload server-side-applies the Workload and reports whether an
// existing Workload's spec just changed. That is read off metadata.generation
// before and after: the API server bumps it only for a real spec change, and
// a no-drift apply stores nothing. A fresh create is not a change -- there
// is no previous release to mistake for the current one.
func (p *Provider) ensureWorkload(ctx context.Context, project, name string, labels map[string]string, spec resource.ComponentSpec) (bool, error) {
	before, err := p.client.Get(ctx, namespacedPath("workloads")+"/"+name)
	existed := err == nil
	if err != nil && !errors.Is(err, kubernetes.ErrNotFound) {
		return false, fmt.Errorf("get Workload %s: %w", name, err)
	}
	after, err := apply(ctx, p.client, namespacedPath("workloads"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Workload",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec:     mustJSON(desiredWorkload(project, name, spec)),
	})
	if err != nil {
		return false, err
	}
	return existed && after.Metadata.Generation != before.Metadata.Generation, nil
}

func desiredWorkload(project, name string, spec resource.ComponentSpec) workloadSpec {
	w := workloadSpec{
		Owner:     ownerRef{ProjectName: project, ComponentName: name},
		Container: workloadContainer{Image: spec.Image},
		Endpoints: make(map[string]workloadEndpoint, len(spec.Ports)),
	}
	for _, port := range spec.Ports {
		w.Endpoints[port.Name] = endpointFor(port)
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w.Container.Env = append(w.Container.Env, envVar{Key: k, Value: spec.Env[k]})
	}
	return w
}

// workloadsEqual compares the fields this provider sets, for telling whether
// a ComponentRelease was cut from the desired workload (not for drift
// detection on the Workload itself -- server-side apply does that).
func workloadsEqual(a, b workloadSpec) bool {
	return a.Container.Image == b.Container.Image &&
		envEqual(a.Container.Env, b.Container.Env) &&
		endpointsEqual(a.Endpoints, b.Endpoints)
}

func envEqual(a, b []envVar) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	am := make(map[string]string, len(a))
	for _, e := range a {
		am[e.Key] = e.Value
	}
	bm := make(map[string]string, len(b))
	for _, e := range b {
		bm[e.Key] = e.Value
	}
	return reflect.DeepEqual(am, bm)
}

func endpointsEqual(a, b map[string]workloadEndpoint) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// failureReasons are upstream Ready-condition reasons (ReleaseBinding and
// ProjectReleaseBinding share most of their vocabulary) that indicate the
// deployment won't converge on its own. Anything else not-ready is
// reported as progressing.
var failureReasons = map[string]bool{
	"ResourceApplyFailed":         true,
	"RenderingFailed":             true,
	"ResourcesDegraded":           true,
	"NamespaceDegraded":           true,
	"EnvironmentNotFound":         true,
	"DataPlaneNotFound":           true,
	"DataPlaneNotConfigured":      true,
	"ProjectNotFound":             true,
	"ComponentNotFound":           true,
	"InvalidReleaseConfiguration": true,
	"ReleaseOwnershipConflict":    true,
	"ReleaseUpdateFailed":         true,
	"JobFailed":                   true,
}

// mapReady translates an upstream object's Ready condition into the
// provider-neutral reasons core/reconcile expects, carrying the upstream
// reason and message in the returned message. A Ready condition computed
// for an older generation of obj's spec says nothing about the current
// one, so it counts as progressing.
func mapReady(obj kubernetes.Object) (bool, string, string) {
	var st objectStatus
	if len(obj.Status) > 0 {
		if err := json.Unmarshal(obj.Status, &st); err != nil {
			return false, reconcile.ReasonProgressing, "status not parseable: " + err.Error()
		}
	}
	for _, c := range st.Conditions {
		if c.Type != "Ready" {
			continue
		}
		detail := c.Reason
		if c.Message != "" {
			detail += ": " + c.Message
		}
		switch {
		case c.ObservedGeneration > 0 && c.ObservedGeneration < obj.Metadata.Generation:
			return false, reconcile.ReasonProgressing, "status not yet observed for latest spec (" + detail + ")"
		case c.Status == "True":
			return true, reconcile.ReasonDeployed, detail
		case failureReasons[c.Reason]:
			return false, reconcile.ReasonDeployFailed, detail
		default:
			return false, reconcile.ReasonProgressing, detail
		}
	}
	return false, reconcile.ReasonProgressing, "no Ready condition reported yet"
}

func (p *Provider) deleteIgnoringNotFound(ctx context.Context, path string) error {
	err := p.client.Delete(ctx, path)
	if err == nil || errors.Is(err, kubernetes.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("delete %s: %w", path, err)
}
