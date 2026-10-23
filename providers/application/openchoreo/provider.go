package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Provider implements reconcile.ApplicationPlatformProvider on
// OpenChoreo: a Serendip Application becomes a Project; a Component becomes
// a Component (auto-deploy off) plus its Workload and a ComponentRelease
// per spec. Since spec 020 (ADR 0019) it also implements the Deployment
// side: each Deployment becomes a ReleaseBinding, plus the Application's
// ProjectReleaseBinding for that Environment, and OpenChoreo renders the
// real Deployment into that (project, environment) data-plane namespace. Everything lives in ControlPlaneNamespace,
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
	Owner        ownerRef                    `json:"owner"`
	Container    workloadContainer           `json:"container"`
	Endpoints    map[string]workloadEndpoint `json:"endpoints,omitempty"`
	Dependencies *workloadDependencies       `json:"dependencies,omitempty"`
}

// workloadDependencies are the endpoints a workload calls (spike finding
// 8): the platform resolves each into an address in an environment
// variable, and lets the traffic through.
type workloadDependencies struct {
	Endpoints []workloadConnection `json:"endpoints"`
}

type workloadConnection struct {
	Component string `json:"component"`
	// Name is the target's endpoint, which is its port's name.
	Name string `json:"name"`
	// Visibility is "project": dependencies stay within an Application,
	// which is one platform project.
	Visibility  string               `json:"visibility"`
	EnvBindings connectionEnvAddress `json:"envBindings"`
}

type connectionEnvAddress struct {
	Address string `json:"address"`
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

// EnsureApplication makes app's platform project exist, following its
// Serendip Project's own pipeline (spec 020). Where it runs is each
// Deployment's business; the project only needs to exist. It also removes
// the single-environment binding every Application had before spec 020,
// once none of its Components still runs there.
func (p *Provider) EnsureApplication(ctx context.Context, app resource.Application) (bool, string, string, error) {
	project := projectName(app.Metadata.ID)
	labels := serendipLabels(app.Metadata.ID, app.Metadata.Name)

	pipeline := deploymentPipeline // an Application not (yet) under a Project
	for _, o := range app.Metadata.OwnerRefs {
		if o.Kind == resource.KindProject {
			pipeline = pipelineName(o.ID)
			if err := p.ensurePipelineExists(ctx, o.ID); err != nil {
				return false, "", "", err
			}
			break
		}
	}
	obj, err := apply(ctx, p.client, namespacedPath("projects"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Project",
		Metadata: kubernetes.ObjectMeta{Name: project, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"deploymentPipelineRef": map[string]string{"name": pipeline},
			"type":                  map[string]string{"kind": "ClusterProjectType", "name": projectTypeName},
		}),
	})
	if err != nil {
		return false, "", "", err
	}
	if err := p.removeLegacyProjectBinding(ctx, project, app.Metadata.ID); err != nil {
		return false, "", "", err
	}
	ready, reason, message := mapReady(obj)
	return ready, reason, message, nil
}

// ensurePipelineExists creates a Project's pipeline empty when it doesn't
// exist yet, so an Application can reference it before the Project's own
// reconcile writes its promotion paths. An existing one is left alone.
func (p *Provider) ensurePipelineExists(ctx context.Context, projectID string) error {
	_, err := p.client.Get(ctx, namespacedPath("deploymentpipelines")+"/"+pipelineName(projectID))
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, kubernetes.ErrNotFound):
		return fmt.Errorf("get pipeline %s: %w", pipelineName(projectID), err)
	}
	if _, err := p.client.Create(ctx, namespacedPath("deploymentpipelines"), pipelineObject(projectID, projectID, resource.Pipeline{})); err != nil {
		// Created meanwhile, by the Project's own reconcile, is fine.
		if _, gerr := p.client.Get(ctx, namespacedPath("deploymentpipelines")+"/"+pipelineName(projectID)); gerr == nil {
			return nil
		}
		return fmt.Errorf("create pipeline %s: %w", pipelineName(projectID), err)
	}
	return nil
}

// removeLegacyProjectBinding deletes the project's binding to the single
// environment of before spec 020 once no Component binding remains there:
// each Deployment removes its Component's legacy binding when it's Ready.
func (p *Provider) removeLegacyProjectBinding(ctx context.Context, project, appID string) error {
	path := namespacedPath("projectreleasebindings") + "/" + projectReleaseBindingName(appID)
	if _, err := p.client.Get(ctx, path); errors.Is(err, kubernetes.ErrNotFound) {
		return nil
	} else if err != nil {
		return fmt.Errorf("get ProjectReleaseBinding %s: %w", projectReleaseBindingName(appID), err)
	}
	bindings, err := p.client.List(ctx, namespacedPath("releasebindings"))
	if err != nil {
		return fmt.Errorf("list bindings: %w", err)
	}
	for _, b := range bindings {
		var s bindingSpec
		if json.Unmarshal(b.Spec, &s) == nil && s.Owner.ProjectName == project && s.Environment == environmentName {
			return nil // a Component still runs there until its Deployment takes over
		}
	}
	return p.deleteIgnoringNotFound(ctx, path)
}

