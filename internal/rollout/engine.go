package rollout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const DefaultHealthTimeout = 5 * time.Minute

// Clock supplies time to the pure rollout stepper.
type Clock interface {
	Now() time.Time
}

// DispatchConfigApply creates one config-apply job for a rollout node.
type DispatchConfigApply interface {
	DispatchConfigApply(ctx context.Context, rollout protocol.Rollout, nodeID string) (jobID string, err error)
}

// HealthReader reports whether a dispatched node has met rollout health gates.
type HealthReader interface {
	NodeHealth(ctx context.Context, rollout protocol.Rollout, node protocol.RolloutNodeProgress) (NodeHealth, error)
}

// DispatchRollback creates one per-node rollback job restoring the most recent
// pre-rollout backup, returning the rollback job ID. It is used only for
// opt-in auto-rollback of already-applied nodes in a failed live batch.
type DispatchRollback interface {
	DispatchRollback(ctx context.Context, rollout protocol.Rollout, nodeID string) (jobID string, err error)
}

// NodeHealth is the engine's normalized view of apply-job and node health.
type NodeHealth struct {
	ApplySucceeded bool
	ApplyFailed    bool
	Offline        bool
	Drift          bool
	Error          string
}

// Engine reconciles one rollout snapshot at a time. It starts no goroutines
// and owns no persistence.
type Engine struct {
	Clock      Clock
	Dispatcher DispatchConfigApply
	Health     HealthReader
	// Rollback is optional. When set, a live rollout with
	// AutoRollbackOnFailure enabled rolls back already-applied nodes of a
	// failed batch before pausing.
	Rollback DispatchRollback
}

// PlanBatches splits resolved node IDs into sequential rollout batches.
func PlanBatches(nodeIDs []string, batchSize int) []protocol.RolloutBatch {
	if batchSize <= 0 {
		batchSize = 1
	}
	batches := []protocol.RolloutBatch{}
	for start := 0; start < len(nodeIDs); start += batchSize {
		end := start + batchSize
		if end > len(nodeIDs) {
			end = len(nodeIDs)
		}
		nodes := append([]string(nil), nodeIDs[start:end]...)
		progress := make(map[string]protocol.RolloutNodeProgress, len(nodes))
		for _, nodeID := range nodes {
			progress[nodeID] = protocol.RolloutNodeProgress{NodeID: nodeID, State: protocol.RolloutNodeStatePending}
		}
		batches = append(batches, protocol.RolloutBatch{
			Index:   len(batches),
			NodeIDs: nodes,
			State:   protocol.RolloutBatchStatePending,
			Nodes:   progress,
		})
	}
	return batches
}

// Step advances one rollout snapshot by at most one reconciliation step.
func (e Engine) Step(ctx context.Context, rollout protocol.Rollout) (protocol.Rollout, error) {
	now := e.now()
	if rollout.CreatedAt.IsZero() {
		rollout.CreatedAt = now
	}
	if terminal(rollout.State) || rollout.State == protocol.RolloutStatePaused {
		return rollout, nil
	}
	if rolloutScheduledForFuture(rollout, now) {
		if rollout.State != protocol.RolloutStateScheduled {
			rollout.State = protocol.RolloutStateScheduled
			rollout.UpdatedAt = now
		}
		return rollout, nil
	}
	rollout.UpdatedAt = now
	if len(rollout.Batches) == 0 {
		rollout.State = protocol.RolloutStateCompleted
		rollout.FinishedAt = now
		return rollout, nil
	}
	if rollout.State == "" || rollout.State == protocol.RolloutStatePending || rollout.State == protocol.RolloutStateScheduled {
		rollout.State = protocol.RolloutStateRunning
	}
	if rollout.State != protocol.RolloutStateRunning {
		return rollout, nil
	}

	active := activeBatchIndex(rollout.Batches)
	if active < 0 {
		rollout.State = protocol.RolloutStateCompleted
		rollout.FinishedAt = now
		return rollout, nil
	}
	batch := &rollout.Batches[active]
	if batch.State == protocol.RolloutBatchStatePending {
		batch.State = protocol.RolloutBatchStateRunning
	}
	ensureBatchProgress(batch)
	var err error
	if rollout, err = e.dispatchPending(ctx, rollout, active, now); err != nil {
		return rollout, err
	}
	if rollout.State == protocol.RolloutStatePaused {
		return rollout, nil
	}

	updated, failed := e.evaluateBatch(ctx, rollout, active, now)
	if failed {
		return updated, nil
	}
	rollout = updated
	if batchComplete(rollout.Batches[active]) {
		rollout.Batches[active].State = protocol.RolloutBatchStateCompleted
		next := activeBatchIndex(rollout.Batches)
		if next < 0 {
			rollout.State = protocol.RolloutStateCompleted
			rollout.FinishedAt = now
		} else {
			if rollout.Batches[next].State == protocol.RolloutBatchStatePending {
				rollout.Batches[next].State = protocol.RolloutBatchStateRunning
			}
			ensureBatchProgress(&rollout.Batches[next])
			rollout, err = e.dispatchPending(ctx, rollout, next, now)
			if err != nil {
				return rollout, err
			}
			if rollout.State == protocol.RolloutStatePaused {
				return rollout, nil
			}
		}
	}
	return rollout, nil
}

