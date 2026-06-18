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

func TestMemoryNodeStoreFailJobPersistsResultJSON(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-failed"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply}, "node-failed", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-failed", now.Add(time.Second)); err != nil {
		t.Fatalf("claim job: %v", err)
	}
	resultJSON := `{"steps":[{"name":"rolled_back","status":"completed"}]}`
	if err := store.FailJob(ctx, job.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusFailed,
		ResultJSON: resultJSON,
		Error:      "apply failed",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got == nil {
		t.Fatalf("failed job not found")
	}
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", got.Status)
	}
	if got.ResultJSON != resultJSON {
		t.Fatalf("result JSON = %q, want %q", got.ResultJSON, resultJSON)
	}
	if got.Error != "apply failed" {
		t.Fatalf("job error = %q, want apply failed", got.Error)
	}
}

func TestMemoryNodeStoreRejectsActiveConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	req := protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now); err != nil {
		t.Fatalf("create first config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now.Add(time.Second)); err != ErrActiveJobExists {
		t.Fatalf("duplicate pending config_apply error = %v, want ErrActiveJobExists", err)
	}
}

func TestMemoryConfigApplyUsesLongLeaseAndDoesNotRequeueAfterTimeout(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}, "node-apply", now); err != nil {
		t.Fatalf("create config_apply: %v", err)
	}
	claimed, err := store.ClaimNextJob(ctx, "node-apply", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if claimed == nil {
		t.Fatal("claimed job is nil")
	}
	if !claimed.ClaimExpiresAt.Equal(claimed.ClaimedAt.Add(configApplyJobClaimLease)) {
		t.Fatalf("claim expires at = %s, want claimedAt + config apply lease", claimed.ClaimExpiresAt)
	}
	if _, err := store.ClaimNextJob(ctx, "node-apply", claimed.ClaimedAt.Add(defaultJobClaimLease+time.Second)); err != nil {
		t.Fatalf("claim during long config_apply lease: %v", err)
	}
	got, err := store.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get job during lease: %v", err)
	}
	if got.Status != protocol.JobStatusClaimed {
		t.Fatalf("job status after default lease = %q, want claimed", got.Status)
	}
	next, err := store.ClaimNextJob(ctx, "node-apply", claimed.ClaimExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("claim after config_apply timeout: %v", err)
	}
	if next != nil {
		t.Fatalf("next job = %#v, want nil after timeout", next)
	}
	got, err = store.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get job after timeout: %v", err)
	}
	if got.Status != protocol.JobStatusFailed || !IsJobClaimTimeout(*got) {
		t.Fatalf("job after timeout = %#v, want failed timeout", got)
	}
}

func TestMemoryNodeStoreRejectsClaimedConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	req := protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now); err != nil {
		t.Fatalf("create first config_apply: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-apply", now.Add(time.Second)); err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now.Add(2*time.Second)); err != ErrActiveJobExists {
		t.Fatalf("duplicate claimed config_apply error = %v, want ErrActiveJobExists", err)
	}
}

func TestMemoryNodeStoreAllowsConfigApplyForDifferentNodeOrPath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for _, nodeID := range []string{"node-a", "node-b"} {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: nodeID}, now); err != nil {
			t.Fatalf("record heartbeat %s: %v", nodeID, err)
		}
	}

	reqA := protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply, PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/a.yaml")}
	reqB := protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply, PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/b.yaml")}
	if _, err := store.CreateJob(ctx, reqA, "node-a", now); err != nil {
		t.Fatalf("create node-a config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, reqB, "node-a", now.Add(time.Second)); err != nil {
		t.Fatalf("create different path config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, reqA, "node-b", now.Add(2*time.Second)); err != nil {
		t.Fatalf("create different node config_apply: %v", err)
	}
}

func TestMemoryAuditEventsInsertAndListNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	older, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{
		Actor:      "operator",
		Action:     "job.create",
		TargetNode: "node-a",
		Detail:     "deep_probe",
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatalf("append older audit: %v", err)
	}
	newer, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{
		Actor:      "sidecar",
		Action:     "job.complete",
		TargetNode: "node-a",
		Detail:     "deep_probe",
		CreatedAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("append newer audit: %v", err)
	}

	events, err := store.ListAuditEvents(ctx, 1)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].ID != newer.ID || events[0].ID == older.ID {
		t.Fatalf("events order/limit = %#v, want newest only", events)
	}
}

func TestMemoryDesiredConfigPersistsCopy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Model: "gpt-5-mini"},
		},
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"node-a/hermes/default": {Provider: "anthropic", Model: "claude-sonnet-4"},
		},
	}
	if err := store.SetDesiredConfig(ctx, desired, time.Now().UTC()); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	desired.NodeOverrides["node-a"] = protocol.ProviderModelConfig{Model: "mutated"}
	desired.NodeRuntimeProfileOverrides["node-a/hermes/default"] = protocol.ProviderModelConfig{Model: "mutated"}

	got, err := store.GetDesiredConfig(ctx)
	if err != nil {
		t.Fatalf("get desired config: %v", err)
	}
	if got.NodeOverrides["node-a"].Model != "gpt-5-mini" {
		t.Fatalf("stored desired config mutated: %#v", got)
	}
	if got.NodeRuntimeProfileOverrides["node-a/hermes/default"].Model != "claude-sonnet-4" {
		t.Fatalf("stored node runtime profile desired config mutated: %#v", got)
	}
}
