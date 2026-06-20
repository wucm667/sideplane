package store

import (
	"context"
	"encoding/json"
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
	heartbeats       map[string][]time.Time
	enrollmentTokens map[string]memoryEnrollmentToken
	nodeCredentials  map[string]string
	operatorTokens   map[string]memoryOperatorToken
	operatorHashes   map[string]string
	jobs             map[string]protocol.Job
	rollouts         map[string]protocol.Rollout
	auditEvents      []protocol.AuditEvent
	desiredConfig    protocol.DesiredConfig
	desiredHistory   []protocol.DesiredConfigHistoryEntry
	alertWebhooks    map[string]memoryAlertWebhook
	settings         protocol.ServerSettings
}

type memoryAlertWebhook struct {
	Metadata protocol.AlertWebhook
	Secret   string
}

type memoryEnrollmentToken struct {
	ExpiresAt time.Time
	UsedAt    time.Time
}

type memoryOperatorToken struct {
	Metadata  protocol.OperatorToken
	TokenHash string
}

// NewMemoryNodeStore returns an empty in-memory node store.
func NewMemoryNodeStore() *MemoryNodeStore {
	return &MemoryNodeStore{
		nodes:            make(map[string]protocol.NodeStatus),
		heartbeats:       make(map[string][]time.Time),
		enrollmentTokens: make(map[string]memoryEnrollmentToken),
		nodeCredentials:  make(map[string]string),
		operatorTokens:   make(map[string]memoryOperatorToken),
		operatorHashes:   make(map[string]string),
		jobs:             make(map[string]protocol.Job),
		rollouts:         make(map[string]protocol.Rollout),
		auditEvents:      []protocol.AuditEvent{},
	}
}

var _ Store = (*MemoryNodeStore)(nil)

// Check reports whether the in-memory store is reachable.
func (s *MemoryNodeStore) Check(context.Context) error {
	if s == nil {
		return errors.New("memory node store is nil")
	}
	return nil
}

// RecordHeartbeat stores the latest heartbeat-derived status for a node.
func (s *MemoryNodeStore) RecordHeartbeat(_ context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error) {
	node := protocol.NodeStatus{
		NodeID:          req.NodeID,
		Hostname:        req.Hostname,
		State:           protocol.NodeStateFresh,
		SidecarVersion:  req.SidecarVersion,
		LastHeartbeatAt: observedAt,
		Runtimes:        cloneRuntimeStatuses(req.Runtimes),
		ConfigHash:      req.ConfigHash,
		LastError:       req.LastError,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.nodes[req.NodeID]; ok {
		node.Labels = cloneLabels(existing.Labels)
	}
	s.nodes[req.NodeID] = node
	if s.heartbeats == nil {
		s.heartbeats = make(map[string][]time.Time)
	}
	s.heartbeats[req.NodeID] = append(s.heartbeats[req.NodeID], observedAt.UTC())

	return node, nil
}

// ListNodes returns a stable snapshot of known nodes.
func (s *MemoryNodeStore) ListNodes(_ context.Context) ([]protocol.NodeStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]protocol.NodeStatus, 0, len(s.nodes))
	for _, node := range s.nodes {
		nodes = append(nodes, cloneNodeStatus(node))
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})

	return nodes, nil
}

// ListNodesFiltered returns a bounded, stable snapshot of known nodes.
func (s *MemoryNodeStore) ListNodesFiltered(ctx context.Context, filter NodeFilter) (NodeList, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return NodeList{}, err
	}
	filter = NormalizeNodeFilter(filter)
	nodes = filterNodesByLabels(nodes, filter.Labels)
	total := len(nodes)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}
	return NodeList{
		Nodes:  nodes[start:end],
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}, nil
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

