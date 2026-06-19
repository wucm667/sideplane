package rollout

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestPlanBatchesCanaryFirst(t *testing.T) {
	batches := PlanBatches([]string{"node-a", "node-b", "node-c"}, 2)
	if len(batches) != 2 {
		t.Fatalf("batches length = %d, want 2", len(batches))
	}
	if got := batches[0].NodeIDs; len(got) != 2 || got[0] != "node-a" || got[1] != "node-b" {
		t.Fatalf("first batch nodes = %#v, want node-a/node-b", got)
	}
	if got := batches[1].NodeIDs; len(got) != 1 || got[0] != "node-c" {
		t.Fatalf("second batch nodes = %#v, want node-c", got)
	}
}

func TestEngineDryRunCompletesSequentialBatches(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)}
	dispatcher := &fakeDispatcher{}
	health := &fakeHealth{byJob: map[string]NodeHealth{}}
	engine := Engine{Clock: clock, Dispatcher: dispatcher, Health: health}
	rollout := rolloutForEngineTest([]string{"node-a", "node-b"}, 1, false, clock.Now())

	var err error
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("first step: %v", err)
	}
	if rollout.State != protocol.RolloutStateRunning || len(dispatcher.jobs) != 1 {
		t.Fatalf("after first step rollout=%#v jobs=%#v, want running one job", rollout, dispatcher.jobs)
	}
	health.byJob[dispatcher.jobs["node-a"]] = NodeHealth{ApplySucceeded: true}
	clock.advance(time.Second)
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("second step: %v", err)
	}
	if rollout.Batches[0].State != protocol.RolloutBatchStateCompleted || len(dispatcher.jobs) != 2 {
		t.Fatalf("after second step rollout=%#v jobs=%#v, want first batch done second dispatched", rollout, dispatcher.jobs)
	}
	health.byJob[dispatcher.jobs["node-b"]] = NodeHealth{ApplySucceeded: true}
	clock.advance(time.Second)
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("third step: %v", err)
	}
	if rollout.State != protocol.RolloutStateCompleted || rollout.FinishedAt.IsZero() {
		t.Fatalf("rollout state = %#v, want completed", rollout)
	}
}

func TestEnginePausesOnFailureOfflineAndTimeout(t *testing.T) {
	tests := []struct {
		name       string
		health     NodeHealth
		advance    time.Duration
		wantState  protocol.RolloutNodeState
		wantReason string
	}{
		{name: "failed", health: NodeHealth{ApplyFailed: true, Error: "apply failed"}, wantState: protocol.RolloutNodeStateFailed, wantReason: "config apply failed"},
		{name: "offline", health: NodeHealth{Offline: true}, wantState: protocol.RolloutNodeStateOffline, wantReason: "node offline"},
		{name: "timeout", advance: 6 * time.Minute, wantState: protocol.RolloutNodeStateTimedOut, wantReason: "health timeout exceeded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)}
			dispatcher := &fakeDispatcher{}
			health := &fakeHealth{byJob: map[string]NodeHealth{}}
			engine := Engine{Clock: clock, Dispatcher: dispatcher, Health: health}
			rollout := rolloutForEngineTest([]string{"node-a", "node-b"}, 1, false, clock.Now())
			var err error
			rollout, err = engine.Step(context.Background(), rollout)
			if err != nil {
				t.Fatalf("dispatch step: %v", err)
			}
			if tt.advance > 0 {
				clock.advance(tt.advance)
			} else {
				health.byJob[dispatcher.jobs["node-a"]] = tt.health
				clock.advance(time.Second)
			}
			rollout, err = engine.Step(context.Background(), rollout)
			if err != nil {
				t.Fatalf("health step: %v", err)
			}
			if rollout.State != protocol.RolloutStatePaused || rollout.PauseReason != tt.wantReason {
				t.Fatalf("rollout = %#v, want paused reason %q", rollout, tt.wantReason)
			}
			if len(dispatcher.jobs) != 1 {
				t.Fatalf("jobs = %#v, want no further dispatch after pause", dispatcher.jobs)
			}
			gotNode := rollout.Batches[0].Nodes["node-a"]
			if gotNode.State != tt.wantState {
				t.Fatalf("node state = %q, want %q", gotNode.State, tt.wantState)
			}
		})
	}
}

