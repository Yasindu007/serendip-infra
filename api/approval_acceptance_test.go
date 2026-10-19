package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// Spec 019 T008: the acceptance criteria end to end, with the API, a real
// SQLite store and the reconcile engine running the approval reconciler,
// as serendipd wires it (cmd/serendipd registerApprovals).

// valueStore is a Secrets provider safe to share with the engine's
// goroutine, which can be told to be unreachable.
type valueStore struct {
	mu     sync.Mutex
	values map[string]bool // Secret ID -> a value exists
	down   bool
}

func (v *valueStore) has(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.values[id]
}

func (v *valueStore) setDown(down bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.down = down
}

func (v *valueStore) Exists(_ context.Context, sec resource.Secret) (bool, error) {
	return v.has(sec.Metadata.ID), nil
}
func (v *valueStore) WriteValue(_ context.Context, sec resource.Secret, _ map[string]any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.values[sec.Metadata.ID] = true
	return nil
}
func (v *valueStore) ReadValue(context.Context, resource.Secret) (map[string]any, error) {
	return nil, nil
}
func (v *valueStore) Delete(_ context.Context, sec resource.Secret) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.down {
		return reconcile.ErrProviderNotReady
	}
	delete(v.values, sec.Metadata.ID)
	return nil
}

// approvalEngine runs the approval reconciler over w's store, resyncing
// often enough that a test needn't share the server's event bus.
type approvalEngine struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startApprovalEngine(t *testing.T, w *authzWorld, values *valueStore) *approvalEngine {
	t.Helper()
	db := w.db
	bus := event.NewInProcessBus()
	approvals := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](db, resource.KindApproval).SetBus(bus)
	secrets := reconcile.NewSecretReconciler(sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret), values)
	del := func(ctx context.Context, kind resource.Kind, id string, cascade bool) error {
		return sqlite.NewStore[json.RawMessage, json.RawMessage](db, kind).SetBus(bus).Delete(ctx, id, cascade)
	}
	exists := func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		return sqlite.ResourceExists(ctx, db, kind, id)
	}
	r := reconcile.NewApprovalReconciler(approvals, del, map[resource.Kind]reconcile.CleanupFunc{resource.KindSecret: secrets.EraseValue}, exists, time.Now)
	engine := reconcile.NewEngine(bus, 50*time.Millisecond, 5*time.Second)
	engine.Register(resource.KindApproval, r, func(ctx context.Context) ([]graph.ResourceRef, error) {
		open, err := sqlite.ListApprovals(ctx, db, sqlite.ApprovalQuery{Unsettled: true})
		refs := make([]graph.ResourceRef, len(open))
		for i, a := range open {
			refs[i] = graph.ResourceRef{Kind: resource.KindApproval, ID: a.Metadata.ID}
		}
		return refs, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	e := &approvalEngine{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(e.done)
		if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("engine: %v", err)
		}
	}()
	t.Cleanup(e.stop)
	return e
}

func (e *approvalEngine) stop() {
	e.cancel()
	<-e.done
}

// awaitApproval polls id until ok holds, failing after a few seconds with
// the approval as it last was.
func awaitApproval(t *testing.T, w *authzWorld, id string, what string, ok func(resource.Approval) bool) resource.Approval {
	t.Helper()
	s := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](w.db, resource.KindApproval)
	var a resource.Approval
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var err error
		if a, err = s.Get(context.Background(), id); err == nil && ok(a) {
			return a
		}
	}
	t.Fatalf("approval %s never became %s: phase %q, settled %v, conditions %+v", id, what, a.Status.Phase, a.Status.Settled, a.Status.Conditions)
	return a
}

func verified(a resource.Approval) bool {
	return a.Status.Phase == resource.ApprovalExecuted && a.Status.Settled &&
		condition(a, resource.ApprovalConditionVerified).Status == resource.ConditionTrue
}

func condition(a resource.Approval, typ string) resource.Condition {
	for _, c := range a.Status.Conditions {
		if c.Type == typ {
			return c
		}
	}
	return resource.Condition{}
}