// SetNodeLabels replaces operator-managed labels for a node.
func (s *MemoryNodeStore) SetNodeLabels(_ context.Context, nodeID string, labels map[string]string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node ID is required")
	}
	normalized, err := ValidateNodeLabels(labels)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[nodeID]
	if !ok {
		return ErrNodeNotFound
	}
	node.Labels = cloneLabels(normalized)
	s.nodes[nodeID] = node
	return nil
}

// GetNodeLabels returns a copy of operator-managed labels for a node.
func (s *MemoryNodeStore) GetNodeLabels(_ context.Context, nodeID string) (map[string]string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	node, ok := s.nodes[nodeID]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return cloneLabels(node.Labels), nil
}

// DeleteNode removes a node and all node-scoped associated data.
func (s *MemoryNodeStore) DeleteNode(_ context.Context, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[nodeID]; !ok {
		return ErrNodeNotFound
	}
	delete(s.nodes, nodeID)
	delete(s.heartbeats, nodeID)
	delete(s.nodeCredentials, nodeID)
	for jobID, job := range s.jobs {
		if job.NodeID == nodeID {
			delete(s.jobs, jobID)
		}
	}
	events := s.auditEvents[:0]
	for _, event := range s.auditEvents {
		if event.TargetNode != nodeID {
			events = append(events, event)
		}
	}
	s.auditEvents = events
	return nil
}

