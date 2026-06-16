package server

import (
	"errors"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	// DefaultStaleAfter is the default time after which a node becomes stale.
	DefaultStaleAfter = 2 * time.Minute
	// DefaultOfflineAfter is the default time after which a node becomes offline.
	DefaultOfflineAfter = 10 * time.Minute
)

// FreshnessPolicy computes the server-side freshness view for nodes.
type FreshnessPolicy struct {
	StaleAfter   time.Duration
	OfflineAfter time.Duration
	Now          func() time.Time
}

// DefaultFreshnessPolicy returns the default node freshness policy.
func DefaultFreshnessPolicy() FreshnessPolicy {
	return FreshnessPolicy{
		StaleAfter:   DefaultStaleAfter,
		OfflineAfter: DefaultOfflineAfter,
		Now:          utcNow,
	}
}

// Validate returns an error when the freshness thresholds are unusable.
func (p FreshnessPolicy) Validate() error {
	if p.OfflineAfter <= p.StaleAfter {
		return errors.New("offline-after must be greater than stale-after")
	}
	return nil
}

// StateFor returns the node state implied by the last heartbeat time.
func (p FreshnessPolicy) StateFor(lastHeartbeatAt time.Time) protocol.NodeState {
	if lastHeartbeatAt.IsZero() {
		return protocol.NodeStateOffline
	}

	now := p.now().UTC()
	age := now.Sub(lastHeartbeatAt.UTC())
	if age <= p.StaleAfter {
		return protocol.NodeStateFresh
	}
	if age <= p.OfflineAfter {
		return protocol.NodeStateStale
	}
	return protocol.NodeStateOffline
}

func (p FreshnessPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return utcNow()
}

func utcNow() time.Time {
	return time.Now().UTC()
}
