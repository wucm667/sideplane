package server

import (
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestFreshnessPolicyStateFor(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	policy := FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return now
		},
	}

	tests := []struct {
		name            string
		lastHeartbeatAt time.Time
		want            protocol.NodeState
	}{
		{
			name:            "fresh before stale threshold",
			lastHeartbeatAt: now.Add(-time.Minute),
			want:            protocol.NodeStateFresh,
		},
		{
			name:            "fresh at stale threshold",
			lastHeartbeatAt: now.Add(-2 * time.Minute),
			want:            protocol.NodeStateFresh,
		},
		{
			name:            "stale after stale threshold",
			lastHeartbeatAt: now.Add(-3 * time.Minute),
			want:            protocol.NodeStateStale,
		},
		{
			name:            "stale at offline threshold",
			lastHeartbeatAt: now.Add(-10 * time.Minute),
			want:            protocol.NodeStateStale,
		},
		{
			name:            "offline after offline threshold",
			lastHeartbeatAt: now.Add(-(10*time.Minute + time.Nanosecond)),
			want:            protocol.NodeStateOffline,
		},
		{
			name:            "offline with zero time",
			lastHeartbeatAt: time.Time{},
			want:            protocol.NodeStateOffline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.StateFor(tt.lastHeartbeatAt); got != tt.want {
				t.Fatalf("state = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFreshnessPolicyValidateRejectsOfflineBeforeStale(t *testing.T) {
	tests := []struct {
		name         string
		staleAfter   time.Duration
		offlineAfter time.Duration
	}{
		{
			name:         "offline before stale",
			staleAfter:   10 * time.Minute,
			offlineAfter: 2 * time.Minute,
		},
		{
			name:         "offline equals stale",
			staleAfter:   10 * time.Minute,
			offlineAfter: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := FreshnessPolicy{
				StaleAfter:   tt.staleAfter,
				OfflineAfter: tt.offlineAfter,
			}

			if err := policy.Validate(); err == nil {
				t.Fatalf("Validate() error = nil, want error")
			}
		})
	}
}
