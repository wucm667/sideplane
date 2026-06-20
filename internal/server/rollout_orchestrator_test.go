package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/audit"
	rolloutengine "github.com/wucm667/sideplane/internal/rollout"
	"github.com/wucm667/sideplane/internal/store"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestRolloutOrchestratorFastIntervalDrivesHTTPRollouts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	clock := newRolloutTestClock(now)
	nodeStore := store.NewMemoryNodeStore()
	keyPair := generateRolloutSigningKey(t)
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       DefaultFreshnessPolicy(),
		AllowUnauthenticatedOperatorAPI: true,
		SigningKeyPair:                  keyPair,
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	enrollRolloutNode(t, nodeStore, "node-ok", now)
	seedRolloutProbe(t, nodeStore, "node-ok", "hermes", "", "/etc/sideplane-test/ok.json", now)
	enrollRolloutNode(t, nodeStore, "node-fail", now)
	enrollRolloutNode(t, nodeStore, "node-hold", now)
	seedRolloutProbe(t, nodeStore, "node-fail", "hermes", "", "/etc/sideplane-test/fail.json", now)
	seedRolloutProbe(t, nodeStore, "node-hold", "hermes", "", "/etc/sideplane-test/hold.json", now)

	StartRolloutOrchestrator(ctx, RolloutOrchestratorConfig{
		Store: nodeStore,
		Freshness: FreshnessPolicy{
			StaleAfter:   time.Minute,
			OfflineAfter: 5 * time.Minute,
			Now:          clock.Now,
		},
		SigningKey: keyPair,
		Interval:   5 * time.Millisecond,
		Now:        clock.Now,
	})

	dryRun := doJSONRequest[protocol.CreateRolloutResponse](t, server.Client(), http.MethodPost, server.URL+"/api/rollouts", "", protocol.CreateRolloutRequest{
		Spec: protocol.RolloutSpec{
			NodeIDs:     []string{"node-ok"},
			RuntimeType: "hermes",
			Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
			BatchSize:   1,
		},
	})
	dispatched := waitForRolloutCondition(t, nodeStore, dryRun.Rollout.ID, func(ro protocol.Rollout) bool {
		return ro.Batches[0].Nodes["node-ok"].JobID != ""
	})
	okJobID := dispatched.Batches[0].Nodes["node-ok"].JobID
	if _, err := nodeStore.ClaimNextJob(ctx, "node-ok", now.Add(time.Second)); err != nil {
		t.Fatalf("claim node-ok config apply: %v", err)
	}
	if err := nodeStore.CompleteJob(ctx, okJobID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete node-ok config apply: %v", err)
	}
	clock.Set(now.Add(3 * time.Second))
	completed := waitForRolloutCondition(t, nodeStore, dryRun.Rollout.ID, func(ro protocol.Rollout) bool {
		return ro.State == protocol.RolloutStateCompleted
	})
	if completed.Batches[0].Nodes["node-ok"].State != protocol.RolloutNodeStateSucceeded {
		t.Fatalf("node-ok state = %q, want succeeded", completed.Batches[0].Nodes["node-ok"].State)
	}

	failing := doJSONRequest[protocol.CreateRolloutResponse](t, server.Client(), http.MethodPost, server.URL+"/api/rollouts", "", protocol.CreateRolloutRequest{
		Spec: protocol.RolloutSpec{
			NodeIDs:     []string{"node-fail", "node-hold"},
			RuntimeType: "hermes",
			Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
			BatchSize:   1,
		},
	})
	failDispatched := waitForRolloutCondition(t, nodeStore, failing.Rollout.ID, func(ro protocol.Rollout) bool {
		return ro.Batches[0].Nodes["node-fail"].JobID != ""
	})
	failJobID := failDispatched.Batches[0].Nodes["node-fail"].JobID
	if _, err := nodeStore.ClaimNextJob(ctx, "node-fail", now.Add(4*time.Second)); err != nil {
		t.Fatalf("claim node-fail config apply: %v", err)
	}
	if err := nodeStore.FailJob(ctx, failJobID, protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: "validation failed"}, now.Add(5*time.Second)); err != nil {
		t.Fatalf("fail node-fail config apply: %v", err)
	}
	clock.Set(now.Add(6 * time.Second))
	paused := waitForRolloutCondition(t, nodeStore, failing.Rollout.ID, func(ro protocol.Rollout) bool {
		return ro.State == protocol.RolloutStatePaused
	})
	if len(paused.FailingNodeIDs) != 1 || paused.FailingNodeIDs[0] != "node-fail" {
		t.Fatalf("failing nodes = %+v, want node-fail", paused.FailingNodeIDs)
	}
	if got := paused.Batches[1].Nodes["node-hold"]; got.JobID != "" || got.State != protocol.RolloutNodeStatePending {
		t.Fatalf("held node progress = %+v, want undispatched pending", got)
	}
}

