package store

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// MemoryNodeStore keeps the latest heartbeat for each node in process memory.
type MemoryNodeStore struct {
	mu               sync.RWMutex
	nodes            map[string]protocol.NodeStatus
	enrollmentTokens map[string]memoryEnrollmentToken
	nodeCredentials  map[string]string
	jobs             map[string]protocol.Job
	auditEvents      []protocol.AuditEvent
}

type memoryEnrollmentToken struct {
	ExpiresAt time.Time
	UsedAt    time.Time
}

// NewMemoryNodeStore returns an empty in-memory node store.
func NewMemoryNodeStore() *MemoryNodeStore {
	return &MemoryNodeStore{
		nodes:            make(map[string]protocol.NodeStatus),
		enrollmentTokens: make(map[string]memoryEnrollmentToken),
		nodeCredentials:  make(map[string]string),
		jobs:             make(map[string]protocol.Job),
		auditEvents:      []protocol.AuditEvent{},
	}
}

var _ Store = (*MemoryNodeStore)(nil)

// RecordHeartbeat stores the latest heartbeat-derived status for a node.
func (s *MemoryNodeStore) RecordHeartbeat(_ context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error) {
	node := protocol.NodeStatus{
		NodeID:          req.NodeID,
		Hostname:        req.Hostname,
		State:           protocol.NodeStateFresh,
		SidecarVersion:  req.SidecarVersion,
		LastHeartbeatAt: observedAt,
		Runtimes:        append([]protocol.RuntimeStatus(nil), req.Runtimes...),
		ConfigHash:      req.ConfigHash,
		LastError:       req.LastError,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[req.NodeID] = node

	return node, nil
}

// ListNodes returns a stable snapshot of known nodes.
func (s *MemoryNodeStore) ListNodes(_ context.Context) ([]protocol.NodeStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]protocol.NodeStatus, 0, len(s.nodes))
	for _, node := range s.nodes {
		node.Runtimes = append([]protocol.RuntimeStatus(nil), node.Runtimes...)
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})

	return nodes, nil
}

// NodeExists reports whether a node is known to the store.
func (s *MemoryNodeStore) NodeExists(_ context.Context, nodeID string) (bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.nodes[nodeID]
	return ok, nil
}

// CreateEnrollmentToken creates a one-time enrollment token and stores only its hash.
func (s *MemoryNodeStore) CreateEnrollmentToken(_ context.Context, expiresAt time.Time, now time.Time) (protocol.CreateEnrollmentTokenResponse, error) {
	if expiresAt.IsZero() {
		return protocol.CreateEnrollmentTokenResponse{}, errors.New("enrollment token expiry is required")
	}
	if !expiresAt.After(now.UTC()) {
		return protocol.CreateEnrollmentTokenResponse{}, errors.New("enrollment token expiry must be in the future")
	}

	token, err := newSecret()
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}
	tokenHash, err := hashSecret(token)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.enrollmentTokens[tokenHash] = memoryEnrollmentToken{
		ExpiresAt: expiresAt.UTC(),
	}

	return protocol.CreateEnrollmentTokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.UTC(),
	}, nil
}

// EnrollNode exchanges a valid enrollment token for a long-lived node credential.
func (s *MemoryNodeStore) EnrollNode(_ context.Context, req protocol.EnrollNodeRequest, now time.Time) (protocol.EnrollNodeResponse, error) {
	tokenHash, err := hashSecret(req.Token)
	if err != nil {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenInvalid
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID, err = newRandomID("node_")
		if err != nil {
			return protocol.EnrollNodeResponse{}, err
		}
	}

	nodeCredential, err := newSecret()
	if err != nil {
		return protocol.EnrollNodeResponse{}, err
	}
	credentialHash, err := hashSecret(nodeCredential)
	if err != nil {
		return protocol.EnrollNodeResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	token, ok := s.enrollmentTokens[tokenHash]
	if !ok {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenInvalid
	}
	if !token.UsedAt.IsZero() {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenUsed
	}
	if !token.ExpiresAt.After(now.UTC()) {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenExpired
	}
	if _, ok := s.nodeCredentials[nodeID]; ok {
		return protocol.EnrollNodeResponse{}, ErrNodeAlreadyEnrolled
	}

	token.UsedAt = now.UTC()
	s.enrollmentTokens[tokenHash] = token
	s.nodeCredentials[nodeID] = credentialHash

	if _, ok := s.nodes[nodeID]; !ok {
		s.nodes[nodeID] = protocol.NodeStatus{
			NodeID:          nodeID,
			Hostname:        strings.TrimSpace(req.Hostname),
			State:           protocol.NodeStateOffline,
			SidecarVersion:  strings.TrimSpace(req.SidecarVersion),
			LastHeartbeatAt: time.Time{},
			Runtimes:        []protocol.RuntimeStatus{},
		}
	}

	return protocol.EnrollNodeResponse{
		NodeID:         nodeID,
		NodeCredential: nodeCredential,
	}, nil
}

// VerifyNodeCredential checks whether a node credential matches the stored hash.
func (s *MemoryNodeStore) VerifyNodeCredential(_ context.Context, nodeID string, credential string) (bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, nil
	}

	s.mu.RLock()
	credentialHash, ok := s.nodeCredentials[nodeID]
	s.mu.RUnlock()
	if !ok {
		return false, nil
	}

	return secretHashMatches(credential, credentialHash)
}

// GetJob retrieves a job by ID.
func (s *MemoryNodeStore) GetJob(_ context.Context, jobID string) (*protocol.Job, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job ID is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return nil, nil
	}
	return &job, nil
}

