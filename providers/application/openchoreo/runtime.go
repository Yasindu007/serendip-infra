package openchoreo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

var _ reconcile.RuntimeProvider = (*Provider)(nil)

// The platform labels a component's data-plane objects with its project,
// component and environment object names (spec 020 T007, seen on 1.3.1).
const (
	labelPlatformComponent   = "openchoreo.dev/component"
	labelPlatformEnvironment = "openchoreo.dev/environment"
)

const (
	// defaultTailLines is how many recent lines a log read starts with
	// when the caller doesn't say.
	defaultTailLines = 100
	// maxLogPods caps how many instances one log read follows.
	maxLogPods = 10
	// maxLogLine is the longest log line passed on; the rest is cut.
	maxLogLine = 16 << 10
	// maxEvents caps the events one read returns, newest first.
	maxEvents = 200
	// recentLogBytes caps each instance's answer when not following.
	recentLogBytes = 4 << 20
)

// renderedResource is one entry of a RenderedRelease's status.resources.
type renderedResource struct {
	Kind         string          `json:"kind"`
	Name         string          `json:"name"`
	Namespace    string          `json:"namespace"`
	HealthStatus string          `json:"healthStatus"`
	Status       json.RawMessage `json:"status"`
}

// running is where a Deployment runs on the data plane.
type running struct {
	binding   bindingSpec
	resources []renderedResource
	// namespace is the data-plane namespace, "" while nothing is rendered.
	namespace string
}

// locate finds d's binding and what the platform rendered for it. Before
// the first deploy there's nothing: a zero running and no error.
func (p *Provider) locate(ctx context.Context, d resource.Deployment, env resource.Environment) (running, error) {
	name := deploymentBindingName(d.Spec.Component, env.Metadata.ID)
	b, err := p.client.Get(ctx, namespacedPath("releasebindings")+"/"+name)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return running{}, nil
	}
	if err != nil {
		return running{}, fmt.Errorf("get binding %s: %w", name, err)
	}
	var out running
	if err := json.Unmarshal(b.Spec, &out.binding); err != nil {
		return running{}, fmt.Errorf("decode binding %s: %w", name, err)
	}
	// The RenderedRelease is named after, and owned by, its binding.
	rr, err := p.client.Get(ctx, namespacedPath("renderedreleases")+"/"+name)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return running{}, fmt.Errorf("get rendered release %s: %w", name, err)
	}
	var status struct {
		Resources []renderedResource `json:"resources"`
	}
	if len(rr.Status) > 0 {
		if err := json.Unmarshal(rr.Status, &status); err != nil {
			return running{}, fmt.Errorf("decode rendered release %s: %w", name, err)
		}
	}
	out.resources = status.Resources
	for _, r := range status.Resources {
		if r.Namespace != "" {
			out.namespace = r.Namespace
			break
		}
	}
	return out, nil
}

// pods lists the instances of what's running, oldest first.
func (p *Provider) pods(ctx context.Context, at running) ([]kubernetes.Object, error) {
	if at.namespace == "" {
		return nil, nil
	}
	selector := labelPlatformComponent + "=" + at.binding.Owner.ComponentName + "," + labelPlatformEnvironment + "=" + at.binding.Environment
	pods, err := p.client.List(ctx, "/api/v1/namespaces/"+url.PathEscape(at.namespace)+"/pods?labelSelector="+url.QueryEscape(selector))
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	sort.SliceStable(pods, func(i, j int) bool {
		if pods[i].Metadata.CreationTimestamp != pods[j].Metadata.CreationTimestamp {
			return pods[i].Metadata.CreationTimestamp < pods[j].Metadata.CreationTimestamp
		}
		return pods[i].Metadata.Name < pods[j].Metadata.Name
	})
	return pods, nil
}

type podSpec struct {
	Containers []struct {
		Name string `json:"name"`
	} `json:"containers"`
}

