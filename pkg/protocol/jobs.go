package protocol

import "time"

// JobType identifies the kind of work a job represents.
type JobType string

const (
	// JobTypeDeepProbe requests a deep runtime status probe.
	JobTypeDeepProbe JobType = "deep_probe"
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	// JobStatusPending means the job has been created but not yet claimed by a sidecar.
	JobStatusPending JobStatus = "pending"
	// JobStatusClaimed means a sidecar has claimed the job and is working on it.
	JobStatusClaimed JobStatus = "claimed"
	// JobStatusCompleted means the job finished successfully.
	JobStatusCompleted JobStatus = "completed"
	// JobStatusFailed means the job finished with an error.
	JobStatusFailed JobStatus = "failed"
)

// Job is a unit of work assigned to a node.
type Job struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"nodeId"`
	Type        JobType   `json:"type"`
	Status      JobStatus `json:"status"`
	PayloadJSON string    `json:"payloadJson,omitempty"`
	ResultJSON  string    `json:"resultJson,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	ClaimedAt   time.Time `json:"claimedAt,omitempty"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
}

// CreateJobRequest is the server-side request to create a new job for a node.
type CreateJobRequest struct {
	Type        JobType `json:"type"`
	PayloadJSON string  `json:"payloadJson,omitempty"`
}

// JobResultRequest is the sidecar's submission of a job result.
type JobResultRequest struct {
	Status     JobStatus `json:"status"`
	ResultJSON string    `json:"resultJson,omitempty"`
	Error      string    `json:"error,omitempty"`
}
