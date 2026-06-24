package store

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
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
	// DefaultRolloutListLimit is the bounded default for rollout listing.
	DefaultRolloutListLimit = 50
	// MaxRolloutListLimit is the largest rollout page size accepted by the store.
	MaxRolloutListLimit = 500
	// DefaultDesiredConfigHistoryListLimit is the bounded default for desired config history.
	DefaultDesiredConfigHistoryListLimit = 50
	// MaxDesiredConfigHistoryListLimit is the largest desired config history page size.
	MaxDesiredConfigHistoryListLimit = 500
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
	// ErrRolloutNotFound means the requested rollout does not exist.
	ErrRolloutNotFound = errors.New("rollout not found")
	// ErrOperatorTokenNotFound means the requested operator token does not exist.
	ErrOperatorTokenNotFound = errors.New("operator token not found")
	// ErrDesiredConfigHistoryNotFound means the requested desired config history entry does not exist.
	ErrDesiredConfigHistoryNotFound = errors.New("desired config history not found")
	// ErrAlertWebhookNotFound means the requested alert webhook does not exist.
	ErrAlertWebhookNotFound = errors.New("alert webhook not found")
	// ErrRolloutTemplateNotFound means the requested rollout template does not exist.
	ErrRolloutTemplateNotFound = errors.New("rollout template not found")
)

const (
	// MaxAlertWebhookURLLength bounds operator-supplied webhook URLs.
	MaxAlertWebhookURLLength = 2048
	// MaxAlertWebhookSecretLength bounds operator-supplied webhook secrets.
	MaxAlertWebhookSecretLength = 512
)

// AlertWebhookTarget is the store-internal delivery target for one enabled
// webhook subscribed to an event, including its signing secret. It is never
// exposed through the operator API.
type AlertWebhookTarget struct {
	ID     string
	Kind   protocol.AlertWebhookKind
	URL    string
	Secret string
}

// ValidateAlertWebhookRequest normalizes and validates a webhook create request.
func ValidateAlertWebhookRequest(req protocol.CreateAlertWebhookRequest) (protocol.CreateAlertWebhookRequest, error) {
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return req, errors.New("webhook url is required")
	}
	if len(req.URL) > MaxAlertWebhookURLLength {
		return req, errors.New("webhook url is too long")
	}
	parsed, err := neturl.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return req, errors.New("webhook url must be a valid http or https url")
	}
	events, err := NormalizeAlertEvents(req.Events)
	if err != nil {
		return req, err
	}
	req.Events = events
	kind, ok := protocol.NormalizeAlertWebhookKind(req.Kind)
	if !ok {
		return req, fmt.Errorf("unknown webhook kind %q", req.Kind)
	}
	req.Kind = kind
	req.Secret = strings.TrimSpace(req.Secret)
	if len(req.Secret) > MaxAlertWebhookSecretLength {
		return req, errors.New("webhook secret is too long")
	}
	if req.Kind == protocol.AlertWebhookKindSlack && (req.Sign || req.Secret != "") {
		return req, errors.New("webhook signing is only supported for generic webhooks")
	}
	return req, nil
}

// NormalizeAlertEvents validates and deduplicates a webhook event subscription.
func NormalizeAlertEvents(events []protocol.AlertEventType) ([]protocol.AlertEventType, error) {
	if len(events) == 0 {
		return nil, errors.New("at least one event is required")
	}
	seen := make(map[protocol.AlertEventType]bool, len(events))
	out := make([]protocol.AlertEventType, 0, len(events))
	for _, event := range events {
		if !protocol.ValidAlertEventType(event) {
			return nil, fmt.Errorf("unknown alert event %q", event)
		}
		if seen[event] {
			continue
		}
		seen[event] = true
		out = append(out, event)
	}
	return out, nil
}

// RolloutTemplateStore persists reusable rollout specs.
type RolloutTemplateStore interface {
	CreateRolloutTemplate(ctx context.Context, name string, spec protocol.RolloutSpec, now time.Time) (protocol.RolloutTemplate, error)
	ListRolloutTemplates(ctx context.Context) ([]protocol.RolloutTemplate, error)
	GetRolloutTemplate(ctx context.Context, id string) (*protocol.RolloutTemplate, error)
	DeleteRolloutTemplate(ctx context.Context, id string) error
}

