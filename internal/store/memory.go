package store

import (
	"context"
	"errors"
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
