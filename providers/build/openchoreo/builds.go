package openchoreo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/internal/gitremote"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Builds is the build capability on the workflow plane (plan decision
// 10): each Serendip Build is one standalone WorkflowRun of
// serendip-dockerfile in BuildsNamespace, which OpenChoreo runs as an
// Argo Workflow of the same name in RunNamespace.
type Builds struct {
	client *kubernetes.Client
	// http reads repositories' heads; its timeout bounds each poll.
	http *http.Client
	// pollEvery is how often a followed log looks for the next step's pod.
	pollEvery time.Duration
}

var _ reconcile.BuildProvider = (*Builds)(nil)

// headTimeout bounds one head-commit read.
const headTimeout = 30 * time.Second

// NewBuilds returns the build provider over client.
func NewBuilds(client *kubernetes.Client) *Builds {
	return &Builds{client: client, http: &http.Client{Timeout: headTimeout}, pollEvery: 2 * time.Second}
}

const (
	argoAPIVersion = "argoproj.io/v1alpha1"
	// buildIDAnnotation holds the full Build ID, which may not fit a label
	// value.
	buildIDAnnotation = "serendip.dev/build-id"
	// Argo's pod label naming the Workflow a pod runs (T001 item 4).
	argoWorkflowLabel = "workflows.argoproj.io/workflow"
	// imageRepoPrefix keeps built images apart from anything else pushed
	// to the registry.
	imageRepoPrefix = "serendip/"
)

// runName is the WorkflowRun (and Argo Workflow) for Build id: derived
// from the ID alone, so a delete needs nothing else, and short because an
// ID can be 128 characters with ones Kubernetes names don't allow.
func runName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "build-" + hex.EncodeToString(sum[:])[:16]
}

// imageRepository is where comp's images are pushed: the same hash the
// application provider names the Component's platform object with, so
// the two can be matched up.
func imageRepository(compID string) string {
	sum := sha256.Sum256([]byte(compID))
	return imageRepoPrefix + "cmp-" + hex.EncodeToString(sum[:])[:10]
}

func runPath(name string) string { return workflowRunsPath + "/" + name }

func gitSecretPath(run string) string {
	return "/api/v1/namespaces/" + RunNamespace + "/secrets/" + run + GitSecretSuffix
}

func argoWorkflowPath(name string) string {
	return "/apis/" + argoAPIVersion + "/namespaces/" + RunNamespace + "/workflows/" + name
}

// basicAuth reads a repository credential from a Secret's value: a
// password or token, and an optional user name ("git" if none; hosts
// that take a token ignore it).
func basicAuth(cred map[string]any) (*gitremote.Credential, error) {
	if cred == nil {
		return nil, nil
	}
	str := func(key string) string { s, _ := cred[key].(string); return s }
	c := &gitremote.Credential{Username: str("username"), Password: str("password")}
	if c.Password == "" {
		c.Password = str("token")
	}
	if c.Password == "" {
		return nil, errors.New("openchoreo builds: the repository's credential has no password or token")
	}
	if c.Username == "" {
		c.Username = "git"
	}
	return c, nil
}