// PruneHeartbeats keeps the latest keep heartbeat observations per node.
func (s *MemoryNodeStore) PruneHeartbeats(_ context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, errors.New("heartbeat keep count must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int64
	for nodeID, observed := range s.heartbeats {
		sort.Slice(observed, func(i, j int) bool {
			return observed[i].After(observed[j])
		})
		if len(observed) <= keep {
			s.heartbeats[nodeID] = observed
			continue
		}
		deleted += int64(len(observed) - keep)
		s.heartbeats[nodeID] = append([]time.Time(nil), observed[:keep]...)
	}
	return deleted, nil
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

// CreateOperatorToken creates a named operator token and stores only its hash.
func (s *MemoryNodeStore) CreateOperatorToken(_ context.Context, name string, scope protocol.OperatorTokenScope, now time.Time) (protocol.CreateOperatorTokenResponse, error) {
	name, err := ValidateOperatorTokenName(name)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	scope, err = ValidateOperatorTokenScope(scope)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	token, err := newSecret()
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	tokenHash, err := hashSecret(token)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	tokenID, err := newRandomID("optok_")
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}

	metadata := protocol.OperatorToken{
		ID:        tokenID,
		Name:      name,
		Scope:     scope,
		CreatedAt: now.UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.operatorTokens == nil {
		s.operatorTokens = make(map[string]memoryOperatorToken)
	}
	if s.operatorHashes == nil {
		s.operatorHashes = make(map[string]string)
	}
	if _, ok := s.operatorHashes[tokenHash]; ok {
		return protocol.CreateOperatorTokenResponse{}, errors.New("operator token hash collision")
	}
	s.operatorTokens[tokenID] = memoryOperatorToken{
		Metadata:  cloneOperatorToken(metadata),
		TokenHash: tokenHash,
	}
	s.operatorHashes[tokenHash] = tokenID

	return protocol.CreateOperatorTokenResponse{
		OperatorToken: cloneOperatorToken(metadata),
		Token:         token,
	}, nil
}

// ListOperatorTokens returns operator token metadata without plaintext secrets.
func (s *MemoryNodeStore) ListOperatorTokens(context.Context) ([]protocol.OperatorToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens := make([]protocol.OperatorToken, 0, len(s.operatorTokens))
	for _, token := range s.operatorTokens {
		tokens = append(tokens, cloneOperatorToken(token.Metadata))
	}
	sort.Slice(tokens, func(i, j int) bool {
		if !tokens[i].CreatedAt.Equal(tokens[j].CreatedAt) {
			return tokens[i].CreatedAt.After(tokens[j].CreatedAt)
		}
		return tokens[i].ID > tokens[j].ID
	})
	return tokens, nil
}

// RevokeOperatorToken marks a named operator token as revoked.
func (s *MemoryNodeStore) RevokeOperatorToken(_ context.Context, tokenID string, now time.Time) (protocol.OperatorToken, error) {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return protocol.OperatorToken{}, ErrOperatorTokenNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.operatorTokens[tokenID]
	if !ok {
		return protocol.OperatorToken{}, ErrOperatorTokenNotFound
	}
	if token.Metadata.RevokedAt == nil {
		revokedAt := now.UTC()
		token.Metadata.RevokedAt = &revokedAt
		s.operatorTokens[tokenID] = token
	}
	return cloneOperatorToken(token.Metadata), nil
}

// VerifyOperatorToken verifies an active named operator token and returns its
// ID and scope.
func (s *MemoryNodeStore) VerifyOperatorToken(_ context.Context, token string) (string, protocol.OperatorTokenScope, bool, error) {
	tokenHash, err := hashSecret(token)
	if err != nil {
		return "", "", false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	tokenID, ok := s.operatorHashes[tokenHash]
	if !ok {
		return "", "", false, nil
	}
	metadata, ok := s.operatorTokens[tokenID]
	if !ok || metadata.Metadata.RevokedAt != nil {
		return "", "", false, nil
	}
	scope, _ := protocol.NormalizeOperatorTokenScope(metadata.Metadata.Scope)
	return tokenID, scope, true, nil
}

// UpdateOperatorTokenLastUsed records a best-effort named token use timestamp.
func (s *MemoryNodeStore) UpdateOperatorTokenLastUsed(_ context.Context, tokenID string, usedAt time.Time) error {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return ErrOperatorTokenNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.operatorTokens[tokenID]
	if !ok {
		return ErrOperatorTokenNotFound
	}
	if token.Metadata.RevokedAt != nil {
		return nil
	}
	lastUsedAt := usedAt.UTC()
	token.Metadata.LastUsedAt = &lastUsedAt
	s.operatorTokens[tokenID] = token
	return nil
}

// CreateAlertWebhook stores an outbound alert webhook configuration.
func (s *MemoryNodeStore) CreateAlertWebhook(_ context.Context, req protocol.CreateAlertWebhookRequest, now time.Time) (protocol.AlertWebhook, error) {
	req, err := ValidateAlertWebhookRequest(req)
	if err != nil {
		return protocol.AlertWebhook{}, err
	}
	id, err := newRandomID("whk_")
	if err != nil {
		return protocol.AlertWebhook{}, err
	}
	metadata := protocol.AlertWebhook{
		ID:        id,
		URL:       req.URL,
		Events:    append([]protocol.AlertEventType(nil), req.Events...),
		HasSecret: req.Secret != "",
		Disabled:  false,
		CreatedAt: now.UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alertWebhooks == nil {
		s.alertWebhooks = make(map[string]memoryAlertWebhook)
	}
	s.alertWebhooks[id] = memoryAlertWebhook{Metadata: cloneAlertWebhook(metadata), Secret: req.Secret}
	return cloneAlertWebhook(metadata), nil
}

// ListAlertWebhooks returns alert webhook metadata newest-first without secrets.
func (s *MemoryNodeStore) ListAlertWebhooks(context.Context) ([]protocol.AlertWebhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	webhooks := make([]protocol.AlertWebhook, 0, len(s.alertWebhooks))
	for _, webhook := range s.alertWebhooks {
		webhooks = append(webhooks, cloneAlertWebhook(webhook.Metadata))
	}
	sort.Slice(webhooks, func(i, j int) bool {
		if !webhooks[i].CreatedAt.Equal(webhooks[j].CreatedAt) {
			return webhooks[i].CreatedAt.After(webhooks[j].CreatedAt)
		}
		return webhooks[i].ID > webhooks[j].ID
	})
	return webhooks, nil
}

// DeleteAlertWebhook removes an alert webhook by ID.
func (s *MemoryNodeStore) DeleteAlertWebhook(_ context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrAlertWebhookNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alertWebhooks[id]; !ok {
		return ErrAlertWebhookNotFound
	}
	delete(s.alertWebhooks, id)
	return nil
}

// ListAlertWebhookTargets returns enabled webhooks subscribed to event with secrets.
func (s *MemoryNodeStore) ListAlertWebhookTargets(_ context.Context, event protocol.AlertEventType) ([]AlertWebhookTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	targets := make([]AlertWebhookTarget, 0)
	for _, webhook := range s.alertWebhooks {
		if webhook.Metadata.Disabled {
			continue
		}
		if !slices.Contains(webhook.Metadata.Events, event) {
			continue
		}
		targets = append(targets, AlertWebhookTarget{ID: webhook.Metadata.ID, URL: webhook.Metadata.URL, Secret: webhook.Secret})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets, nil
}

// GetServerSettings returns the operator-tunable server settings.
func (s *MemoryNodeStore) GetServerSettings(context.Context) (protocol.ServerSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

// SetExpectedSidecarVersion records the operator-configured expected sidecar version.
func (s *MemoryNodeStore) SetExpectedSidecarVersion(_ context.Context, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings.ExpectedSidecarVersion = strings.TrimSpace(version)
	return nil
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
	for _, existing := range s.jobs {
		if activeJobConflict(job, existing) {
			return protocol.Job{}, ErrActiveJobExists
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
	oldestJob.ClaimExpiresAt = now.Add(jobClaimLease(oldestJob.Type))
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
		if IsJobClaimTimeout(job) {
			job.ResultJSON = result.ResultJSON
			job.Error = lateJobResultError(result)
			job.FinishedAt = now.UTC()
			job.ClaimExpiresAt = time.Time{}
			s.jobs[jobID] = job
			return ErrLateJobResultRecorded
		}
		return errors.New("job not in claimed state")
	}

	job.Status = protocol.JobStatusCompleted
	job.ResultJSON = result.ResultJSON
	job.FinishedAt = now.UTC()
	job.ClaimExpiresAt = time.Time{}
	s.jobs[jobID] = job

	return nil
}

// FailJob marks a job as failed with an error message and optional result JSON.
func (s *MemoryNodeStore) FailJob(_ context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error {
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
		if IsJobClaimTimeout(job) {
			job.ResultJSON = result.ResultJSON
			job.Error = lateJobResultError(result)
			job.FinishedAt = now.UTC()
			job.ClaimExpiresAt = time.Time{}
			s.jobs[jobID] = job
			return ErrLateJobResultRecorded
		}
		return errors.New("job not in claimed state")
	}

	job.Status = protocol.JobStatusFailed
	job.Error = result.Error
	job.ResultJSON = result.ResultJSON
	job.FinishedAt = now.UTC()
	job.ClaimExpiresAt = time.Time{}
	s.jobs[jobID] = job

	return nil
}

// ListNodeJobs returns the default page of jobs for a node.
func (s *MemoryNodeStore) ListNodeJobs(ctx context.Context, nodeID string) ([]protocol.Job, error) {
	return s.ListNodeJobsFiltered(ctx, nodeID, JobFilter{})
}

// ListNodeJobsFiltered returns bounded jobs for a node, optionally filtered by status.
func (s *MemoryNodeStore) ListNodeJobsFiltered(_ context.Context, nodeID string, filter JobFilter) ([]protocol.Job, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}
	filter = normalizeJobFilter(filter)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireClaimedJobsLocked(time.Now().UTC())

	var jobs []protocol.Job
	for _, job := range s.jobs {
		if job.NodeID == nodeID && (filter.Status == "" || job.Status == filter.Status) {
			jobs = append(jobs, job)
		}
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	if len(jobs) > filter.Limit {
		jobs = jobs[:filter.Limit]
	}

	return jobs, nil
}

// PruneTerminalJobs removes completed and failed jobs finished before before.
func (s *MemoryNodeStore) PruneTerminalJobs(_ context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("job retention cutoff is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int64
	cutoff := before.UTC()
	for id, job := range s.jobs {
		if job.Status != protocol.JobStatusCompleted && job.Status != protocol.JobStatusFailed {
			continue
		}
		if job.FinishedAt.IsZero() || !job.FinishedAt.Before(cutoff) {
			continue
		}
		delete(s.jobs, id)
		deleted++
	}
	return deleted, nil
}

// CreateRollout stores a new rollout snapshot and assigns an ID when needed.
func (s *MemoryNodeStore) CreateRollout(_ context.Context, rollout protocol.Rollout) (protocol.Rollout, error) {
	if rollout.ID == "" {
		id, err := newRandomID("rollout_")
		if err != nil {
			return protocol.Rollout{}, err
		}
		rollout.ID = id
	}
	if rollout.State == "" {
		rollout.State = protocol.RolloutStatePending
	}
	if rollout.CreatedAt.IsZero() {
		rollout.CreatedAt = time.Now().UTC()
	} else {
		rollout.CreatedAt = rollout.CreatedAt.UTC()
	}
	if rollout.UpdatedAt.IsZero() {
		rollout.UpdatedAt = rollout.CreatedAt
	} else {
		rollout.UpdatedAt = rollout.UpdatedAt.UTC()
	}
	if !rollout.FinishedAt.IsZero() {
		rollout.FinishedAt = rollout.FinishedAt.UTC()
	}
	clone, err := cloneRollout(rollout)
	if err != nil {
		return protocol.Rollout{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rollouts == nil {
		s.rollouts = make(map[string]protocol.Rollout)
	}
	if _, exists := s.rollouts[rollout.ID]; exists {
		return protocol.Rollout{}, errors.New("rollout already exists")
	}
	s.rollouts[rollout.ID] = clone
	return cloneRollout(clone)
}

// GetRollout returns one rollout snapshot by ID.
func (s *MemoryNodeStore) GetRollout(_ context.Context, rolloutID string) (*protocol.Rollout, error) {
	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return nil, errors.New("rollout ID is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	rollout, ok := s.rollouts[rolloutID]
	if !ok {
		return nil, nil
	}
	clone, err := cloneRollout(rollout)
	if err != nil {
		return nil, err
	}
	return &clone, nil
}

// ListRollouts returns a paginated newest-first rollout list.
func (s *MemoryNodeStore) ListRollouts(_ context.Context, filter RolloutFilter) (RolloutList, error) {
	filter = NormalizeRolloutFilter(filter)

	s.mu.RLock()
	defer s.mu.RUnlock()
	rollouts := make([]protocol.Rollout, 0, len(s.rollouts))
	for _, rollout := range s.rollouts {
		clone, err := cloneRollout(rollout)
		if err != nil {
			return RolloutList{}, err
		}
		rollouts = append(rollouts, clone)
	}
	slices.SortStableFunc(rollouts, func(a, b protocol.Rollout) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(b.ID, a.ID)
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		return 1
	})
	total := len(rollouts)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}
	return RolloutList{
		Rollouts: rollouts[start:end],
		Total:    total,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}, nil
}

// UpdateRollout replaces a rollout snapshot.
func (s *MemoryNodeStore) UpdateRollout(_ context.Context, rollout protocol.Rollout) error {
	rollout.ID = strings.TrimSpace(rollout.ID)
	if rollout.ID == "" {
		return errors.New("rollout ID is required")
	}
	clone, err := cloneRollout(rollout)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rollouts[rollout.ID]; !ok {
		return ErrRolloutNotFound
	}
	s.rollouts[rollout.ID] = clone
	return nil
}

// PruneTerminalRollouts removes terminal rollouts finished before before.
func (s *MemoryNodeStore) PruneTerminalRollouts(_ context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("rollout retention cutoff is required")
	}
	cutoff := before.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	for id, rollout := range s.rollouts {
		if !rolloutStateTerminal(rollout.State) {
			continue
		}
		if rollout.FinishedAt.IsZero() || !rollout.FinishedAt.Before(cutoff) {
			continue
		}
		delete(s.rollouts, id)
		deleted++
	}
	return deleted, nil
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
	return s.listAuditEvents(AuditFilter{Limit: limit}, 100), nil
}

// ListAuditEventsFiltered returns recent audit events matching all filters.
func (s *MemoryNodeStore) ListAuditEventsFiltered(_ context.Context, filter AuditFilter) ([]protocol.AuditEvent, error) {
	return s.listAuditEvents(filter, 500), nil
}

// PruneAuditEvents removes audit events created before before.
func (s *MemoryNodeStore) PruneAuditEvents(_ context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("audit retention cutoff is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := before.UTC()
	events := s.auditEvents[:0]
	var deleted int64
	for _, event := range s.auditEvents {
		if !event.CreatedAt.IsZero() && event.CreatedAt.Before(cutoff) {
			deleted++
			continue
		}
		events = append(events, event)
	}
	s.auditEvents = events
	return deleted, nil
}

func (s *MemoryNodeStore) listAuditEvents(filter AuditFilter, maxLimit int) []protocol.AuditEvent {
	limit := normalizeAuditLimit(filter.Limit, maxLimit)
	nodeID := strings.TrimSpace(filter.NodeID)
	action := strings.TrimSpace(filter.Action)

	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]protocol.AuditEvent, 0, len(s.auditEvents))
	for _, event := range s.auditEvents {
		if nodeID != "" && event.TargetNode != nodeID {
			continue
		}
		if action != "" && event.Action != action {
			continue
		}
		events = append(events, event)
	}
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
	return events
}

// GetDesiredConfig returns the layered desired runtime config.
func (s *MemoryNodeStore) GetDesiredConfig(_ context.Context) (protocol.DesiredConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneDesiredConfig(s.desiredConfig), nil
}

// SetDesiredConfig replaces the layered desired runtime config.
func (s *MemoryNodeStore) SetDesiredConfig(_ context.Context, desired protocol.DesiredConfig, now time.Time) error {
	entry, err := newDesiredConfigHistoryEntry(desired, desiredConfigHistoryActorOperator, now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desiredConfig = cloneDesiredConfig(entry.Config)
	s.desiredHistory = append(s.desiredHistory, cloneDesiredConfigHistoryEntry(entry))
	return nil
}

// ListDesiredConfigHistory returns a bounded desired-config history page.
func (s *MemoryNodeStore) ListDesiredConfigHistory(_ context.Context, filter DesiredConfigHistoryFilter) (DesiredConfigHistoryList, error) {
	filter = NormalizeDesiredConfigHistoryFilter(filter)

	s.mu.RLock()
	defer s.mu.RUnlock()
	history := make([]protocol.DesiredConfigHistoryEntry, len(s.desiredHistory))
	for i, entry := range s.desiredHistory {
		history[i] = cloneDesiredConfigHistoryEntry(entry)
	}
	slices.SortStableFunc(history, func(a, b protocol.DesiredConfigHistoryEntry) int {
		if a.UpdatedAt.Equal(b.UpdatedAt) {
			return strings.Compare(b.ID, a.ID)
		}
		if a.UpdatedAt.After(b.UpdatedAt) {
			return -1
		}
		return 1
	})
	total := len(history)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}
	return DesiredConfigHistoryList{
		History: history[start:end],
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
	}, nil
}

// RevertDesiredConfig restores a past desired-config version and records a new history entry.
func (s *MemoryNodeStore) RevertDesiredConfig(_ context.Context, historyID string) (protocol.DesiredConfigHistoryEntry, error) {
	historyID = strings.TrimSpace(historyID)
	if historyID == "" {
		return protocol.DesiredConfigHistoryEntry{}, ErrDesiredConfigHistoryNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.desiredHistory {
		if entry.ID != historyID {
			continue
		}
		reverted, err := newDesiredConfigHistoryEntry(entry.Config, desiredConfigHistoryActorOperator, time.Now().UTC())
		if err != nil {
			return protocol.DesiredConfigHistoryEntry{}, err
		}
		s.desiredConfig = cloneDesiredConfig(reverted.Config)
		s.desiredHistory = append(s.desiredHistory, cloneDesiredConfigHistoryEntry(reverted))
		return cloneDesiredConfigHistoryEntry(reverted), nil
	}
	return protocol.DesiredConfigHistoryEntry{}, ErrDesiredConfigHistoryNotFound
}

func cloneDesiredConfig(desired protocol.DesiredConfig) protocol.DesiredConfig {
	clone := protocol.DesiredConfig{Global: desired.Global}
	if desired.NodeOverrides != nil {
		clone.NodeOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.NodeOverrides))
		for key, value := range desired.NodeOverrides {
			clone.NodeOverrides[key] = value
		}
	}
	if desired.RuntimeProfileOverrides != nil {
		clone.RuntimeProfileOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.RuntimeProfileOverrides))
		for key, value := range desired.RuntimeProfileOverrides {
			clone.RuntimeProfileOverrides[key] = value
		}
	}
	if desired.NodeRuntimeProfileOverrides != nil {
		clone.NodeRuntimeProfileOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.NodeRuntimeProfileOverrides))
		for key, value := range desired.NodeRuntimeProfileOverrides {
			clone.NodeRuntimeProfileOverrides[key] = value
		}
	}
	return clone
}

func cloneDesiredConfigHistoryEntry(entry protocol.DesiredConfigHistoryEntry) protocol.DesiredConfigHistoryEntry {
	return protocol.DesiredConfigHistoryEntry{
		ID:          entry.ID,
		Config:      cloneDesiredConfig(entry.Config),
		DesiredHash: entry.DesiredHash,
		UpdatedAt:   entry.UpdatedAt,
		Actor:       entry.Actor,
	}
}

func cloneNodeStatus(node protocol.NodeStatus) protocol.NodeStatus {
	node.Runtimes = cloneRuntimeStatuses(node.Runtimes)
	node.Labels = cloneLabels(node.Labels)
	return node
}

func cloneAlertWebhook(webhook protocol.AlertWebhook) protocol.AlertWebhook {
	clone := webhook
	clone.Events = append([]protocol.AlertEventType(nil), webhook.Events...)
	return clone
}

func cloneOperatorToken(token protocol.OperatorToken) protocol.OperatorToken {
	clone := token
	if token.LastUsedAt != nil {
		lastUsedAt := token.LastUsedAt.UTC()
		clone.LastUsedAt = &lastUsedAt
	}
	if token.RevokedAt != nil {
		revokedAt := token.RevokedAt.UTC()
		clone.RevokedAt = &revokedAt
	}
	return clone
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	clone := make(map[string]string, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

func cloneRollout(rollout protocol.Rollout) (protocol.Rollout, error) {
	payload, err := json.Marshal(rollout)
	if err != nil {
		return protocol.Rollout{}, err
	}
	var clone protocol.Rollout
	if err := json.Unmarshal(payload, &clone); err != nil {
		return protocol.Rollout{}, err
	}
	return clone, nil
}

func rolloutStateTerminal(state protocol.RolloutState) bool {
	return state == protocol.RolloutStateCompleted || state == protocol.RolloutStateAborted || state == protocol.RolloutStateFailed
}

func cloneRuntimeStatuses(runtimes []protocol.RuntimeStatus) []protocol.RuntimeStatus {
	if runtimes == nil {
		return nil
	}
	clone := make([]protocol.RuntimeStatus, len(runtimes))
	copy(clone, runtimes)
	for i := range clone {
		clone[i].Warnings = append([]string(nil), clone[i].Warnings...)
	}
	return clone
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