// SettingsStore persists operator-tunable server settings.
type SettingsStore interface {
	GetServerSettings(ctx context.Context) (protocol.ServerSettings, error)
	SetExpectedSidecarVersion(ctx context.Context, version string) error
	SetExpectedRuntimeVersions(ctx context.Context, versions map[string]string) error
}

// AlertWebhookStore persists operator-configured outbound alert webhooks.
type AlertWebhookStore interface {
	CreateAlertWebhook(ctx context.Context, req protocol.CreateAlertWebhookRequest, now time.Time) (protocol.AlertWebhook, error)
	ListAlertWebhooks(ctx context.Context) ([]protocol.AlertWebhook, error)
	DeleteAlertWebhook(ctx context.Context, id string) error
	ListAlertWebhookTargets(ctx context.Context, event protocol.AlertEventType) ([]AlertWebhookTarget, error)
}

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
	SetNodeMaintenance(ctx context.Context, nodeID string, maintenance bool) error
	GetNodeMaintenance(ctx context.Context, nodeID string) (bool, error)
	DeleteNode(ctx context.Context, nodeID string) error
	PruneHeartbeats(ctx context.Context, keep int) (int64, error)
}

// NodeFilter constrains fleet inventory listing.
type NodeFilter struct {
	Limit  int
	Offset int
	Labels map[string]string
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

const (
	// MaxOperatorTokenNameLength bounds operator-visible token names.
	MaxOperatorTokenNameLength = 120
)

// ValidateOperatorTokenName trims and validates an operator API token name.
func ValidateOperatorTokenName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("operator token name is required")
	}
	if len(name) > MaxOperatorTokenNameLength {
		return "", errors.New("operator token name is too long")
	}
	if hasControlCharacter(name) {
		return "", errors.New("operator token name must not contain control characters")
	}
	return name, nil
}

// ValidateRolloutTemplateName trims and validates a rollout template name.
func ValidateRolloutTemplateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("rollout template name is required")
	}
	if len(name) > 120 {
		return "", errors.New("rollout template name is too long")
	}
	if hasControlCharacter(name) {
		return "", errors.New("rollout template name must not contain control characters")
	}
	return name, nil
}

// ValidateOperatorTokenScope defaults an empty scope to admin and rejects
// unknown scopes.
func ValidateOperatorTokenScope(scope protocol.OperatorTokenScope) (protocol.OperatorTokenScope, error) {
	normalized, ok := protocol.NormalizeOperatorTokenScope(scope)
	if !ok {
		return "", errors.New("operator token scope must be admin or readonly")
	}
	return normalized, nil
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
	filter.Labels, _ = ValidateNodeLabels(filter.Labels)
	return filter
}

func nodeMatchesLabels(node protocol.NodeStatus, labels map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	for key, value := range labels {
		if node.Labels[key] != value {
			return false
		}
	}
	return true
}