// Resume returns a paused rollout to running and re-queues unfinished active nodes.
func Resume(rollout protocol.Rollout, now time.Time) protocol.Rollout {
	if rollout.State != protocol.RolloutStatePaused {
		return rollout
	}
	rollout.State = protocol.RolloutStateRunning
	rollout.PauseReason = ""
	rollout.FailingNodeIDs = nil
	rollout.UpdatedAt = now.UTC()
	for i := range rollout.Batches {
		if rollout.Batches[i].State != protocol.RolloutBatchStatePaused && rollout.Batches[i].State != protocol.RolloutBatchStateFailed {
			continue
		}
		rollout.Batches[i].State = protocol.RolloutBatchStateRunning
		for nodeID, progress := range rollout.Batches[i].Nodes {
			if progress.State == protocol.RolloutNodeStateSucceeded {
				continue
			}
			rollout.Batches[i].Nodes[nodeID] = protocol.RolloutNodeProgress{NodeID: nodeID, State: protocol.RolloutNodeStatePending}
		}
		break
	}
	return rollout
}

// Abort marks a non-terminal rollout aborted.
func Abort(rollout protocol.Rollout, now time.Time) protocol.Rollout {
	if terminal(rollout.State) {
		return rollout
	}
	rollout.State = protocol.RolloutStateAborted
	rollout.UpdatedAt = now.UTC()
	rollout.FinishedAt = now.UTC()
	return rollout
}

func (e Engine) evaluateBatch(ctx context.Context, rollout protocol.Rollout, batchIndex int, now time.Time) (protocol.Rollout, bool) {
	if e.Health == nil {
		return rollout, false
	}
	batch := &rollout.Batches[batchIndex]
	timeout := rollout.Spec.HealthTimeout
	if timeout <= 0 {
		timeout = DefaultHealthTimeout
	}
	for _, nodeID := range batch.NodeIDs {
		node := batch.Nodes[nodeID]
		if node.State == protocol.RolloutNodeStateSucceeded {
			continue
		}
		if node.State != protocol.RolloutNodeStateDispatched {
			continue
		}
		if !node.StartedAt.IsZero() && !now.Before(node.StartedAt.Add(timeout)) {
			node.State = protocol.RolloutNodeStateTimedOut
			node.LastError = "health timeout exceeded"
			node.FinishedAt = now
			batch.Nodes[nodeID] = node
			return e.pauseBatchFailure(ctx, rollout, batchIndex, now, "health timeout exceeded", []string{nodeID}), true
		}
		health, err := e.Health.NodeHealth(ctx, rollout, node)
		if err != nil {
			node.State = protocol.RolloutNodeStateFailed
			node.LastError = err.Error()
			node.FinishedAt = now
			batch.Nodes[nodeID] = node
			return e.pauseBatchFailure(ctx, rollout, batchIndex, now, "health check failed: "+err.Error(), []string{nodeID}), true
		}
		switch {
		case health.Offline:
			node.State = protocol.RolloutNodeStateOffline
			node.LastError = firstNonEmpty(health.Error, "node offline")
			node.FinishedAt = now
			batch.Nodes[nodeID] = node
			return e.pauseBatchFailure(ctx, rollout, batchIndex, now, "node offline", []string{nodeID}), true
		case health.ApplyFailed:
			node.State = protocol.RolloutNodeStateFailed
			node.LastError = firstNonEmpty(health.Error, "config apply failed")
			node.FinishedAt = now
			batch.Nodes[nodeID] = node
			return e.pauseBatchFailure(ctx, rollout, batchIndex, now, "config apply failed", []string{nodeID}), true
		case health.ApplySucceeded && (!rollout.Spec.Live || !health.Drift):
			node.State = protocol.RolloutNodeStateSucceeded
			node.FinishedAt = now
			batch.Nodes[nodeID] = node
		}
	}
	return rollout, false
}

func (e Engine) dispatchPending(ctx context.Context, rollout protocol.Rollout, batchIndex int, now time.Time) (protocol.Rollout, error) {
	batch := &rollout.Batches[batchIndex]
	for _, nodeID := range batch.NodeIDs {
		node := batch.Nodes[nodeID]
		if node.State != "" && node.State != protocol.RolloutNodeStatePending {
			continue
		}
		if e.Dispatcher == nil {
			return rollout, errors.New("rollout dispatcher is required")
		}
		jobID, err := e.Dispatcher.DispatchConfigApply(ctx, rollout, nodeID)
		if err != nil {
			return e.pauseBatchFailure(ctx, rollout, batchIndex, now, "dispatch failed: "+err.Error(), []string{nodeID}), nil
		}
		node.NodeID = nodeID
		node.JobID = jobID
		node.State = protocol.RolloutNodeStateDispatched
		node.StartedAt = now
		batch.Nodes[nodeID] = node
	}
	return rollout, nil
}