func TestRolloutOrchestratorDispatchesExistingConfigApplyPipeline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	enrollRolloutNode(t, nodeStore, "node-a", now)
	seedRolloutProbe(t, nodeStore, "node-a", "hermes", "default", "/etc/sideplane-test/hermes.json", now)
	keyPair := generateRolloutSigningKey(t)
	created := createTestRollout(t, nodeStore, protocol.RolloutSpec{
		NodeIDs:       []string{"node-a"},
		RuntimeType:   "hermes",
		Profile:       "default",
		Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:     1,
		HealthTimeout: time.Minute,
	}, now)
	orchestrator := newTestRolloutOrchestrator(nodeStore, keyPair, now)

	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile dispatch: %v", err)
	}

	updated := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	progress := updated.Batches[0].Nodes["node-a"]
	if updated.State != protocol.RolloutStateRunning || progress.State != protocol.RolloutNodeStateDispatched {
		t.Fatalf("rollout state=%q node state=%q, want running/dispatched", updated.State, progress.State)
	}
	job, err := nodeStore.GetJob(ctx, progress.JobID)
	if err != nil {
		t.Fatalf("get dispatched job: %v", err)
	}
	if job == nil || job.Type != protocol.JobTypeConfigApply || job.Status != protocol.JobStatusPending {
		t.Fatalf("job = %+v, want pending config_apply", job)
	}
	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signed); err != nil {
		t.Fatalf("decode signed rollout plan: %v", err)
	}
	if err := protocol.VerifySignedConfigPlan(signed, keyPair.PublicKey); err != nil {
		t.Fatalf("verify signed rollout plan: %v", err)
	}
	if signed.Plan.TargetNodeID != "node-a" || signed.Plan.Mode != protocol.ConfigPlanModeDryRun || !signed.Plan.Body.DryRun {
		t.Fatalf("signed plan target/mode = node:%q mode:%q dryRun:%t", signed.Plan.TargetNodeID, signed.Plan.Mode, signed.Plan.Body.DryRun)
	}
	if signed.Plan.Body.Profile != "/etc/sideplane-test/hermes.json" {
		t.Fatalf("signed plan profile = %q, want config path", signed.Plan.Body.Profile)
	}
	if signed.Plan.Body.Desired.Provider != "openai" || signed.Plan.Body.Desired.Model != "gpt-4o" {
		t.Fatalf("signed plan desired = %+v, want openai/gpt-4o", signed.Plan.Body.Desired)
	}

	if _, err := nodeStore.ClaimNextJob(ctx, "node-a", now.Add(time.Second)); err != nil {
		t.Fatalf("claim config apply: %v", err)
	}
	result, err := json.Marshal(protocol.ConfigApplyResult{PlanID: signed.Plan.ID, DryRun: true})
	if err != nil {
		t.Fatalf("marshal apply result: %v", err)
	}
	if err := nodeStore.CompleteJob(ctx, job.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted, ResultJSON: string(result)}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete config apply: %v", err)
	}
	orchestrator.now = func() time.Time { return now.Add(3 * time.Second) }
	orchestrator.freshness.Now = orchestrator.now
	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile completion: %v", err)
	}

	completed := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	if completed.State != protocol.RolloutStateCompleted {
		t.Fatalf("rollout state = %q, want completed", completed.State)
	}
	if completed.Batches[0].Nodes["node-a"].State != protocol.RolloutNodeStateSucceeded {
		t.Fatalf("node state = %q, want succeeded", completed.Batches[0].Nodes["node-a"].State)
	}
}

