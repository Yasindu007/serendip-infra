package main

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The web console's polling pattern on a Component's detail page
// (specs/016-ui-mvp plan.md decision 12): the Component and its
// Application every 5 s, and the topology banner's node list every 60 s.
const (
	consoleDetailInterval   = 5 * time.Second
	consoleTopologyInterval = 60 * time.Second
)

// pollStats counts what the simulated console tabs did.
type pollStats struct {
	requests atomic.Int64
	errors   atomic.Int64
}

// startConsoleTabs starts clients simulated console tabs, each open on one
// of compIDs (round-robin). stop ends them and waits until they have all
// returned. Requests use the harness's API key: the extra cost of a session
// cookie is measured separately by a Go benchmark in api/.
func (a api) startConsoleTabs(ctx context.Context, appID string, compIDs []string, clients int) (st *pollStats, stop func()) {
	st = &pollStats{}
	if len(compIDs) == 0 || clients <= 0 {
		return st, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i := range clients {
		comp := compIDs[i%len(compIDs)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			detail := time.NewTicker(consoleDetailInterval)
			defer detail.Stop()
			topology := time.NewTicker(consoleTopologyInterval)
			defer topology.Stop()
			for _, p := range []string{"/v1/components/" + comp, "/v1/applications/" + appID, "/v1/nodes"} {
				a.countedGet(ctx, p, st)
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-detail.C:
					a.countedGet(ctx, "/v1/components/"+comp, st)
					a.countedGet(ctx, "/v1/applications/"+appID, st)
				case <-topology.C:
					a.countedGet(ctx, "/v1/nodes", st)
				}
			}
		}()
	}
	return st, func() {
		cancel()
		wg.Wait()
	}
}

// countedGet makes one console request, counting failures. A request
// cancelled by stop at the end of the phase isn't an error.
func (a api) countedGet(ctx context.Context, path string, st *pollStats) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		st.errors.Add(1)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			st.errors.Add(1)
		}
		return
	}
	_, readErr := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	st.requests.Add(1)
	if resp.StatusCode != http.StatusOK || (readErr != nil && ctx.Err() == nil) {
		st.errors.Add(1)
	}
}