// pauseBatchFailure dispatches per-node rollbacks for already-applied nodes of
// a failed live batch (only when opted in) and then pauses the rollout. The
// rollback dispatch is best-effort and never retried, so a rollback failure
// cannot trigger another rollback. Dry-run rollouts are never rolled back.
func (e Engine) pauseBatchFailure(ctx context.Context, rollout protocol.Rollout, batchIndex int, now time.Time, reason string, failingNodeIDs []string) protocol.Rollout {
	attempted := 0
	if rollout.Spec.Live && rollout.Spec.AutoRollbackOnFailure && e.Rollback != nil && batchIndex >= 0 && batchIndex < len(rollout.Batches) {
		batch := &rollout.Batches[batchIndex]
		for _, nodeID := range batch.NodeIDs {
			node := batch.Nodes[nodeID]
			// Only already-applied nodes carry the new (failed) config; the
			// failing node itself never applied successfully.
			if node.State != protocol.RolloutNodeStateSucceeded || node.RolledBack || node.RollbackJobID != "" {
				continue
			}
			jobID, err := e.Rollback.DispatchRollback(ctx, rollout, nodeID)
			attempted++
			if err != nil {
				node.LastError = firstNonEmpty(node.LastError, "auto-rollback dispatch failed: "+err.Error())
			} else {
				node.RollbackJobID = jobID
				node.RolledBack = true
			}
			batch.Nodes[nodeID] = node
		}
	}
	if attempted > 0 {
		reason = strings.TrimSpace(reason) + fmt.Sprintf("; auto-rollback attempted for %d node(s)", attempted)
	}
	return pauseRollout(rollout, now, reason, failingNodeIDs)
}

func pauseRollout(rollout protocol.Rollout, now time.Time, reason string, nodeIDs []string) protocol.Rollout {
	rollout.State = protocol.RolloutStatePaused
	rollout.PauseReason = strings.TrimSpace(reason)
	rollout.FailingNodeIDs = append([]string(nil), nodeIDs...)
	rollout.UpdatedAt = now.UTC()
	for i := range rollout.Batches {
		if rollout.Batches[i].State == protocol.RolloutBatchStateRunning {
			rollout.Batches[i].State = protocol.RolloutBatchStatePaused
			break
		}
	}
	return rollout
}

func ensureBatchProgress(batch *protocol.RolloutBatch) {
	if batch.Nodes == nil {
		batch.Nodes = make(map[string]protocol.RolloutNodeProgress, len(batch.NodeIDs))
	}
	for _, nodeID := range batch.NodeIDs {
		if _, ok := batch.Nodes[nodeID]; !ok {
			batch.Nodes[nodeID] = protocol.RolloutNodeProgress{NodeID: nodeID, State: protocol.RolloutNodeStatePending}
		}
	}
}

func activeBatchIndex(batches []protocol.RolloutBatch) int {
	for i, batch := range batches {
		if batch.State == protocol.RolloutBatchStatePending || batch.State == protocol.RolloutBatchStateRunning || batch.State == protocol.RolloutBatchStatePaused || batch.State == protocol.RolloutBatchStateFailed {
			if batch.State == protocol.RolloutBatchStateCompleted {
				continue
			}
			return i
		}
	}
	return -1
}

func batchComplete(batch protocol.RolloutBatch) bool {
	for _, nodeID := range batch.NodeIDs {
		if batch.Nodes[nodeID].State != protocol.RolloutNodeStateSucceeded {
			return false
		}
	}
	return true
}

func terminal(state protocol.RolloutState) bool {
	return state == protocol.RolloutStateCompleted || state == protocol.RolloutStateAborted || state == protocol.RolloutStateFailed
}

func rolloutScheduledForFuture(rollout protocol.Rollout, now time.Time) bool {
	if rollout.Spec.StartAt.IsZero() {
		return false
	}
	return now.Before(rollout.Spec.StartAt.UTC())
}

func (e Engine) now() time.Time {
	if e.Clock == nil {
		return time.Now().UTC()
	}
	return e.Clock.Now().UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ValidateRolloutSpec(spec protocol.RolloutSpec) error {
	if len(spec.NodeIDs) == 0 && len(spec.Selector) == 0 {
		return fmt.Errorf("selector or nodeIds is required")
	}
	if strings.TrimSpace(spec.RuntimeType) == "" {
		return fmt.Errorf("runtimeType is required")
	}
	if strings.TrimSpace(spec.Target.Provider) == "" || strings.TrimSpace(spec.Target.Model) == "" {
		return fmt.Errorf("target provider and model are required")
	}
	return nil
}