func filterNodesByLabels(nodes []protocol.NodeStatus, labels map[string]string) []protocol.NodeStatus {
	if len(labels) == 0 {
		return nodes
	}
	filtered := make([]protocol.NodeStatus, 0, len(nodes))
	for _, node := range nodes {
		if nodeMatchesLabels(node, labels) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// EnrollmentStore persists one-time enrollment tokens and node credentials.
type EnrollmentStore interface {
	CreateEnrollmentToken(ctx context.Context, expiresAt time.Time, now time.Time) (protocol.CreateEnrollmentTokenResponse, error)
	EnrollNode(ctx context.Context, req protocol.EnrollNodeRequest, now time.Time) (protocol.EnrollNodeResponse, error)
	VerifyNodeCredential(ctx context.Context, nodeID string, credential string) (bool, error)
}

// OperatorTokenStore persists named, revocable operator API tokens.
type OperatorTokenStore interface {
	CreateOperatorToken(ctx context.Context, name string, scope protocol.OperatorTokenScope, now time.Time) (protocol.CreateOperatorTokenResponse, error)
	ListOperatorTokens(ctx context.Context) ([]protocol.OperatorToken, error)
	RevokeOperatorToken(ctx context.Context, tokenID string, now time.Time) (protocol.OperatorToken, error)
	VerifyOperatorToken(ctx context.Context, token string) (string, protocol.OperatorTokenScope, bool, error)
	UpdateOperatorTokenLastUsed(ctx context.Context, tokenID string, usedAt time.Time) error
}

// OnlineBackupStore can snapshot itself to a destination path while serving.
// Only durable stores (SQLite) implement it; the in-memory store does not.
type OnlineBackupStore interface {
	BackupTo(ctx context.Context, destPath string) error
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

// RolloutStore persists staged fleet rollouts and their nested progress.
type RolloutStore interface {
	CreateRollout(ctx context.Context, rollout protocol.Rollout) (protocol.Rollout, error)
	GetRollout(ctx context.Context, rolloutID string) (*protocol.Rollout, error)
	ListRollouts(ctx context.Context, filter RolloutFilter) (RolloutList, error)
	ListActiveRolloutConflicts(ctx context.Context, nodeIDs []string) ([]RolloutNodeConflict, error)
	UpdateRollout(ctx context.Context, rollout protocol.Rollout) error
	PruneTerminalRollouts(ctx context.Context, before time.Time) (int64, error)
}

// RolloutNodeConflict names a non-terminal rollout already targeting a node.
type RolloutNodeConflict struct {
	NodeID    string
	RolloutID string
	State     protocol.RolloutState
}

// RolloutFilter constrains rollout listing.
type RolloutFilter struct {
	Limit  int
	Offset int
}

// RolloutList is a paginated rollout snapshot.
type RolloutList struct {
	Rollouts []protocol.Rollout
	Total    int
	Limit    int
	Offset   int
}

func NormalizeRolloutFilter(filter RolloutFilter) RolloutFilter {
	if filter.Limit <= 0 {
		filter.Limit = DefaultRolloutListLimit
	}
	if filter.Limit > MaxRolloutListLimit {
		filter.Limit = MaxRolloutListLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
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
	ListDesiredConfigHistory(ctx context.Context, filter DesiredConfigHistoryFilter) (DesiredConfigHistoryList, error)
	RevertDesiredConfig(ctx context.Context, historyID string) (protocol.DesiredConfigHistoryEntry, error)
}

// ProviderSecretStore persists encrypted provider API keys keyed by env var name.
// Ciphertext is opaque to the store; encryption and decryption happen in the server.
type ProviderSecretStore interface {
	SetProviderSecret(ctx context.Context, envName string, ciphertext []byte, now time.Time) error
	GetProviderSecret(ctx context.Context, envName string) ([]byte, bool, error)
	DeleteProviderSecret(ctx context.Context, envName string) error
	HasProviderSecret(ctx context.Context, envName string) (bool, error)
}

// DesiredConfigHistoryFilter constrains desired-config history listing.
type DesiredConfigHistoryFilter struct {
	Limit  int
	Offset int
}

// DesiredConfigHistoryList is a paginated desired-config history snapshot.
type DesiredConfigHistoryList struct {
	History []protocol.DesiredConfigHistoryEntry
	Total   int
	Limit   int
	Offset  int
}

func NormalizeDesiredConfigHistoryFilter(filter DesiredConfigHistoryFilter) DesiredConfigHistoryFilter {
	if filter.Limit <= 0 {
		filter.Limit = DefaultDesiredConfigHistoryListLimit
	}
	if filter.Limit > MaxDesiredConfigHistoryListLimit {
		filter.Limit = MaxDesiredConfigHistoryListLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
}

// HealthStore reports whether the persistence layer is reachable.
type HealthStore interface {
	Check(ctx context.Context) error
}

// Store is the complete persistence contract currently required by the server.
type Store interface {
	NodeStore
	EnrollmentStore
	OperatorTokenStore
	JobStore
	RolloutStore
	AuditStore
	DesiredConfigStore
	ProviderSecretStore
	HealthStore
	AlertWebhookStore
	SettingsStore
	RolloutTemplateStore
}