func TestRolloutOrchestratorPausesOnApplyFailureAndStopsDispatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	enrollRolloutNode(t, nodeStore, "node-a", now)
	enrollRolloutNode(t, nodeStore, "node-b", now)
	seedRolloutProbe(t, nodeStore, "node-a", "hermes", "", "/etc/sideplane-test/a.json", now)
	seedRolloutProbe(t, nodeStore, "node-b", "hermes", "", "/etc/sideplane-test/b.json", now)
	created := createTestRollout(t, nodeStore, protocol.RolloutSpec{
		NodeIDs:       []string{"node-a", "node-b"},
		RuntimeType:   "hermes",
		Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:     1,
		HealthTimeout: time.Minute,
	}, now)
	orchestrator := newTestRolloutOrchestrator(nodeStore, generateRolloutSigningKey(t), now)

	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile dispatch: %v", err)
	}
	running := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	jobID := running.Batches[0].Nodes["node-a"].JobID
	if _, err := nodeStore.ClaimNextJob(ctx, "node-a", now.Add(time.Second)); err != nil {
		t.Fatalf("claim config apply: %v", err)
	}
	if err := nodeStore.FailJob(ctx, jobID, protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: "validation failed"}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("fail config apply: %v", err)
	}
	orchestrator.now = func() time.Time { return now.Add(3 * time.Second) }
	orchestrator.freshness.Now = orchestrator.now
	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile failure: %v", err)
	}

	paused := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	if paused.State != protocol.RolloutStatePaused {
		t.Fatalf("rollout state = %q, want paused", paused.State)
	}
	if !strings.Contains(paused.PauseReason, "config apply failed") {
		t.Fatalf("pause reason = %q, want config apply failed", paused.PauseReason)
	}
	if len(paused.FailingNodeIDs) != 1 || paused.FailingNodeIDs[0] != "node-a" {
		t.Fatalf("failing nodes = %+v, want node-a", paused.FailingNodeIDs)
	}
	if got := paused.Batches[1].Nodes["node-b"]; got.JobID != "" || got.State != protocol.RolloutNodeStatePending {
		t.Fatalf("second batch progress = %+v, want undispatched pending", got)
	}
	jobs, err := nodeStore.ListNodeJobs(ctx, "node-b")
	if err != nil {
		t.Fatalf("list node-b jobs: %v", err)
	}
	configApplyJobs := 0
	for _, job := range jobs {
		if job.Type == protocol.JobTypeConfigApply {
			configApplyJobs++
		}
	}
	if configApplyJobs != 0 {
		t.Fatalf("node-b config apply jobs = %d, want 0", configApplyJobs)
	}
}

