package store

import (
	"context"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// NodeStore persists heartbeat-derived node status snapshots.
type NodeStore interface {
	RecordHeartbeat(ctx context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error)
	ListNodes(ctx context.Context) ([]protocol.NodeStatus, error)
}
