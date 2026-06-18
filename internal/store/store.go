package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	defaultJobClaimLease     = 5 * time.Minute
	configApplyJobClaimLease = 30 * time.Minute
	jobClaimTimeoutError     = "job claim timed out"
	// DefaultJobListLimit is the bounded default for node job history.
	DefaultJobListLimit = 50
	// MaxJobListLimit is the largest node job page size accepted by the store.
	MaxJobListLimit = 500
)

var (
	// ErrEnrollmentTokenInvalid means no matching enrollment token exists.
	ErrEnrollmentTokenInvalid = errors.New("enrollment token is invalid")
	// ErrEnrollmentTokenExpired means the matching enrollment token is past its expiry.
	ErrEnrollmentTokenExpired = errors.New("enrollment token is expired")
	// ErrEnrollmentTokenUsed means the matching enrollment token has already been used.
	ErrEnrollmentTokenUsed = errors.New("enrollment token has already been used")
	// ErrNodeAlreadyEnrolled means the node already has a long-lived credential.
	ErrNodeAlreadyEnrolled = errors.New("node is already enrolled")
	// ErrNodeNotFound means the requested node does not exist.
	ErrNodeNotFound = errors.New("node not found")
	// ErrActiveJobExists means the node already has an active job of that type.
	ErrActiveJobExists = errors.New("active job already exists")
	// ErrLateJobResultRecorded means a sidecar submitted a result after the
	// server had already timed out the job; the result was attached for audit.
	ErrLateJobResultRecorded = errors.New("late job result recorded after timeout")
)

func jobClaimLease(jobType protocol.JobType) time.Duration {
	if jobType == protocol.JobTypeConfigApply {
		return configApplyJobClaimLease
	}
	return defaultJobClaimLease
}

// IsJobClaimTimeout reports whether a job is a timeout failure, including
// timeout failures later annotated with a late sidecar result.
func IsJobClaimTimeout(job protocol.Job) bool {
	return job.Status == protocol.JobStatusFailed && strings.HasPrefix(job.Error, jobClaimTimeoutError)
}

func lateJobResultError(result protocol.JobResultRequest) string {
	status := strings.TrimSpace(string(result.Status))
	if status == "" {
		status = "unknown"
	}
	msg := jobClaimTimeoutError + "; late sidecar result status=" + status
	if detail := strings.TrimSpace(result.Error); detail != "" {
		msg += ": " + detail
	}
	return msg
}

// NodeStore persists heartbeat-derived node status snapshots.
type NodeStore interface {
	RecordHeartbeat(ctx context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error)
	ListNodes(ctx context.Context) ([]protocol.NodeStatus, error)
	NodeExists(ctx context.Context, nodeID string) (bool, error)
	DeleteNode(ctx context.Context, nodeID string) error
}

// EnrollmentStore persists one-time enrollment tokens and node credentials.
type EnrollmentStore interface {
	CreateEnrollmentToken(ctx context.Context, expiresAt time.Time, now time.Time) (protocol.CreateEnrollmentTokenResponse, error)
	EnrollNode(ctx context.Context, req protocol.EnrollNodeRequest, now time.Time) (protocol.EnrollNodeResponse, error)
	VerifyNodeCredential(ctx context.Context, nodeID string, credential string) (bool, error)
}

// JobStore persists server-assigned jobs and their lifecycle.
type JobStore interface {
	CreateJob(ctx context.Context, req protocol.CreateJobRequest, nodeID string, now time.Time) (protocol.Job, error)
	GetJob(ctx context.Context, jobID string) (*protocol.Job, error)
	ClaimNextJob(ctx context.Context, nodeID string, now time.Time) (*protocol.Job, error)
	CompleteJob(ctx context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error
	FailJob(ctx context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error
	ListNodeJobs(ctx context.Context, nodeID string) ([]protocol.Job, error)
	ListNodeJobsFiltered(ctx context.Context, nodeID string, filter JobFilter) ([]protocol.Job, error)
}

// JobFilter constrains node job listing.
type JobFilter struct {
	Limit  int
	Status protocol.JobStatus
}

func normalizeJobFilter(filter JobFilter) JobFilter {
	if filter.Limit <= 0 {
		filter.Limit = DefaultJobListLimit
	}
	if filter.Limit > MaxJobListLimit {
		filter.Limit = MaxJobListLimit
	}
	return filter
}

// AuditStore persists bounded audit events for operator-visible history.
type AuditStore interface {
	AppendAuditEvent(ctx context.Context, event protocol.AuditEvent) (protocol.AuditEvent, error)
	ListAuditEvents(ctx context.Context, limit int) ([]protocol.AuditEvent, error)
	ListAuditEventsFiltered(ctx context.Context, filter AuditFilter) ([]protocol.AuditEvent, error)
}

// AuditFilter constrains audit event listing.
type AuditFilter struct {
	NodeID string
	Action string
	Limit  int
}

// DesiredConfigStore persists the layered desired runtime config.
type DesiredConfigStore interface {
	GetDesiredConfig(ctx context.Context) (protocol.DesiredConfig, error)
	SetDesiredConfig(ctx context.Context, desired protocol.DesiredConfig, now time.Time) error
}

// Store is the complete persistence contract currently required by the server.
type Store interface {
	NodeStore
	EnrollmentStore
	JobStore
	AuditStore
	DesiredConfigStore
}
