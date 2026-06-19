package protocol

import "time"

// JobType identifies the kind of work a job represents.
type JobType string

const (
	// JobTypeDeepProbe requests a deep runtime status probe.
	JobTypeDeepProbe JobType = "deep_probe"
	// JobTypeConfigApply requests a signed configuration apply.
	JobTypeConfigApply JobType = "config_apply"
	// JobTypeRestart requests an allowlisted runtime service restart.
	JobTypeRestart JobType = "restart"
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
	ID             string    `json:"id"`
	NodeID         string    `json:"nodeId"`
	Type           JobType   `json:"type"`
	Status         JobStatus `json:"status"`
	PayloadJSON    string    `json:"payloadJson,omitempty"`
	ResultJSON     string    `json:"resultJson,omitempty"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ClaimedAt      time.Time `json:"claimedAt,omitzero"`
	ClaimExpiresAt time.Time `json:"claimExpiresAt,omitzero"`
	FinishedAt     time.Time `json:"finishedAt,omitzero"`
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

// DeepProbeResult is the stable result payload for deep_probe jobs.
type DeepProbeResult struct {
	Runtimes        []RuntimeStatus         `json:"runtimes"`
	ConfigSnapshots []RuntimeConfigSnapshot `json:"configSnapshots"`
}

// ConfigApplyStep is one reported step in the config apply pipeline.
type ConfigApplyStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ConfigApplyResult is the dry-run/live config apply result payload.
type ConfigApplyResult struct {
	PlanID     string            `json:"planId"`
	DryRun     bool              `json:"dryRun"`
	BackupPath string            `json:"backupPath,omitempty"`
	TempPath   string            `json:"tempPath,omitempty"`
	Steps      []ConfigApplyStep `json:"steps"`
}

// RestartJobPayload is the explicit operator request executed by a sidecar.
type RestartJobPayload struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Reason      string `json:"reason,omitempty"`
	DryRun      bool   `json:"dryRun"`
}

// RestartJobResult is the structured sidecar result for restart jobs.
type RestartJobResult struct {
	Controller   string            `json:"controller,omitempty"`
	Steps        []ConfigApplyStep `json:"steps"`
	HealthStatus string            `json:"healthStatus,omitempty"`
}
