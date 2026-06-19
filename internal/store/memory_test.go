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
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes", Warnings: []string{"config path unreadable"}}},
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
	nodes[0].Runtimes[0].Warnings[0] = "mutated"
	again, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes again: %v", err)
	}
	if again[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("store snapshot was mutated: %#v", again[0].Runtimes)
	}
	if len(again[0].Runtimes[0].Warnings) != 1 || again[0].Runtimes[0].Warnings[0] != "config path unreadable" {
		t.Fatalf("runtime warnings = %#v, want preserved warning", again[0].Runtimes[0].Warnings)
	}
}

func TestMemoryNodeStorePruneHeartbeatsKeepsLatestPerNode(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-a"}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record node-a heartbeat %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-b"}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record node-b heartbeat %d: %v", i, err)
		}
	}

	deleted, err := store.PruneHeartbeats(ctx, 2)
	if err != nil {
		t.Fatalf("prune heartbeats: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}

	gotA := store.heartbeats["node-a"]
	if len(gotA) != 2 || !gotA[0].Equal(now.Add(4*time.Minute)) || !gotA[1].Equal(now.Add(3*time.Minute)) {
		t.Fatalf("node-a heartbeats = %v, want latest two", gotA)
	}
	gotB := store.heartbeats["node-b"]
	if len(gotB) != 2 || !gotB[0].Equal(now.Add(2*time.Minute)) || !gotB[1].Equal(now.Add(time.Minute)) {
		t.Fatalf("node-b heartbeats = %v, want latest two", gotB)
	}

	deleted, err = store.PruneHeartbeats(ctx, 2)
	if err != nil {
		t.Fatalf("second prune heartbeats: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second deleted = %d, want 0", deleted)
	}
}

func TestMemoryNodeStoreDeleteNodeRemovesAssociatedData(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	tokenResp, err := store.CreateEnrollmentToken(ctx, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	enrollResp, err := store.EnrollNode(ctx, protocol.EnrollNodeRequest{Token: tokenResp.Token, NodeID: "node-delete"}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("enroll node-delete: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-delete",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-keep"}, now); err != nil {
		t.Fatalf("record keep heartbeat: %v", err)
	}
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-delete", now); err != nil {
		t.Fatalf("create delete job: %v", err)
	}
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-keep", now); err != nil {
		t.Fatalf("create keep job: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "job.create", TargetNode: "node-delete", CreatedAt: now}); err != nil {
		t.Fatalf("append delete audit: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "job.create", TargetNode: "node-keep", CreatedAt: now}); err != nil {
		t.Fatalf("append keep audit: %v", err)
	}

	if err := store.DeleteNode(ctx, "node-delete"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	exists, err := store.NodeExists(ctx, "node-delete")
	if err != nil {
		t.Fatalf("node exists: %v", err)
	}
	if exists {
		t.Fatalf("node-delete still exists")
	}
	ok, err := store.VerifyNodeCredential(ctx, "node-delete", enrollResp.NodeCredential)
	if err != nil {
		t.Fatalf("verify deleted credential: %v", err)
	}
	if ok {
		t.Fatalf("deleted node credential still verifies")
	}
	jobs, err := store.ListNodeJobs(ctx, "node-delete")
	if err != nil {
		t.Fatalf("list deleted jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("deleted jobs length = %d, want 0", len(jobs))
	}
	events, err := store.ListAuditEvents(ctx, 100)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	for _, event := range events {
		if event.TargetNode == "node-delete" {
			t.Fatalf("audit event for deleted node remains: %#v", event)
		}
	}
	if err := store.DeleteNode(ctx, "node-delete"); err != ErrNodeNotFound {
		t.Fatalf("delete missing node error = %v, want ErrNodeNotFound", err)
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

func TestMemoryNodeStoreListNodeJobsFiltered(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-jobs"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	older, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create older job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-jobs", now.Add(time.Second)); err != nil {
		t.Fatalf("claim older job: %v", err)
	}
	if err := store.CompleteJob(ctx, older.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete older job: %v", err)
	}
	newer, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create newer job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-jobs", now.Add(4*time.Second)); err != nil {
		t.Fatalf("claim newer job: %v", err)
	}
	if err := store.CompleteJob(ctx, newer.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(5*time.Second)); err != nil {
		t.Fatalf("complete newer job: %v", err)
	}
	pending, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	completed, err := store.ListNodeJobsFiltered(ctx, "node-jobs", JobFilter{
		Limit:  1,
		Status: protocol.JobStatusCompleted,
	})
	if err != nil {
		t.Fatalf("list completed jobs: %v", err)
	}
	if len(completed) != 1 || completed[0].ID != newer.ID {
		t.Fatalf("completed jobs = %#v, want newest completed job %s", completed, newer.ID)
	}

	pendingJobs, err := store.ListNodeJobsFiltered(ctx, "node-jobs", JobFilter{Status: protocol.JobStatusPending})
	if err != nil {
		t.Fatalf("list pending jobs: %v", err)
	}
	if len(pendingJobs) != 1 || pendingJobs[0].ID != pending.ID {
		t.Fatalf("pending jobs = %#v, want pending job %s", pendingJobs, pending.ID)
	}
}

func TestMemoryNodeStorePrunesTerminalJobsAndAuditEvents(t *testing.T) {
	assertRetentionPruning(t, NewMemoryNodeStore())
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

func TestMemoryAuditEventsFilteredByNodeActionAndLimit(t *testing.T) {
	assertAuditFiltering(t, NewMemoryNodeStore())
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

func assertAuditFiltering(t *testing.T, auditStore AuditStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	events := []protocol.AuditEvent{
		{Actor: "operator", Action: "job.create", TargetNode: "node-a", CreatedAt: now},
		{Actor: "operator", Action: "job.create", TargetNode: "node-b", CreatedAt: now.Add(time.Minute)},
		{Actor: "sidecar", Action: "job.fail", TargetNode: "node-a", CreatedAt: now.Add(2 * time.Minute)},
	}
	for _, event := range events {
		if _, err := auditStore.AppendAuditEvent(ctx, event); err != nil {
			t.Fatalf("append audit event: %v", err)
		}
	}

	got, err := auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a"})
	if err != nil {
		t.Fatalf("filter by node: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.fail", "node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{Action: "job.create"})
	if err != nil {
		t.Fatalf("filter by action: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-b/job.create", "node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a", Action: "job.create"})
	if err != nil {
		t.Fatalf("filter by node and action: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a", Limit: 1})
	if err != nil {
		t.Fatalf("filter with limit: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.fail"})
}

func assertRetentionPruning(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-retention"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	oldCompleted, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("create old completed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", oldCompleted.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim old completed job: %v", err)
	}
	if err := store.CompleteJob(ctx, oldCompleted.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, oldCompleted.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("complete old job: %v", err)
	}

	oldFailed, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-71*time.Hour))
	if err != nil {
		t.Fatalf("create old failed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", oldFailed.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim old failed job: %v", err)
	}
	if err := store.FailJob(ctx, oldFailed.ID, protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: "probe failed"}, oldFailed.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("fail old job: %v", err)
	}

	recentCompleted, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("create recent completed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", recentCompleted.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim recent completed job: %v", err)
	}
	if err := store.CompleteJob(ctx, recentCompleted.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, recentCompleted.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("complete recent job: %v", err)
	}

	claimed, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRestart}, "node-retention", now.Add(-70*time.Hour))
	if err != nil {
		t.Fatalf("create claimed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", claimed.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim active job: %v", err)
	}
	pending, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRollback}, "node-retention", claimed.CreatedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	deletedJobs, err := store.PruneTerminalJobs(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune terminal jobs: %v", err)
	}
	if deletedJobs != 2 {
		t.Fatalf("deleted jobs = %d, want 2", deletedJobs)
	}
	for _, jobID := range []string{oldCompleted.ID, oldFailed.ID} {
		got, err := store.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get pruned job %s: %v", jobID, err)
		}
		if got != nil {
			t.Fatalf("job %s remains after pruning: %#v", jobID, got)
		}
	}
	for _, jobID := range []string{recentCompleted.ID, claimed.ID, pending.ID} {
		got, err := store.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get retained job %s: %v", jobID, err)
		}
		if got == nil {
			t.Fatalf("job %s was pruned unexpectedly", jobID)
		}
	}

	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "old", TargetNode: "node-retention", CreatedAt: now.Add(-72 * time.Hour)}); err != nil {
		t.Fatalf("append old audit event: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "recent", TargetNode: "node-retention", CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("append recent audit event: %v", err)
	}
	deletedEvents, err := store.PruneAuditEvents(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune audit events: %v", err)
	}
	if deletedEvents != 1 {
		t.Fatalf("deleted audit events = %d, want 1", deletedEvents)
	}
	events, err := store.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-retention"})
	if err != nil {
		t.Fatalf("list audit events after pruning: %v", err)
	}
	assertAuditEventKeys(t, events, []string{"node-retention/recent"})
}

func assertAuditEventKeys(t *testing.T, events []protocol.AuditEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event length = %d, want %d: %#v", len(events), len(want), events)
	}
	for i, event := range events {
		got := event.TargetNode + "/" + event.Action
		if got != want[i] {
			t.Fatalf("event[%d] = %q, want %q; events=%#v", i, got, want[i], events)
		}
	}
}