func TestRolloutTemplatesEndpointsAndPrefill(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	enrollRolloutNode(t, nodeStore, "node-a", now)
	seedRolloutProbe(t, nodeStore, "node-a", "hermes", "", "/etc/sideplane-test/a.json", now)
	keyPair := generateRolloutSigningKey(t)
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       DefaultFreshnessPolicy(),
		AllowUnauthenticatedOperatorAPI: true,
		SigningKeyPair:                  keyPair,
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	created := doJSONRequest[protocol.CreateRolloutTemplateResponse](t, server.Client(), http.MethodPost, server.URL+"/api/rollout-templates", "", protocol.CreateRolloutTemplateRequest{
		Name: "canary",
		Spec: protocol.RolloutSpec{
			NodeIDs:     []string{"node-a"},
			RuntimeType: "hermes",
			Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
			BatchSize:   3,
		},
	})
	if created.Template.ID == "" || created.Template.Spec.BatchSize != 3 {
		t.Fatalf("created template = %+v, want id and spec", created.Template)
	}

	listed := doJSONRequest[protocol.ListRolloutTemplatesResponse](t, server.Client(), http.MethodGet, server.URL+"/api/rollout-templates", "", nil)
	if len(listed.Templates) != 1 || listed.Templates[0].ID != created.Template.ID {
		t.Fatalf("listed templates = %+v, want created template", listed.Templates)
	}

	// Creating a rollout from the template prefills the spec (batchSize 3).
	rollout := doJSONRequest[protocol.CreateRolloutResponse](t, server.Client(), http.MethodPost, server.URL+"/api/rollouts", "", protocol.CreateRolloutRequest{
		TemplateID: created.Template.ID,
	})
	if rollout.Rollout.Spec.BatchSize != 3 || rollout.Rollout.Spec.Target.Model != "gpt-4o" {
		t.Fatalf("rollout spec = %+v, want prefilled from template", rollout.Rollout.Spec)
	}
	if len(rollout.Rollout.Spec.NodeIDs) != 1 || rollout.Rollout.Spec.NodeIDs[0] != "node-a" {
		t.Fatalf("rollout nodes = %+v, want resolved node-a", rollout.Rollout.Spec.NodeIDs)
	}

	// Unknown template id -> 404.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/rollouts", strings.NewReader(`{"templateId":"rtpl_missing"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("missing template request: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing template status = %d, want 404", res.StatusCode)
	}

	// Delete template.
	delReq, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/rollout-templates/"+created.Template.ID, nil)
	delRes, err := server.Client().Do(delReq)
	if err != nil {
		t.Fatalf("delete template: %v", err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delRes.StatusCode)
	}

	events, err := nodeStore.ListAuditEventsFiltered(ctx, store.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	var sawCreate, sawDelete bool
	for _, event := range events {
		switch event.Action {
		case audit.ActionRolloutTemplateCreate:
			sawCreate = true
		case audit.ActionRolloutTemplateDelete:
			sawDelete = true
		}
	}
	if !sawCreate || !sawDelete {
		t.Fatalf("template audit actions create=%t delete=%t", sawCreate, sawDelete)
	}
}

