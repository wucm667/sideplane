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
	// JobTypeRollback requests restore of a sidecar-reported backup.
	JobTypeRollback JobType = "rollback"
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

// BulkJobRequest creates one job per matched node. Exactly one of Selector or
// NodeIDs must be set. Type currently must be deep_probe.
type BulkJobRequest struct {
	Selector           map[string]string `json:"selector,omitempty"`
	NodeIDs            []string          `json:"nodeIds,omitempty"`
	Type               JobType           `json:"type"`
	IncludeMaintenance bool              `json:"includeMaintenance,omitempty"`
}

// BulkJobResult is one matched node's outcome in a bulk job creation.
type BulkJobResult struct {
	NodeID string `json:"nodeId"`
	JobID  string `json:"jobId,omitempty"`
	Error  string `json:"error,omitempty"`
}

// BulkJobResponse returns the per-node outcomes and the count of jobs created.
type BulkJobResponse struct {
	Jobs    []BulkJobResult `json:"jobs"`
	Created int             `json:"created"`
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
	Backup     *RollbackBackup   `json:"backup,omitempty"`
	TempPath   string            `json:"tempPath,omitempty"`
	Steps      []ConfigApplyStep `json:"steps"`
}

// RollbackBackup is the server-known metadata for a sidecar-reported config
// backup. Paths remain sidecar-local; operators reference backups by Ref.
type RollbackBackup struct {
	Ref         string    `json:"ref"`
	SourceJobID string    `json:"sourceJobId"`
	PlanID      string    `json:"planId,omitempty"`
	RuntimeType string    `json:"runtimeType,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	ConfigHash  string    `json:"configHash,omitempty"`
	ConfigPath  string    `json:"configPath,omitempty"`
	BackupPath  string    `json:"backupPath,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
}

// RollbackBackupInventoryItem is the operator-facing backup list shape. It
// omits sidecar-local file paths while retaining the stable rollback Ref.
type RollbackBackupInventoryItem struct {
	Ref         string    `json:"ref"`
	SourceJobID string    `json:"sourceJobId"`
	RuntimeType string    `json:"runtimeType,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	ConfigHash  string    `json:"configHash,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
}

// ListRollbackBackupsResponse is a paginated node backup inventory response.
type ListRollbackBackupsResponse struct {
	Backups []RollbackBackupInventoryItem `json:"backups"`
	Total   int                           `json:"total"`
	Limit   int                           `json:"limit"`
}

// RestartJobPayload is the explicit operator request executed by a sidecar.
type RestartJobPayload struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Reason      string `json:"reason,omitempty"`
	DryRun      bool   `json:"dryRun"`
}

// RestartRequest is the operator API request to enqueue a restart job.
type RestartRequest struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Live        bool   `json:"live,omitempty"`
}

// RestartJobResult is the structured sidecar result for restart jobs.
type RestartJobResult struct {
	Controller   string            `json:"controller,omitempty"`
	Steps        []ConfigApplyStep `json:"steps"`
	HealthStatus string            `json:"healthStatus,omitempty"`
}

// RollbackRequest is the operator API request to enqueue a rollback job.
type RollbackRequest struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Profile     string `json:"profile,omitempty"`
	BackupRef   string `json:"backupRef"`
	Live        bool   `json:"live,omitempty"`
}

// RollbackJobPayload is the server-derived rollback job sent to a sidecar.
type RollbackJobPayload struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Profile     string `json:"profile,omitempty"`
	BackupRef   string `json:"backupRef"`
	ConfigPath  string `json:"configPath"`
	BackupPath  string `json:"backupPath"`
	DryRun      bool   `json:"dryRun"`
}

// RollbackJobResult is the structured sidecar result for rollback jobs.
type RollbackJobResult struct {
	BackupRef    string            `json:"backupRef"`
	Steps        []ConfigApplyStep `json:"steps"`
	HealthStatus string            `json:"healthStatus,omitempty"`
}