// CreateJob creates a new job for a node.
func (s *MemoryNodeStore) CreateJob(_ context.Context, req protocol.CreateJobRequest, nodeID string, now time.Time) (protocol.Job, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return protocol.Job{}, errors.New("node ID is required")
	}

	jobID, err := newRandomID("job_")
	if err != nil {
		return protocol.Job{}, err
	}

	job := protocol.Job{
		ID:          jobID,
		NodeID:      nodeID,
		Type:        req.Type,
		Status:      protocol.JobStatusPending,
		PayloadJSON: req.PayloadJSON,
		CreatedAt:   now.UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = make(map[string]protocol.Job)
	}
	s.expireClaimedJobsLocked(now.UTC())
	if req.Type == protocol.JobTypeDeepProbe {
		for _, existing := range s.jobs {
			if existing.NodeID == nodeID && existing.Type == req.Type && jobStatusIsActive(existing.Status) {
				return protocol.Job{}, ErrActiveJobExists
			}
		}
	}
	s.jobs[jobID] = job

	return job, nil
}

// ClaimNextJob claims the next pending job for a node.
func (s *MemoryNodeStore) ClaimNextJob(_ context.Context, nodeID string, now time.Time) (*protocol.Job, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.jobs == nil {
		return nil, nil
	}
	now = now.UTC()
	s.expireClaimedJobsLocked(now)

	// Find oldest pending job for this node
	var oldestJob *protocol.Job
	for _, job := range s.jobs {
		if job.NodeID == nodeID && job.Status == protocol.JobStatusPending {
			if oldestJob == nil || job.CreatedAt.Before(oldestJob.CreatedAt) {
				jobCopy := job
				oldestJob = &jobCopy
			}
		}
	}

	if oldestJob == nil {
		return nil, nil
	}

	oldestJob.Status = protocol.JobStatusClaimed
	oldestJob.ClaimedAt = now
	oldestJob.ClaimExpiresAt = now.Add(defaultJobClaimLease)
	s.jobs[oldestJob.ID] = *oldestJob

	return oldestJob, nil
}

// CompleteJob marks a job as completed with a result.
func (s *MemoryNodeStore) CompleteJob(_ context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return errors.New("job not found")
	}
	if job.Status != protocol.JobStatusClaimed {
		return errors.New("job not in claimed state")
	}

	job.Status = protocol.JobStatusCompleted
	job.ResultJSON = result.ResultJSON
	job.FinishedAt = now.UTC()
	job.ClaimExpiresAt = time.Time{}
	s.jobs[jobID] = job

	return nil
}

// FailJob marks a job as failed with an error message.
func (s *MemoryNodeStore) FailJob(_ context.Context, jobID string, errMsg string, now time.Time) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return errors.New("job not found")
	}
	if job.Status != protocol.JobStatusClaimed {
		return errors.New("job not in claimed state")
	}

	job.Status = protocol.JobStatusFailed
	job.Error = errMsg
	job.FinishedAt = now.UTC()
	job.ClaimExpiresAt = time.Time{}
	s.jobs[jobID] = job

	return nil
}

// ListNodeJobs returns all jobs for a node.
func (s *MemoryNodeStore) ListNodeJobs(_ context.Context, nodeID string) ([]protocol.Job, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireClaimedJobsLocked(time.Now().UTC())

	var jobs []protocol.Job
	for _, job := range s.jobs {
		if job.NodeID == nodeID {
			jobs = append(jobs, job)
		}
	}

	// Sort by created_at descending
	for i := 0; i < len(jobs); i++ {
		for j := i + 1; j < len(jobs); j++ {
			if jobs[i].CreatedAt.Before(jobs[j].CreatedAt) {
				jobs[i], jobs[j] = jobs[j], jobs[i]
			}
		}
	}

	return jobs, nil
}

// AppendAuditEvent stores an audit event and assigns an ID when needed.
func (s *MemoryNodeStore) AppendAuditEvent(_ context.Context, event protocol.AuditEvent) (protocol.AuditEvent, error) {
	event.Actor = strings.TrimSpace(event.Actor)
	event.Action = strings.TrimSpace(event.Action)
	event.TargetNode = strings.TrimSpace(event.TargetNode)
	event.Detail = strings.TrimSpace(event.Detail)
	if event.Actor == "" {
		return protocol.AuditEvent{}, errors.New("audit actor is required")
	}
	if event.Action == "" {
		return protocol.AuditEvent{}, errors.New("audit action is required")
	}
	if event.ID == "" {
		id, err := newRandomID("audit_")
		if err != nil {
			return protocol.AuditEvent{}, err
		}
		event.ID = id
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	} else {
		event.CreatedAt = event.CreatedAt.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditEvents = append(s.auditEvents, event)
	return event, nil
}

// ListAuditEvents returns recent audit events newest first.
func (s *MemoryNodeStore) ListAuditEvents(_ context.Context, limit int) ([]protocol.AuditEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	events := append([]protocol.AuditEvent(nil), s.auditEvents...)
	slices.SortStableFunc(events, func(a, b protocol.AuditEvent) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(b.ID, a.ID)
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		return 1
	})
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func jobStatusIsActive(status protocol.JobStatus) bool {
	return status == protocol.JobStatusPending || status == protocol.JobStatusClaimed
}

func (s *MemoryNodeStore) expireClaimedJobsLocked(now time.Time) {
	for id, job := range s.jobs {
		if job.Status != protocol.JobStatusClaimed || job.ClaimExpiresAt.IsZero() || job.ClaimExpiresAt.After(now) {
			continue
		}
		job.Status = protocol.JobStatusFailed
		job.Error = jobClaimTimeoutError
		job.FinishedAt = now
		job.ClaimExpiresAt = time.Time{}
		s.jobs[id] = job
	}
}