type podStatus struct {
	Phase             string `json:"phase"`
	ContainerStatuses []struct {
		Name         string `json:"name"`
		Ready        bool   `json:"ready"`
		RestartCount int    `json:"restartCount"`
		State        struct {
			Waiting *struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"waiting"`
			Terminated *struct {
				Reason string `json:"reason"`
			} `json:"terminated"`
		} `json:"state"`
	} `json:"containerStatuses"`
}

// podHealth sums a pod up in the platform's health words.
func podHealth(pod kubernetes.Object) (health, message string) {
	var st podStatus
	if len(pod.Status) > 0 && json.Unmarshal(pod.Status, &st) != nil {
		return "Unknown", ""
	}
	var notes []string
	restarts, ready := 0, len(st.ContainerStatuses) > 0
	health = "Healthy"
	for _, c := range st.ContainerStatuses {
		restarts += c.RestartCount
		ready = ready && c.Ready
		if w := c.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" {
			health = "Degraded"
			notes = append(notes, strings.TrimSuffix(w.Reason+": "+w.Message, ": "))
		}
	}
	switch {
	case health == "Degraded":
	case st.Phase == "Failed":
		health = "Degraded"
	case st.Phase == "Succeeded":
	case st.Phase != "Running" || !ready:
		health = "Progressing"
	}
	if restarts > 0 {
		notes = append(notes, "restarts: "+strconv.Itoa(restarts))
	}
	return health, strings.Join(notes, "; ")
}

// resourceMessage is the reason a rendered object isn't healthy, from
// its own conditions.
func resourceMessage(r renderedResource) string {
	if r.HealthStatus == "Healthy" || len(r.Status) == 0 {
		return ""
	}
	var st struct {
		Conditions []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"conditions"`
	}
	if json.Unmarshal(r.Status, &st) != nil {
		return ""
	}
	for _, c := range st.Conditions {
		if c.Status == "False" && c.Message != "" {
			return c.Message
		}
	}
	return ""
}

// RuntimeObjects lists what d runs as: the rendered objects with the
// platform's health, then each pod.
func (p *Provider) RuntimeObjects(ctx context.Context, d resource.Deployment, env resource.Environment) ([]reconcile.RuntimeObject, error) {
	at, err := p.locate(ctx, d, env)
	if err != nil {
		return nil, err
	}
	out := make([]reconcile.RuntimeObject, 0, len(at.resources))
	for _, r := range at.resources {
		health := r.HealthStatus
		if health == "" {
			health = "Unknown"
		}
		out = append(out, reconcile.RuntimeObject{Kind: r.Kind, Name: r.Name, Health: health, Message: resourceMessage(r)})
	}
	pods, err := p.pods(ctx, at)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		health, message := podHealth(pod)
		out = append(out, reconcile.RuntimeObject{Kind: "Pod", Name: pod.Metadata.Name, Health: health, Message: message})
	}
	return out, nil
}

// event is the part of a Kubernetes Event that's read; its fields sit at
// the top level, so it's read raw (kubernetes.Object would drop them).
type event struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	FirstTimestamp string `json:"firstTimestamp"`
	LastTimestamp  string `json:"lastTimestamp"`
	EventTime      string `json:"eventTime"`
	Series         *struct {
		Count            int    `json:"count"`
		LastObservedTime string `json:"lastObservedTime"`
	} `json:"series"`
	InvolvedObject struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"involvedObject"`
}

func (e event) when() time.Time {
	for _, s := range []string{e.LastTimestamp, e.seriesTime(), e.EventTime, e.FirstTimestamp} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (e event) seriesTime() string {
	if e.Series == nil {
		return ""
	}
	return e.Series.LastObservedTime
}

// Events lists recent events about what d runs as, newest first. The
// data-plane namespace holds every component of the project in that
// environment, so events are asked for by exact object name: d's rendered
// objects, its pods, and their ReplicaSets (named from the workload and
// the pods' template hash). A name prefix would also match a sibling
// component whose name starts with this one's.
func (p *Provider) Events(ctx context.Context, d resource.Deployment, env resource.Environment, since time.Time) ([]reconcile.RuntimeEvent, error) {
	at, err := p.locate(ctx, d, env)
	if err != nil || at.namespace == "" {
		return nil, err
	}
	pods, err := p.pods(ctx, at)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var workloads []string
	for _, r := range at.resources {
		names[r.Name] = true
		if r.Kind == "Deployment" {
			workloads = append(workloads, r.Name)
		}
	}
	for _, pod := range pods {
		names[pod.Metadata.Name] = true
		if hash := pod.Metadata.Labels["pod-template-hash"]; hash != "" {
			for _, w := range workloads {
				names[w+"-"+hash] = true
			}
		}
	}
	var out []reconcile.RuntimeEvent
	for _, name := range slices.Sorted(maps.Keys(names)) {
		q := url.Values{"fieldSelector": {"involvedObject.name=" + name}, "limit": {strconv.Itoa(maxEvents)}}
		raw, err := p.client.GetRaw(ctx, "/api/v1/namespaces/"+url.PathEscape(at.namespace)+"/events?"+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("list events of %s: %w", name, err)
		}
		var list struct {
			Items []event `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode events of %s: %w", name, err)
		}
		for _, e := range list.Items {
			t := e.when()
			if e.InvolvedObject.Name != name || (!since.IsZero() && t.Before(since)) {
				continue
			}
			count := e.Count
			if e.Series != nil && e.Series.Count > count {
				count = e.Series.Count
			}
			out = append(out, reconcile.RuntimeEvent{Time: t, Type: e.Type, Reason: e.Reason, Message: e.Message,
				ObjectKind: e.InvolvedObject.Kind, ObjectName: e.InvolvedObject.Name, Count: count})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}
	return out, nil
}

// openTimeout bounds opening the instances' log streams; once open, a
// stream lasts as long as its context.
const openTimeout = 30 * time.Second

