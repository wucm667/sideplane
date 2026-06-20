package protocol

import "time"

// RolloutState is the lifecycle state of a staged fleet rollout.
type RolloutState string

const (
	RolloutStatePending   RolloutState = "pending"
	RolloutStateRunning   RolloutState = "running"
	RolloutStatePaused    RolloutState = "paused"
	RolloutStateCompleted RolloutState = "completed"
	RolloutStateAborted   RolloutState = "aborted"
	RolloutStateFailed    RolloutState = "failed"
)

// RolloutBatchState is the lifecycle state of one rollout batch.
type RolloutBatchState string

const (
	RolloutBatchStatePending   RolloutBatchState = "pending"
	RolloutBatchStateRunning   RolloutBatchState = "running"
	RolloutBatchStateCompleted RolloutBatchState = "completed"
	RolloutBatchStatePaused    RolloutBatchState = "paused"
	RolloutBatchStateFailed    RolloutBatchState = "failed"
)

// RolloutNodeState is the per-node progress state within a rollout batch.
type RolloutNodeState string

const (
	RolloutNodeStatePending    RolloutNodeState = "pending"
	RolloutNodeStateDispatched RolloutNodeState = "dispatched"
	RolloutNodeStateSucceeded  RolloutNodeState = "succeeded"
	RolloutNodeStateFailed     RolloutNodeState = "failed"
	RolloutNodeStateTimedOut   RolloutNodeState = "timed_out"
	RolloutNodeStateOffline    RolloutNodeState = "offline"
)

// RolloutAction is an operator action against a rollout.
type RolloutAction string

const (
	RolloutActionPause  RolloutAction = "pause"
	RolloutActionResume RolloutAction = "resume"
	RolloutActionAbort  RolloutAction = "abort"
)

// RolloutSpec describes a staged provider/model rollout target and selection.
type RolloutSpec struct {
	Selector      map[string]string   `json:"selector,omitempty"`
	NodeIDs       []string            `json:"nodeIds,omitempty"`
	RuntimeType   string              `json:"runtimeType"`
	Profile       string              `json:"profile,omitempty"`
	Target        ProviderModelConfig `json:"target"`
	BatchSize     int                 `json:"batchSize,omitempty"`
	Live          bool                `json:"live"`
	HealthTimeout time.Duration       `json:"healthTimeout,omitempty"`
	// AutoRollbackOnFailure opts a live rollout into per-node rollback of the
	// failed batch's already-applied nodes before the rollout pauses. Default
	// false preserves the existing pause-only behavior. Never applies to
	// dry-run rollouts.
	AutoRollbackOnFailure bool `json:"autoRollbackOnFailure,omitempty"`
}

// RolloutNodeProgress tracks one node's rollout job and health state.
type RolloutNodeProgress struct {
	NodeID     string           `json:"nodeId"`
	JobID      string           `json:"jobId,omitempty"`
	State      RolloutNodeState `json:"state"`
	LastError  string           `json:"lastError,omitempty"`
	StartedAt  time.Time        `json:"startedAt,omitzero"`
	FinishedAt time.Time        `json:"finishedAt,omitzero"`
	// RollbackJobID references the per-node rollback job dispatched by an
	// auto-rollback when the node's batch failed. Empty when no rollback was
	// attempted for this node.
	RollbackJobID string `json:"rollbackJobId,omitempty"`
	// RolledBack reports whether an auto-rollback was dispatched for this node.
	RolledBack bool `json:"rolledBack,omitempty"`
}

// RolloutBatch is one sequential batch in a staged rollout.
type RolloutBatch struct {
	Index   int                            `json:"index"`
	NodeIDs []string                       `json:"nodeIds"`
	State   RolloutBatchState              `json:"state"`
	Nodes   map[string]RolloutNodeProgress `json:"nodes"`
}

// Rollout is the operator-visible staged fleet rollout.
type Rollout struct {
	ID             string         `json:"id"`
	Spec           RolloutSpec    `json:"spec"`
	State          RolloutState   `json:"state"`
	Batches        []RolloutBatch `json:"batches"`
	PauseReason    string         `json:"pauseReason,omitempty"`
	FailingNodeIDs []string       `json:"failingNodeIds,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	FinishedAt     time.Time      `json:"finishedAt,omitzero"`
}

// CreateRolloutRequest creates a rollout from the provided spec.
type CreateRolloutRequest struct {
	Spec RolloutSpec `json:"spec"`
}

// CreateRolloutResponse returns the created rollout.
type CreateRolloutResponse struct {
	Rollout Rollout `json:"rollout"`
}

// ListRolloutsResponse is a paginated rollout list response.
type ListRolloutsResponse struct {
	Rollouts []Rollout `json:"rollouts"`
	Total    int       `json:"total"`
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
}

// GetRolloutResponse returns one rollout.
type GetRolloutResponse struct {
	Rollout Rollout `json:"rollout"`
}

// RolloutActionRequest applies an operator action to a rollout.
type RolloutActionRequest struct {
	Action RolloutAction `json:"action"`
}

// RolloutActionResponse returns the rollout after an operator action.
type RolloutActionResponse struct {
	Rollout Rollout `json:"rollout"`
}
