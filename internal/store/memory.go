package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// MemoryNodeStore keeps the latest heartbeat for each node in process memory.
type MemoryNodeStore struct {
	mu    sync.RWMutex
	nodes map[string]protocol.NodeStatus
}

// NewMemoryNodeStore returns an empty in-memory node store.
func NewMemoryNodeStore() *MemoryNodeStore {
	return &MemoryNodeStore{
		nodes: make(map[string]protocol.NodeStatus),
	}
}

var _ NodeStore = (*MemoryNodeStore)(nil)

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
