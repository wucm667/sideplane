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
	// DefaultNodeListLimit is the bounded default for fleet inventory listing.
	DefaultNodeListLimit = 100
	// MaxNodeListLimit is the largest fleet inventory page size accepted by the store.
	MaxNodeListLimit = 1000
	// DefaultHeartbeatRetention is the default number of recent heartbeats to keep per node.
	DefaultHeartbeatRetention = 100
	// DefaultJobRetention is the default age to retain completed and failed jobs.
	DefaultJobRetention = 30 * 24 * time.Hour
	// DefaultAuditRetention is the default age to retain audit events.
	DefaultAuditRetention = 180 * 24 * time.Hour
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
	ListNodesFiltered(ctx context.Context, filter NodeFilter) (NodeList, error)
	NodeExists(ctx context.Context, nodeID string) (bool, error)
	SetNodeLabels(ctx context.Context, nodeID string, labels map[string]string) error
	GetNodeLabels(ctx context.Context, nodeID string) (map[string]string, error)
	DeleteNode(ctx context.Context, nodeID string) error
	PruneHeartbeats(ctx context.Context, keep int) (int64, error)
}

// NodeFilter constrains fleet inventory listing.
type NodeFilter struct {
	Limit  int
	Offset int
}

// NodeList is a paginated fleet inventory snapshot.
type NodeList struct {
	Nodes  []protocol.NodeStatus
	Total  int
	Limit  int
	Offset int
}

const (
	// MaxNodeLabelKeyLength bounds operator-managed label keys.
	MaxNodeLabelKeyLength = 63
	// MaxNodeLabelValueLength bounds operator-managed label values.
	MaxNodeLabelValueLength = 255
)

// ValidateNodeLabels returns a trimmed copy of labels when all keys and values
// fit Sideplane's operator metadata constraints.
func ValidateNodeLabels(labels map[string]string) (map[string]string, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(labels))
	for rawKey, rawValue := range labels {
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)
		if key == "" {
			return nil, errors.New("label key is required")
		}
		if len(key) > MaxNodeLabelKeyLength {
			return nil, errors.New("label key is too long")
		}
		if len(value) > MaxNodeLabelValueLength {
			return nil, errors.New("label value is too long")
		}
		if hasControlCharacter(key) || hasControlCharacter(value) {
			return nil, errors.New("label key and value must not contain control characters")
		}
		normalized[key] = value
	}
	return normalized, nil
}

func hasControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func NormalizeNodeFilter(filter NodeFilter) NodeFilter {
	if filter.Limit <= 0 {
		filter.Limit = DefaultNodeListLimit
	}
	if filter.Limit > MaxNodeListLimit {
		filter.Limit = MaxNodeListLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
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
	PruneTerminalJobs(ctx context.Context, before time.Time) (int64, error)
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
	PruneAuditEvents(ctx context.Context, before time.Time) (int64, error)
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

// HealthStore reports whether the persistence layer is reachable.
type HealthStore interface {
	Check(ctx context.Context) error
}

// Store is the complete persistence contract currently required by the server.
type Store interface {
	NodeStore
	EnrollmentStore
	JobStore
	AuditStore
	DesiredConfigStore
	HealthStore
}