// StartBuild creates b's credential copy, when there's a credential, and
// then its WorkflowRun. The copy must exist before the run starts: the
// checkout mounts it optionally, so a late one means an anonymous clone
// (T001 item 6). An existing run is left as it is, its copy included, so
// a credential changed mid-run doesn't change under it.
func (p *Builds) StartBuild(ctx context.Context, app resource.Application, comp resource.Component, b resource.Build, cred map[string]any) error {
	if comp.Spec.Source == nil {
		return fmt.Errorf("openchoreo builds: component %s has no source to build", comp.Metadata.ID)
	}
	src := comp.Spec.Source.WithDefaults()
	auth, err := basicAuth(cred)
	if err != nil {
		return err
	}
	name := runName(b.Metadata.ID)
	switch _, err := p.client.Get(ctx, runPath(name)); {
	case err == nil:
		return nil
	case !errors.Is(err, kubernetes.ErrNotFound):
		return fmt.Errorf("openchoreo builds: get run %s: %w", name, err)
	}
	labels := map[string]string{BuildLabel: name}
	annotations := map[string]string{buildIDAnnotation: b.Metadata.ID}
	if auth != nil {
		if err := ensureNamespace(ctx, p.client, RunNamespace); err != nil {
			return fmt.Errorf("openchoreo builds: %w", err)
		}
		_, err := apply(ctx, p.client, "/api/v1/namespaces/"+RunNamespace+"/secrets", kubernetes.Object{
			APIVersion: "v1", Kind: "Secret",
			Metadata: kubernetes.ObjectMeta{Name: name + GitSecretSuffix, Namespace: RunNamespace, Labels: labels, Annotations: annotations},
			Data:     mustJSON(map[string][]byte{"username": []byte(auth.Username), "password": []byte(auth.Password)}),
		})
		if err != nil {
			return fmt.Errorf("openchoreo builds: %w", err)
		}
	}
	run := kubernetes.Object{
		APIVersion: apiVersion, Kind: "WorkflowRun",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: BuildsNamespace, Labels: labels, Annotations: annotations},
		Spec: mustJSON(map[string]any{
			"workflow": map[string]any{
				"kind": "ClusterWorkflow",
				"name": WorkflowName,
				"parameters": map[string]any{
					"repository": map[string]any{
						"url": src.Repository, "branch": src.Branch, "commit": b.Spec.Commit, "path": src.Path,
					},
					"dockerfile": src.Dockerfile,
					"image":      map[string]any{"repository": imageRepository(comp.Metadata.ID)},
				},
			},
		}),
	}
	// Builds are only created and cancelled (plan decision 5: no edit
	// route), so a run of the same name, from a racing start, is this
	// Build's.
	_, err = p.client.Create(ctx, workflowRunsPath, run)
	var status *kubernetes.StatusError
	if errors.As(err, &status) && status.Code == http.StatusConflict {
		return nil
	}
	if err != nil {
		return fmt.Errorf("openchoreo builds: create run %s: %w", name, err)
	}
	return nil
}

// CancelBuild deletes b's run, which stops it within seconds (T001 item
// 5), and its credential copy, which nothing needs any more.
func (p *Builds) CancelBuild(ctx context.Context, b resource.Build) error {
	return p.RemoveBuild(ctx, b)
}

// RemoveBuild deletes b's run and its credential copy. The run's
// finalizer removes its Workflow, pods and workspace.
func (p *Builds) RemoveBuild(ctx context.Context, b resource.Build) error {
	name := runName(b.Metadata.ID)
	if err := deleteIfExists(ctx, p.client, runPath(name)); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	if err := deleteIfExists(ctx, p.client, gitSecretPath(name)); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	return nil
}

// HeadCommit reads the head of src's branch over smart HTTP.
func (p *Builds) HeadCommit(ctx context.Context, src resource.ComponentSource, cred map[string]any) (string, error) {
	src = src.WithDefaults()
	auth, err := basicAuth(cred)
	if err != nil {
		return "", err
	}
	head, err := gitremote.HeadCommit(ctx, p.http, src.Repository, src.Branch, auth)
	return head, headError(err)
}

// headError wraps gitremote's errors in the core ones auto-build tells
// apart, keeping the original's text.
func headError(err error) error {
	for _, m := range []struct{ from, to error }{
		{gitremote.ErrSSH, reconcile.ErrSourceSSH},
		{gitremote.ErrAuth, reconcile.ErrSourceAuth},
		{gitremote.ErrNotFound, reconcile.ErrSourceNotFound},
		{gitremote.ErrNoBranch, reconcile.ErrSourceNoBranch},
	} {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%w: %w", m.to, err)
		}
	}
	return err
}