func (p *Provider) DeleteApplication(ctx context.Context, app resource.Application) error {
	// The Project's own cleanup finalizer removes its Components,
	// ProjectReleaseBindings, and data-plane cell namespaces.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("projects")+"/"+projectName(app.Metadata.ID))
}

// reasonReleased is a Component's Ready reason once its current spec is a
// release: ready to deploy, not running anywhere by itself.
const reasonReleased = "Released"

// EnsureComponent makes comp's platform component and workload match its
// spec, and its current spec a release (spec 020). It runs nowhere by
// itself: auto-deploy is off, and Deployments run releases in
// Environments. So it's Ready once its current release exists, and
// reachability belongs to each Deployment.
func (p *Provider) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (reconcile.ComponentObservation, error) {
	if len(comp.Spec.Ports) == 0 {
		return reconcile.ComponentObservation{Reason: reconcile.ReasonUnsupported,
			Message: "components without ports are not supported by this provider yet"}, nil
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
		return reconcile.ComponentObservation{}, fmt.Errorf("get Component %s: %w", name, err)
	default:
		var compSpec struct {
			Owner ownerRef `json:"owner"`
		}
		if len(existing.Spec) == 0 {
			// Nothing to compare yet; the apply below sets the owner.
		} else if err := json.Unmarshal(existing.Spec, &compSpec); err != nil {
			return reconcile.ComponentObservation{}, fmt.Errorf("decode Component %s spec: %w", name, err)
		}
		if compSpec.Owner.ProjectName != "" && compSpec.Owner.ProjectName != project {
			return reconcile.ComponentObservation{Reason: reconcile.ReasonUnsupported,
				Message: "moving a component to a different application is not supported; delete it and create a new component under the target application"}, nil
		}
	}
	if _, err := apply(ctx, p.client, namespacedPath("components"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Component",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"owner":         ownerRef{ProjectName: project},
			"autoDeploy":    false,
			"componentType": map[string]string{"kind": "ClusterComponentType", "name": componentTypeWorkload + "/" + componentTypeName},
		}),
	}); err != nil {
		return reconcile.ComponentObservation{}, err
	}
	if err := p.ensureWorkload(ctx, project, name, labels, comp.Spec); err != nil {
		return reconcile.ComponentObservation{}, err
	}
	rel, err := p.createRelease(ctx, project, comp)
	if err != nil {
		return reconcile.ComponentObservation{}, err
	}
	return reconcile.ComponentObservation{Ready: true, Reason: reasonReleased,
		Message: "release " + rel.Name + " is ready to deploy"}, nil
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
func (p *Provider) observeReachability(ctx context.Context, prbName string, comp resource.Component, binding kubernetes.Object, exposed []string) (reconcile.Reachability, error) {
	pending := func(format string, args ...any) reconcile.Reachability {
		return reconcile.Reachability{Reason: reconcile.ReasonPending, Message: fmt.Sprintf(format, args...)}
	}

	prb, err := p.client.Get(ctx, namespacedPath("projectreleasebindings")+"/"+prbName)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return pending("application binding not found"), nil
	}
	if err != nil {
		return reconcile.Reachability{}, fmt.Errorf("get ProjectReleaseBinding %s: %w", prbName, err)
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

func (p *Provider) DeleteComponent(ctx context.Context, comp resource.Component) error {
	// The Component's own cleanup finalizer removes its Workload, releases,
	// and live data-plane resources.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("components")+"/"+componentName(comp.Metadata.ID))
}

// ensureWorkload server-side-applies the Workload. A no-drift apply stores
// nothing.
func (p *Provider) ensureWorkload(ctx context.Context, project, name string, labels map[string]string, spec resource.ComponentSpec) error {
	_, err := apply(ctx, p.client, namespacedPath("workloads"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Workload",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec:     mustJSON(desiredWorkload(project, name, spec)),
	})
	return err
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
	if len(spec.Dependencies) > 0 {
		w.Dependencies = &workloadDependencies{}
		for _, d := range spec.Dependencies {
			w.Dependencies.Endpoints = append(w.Dependencies.Endpoints, workloadConnection{
				Component: componentName(d.Component), Name: d.Port, Visibility: "project",
				EnvBindings: connectionEnvAddress{Address: d.Env},
			})
		}
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
