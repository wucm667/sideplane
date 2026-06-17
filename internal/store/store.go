package store

import (
	"context"
	"errors"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
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
)

// NodeStore persists heartbeat-derived node status snapshots.
type NodeStore interface {
	RecordHeartbeat(ctx context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error)
	ListNodes(ctx context.Context) ([]protocol.NodeStatus, error)
}

// EnrollmentStore persists one-time enrollment tokens and node credentials.
type EnrollmentStore interface {
	CreateEnrollmentToken(ctx context.Context, expiresAt time.Time, now time.Time) (protocol.CreateEnrollmentTokenResponse, error)
	EnrollNode(ctx context.Context, req protocol.EnrollNodeRequest, now time.Time) (protocol.EnrollNodeResponse, error)
	VerifyNodeCredential(ctx context.Context, nodeID string, credential string) (bool, error)
}

// Store is the complete persistence contract currently required by the server.
type Store interface {
	NodeStore
	EnrollmentStore
}