func (w *authzWorld) exists(name string) bool {
	w.t.Helper()
	s := w.scope(name)
	ok, err := sqlite.ResourceExists(context.Background(), w.db, s.Kind, s.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return ok
}

func TestApprovalsAcceptance(t *testing.T) {
	w := approvalsWorld(t)
	values := &valueStore{values: map[string]bool{w.id["secret"]: true}}
	engine := startApprovalEngine(t, w, values)

	// Criterion 1: an allowed DELETE happens at once, its Approval decided
	// by policy, then verified and settled.
	t.Run("policy approves a person's delete", func(t *testing.T) {
		got := w.do("tok-contrib", http.MethodDelete, "/v1/components/"+w.id["devweb"], nil)
		if got.Code != http.StatusNoContent || w.exists("devweb") {
			t.Fatalf("DELETE: %d %s", got.Code, got.Body)
		}
		a := awaitApproval(t, w, got.Header().Get("Serendip-Approval-Id"), "verified", verified)
		if a.Status.Decision == nil || !a.Status.Decision.ByPolicy {
			t.Errorf("decision = %+v, want policy", a.Status.Decision)
		}
	})

	// Criteria 3, 4 and 10: an agent waits for a person, who approves; the
	// reconciler deletes and verifies; every step is audited.
	t.Run("an agent's request, approved by a person", func(t *testing.T) {
		req := w.requestApproval("tok-agent", deleteRequest(w.scope("web"), false, "retired"), http.StatusCreated)
		time.Sleep(200 * time.Millisecond) // several resyncs: a pending request is never executed
		if !w.exists("web") {
			t.Fatal("a pending request deleted its target")
		}
		w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+req.Metadata.ID+"/approve", map[string]any{"comment": "ok"}, http.StatusOK)
		awaitApproval(t, w, req.Metadata.ID, "verified", verified)
		if w.exists("web") {
			t.Fatal("the approved target still exists")
		}

		rows, err := w.db.Query(`SELECT actor, kind, COALESCE(policy_decision, '') FROM audit_log WHERE resource_id IN (?, ?) ORDER BY at`, req.Metadata.ID, w.id["web"])
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var trail []string
		for rows.Next() {
			var actor, kind, decision string
			if err := rows.Scan(&actor, &kind, &decision); err != nil {
				t.Fatal(err)
			}
			trail = append(trail, actor+" "+kind+" "+decision)
		}
		joined := strings.Join(trail, "\n")
		for _, want := range []string{
			"agent:am-dev/agent-1 Approval needsApproval: serendip/components/delete",                                          // the request
			"person:owner Approval allow: ",                                                                                    // the decision
			"system:reconciler Component approved: " + req.Metadata.ID + " by person:owner; requested by agent:am-dev/agent-1", // the delete
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("audit trail lacks %q:\n%s", want, joined)
			}
		}
	})

	// Criterion 8: an approved cascade removes exactly what was reviewed.
	t.Run("cascade", func(t *testing.T) {
		req := w.requestApproval("tok-agent", deleteRequest(w.scope("devapp"), true, "retire dev"), http.StatusCreated)
		w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+req.Metadata.ID+"/approve", nil, http.StatusOK)
		a := awaitApproval(t, w, req.Metadata.ID, "verified", verified)
		if w.exists("devapp") || len(a.Status.BlastRadius) != len(req.Status.BlastRadius) {
			t.Errorf("devapp exists %v; removed %d, reviewed %d", w.exists("devapp"), len(a.Status.BlastRadius), len(req.Status.BlastRadius))
		}
	})

	// Criterion 7: a target changed after the request is never deleted.
	t.Run("stale target", func(t *testing.T) {
		req := w.requestApproval("tok-agent", deleteRequest(w.scope("app"), true, "retire app"), http.StatusCreated)
		app := w.current("/v1/applications/"+w.id["app"], "")
		app["metadata"].(map[string]any)["name"] = "app-renamed"
		w.expect("key-ci", http.MethodPut, "/v1/applications/"+w.id["app"], app, http.StatusOK)
		w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+req.Metadata.ID+"/approve", nil, http.StatusOK)
		a := awaitApproval(t, w, req.Metadata.ID, "failed", func(a resource.Approval) bool { return a.Status.Phase == resource.ApprovalFailed })
		if c := condition(a, resource.ApprovalConditionExecuted); c.Reason != reconcile.ReasonStaleTarget || !a.Status.Settled {
			t.Errorf("Executed = %+v, settled %v; want StaleTarget, settled", c, a.Status.Settled)
		}
		if !w.exists("app") || !w.exists("secret") {
			t.Error("a stale approval deleted something")
		}
	})

	// Criterion 6: a rejected request never deletes anything.
	t.Run("reject", func(t *testing.T) {
		req := w.requestApproval("tok-agent", deleteRequest(w.scope("dev"), true, "drop dev"), http.StatusCreated)
		w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+req.Metadata.ID+"/reject", nil, http.StatusOK)
		time.Sleep(200 * time.Millisecond)
		if !w.exists("dev") {
			t.Error("a rejected request deleted its target")
		}
	})

	// Criterion 15: a deleted Secret's value is erased afterwards; while the
	// provider is down the approval waits, across an engine restart, and is
	// verified only once the value is gone.
	t.Run("secret value erased, across an outage and a restart", func(t *testing.T) {
		values.setDown(true)
		got := w.do("tok-contrib", http.MethodDelete, "/v1/secrets/"+w.id["secret"], nil)
		if got.Code != http.StatusNoContent {
			t.Fatalf("DELETE secret: %d %s", got.Code, got.Body)
		}
		id := got.Header().Get("Serendip-Approval-Id")
		awaitApproval(t, w, id, "waiting for its capability", func(a resource.Approval) bool {
			return condition(a, resource.ApprovalConditionCleanedUp).Reason == reconcile.ReasonProviderNotReady
		})
		if !values.has(w.id["secret"]) {
			t.Fatal("the value went while the provider was down")
		}
		if w.exists("secret") {
			t.Fatal("the Secret's record is still there")
		}

		// serendipd restarts and the provider comes back: resync finds the
		// unsettled approval and finishes it.
		engine.stop()
		values.setDown(false)
		engine = startApprovalEngine(t, w, values)
		awaitApproval(t, w, id, "verified", verified)
		if values.has(w.id["secret"]) {
			t.Error("the value outlived its verified approval")
		}
	})
}