// Logs streams d's logs from each of its instances (the newest 10),
// merged in arrival order. Without Follow the stream ends once every
// instance's recent lines are read. An instance that can't be read makes
// the stream end with its error once the others are read, so a partial
// answer never looks complete; with Previous, an instance that has no
// previous run is skipped.
func (p *Provider) Logs(ctx context.Context, d resource.Deployment, env resource.Environment, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	at, err := p.locate(ctx, d, env)
	if err != nil {
		return nil, err
	}
	pods, err := p.pods(ctx, at)
	if err != nil {
		return nil, err
	}
	if len(pods) > maxLogPods {
		pods = pods[len(pods)-maxLogPods:] // the newest
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &logStream{lines: make(chan reconcile.LogLine, 64), cancel: cancel}
	timer := time.AfterFunc(openTimeout, cancel)
	var bodies []io.ReadCloser
	var names []string
	for _, pod := range pods {
		body, err := p.client.Stream(ctx, logPath(at.namespace, pod, opts))
		var status *kubernetes.StatusError
		if opts.Previous && errors.As(err, &status) && status.Code == 400 {
			continue // no previous run of this instance
		}
		if err != nil {
			s.fail(fmt.Errorf("logs of %s: %w", pod.Metadata.Name, err))
			continue
		}
		bodies, names = append(bodies, body), append(names, pod.Metadata.Name)
	}
	if !timer.Stop() {
		for _, b := range bodies {
			b.Close()
		}
		return nil, fmt.Errorf("opening the logs took longer than %s", openTimeout)
	}
	if len(bodies) == 0 {
		cancel()
		if s.err != nil {
			return nil, s.err
		}
	}
	for i, body := range bodies {
		s.wg.Add(1)
		go s.read(ctx, names[i], body)
	}
	go func() {
		s.wg.Wait()
		close(s.lines)
	}()
	return s, nil
}

// logPath is a pod's log request: the workload's container, with
// timestamps so lines can be told apart and ordered.
func logPath(namespace string, pod kubernetes.Object, opts reconcile.LogOptions) string {
	q := url.Values{"timestamps": {"true"}}
	var spec podSpec
	if json.Unmarshal(pod.Spec, &spec) == nil && len(spec.Containers) > 1 {
		container := spec.Containers[0].Name
		for _, c := range spec.Containers {
			if c.Name == "main" {
				container = c.Name
			}
		}
		q.Set("container", container)
	}
	tail := opts.TailLines
	if tail <= 0 {
		tail = defaultTailLines
	}
	q.Set("tailLines", strconv.Itoa(tail))
	if opts.Since > 0 {
		q.Set("sinceSeconds", strconv.Itoa(int((opts.Since+time.Second-1)/time.Second)))
	}
	if opts.Previous {
		q.Set("previous", "true")
	}
	if opts.Follow {
		q.Set("follow", "true")
	} else {
		q.Set("limitBytes", strconv.Itoa(recentLogBytes))
	}
	return "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(pod.Metadata.Name) + "/log?" + q.Encode()
}

// logStream merges each instance's lines onto one channel.
type logStream struct {
	lines     chan reconcile.LogLine
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closing   atomic.Bool
	mu        sync.Mutex
	err       error // the first failure, returned once the lines run out
}

func (s *logStream) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *logStream) read(ctx context.Context, instance string, body io.ReadCloser) {
	defer s.wg.Done()
	defer body.Close()
	r := bufio.NewReaderSize(body, 4096)
	for {
		line, err := readCapped(r, maxLogLine)
		if len(line) > 0 || err == nil {
			select {
			case s.lines <- parseLogLine(instance, line):
			case <-ctx.Done():
				// Ended by anything but Close (a deadline), a line was
				// dropped: the answer mustn't look complete.
				if !s.closing.Load() {
					s.fail(fmt.Errorf("logs of %s: %w", instance, ctx.Err()))
				}
				return
			}
		}
		if err != nil {
			// Closing ends every read; that's not a failure. Anything
			// else (a reset connection, the caller's deadline) is.
			if !errors.Is(err, io.EOF) && !s.closing.Load() {
				s.fail(fmt.Errorf("logs of %s: %w", instance, err))
			}
			return
		}
	}
}

// readCapped reads one line, keeping at most max bytes of it and never
// half a character.
func readCapped(r *bufio.Reader, max int) (string, error) {
	var b strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if room := max - b.Len(); room > 0 {
			b.Write(chunk[:min(len(chunk), room)])
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return strings.ToValidUTF8(strings.TrimRight(b.String(), "\r\n"), ""), err
	}
}

// parseLogLine splits the timestamp the platform puts before each line.
func parseLogLine(instance, line string) reconcile.LogLine {
	if ts, rest, ok := strings.Cut(line, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return reconcile.LogLine{Time: t, Instance: instance, Line: rest}
		}
	}
	return reconcile.LogLine{Instance: instance, Line: line}
}

// Next returns the next line; once they run out, the first failure, or
// io.EOF.
func (s *logStream) Next() (reconcile.LogLine, error) {
	line, ok := <-s.lines
	if !ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.err != nil {
			return reconcile.LogLine{}, s.err
		}
		return reconcile.LogLine{}, io.EOF
	}
	return line, nil
}

// Close ends every instance's stream and waits for their readers.
func (s *logStream) Close() error {
	s.closeOnce.Do(func() {
		s.closing.Store(true)
		s.cancel()
		s.wg.Wait()
	})
	return nil
}
