package openchoreo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Spec 020's caps (providers/application/openchoreo/runtime.go), repeated
// here so the two providers share nothing.
const (
	defaultTailLines = 100
	maxLogLine       = 16 << 10
	maxEvents        = 200
	recentLogBytes   = 4 << 20
	openTimeout      = 30 * time.Second
	// argoSlack is how many more lines a recent read asks for than it
	// returns, so that Argo's own lines, filtered out after Kubernetes has
	// cut the tail, don't shorten it (T014 U3). Argo writes a few per pod.
	argoSlack = 20
)

// argoLineRE matches the lines Argo's emissary writes into the main
// container itself (T001 item 4): logfmt with an argo=true field.
var argoLineRE = regexp.MustCompile(`(^|\s)level=\w+\s.*(^|\s)argo=true(\s|$)`)

// BuildLogs streams b's logs one step after another, in the order the
// steps run, each line's Instance naming its step. Without Follow it
// ends once each existing step's recent lines are read. With Follow it
// waits for each step's pod, follows it to its end, and ends with the
// run, or when ctx does. A step that can't be read ends the stream with
// its error once the lines before it are passed on, so a partial answer
// never looks complete. The one exception is a pod that's gone (404): it
// went with its run, and its lines with it.
func (p *Builds) BuildLogs(ctx context.Context, b resource.Build, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	run := runName(b.Metadata.ID)
	if _, err := p.client.Get(ctx, runPath(run)); errors.Is(err, kubernetes.ErrNotFound) {
		return nil, errors.New("openchoreo builds: the run is gone, so its logs have expired")
	} else if err != nil {
		return nil, fmt.Errorf("openchoreo builds: get run %s: %w", run, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &buildLogStream{lines: make(chan reconcile.LogLine, 64), cancel: cancel, done: make(chan struct{})}
	go s.produce(ctx, p, b, run, opts)
	return s, nil
}

// buildLogStream passes on the lines its producer reads.
type buildLogStream struct {
	lines     chan reconcile.LogLine
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closing   bool
	mu        sync.Mutex
	err       error
}

func (s *buildLogStream) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil && !s.closing {
		s.err = err
	}
}

func (s *buildLogStream) produce(ctx context.Context, p *Builds, b resource.Build, run string, opts reconcile.LogOptions) {
	defer close(s.done)
	defer close(s.lines)
	for step := 0; step < len(steps); {
		pod, more, err := p.nextPod(ctx, b, run, step, opts.Follow)
		if err != nil {
			s.fail(err)
			return
		}
		if pod == nil {
			if !more {
				return
			}
			step++ // never ran: the run ended before it
			continue
		}
		if err := s.streamPod(ctx, p, b, *pod, steps[step], opts); err != nil {
			s.fail(err)
			return
		}
		step++
	}
}

// nextPod finds the newest pod for steps[step]. Without wait, a missing
// one is skipped (nil, true). With wait, it polls until the pod appears,
// or the run ends or goes without it (nil, true, so later steps are
// still read), or a later step's pod shows this one was skipped.
func (p *Builds) nextPod(ctx context.Context, b resource.Build, run string, step int, wait bool) (*kubernetes.Object, bool, error) {
	for {
		found, later, err := p.findPod(ctx, run, step)
		if err != nil || found != nil || !wait || later {
			return found, err == nil, err
		}
		ended, gone, err := p.runEnded(ctx, b)
		if err != nil {
			return nil, false, err
		}
		if ended {
			// A pod made and finished between the list and the
			// observation would otherwise be missed.
			found, _, err := p.findPod(ctx, run, step)
			return found, err == nil && !gone, err
		}
		if err := sleep(ctx, p.pollEvery); err != nil {
			return nil, false, err
		}
	}
}

// findPod is the newest pod of steps[step], and whether a later step has
// one.
func (p *Builds) findPod(ctx context.Context, run string, step int) (found *kubernetes.Object, later bool, err error) {
	pods, err := p.stepPods(ctx, run)
	if err != nil {
		return nil, false, err
	}
	for _, sp := range pods {
		if sp.step == step {
			found = &sp.pod
		}
		later = later || sp.step > step
	}
	return found, later, nil
}

// runEnded reports whether b's run has finished, or is gone.
func (p *Builds) runEnded(ctx context.Context, b resource.Build) (ended, gone bool, err error) {
	obs, err := p.ObserveBuild(ctx, b)
	if err != nil {
		return false, false, err
	}
	return obs.Gone || obs.Phase.Done(), obs.Gone, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// errOpenTimeout is the cause when opening a step's logs takes too long.
var errOpenTimeout = fmt.Errorf("opening the logs took longer than %s", openTimeout)

// streamPod passes on one step pod's main-container lines. A container
// that hasn't started yet answers 400: with Follow that's waited out
// until the run ends, without it the step has nothing to show yet.
func (s *buildLogStream) streamPod(ctx context.Context, p *Builds, b resource.Build, pod kubernetes.Object, step string, opts reconcile.LogOptions) error {
	path := buildLogPath(pod.Metadata.Name, opts)
	for {
		// openTimeout bounds opening only; the opened stream lasts as long
		// as streamCtx, which ends with ctx or when this returns.
		streamCtx, stop := context.WithCancelCause(ctx)
		timer := time.AfterFunc(openTimeout, func() { stop(errOpenTimeout) })
		body, err := p.client.Stream(streamCtx, path)
		if !timer.Stop() {
			if body != nil {
				body.Close()
			}
			stop(nil)
			if ctx.Err() != nil {
				return fmt.Errorf("logs of %s: %w", step, ctx.Err())
			}
			return fmt.Errorf("logs of %s: %w", step, errOpenTimeout)
		}
		if err == nil {
			keep := 0 // a follow passes every line on as it comes
			if !opts.Follow {
				keep = tailLines(opts)
			}
			err = s.copy(ctx, body, step, keep)
			body.Close()
			stop(nil)
			return err
		}
		stop(nil)
		var status *kubernetes.StatusError
		switch {
		case errors.As(err, &status) && status.Code == http.StatusBadRequest:
			if !opts.Follow {
				return nil
			}
			if ended, _, err := p.runEnded(ctx, b); err != nil || ended {
				return err // it never started
			}
			if err := sleep(ctx, p.pollEvery); err != nil {
				return err
			}
		case errors.Is(err, kubernetes.ErrNotFound):
			return nil // removed with the run
		default:
			return fmt.Errorf("logs of %s: %w", step, err)
		}
	}
}

// copy passes on body's lines, without Argo's own, until it ends. With
// keep > 0 only the last keep of them are passed on, once body ends.
func (s *buildLogStream) copy(ctx context.Context, body io.Reader, step string, keep int) error {
	r := bufio.NewReaderSize(body, 4096)
	var held []reconcile.LogLine
	send := func(l reconcile.LogLine) error {
		select {
		case s.lines <- l:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("logs of %s: %w", step, ctx.Err())
		}
	}
	for {
		line, err := readCapped(r, maxLogLine)
		if (len(line) > 0 || err == nil) && !argoLineRE.MatchString(line) {
			l := parseLogLine(step, line)
			if keep <= 0 {
				if serr := send(l); serr != nil {
					return serr
				}
			} else {
				if len(held) == keep {
					held = held[1:]
				}
				held = append(held, l)
			}
		}
		if err != nil {
			// The lines read so far are passed on even when the read failed,
			// so the error ends a partial answer, as it would unheld.
			for _, l := range held {
				if serr := send(l); serr != nil {
					return serr
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("logs of %s: %w", step, err)
		}
	}
}

// tailLines is how many recent lines opts asks of each step.
func tailLines(opts reconcile.LogOptions) int {
	if opts.TailLines > 0 {
		return opts.TailLines
	}
	return defaultTailLines
}

// buildLogPath is a step pod's log request: its main container, where
// the step's own output is, with timestamps.
func buildLogPath(pod string, opts reconcile.LogOptions) string {
	q := url.Values{"timestamps": {"true"}, "container": {"main"}}
	tail := tailLines(opts)
	if !opts.Follow {
		// copy trims the answer back to tail once Argo's lines are out. A
		// follow's backlog isn't trimmed, so it asks for exactly tail and
		// may show a few fewer (RP-047).
		tail += argoSlack
	}
	q.Set("tailLines", strconv.Itoa(tail))
	if opts.Since > 0 {
		q.Set("sinceSeconds", strconv.Itoa(int((opts.Since+time.Second-1)/time.Second)))
	}
	if opts.Follow {
		q.Set("follow", "true")
	} else {
		q.Set("limitBytes", strconv.Itoa(recentLogBytes))
	}
	return "/api/v1/namespaces/" + RunNamespace + "/pods/" + url.PathEscape(pod) + "/log?" + q.Encode()
}

// readCapped reads one line, keeping at most limit bytes of it and never
// half a character.
func readCapped(r *bufio.Reader, limit int) (string, error) {
	var b strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if room := limit - b.Len(); room > 0 {
			b.Write(chunk[:min(len(chunk), room)])
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return strings.ToValidUTF8(strings.TrimRight(b.String(), "\r\n"), ""), err
	}
}

// parseLogLine splits the timestamp Kubernetes puts before each line.
func parseLogLine(step, line string) reconcile.LogLine {
	if ts, rest, ok := strings.Cut(line, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return reconcile.LogLine{Time: t, Instance: step, Line: rest}
		}
	}
	return reconcile.LogLine{Instance: step, Line: line}
}

// Next returns the next line; once they run out, the first failure, or
// io.EOF.
func (s *buildLogStream) Next() (reconcile.LogLine, error) {
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

// Close ends the stream and waits for its producer.
func (s *buildLogStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.cancel()
		for range s.lines { // unblock a pending send
		}
		<-s.done
	})
	return nil
}

// event is the part of a Kubernetes Event that's read, raw, since its
// fields sit at the top level.
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
	series := ""
	if e.Series != nil {
		series = e.Series.LastObservedTime
	}
	for _, s := range []string{e.LastTimestamp, series, e.EventTime, e.FirstTimestamp} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// BuildEvents lists recent events about b's run, newest first: the
// WorkflowRun, its Argo Workflow, and each step pod, asked for by exact
// name (T001 item 4). A run that's gone has none.
func (p *Builds) BuildEvents(ctx context.Context, b resource.Build, since time.Time) ([]reconcile.RuntimeEvent, error) {
	run := runName(b.Metadata.ID)
	pods, err := p.stepPods(ctx, run)
	if err != nil {
		return nil, err
	}
	type target struct{ namespace, name string }
	targets := []target{{BuildsNamespace, run}, {RunNamespace, run}}
	for _, sp := range pods {
		targets = append(targets, target{RunNamespace, sp.pod.Metadata.Name})
	}
	var out []reconcile.RuntimeEvent
	for _, t := range targets {
		q := url.Values{"fieldSelector": {"involvedObject.name=" + t.name}, "limit": {strconv.Itoa(maxEvents)}}
		raw, err := p.client.GetRaw(ctx, "/api/v1/namespaces/"+t.namespace+"/events?"+q.Encode())
		if errors.Is(err, kubernetes.ErrNotFound) {
			continue // nothing to say about a missing namespace
		}
		if err != nil {
			return nil, fmt.Errorf("openchoreo builds: list events of %s: %w", t.name, err)
		}
		var list struct {
			Items []event `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("openchoreo builds: decode events of %s: %w", t.name, err)
		}
		for _, e := range list.Items {
			when := e.when()
			if e.InvolvedObject.Name != t.name || (!since.IsZero() && when.Before(since)) {
				continue
			}
			count := e.Count
			if e.Series != nil && e.Series.Count > count {
				count = e.Series.Count
			}
			out = append(out, reconcile.RuntimeEvent{Time: when, Type: e.Type, Reason: e.Reason, Message: e.Message,
				ObjectKind: e.InvolvedObject.Kind, ObjectName: e.InvolvedObject.Name, Count: count})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}
	return out, nil
}