func TestRolloutOrchestratorAutoRollbackDispatchesRollbackJob(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	enrollRolloutNode(t, nodeStore, "node-a", now)
	enrollRolloutNode(t, nodeStore, "node-b", now)
	seedRolloutProbe(t, nodeStore, "node-a", "hermes", "", "/etc/sideplane-test/a.json", now)
	seedRolloutProbe(t, nodeStore, "node-b", "hermes", "", "/etc/sideplane-test/b.json", now)
	created := createTestRollout(t, nodeStore, protocol.RolloutSpec{
		NodeIDs:               []string{"node-a", "node-b"},
		RuntimeType:           "hermes",
		Target:                protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:             2,
		Live:                  true,
		AutoRollbackOnFailure: true,
		HealthTimeout:         time.Minute,
	}, now)
	orchestrator := newTestRolloutOrchestrator(nodeStore, generateRolloutSigningKey(t), now)

	// Dispatch config-apply for both nodes in the single batch.
	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile dispatch: %v", err)
	}
	dispatched := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	applyJobID := dispatched.Batches[0].Nodes["node-a"].JobID
	failJobID := dispatched.Batches[0].Nodes["node-b"].JobID

	// node-a applies cleanly with a known backup, and its runtime now matches
	// the target so the live rollout sees no drift.
	if _, err := nodeStore.ClaimNextJob(ctx, "node-a", now.Add(time.Second)); err != nil {
		t.Fatalf("claim node-a apply: %v", err)
	}
	applyResult, err := json.Marshal(protocol.ConfigApplyResult{
		PlanID:     "plan-a",
		BackupPath: "/etc/sideplane-test/a.json.bak",
	})
	if err != nil {
		t.Fatalf("marshal node-a apply result: %v", err)
	}
	if err := nodeStore.CompleteJob(ctx, applyJobID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted, ResultJSON: string(applyResult)}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete node-a apply: %v", err)
	}
	if _, err := nodeStore.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-a",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes", Provider: "openai", Model: "gpt-4o"}},
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("record node-a matching heartbeat: %v", err)
	}

	// node-b fails its apply, which fails the batch.
	if _, err := nodeStore.ClaimNextJob(ctx, "node-b", now.Add(time.Second)); err != nil {
		t.Fatalf("claim node-b apply: %v", err)
	}
	if err := nodeStore.FailJob(ctx, failJobID, protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: "validation failed"}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("fail node-b apply: %v", err)
	}

	orchestrator.now = func() time.Time { return now.Add(3 * time.Second) }
	orchestrator.freshness.Now = orchestrator.now
	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile failure: %v", err)
	}

	paused := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	if paused.State != protocol.RolloutStatePaused {
		t.Fatalf("rollout state = %q, want paused", paused.State)
	}
	if !strings.Contains(paused.PauseReason, "auto-rollback attempted") {
		t.Fatalf("pause reason = %q, want auto-rollback note", paused.PauseReason)
	}
	nodeA := paused.Batches[0].Nodes["node-a"]
	if !nodeA.RolledBack || nodeA.RollbackJobID == "" {
		t.Fatalf("node-a = %+v, want rolled back with job id", nodeA)
	}
	if nodeB := paused.Batches[0].Nodes["node-b"]; nodeB.RolledBack {
		t.Fatalf("node-b = %+v, want failing node not rolled back", nodeB)
	}
	rollbackJob, err := nodeStore.GetJob(ctx, nodeA.RollbackJobID)
	if err != nil {
		t.Fatalf("get rollback job: %v", err)
	}
	if rollbackJob == nil || rollbackJob.Type != protocol.JobTypeRollback {
		t.Fatalf("rollback job = %+v, want a rollback job", rollbackJob)
	}
	var payload protocol.RollbackJobPayload
	if err := json.Unmarshal([]byte(rollbackJob.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode rollback payload: %v", err)
	}
	if payload.DryRun {
		t.Fatalf("rollback payload = %+v, want live rollback for live rollout", payload)
	}
	if payload.BackupPath != "/etc/sideplane-test/a.json.bak" {
		t.Fatalf("rollback payload backup path = %q, want node-a backup", payload.BackupPath)
	}
}

func TestRolloutOrchestratorSkipsPausedRollouts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	enrollRolloutNode(t, nodeStore, "node-paused", now)
	seedRolloutProbe(t, nodeStore, "node-paused", "hermes", "", "/etc/sideplane-test/paused.json", now)
	created := createTestRollout(t, nodeStore, protocol.RolloutSpec{
		NodeIDs:       []string{"node-paused"},
		RuntimeType:   "hermes",
		Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:     1,
		HealthTimeout: time.Minute,
	}, now)
	created.State = protocol.RolloutStatePaused
	created.PauseReason = "operator paused"
	created.Batches[0].State = protocol.RolloutBatchStatePaused
	if err := nodeStore.UpdateRollout(ctx, created); err != nil {
		t.Fatalf("pause rollout: %v", err)
	}

	orchestrator := newTestRolloutOrchestrator(nodeStore, generateRolloutSigningKey(t), now)
	if err := orchestrator.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile paused: %v", err)
	}

	paused := getRolloutForOrchestratorTest(t, nodeStore, created.ID)
	if paused.State != protocol.RolloutStatePaused || paused.Batches[0].Nodes["node-paused"].JobID != "" {
		t.Fatalf("paused rollout changed unexpectedly: %+v", paused)
	}
}