func TestEngineLiveWaitsForNoDrift(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)}
	dispatcher := &fakeDispatcher{}
	health := &fakeHealth{byJob: map[string]NodeHealth{}}
	engine := Engine{Clock: clock, Dispatcher: dispatcher, Health: health}
	rollout := rolloutForEngineTest([]string{"node-a"}, 1, true, clock.Now())

	var err error
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("dispatch step: %v", err)
	}
	health.byJob[dispatcher.jobs["node-a"]] = NodeHealth{ApplySucceeded: true, Drift: true}
	clock.advance(time.Second)
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("drift step: %v", err)
	}
	if rollout.State != protocol.RolloutStateRunning {
		t.Fatalf("rollout state = %q, want running while drift remains", rollout.State)
	}

	health.byJob[dispatcher.jobs["node-a"]] = NodeHealth{ApplySucceeded: true, Drift: false}
	clock.advance(time.Second)
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("healthy step: %v", err)
	}
	if rollout.State != protocol.RolloutStateCompleted {
		t.Fatalf("rollout state = %q, want completed after drift clears", rollout.State)
	}
}

func TestResumeRedispatchesUnfinishedNodesAndAbortTerminates(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)}
	dispatcher := &fakeDispatcher{}
	health := &fakeHealth{byJob: map[string]NodeHealth{}}
	engine := Engine{Clock: clock, Dispatcher: dispatcher, Health: health}
	rollout := rolloutForEngineTest([]string{"node-a"}, 1, false, clock.Now())

	var err error
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("dispatch step: %v", err)
	}
	health.byJob[dispatcher.jobs["node-a"]] = NodeHealth{ApplyFailed: true}
	clock.advance(time.Second)
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("pause step: %v", err)
	}
	if rollout.State != protocol.RolloutStatePaused {
		t.Fatalf("state = %q, want paused", rollout.State)
	}

	rollout = Resume(rollout, clock.Now())
	delete(health.byJob, dispatcher.jobs["node-a"])
	dispatcher.jobs = map[string]string{}
	rollout, err = engine.Step(context.Background(), rollout)
	if err != nil {
		t.Fatalf("resume dispatch step: %v", err)
	}
	if rollout.State != protocol.RolloutStateRunning || len(dispatcher.jobs) != 1 {
		t.Fatalf("resumed rollout=%#v jobs=%#v, want redispatched running rollout", rollout, dispatcher.jobs)
	}

	rollout = Abort(rollout, clock.Now())
	if rollout.State != protocol.RolloutStateAborted || rollout.FinishedAt.IsZero() {
		t.Fatalf("aborted rollout = %#v, want terminal aborted", rollout)
	}
}

func rolloutForEngineTest(nodeIDs []string, batchSize int, live bool, now time.Time) protocol.Rollout {
	return protocol.Rollout{
		ID:    "rollout-test",
		State: protocol.RolloutStatePending,
		Spec: protocol.RolloutSpec{
			NodeIDs:       append([]string(nil), nodeIDs...),
			RuntimeType:   "hermes",
			Profile:       "default",
			Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			BatchSize:     batchSize,
			Live:          live,
			HealthTimeout: DefaultHealthTimeout,
		},
		Batches:   PlanBatches(nodeIDs, batchSize),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type fakeDispatcher struct {
	jobs map[string]string
}

func (d *fakeDispatcher) DispatchConfigApply(_ context.Context, _ protocol.Rollout, nodeID string) (string, error) {
	if d.jobs == nil {
		d.jobs = map[string]string{}
	}
	jobID := fmt.Sprintf("job-%s-%d", nodeID, len(d.jobs)+1)
	d.jobs[nodeID] = jobID
	return jobID, nil
}

type fakeHealth struct {
	byJob map[string]NodeHealth
}

func (h *fakeHealth) NodeHealth(_ context.Context, _ protocol.Rollout, node protocol.RolloutNodeProgress) (NodeHealth, error) {
	return h.byJob[node.JobID], nil
}