// runStatus is the part of a WorkflowRun's status that's read.
type runStatus struct {
	Conditions []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"conditions"`
	Tasks []struct {
		Name        string `json:"name"`
		Phase       string `json:"phase"`
		StartedAt   string `json:"startedAt"`
		CompletedAt string `json:"completedAt"`
		Message     string `json:"message"`
	} `json:"tasks"`
	RunReference *struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"runReference"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

// argoStatus is the part of an Argo Workflow's status that's read.
type argoStatus struct {
	Phase      string              `json:"phase"`
	Message    string              `json:"message"`
	StartedAt  string              `json:"startedAt"`
	FinishedAt string              `json:"finishedAt"`
	Nodes      map[string]argoNode `json:"nodes"`
}

type argoNode struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Type         string `json:"type"`
	TemplateName string `json:"templateName"`
	Phase        string `json:"phase"`
	Message      string `json:"message"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt"`
	Outputs      *struct {
		Parameters []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"parameters"`
	} `json:"outputs"`
}

func (n argoNode) output(name string) string {
	if n.Outputs == nil {
		return ""
	}
	for _, p := range n.Outputs.Parameters {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

// steps are the workflow's steps in the order they run.
var steps = []string{CheckoutStep, BuildPushStep}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// stepPhase maps a task's or node's phase, as OpenChoreo and Argo write
// them, onto a Build's.
func stepPhase(phase string) resource.BuildPhase {
	switch phase {
	case "Running":
		return resource.BuildRunning
	case "Succeeded":
		return resource.BuildSucceeded
	case "Failed", "Error":
		return resource.BuildFailed
	case "Skipped", "Omitted":
		return resource.BuildCancelled
	default: // "", Pending
		return resource.BuildQueued
	}
}

// ObserveBuild reads b's run. The phase comes from the run's conditions,
// or from its Argo Workflow when that's further on, because the run's
// status lags the Workflow's (T001 item 3); the steps, commit and image
// come from the Workflow's nodes, falling back to the run's tasks before
// the Workflow exists. A run that's gone (expired, or never started) is
// Gone.
func (p *Builds) ObserveBuild(ctx context.Context, b resource.Build) (reconcile.BuildObservation, error) {
	name := runName(b.Metadata.ID)
	run, err := p.client.Get(ctx, runPath(name))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return reconcile.BuildObservation{Gone: true}, nil
	}
	if err != nil {
		return reconcile.BuildObservation{}, fmt.Errorf("openchoreo builds: get run %s: %w", name, err)
	}
	var rs runStatus
	if len(run.Status) > 0 {
		if err := json.Unmarshal(run.Status, &rs); err != nil {
			return reconcile.BuildObservation{}, fmt.Errorf("openchoreo builds: decode run %s: %w", name, err)
		}
	}
	var wf *argoStatus
	wfName := name
	if rs.RunReference != nil && rs.RunReference.Name != "" {
		wfName = rs.RunReference.Name
	}
	obj, err := p.client.Get(ctx, argoWorkflowPath(url.PathEscape(wfName)))
	switch {
	case errors.Is(err, kubernetes.ErrNotFound):
	case err != nil:
		return reconcile.BuildObservation{}, fmt.Errorf("openchoreo builds: get workflow %s: %w", wfName, err)
	default:
		wf = &argoStatus{}
		if len(obj.Status) > 0 {
			if err := json.Unmarshal(obj.Status, wf); err != nil {
				return reconcile.BuildObservation{}, fmt.Errorf("openchoreo builds: decode workflow %s: %w", wfName, err)
			}
		}
	}
	return observe(rs, wf), nil
}

// observe combines a run's status and its Workflow's, nil if there's
// none yet.
func observe(rs runStatus, wf *argoStatus) reconcile.BuildObservation {
	var obs reconcile.BuildObservation
	obs.Phase, obs.Message = runPhase(rs)
	// The run's own times are when its controller noticed; the Workflow's
	// are when the build ran, so they're preferred once it exists.
	obs.StartedAt, obs.FinishedAt = parseTime(rs.StartedAt), parseTime(rs.CompletedAt)
	if wf != nil && wf.StartedAt != "" {
		obs.StartedAt, obs.FinishedAt = parseTime(wf.StartedAt), parseTime(wf.FinishedAt)
	}

	byStep := map[string]resource.BuildStep{}
	for _, t := range rs.Tasks {
		byStep[t.Name] = resource.BuildStep{Name: t.Name, Phase: stepPhase(t.Phase),
			StartedAt: parseTime(t.StartedAt), FinishedAt: parseTime(t.CompletedAt), Message: t.Message}
	}
	if wf != nil {
		for _, n := range wf.Nodes {
			if n.Type != "Pod" || !isStep(n.TemplateName) {
				continue
			}
			byStep[n.TemplateName] = resource.BuildStep{Name: n.TemplateName, Phase: stepPhase(n.Phase),
				StartedAt: parseTime(n.StartedAt), FinishedAt: parseTime(n.FinishedAt), Message: n.Message}
			switch n.TemplateName {
			case CheckoutStep:
				obs.Commit = n.output(CommitOutput)
			case BuildPushStep:
				obs.Image = n.output(ImageOutput)
			}
		}
		if phase := stepPhase(wf.Phase); later(phase, obs.Phase) {
			obs.Phase = phase
			if phase == resource.BuildFailed {
				obs.Message = wf.Message
			}
		}
	}
	for _, s := range steps {
		if step, ok := byStep[s]; ok {
			obs.Steps = append(obs.Steps, step)
		}
	}
	if obs.Phase == resource.BuildFailed {
		obs.Message = failureMessage(obs.Message, obs.Steps)
	}
	if obs.Phase == resource.BuildSucceeded && obs.Image == "" {
		// Succeeded without its image would build nothing; say so rather
		// than hand an empty image on.
		obs.Phase, obs.Message = resource.BuildFailed, "the build finished without reporting its image"
	}
	if !obs.Phase.Done() {
		obs.FinishedAt = time.Time{}
	}
	return obs
}

func isStep(name string) bool { return name == CheckoutStep || name == BuildPushStep }

// later reports whether a is further on than b: a final phase is
// further than Running, which is further than Queued.
func later(a, b resource.BuildPhase) bool {
	rank := func(p resource.BuildPhase) int {
		switch {
		case p.Done():
			return 2
		case p == resource.BuildRunning:
			return 1
		}
		return 0
	}
	return rank(a) > rank(b)
}

// runPhase reads a run's conditions (OpenChoreo 1.3.1's
// controller_conditions.go). A run with none yet is Queued. One that
// can't start, because the plane or the workflow can't be resolved or
// rendered, stays Queued with WorkflowCompleted False and that reason,
// which is passed on. WorkflowFailed's own message is always the same
// sentence, so failureMessage looks for a better one.
func runPhase(rs runStatus) (resource.BuildPhase, string) {
	is := map[string]bool{}
	var failed, waiting string
	for _, c := range rs.Conditions {
		if c.Type == "WorkflowCompleted" && c.Status == "False" && c.Reason != "" && c.Reason != "WorkflowPending" {
			waiting = strings.TrimSuffix(c.Reason+": "+c.Message, ": ")
		}
		if c.Status != "True" {
			continue
		}
		is[c.Type] = true
		if c.Type == "WorkflowFailed" {
			failed = c.Message
		}
	}
	switch {
	case is["WorkflowFailed"]:
		return resource.BuildFailed, failed
	case is["WorkflowSucceeded"]:
		return resource.BuildSucceeded, ""
	case is["WorkflowRunning"]:
		return resource.BuildRunning, ""
	}
	return resource.BuildQueued, waiting
}

// failureMessage prefers the failed step's own message, which names the
// cause (a pod that couldn't be created, an exit code), to the
// workflow's summary.
func failureMessage(msg string, steps []resource.BuildStep) string {
	for _, s := range steps {
		if s.Phase == resource.BuildFailed && s.Message != "" {
			return s.Name + ": " + s.Message
		}
	}
	if msg == "" {
		return "the build failed"
	}
	return msg
}

// stepPods lists b's run's pods by step, in the order the steps run.
func (p *Builds) stepPods(ctx context.Context, run string) ([]stepPod, error) {
	pods, err := p.client.List(ctx, "/api/v1/namespaces/"+RunNamespace+"/pods?labelSelector="+url.QueryEscape(argoWorkflowLabel+"="+run))
	if err != nil {
		return nil, fmt.Errorf("openchoreo builds: list pods of %s: %w", run, err)
	}
	var out []stepPod
	for _, pod := range pods {
		if step := podStep(run, pod); step >= 0 {
			out = append(out, stepPod{step: step, pod: pod})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].step != out[j].step {
			return out[i].step < out[j].step
		}
		return out[i].pod.Metadata.CreationTimestamp < out[j].pod.Metadata.CreationTimestamp
	})
	return out, nil
}

type stepPod struct {
	step int // index into steps
	pod  kubernetes.Object
}

// podStep is which step a run's pod runs, or -1. Argo names a pod
// <workflow>-<template>-<fnv32a of the node name, in decimal> (POD_NAMES
// v2, Argo 4.0.8's pod_name.go).
func podStep(run string, pod kubernetes.Object) int {
	rest, ok := strings.CutPrefix(pod.Metadata.Name, run+"-")
	if !ok {
		return -1
	}
	for i, s := range steps {
		if strings.HasPrefix(rest, s+"-") && !strings.Contains(strings.TrimPrefix(rest, s+"-"), "-") {
			return i
		}
	}
	return -1
}