func enrollRolloutNode(t *testing.T, nodeStore store.Store, nodeID string, now time.Time) {
	t.Helper()
	enrollTestNode(t, nodeStore, nodeID)
	if _, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
		NodeID: nodeID,
		Runtimes: []protocol.RuntimeStatus{{
			Name:     "default",
			Type:     "hermes",
			Provider: "anthropic",
			Model:    "claude-3-7-sonnet",
		}},
	}, now); err != nil {
		t.Fatalf("record heartbeat for %s: %v", nodeID, err)
	}
}

func seedRolloutProbe(t *testing.T, nodeStore store.Store, nodeID string, runtimeType string, profile string, configPath string, now time.Time) {
	t.Helper()
	probe, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, nodeID, now)
	if err != nil {
		t.Fatalf("create rollout probe for %s: %v", nodeID, err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), nodeID, now.Add(time.Second)); err != nil {
		t.Fatalf("claim rollout probe for %s: %v", nodeID, err)
	}
	result, err := json.Marshal(protocol.DeepProbeResult{
		ConfigSnapshots: []protocol.RuntimeConfigSnapshot{{
			RuntimeName: "default",
			RuntimeType: runtimeType,
			ConfigPath:  configPath,
			Profile:     profile,
			Provider:    "anthropic",
			Model:       "claude-3-7-sonnet",
			ConfigHash:  "sha256:test",
		}},
	})
	if err != nil {
		t.Fatalf("marshal rollout probe result: %v", err)
	}
	if err := nodeStore.CompleteJob(context.Background(), probe.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted, ResultJSON: string(result)}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete rollout probe for %s: %v", nodeID, err)
	}
}

func createTestRollout(t *testing.T, nodeStore store.Store, spec protocol.RolloutSpec, now time.Time) protocol.Rollout {
	t.Helper()
	created, err := nodeStore.CreateRollout(context.Background(), protocol.Rollout{
		Spec:      spec,
		State:     protocol.RolloutStatePending,
		Batches:   rolloutengine.PlanBatches(spec.NodeIDs, spec.BatchSize),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create rollout: %v", err)
	}
	return created
}

func newTestRolloutOrchestrator(nodeStore store.Store, keyPair spcrypto.KeyPair, now time.Time) *RolloutOrchestrator {
	return NewRolloutOrchestrator(RolloutOrchestratorConfig{
		Store: nodeStore,
		Freshness: FreshnessPolicy{
			StaleAfter:   time.Minute,
			OfflineAfter: 5 * time.Minute,
			Now:          func() time.Time { return now },
		},
		SigningKey: keyPair,
		Now:        func() time.Time { return now },
	})
}

func generateRolloutSigningKey(t *testing.T) spcrypto.KeyPair {
	t.Helper()
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return keyPair
}

func getRolloutForOrchestratorTest(t *testing.T, nodeStore store.Store, rolloutID string) protocol.Rollout {
	t.Helper()
	rollout, err := nodeStore.GetRollout(context.Background(), rolloutID)
	if err != nil {
		t.Fatalf("get rollout: %v", err)
	}
	if rollout == nil {
		t.Fatalf("rollout %q not found", rolloutID)
	}
	return *rollout
}

type rolloutTestClock struct {
	unixNano atomic.Int64
}

func newRolloutTestClock(now time.Time) *rolloutTestClock {
	clock := &rolloutTestClock{}
	clock.Set(now)
	return clock
}

func (c *rolloutTestClock) Now() time.Time {
	return time.Unix(0, c.unixNano.Load()).UTC()
}

func (c *rolloutTestClock) Set(now time.Time) {
	c.unixNano.Store(now.UTC().UnixNano())
}

func waitForRolloutCondition(t *testing.T, nodeStore store.Store, rolloutID string, ready func(protocol.Rollout) bool) protocol.Rollout {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last protocol.Rollout
	for time.Now().Before(deadline) {
		last = getRolloutForOrchestratorTest(t, nodeStore, rolloutID)
		if ready(last) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("rollout %s did not reach expected state before timeout; last=%+v", rolloutID, last)
	return protocol.Rollout{}
}
