package store

import (
	"context"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestMemoryNodeStoreRecordsAndListsNodes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-b",
		Hostname: "worker-b",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "openclaw"}},
	}, now); err != nil {
		t.Fatalf("record node-b heartbeat: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-a",
		Hostname: "worker-a",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("record node-a heartbeat: %v", err)
	}

	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes length = %d, want 2", len(nodes))
	}
	if nodes[0].NodeID != "node-a" || nodes[1].NodeID != "node-b" {
		t.Fatalf("nodes are not sorted by node ID: %#v", nodes)
	}
	if nodes[0].State != protocol.NodeStateFresh {
		t.Fatalf("node state = %q, want fresh", nodes[0].State)
	}

	nodes[0].Runtimes[0].Type = "mutated"
	again, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes again: %v", err)
	}
	if again[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("store snapshot was mutated: %#v", again[0].Runtimes)
	}
}

func TestMemoryNodeStoreTimesOutExpiredClaimedJob(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-timeout"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimed, err := store.ClaimNextJob(ctx, "node-timeout", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimed == nil {
		t.Fatalf("claimed job is nil")
	}
	if !claimed.ClaimExpiresAt.Equal(claimed.ClaimedAt.Add(defaultJobClaimLease)) {
		t.Fatalf("claim expires at = %s, want claimedAt + lease", claimed.ClaimExpiresAt)
	}

	next, err := store.ClaimNextJob(ctx, "node-timeout", claimed.ClaimExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("claim after timeout: %v", err)
	}
	if next != nil {
		t.Fatalf("next job = %#v, want nil", next)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got == nil {
		t.Fatalf("timed out job not found")
	}
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", got.Status)
	}
	if got.Error != jobClaimTimeoutError {
		t.Fatalf("job error = %q, want %q", got.Error, jobClaimTimeoutError)
	}
	if got.FinishedAt.IsZero() {
		t.Fatalf("finishedAt is zero")
	}
	if !got.ClaimExpiresAt.IsZero() {
		t.Fatalf("claimExpiresAt = %s, want zero after timeout", got.ClaimExpiresAt)
	}

	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", got.FinishedAt.Add(time.Second)); err != nil {
		t.Fatalf("create job after timeout: %v", err)
	}
}
