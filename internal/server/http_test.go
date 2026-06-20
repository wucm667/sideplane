package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/audit"
	rolloutengine "github.com/wucm667/sideplane/internal/rollout"
	"github.com/wucm667/sideplane/internal/store"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func newDevHandlerWithStore(t *testing.T, nodeStore store.Store) http.Handler {
	t.Helper()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       DefaultFreshnessPolicy(),
		AllowUnauthenticatedOperatorAPI: true,
	})
	if err != nil {
		t.Fatalf("build dev handler: %v", err)
	}
	return handler
}

func newDevHandler(t *testing.T) http.Handler {
	t.Helper()
	return newDevHandlerWithStore(t, store.NewMemoryNodeStore())
}

func configApplyPayloadForHTTPTest(t *testing.T, runtimeType string, configPath string) string {
	t.Helper()
	payload, err := json.Marshal(protocol.SignedConfigPlan{
		Plan: protocol.ConfigPlan{
			Body: protocol.ConfigPlanBody{
				RuntimeType: runtimeType,
				Profile:     configPath,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal config apply payload: %v", err)
	}
	return string(payload)
}

func TestCreateJobAPI(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	body := strings.NewReader(`{"type":"deep_probe","payloadJson":"{}"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", body)

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if job.NodeID != "node-jobs" {
		t.Fatalf("job nodeId = %q, want node-jobs", job.NodeID)
	}
	if job.Type != protocol.JobTypeDeepProbe {
		t.Fatalf("job type = %q, want deep_probe", job.Type)
	}
	if job.Status != protocol.JobStatusPending {
		t.Fatalf("job status = %q, want pending", job.Status)
	}
}

func seedDesiredAndProbe(t *testing.T, nodeStore store.Store, nodeID, configPath string) {
	t.Helper()
	seedDesiredAndProfileProbe(t, nodeStore, nodeID, configPath, "")
}

func seedDesiredAndProfileProbe(t *testing.T, nodeStore store.Store, nodeID, configPath, profile string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := nodeStore.SetDesiredConfig(ctx, protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}, now); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	probe, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, nodeID, now)
	if err != nil {
		t.Fatalf("create probe: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(ctx, nodeID, now); err != nil {
		t.Fatalf("claim probe: %v", err)
	}
	resJSON, _ := json.Marshal(protocol.DeepProbeResult{
		Runtimes: []protocol.RuntimeStatus{},
		ConfigSnapshots: []protocol.RuntimeConfigSnapshot{{
			RuntimeName: "hermes",
			RuntimeType: "hermes",
			ConfigPath:  configPath,
			Profile:     profile,
			Provider:    "anthropic",
			Model:       "claude-3.7-sonnet",
		}},
	})
	if err := nodeStore.CompleteJob(ctx, probe.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(resJSON),
	}, now); err != nil {
		t.Fatalf("complete probe: %v", err)
	}
}

func seedRuntimeConfigSnapshot(t *testing.T, nodeStore store.Store, nodeID, provider, model string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	probe, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, nodeID, now)
	if err != nil {
		t.Fatalf("create probe for %s: %v", nodeID, err)
	}
	if _, err := nodeStore.ClaimNextJob(ctx, nodeID, now); err != nil {
		t.Fatalf("claim probe for %s: %v", nodeID, err)
	}
	resJSON, _ := json.Marshal(protocol.DeepProbeResult{
		ConfigSnapshots: []protocol.RuntimeConfigSnapshot{{
			RuntimeName: "hermes",
			RuntimeType: "hermes",
			ConfigPath:  "/etc/sideplane-test/runtime.json",
			Source:      "file",
			Provider:    provider,
			Model:       model,
			ConfigHash:  "sha256:test",
		}},
	})
	if err := nodeStore.CompleteJob(ctx, probe.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(resJSON),
	}, now); err != nil {
		t.Fatalf("complete probe for %s: %v", nodeID, err)
	}
}

func seedRollbackBackup(t *testing.T, nodeStore store.Store, nodeID, planID, configPath, backupPath string) protocol.RollbackBackup {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForHTTPTest(t, "hermes", configPath),
	}, nodeID, now)
	if err != nil {
		t.Fatalf("create config apply backup job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(ctx, nodeID, now); err != nil {
		t.Fatalf("claim config apply backup job: %v", err)
	}
	resultJSON, err := json.Marshal(protocol.ConfigApplyResult{
		PlanID:     planID,
		DryRun:     false,
		BackupPath: backupPath,
		Backup:     &protocol.RollbackBackup{ConfigHash: "sha256:" + planID},
		Steps:      []protocol.ConfigApplyStep{{Name: "backup_created", Status: "completed"}},
	})
	if err != nil {
		t.Fatalf("marshal config apply result: %v", err)
	}
	if err := nodeStore.CompleteJob(ctx, job.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(resultJSON),
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("complete config apply backup job: %v", err)
	}
	backup, ok := store.RollbackBackupFromJob(protocol.Job{
		ID:          job.ID,
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusCompleted,
		PayloadJSON: configApplyPayloadForHTTPTest(t, "hermes", configPath),
		ResultJSON:  string(resultJSON),
		CreatedAt:   now,
		FinishedAt:  now.Add(time.Second),
	})
	if !ok {
		t.Fatalf("seeded backup did not derive rollback metadata")
	}
	return backup
}

func TestCreateConfigApplyJobDryRun(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.Type != protocol.JobTypeConfigApply {
		t.Fatalf("job type = %q, want config_apply", job.Type)
	}

	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signed); err != nil {
		t.Fatalf("decode signed plan: %v", err)
	}
	if !signed.Plan.Body.DryRun || signed.Plan.Mode != protocol.ConfigPlanModeDryRun {
		t.Errorf("plan not dry-run: mode=%q dryRun=%t", signed.Plan.Mode, signed.Plan.Body.DryRun)
	}
	if signed.Plan.Body.Profile != "/etc/hermes/config.json" {
		t.Errorf("plan config path = %q, want /etc/hermes/config.json", signed.Plan.Body.Profile)
	}
	if signed.Plan.Body.Desired.Provider != "openai" || signed.Plan.Body.Desired.Model != "gpt-4o" {
		t.Errorf("plan desired = %+v, want openai/gpt-4o", signed.Plan.Body.Desired)
	}

	keyRec := httptest.NewRecorder()
	handler.ServeHTTP(keyRec, httptest.NewRequest(http.MethodGet, "/api/signing-key", nil))
	assertStatus(t, keyRec, http.StatusOK)
	var keyResp protocol.PublicSigningKeyResponse
	if err := json.NewDecoder(keyRec.Body).Decode(&keyResp); err != nil {
		t.Fatalf("decode signing key: %v", err)
	}
	pub, err := spcrypto.ParsePublicKey(keyResp.PublicKey)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if err := protocol.VerifySignedConfigPlan(signed, pub); err != nil {
		t.Errorf("server-signed plan failed verification: %v", err)
	}
}

func TestCreateConfigApplyJobUsesNodeRuntimeProfileOverride(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProfileProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json", "default")
	if err := nodeStore.SetDesiredConfig(context.Background(), protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"node-apply/hermes/default": {Provider: "local", Model: "qwen3"},
		},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{"runtimeType":"hermes","profile":"default"}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signed); err != nil {
		t.Fatalf("decode signed plan: %v", err)
	}
	if signed.Plan.Body.Desired.Provider != "local" || signed.Plan.Body.Desired.Model != "qwen3" {
		t.Fatalf("plan desired = %+v, want scoped local/qwen3 override", signed.Plan.Body.Desired)
	}
}

func TestCreateConfigApplyJobRejectsUnsafeProviderModelValues(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
	if err := nodeStore.SetDesiredConfig(context.Background(), protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5:bad"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set desired config: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid desired provider/model") {
		t.Fatalf("response = %q, want invalid desired provider/model", rec.Body.String())
	}
}

func TestCreateConfigApplyJobRejectsDuplicatePendingApply(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusConflict)
}

func TestCreateConfigApplyJobRejectsDuplicateClaimedApply(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-apply", time.Now().UTC()); err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusConflict)
}

func TestCreateConfigApplyJobAllowsDifferentNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-a")
	enrollTestNode(t, nodeStore, "node-b")
	seedDesiredAndProbe(t, nodeStore, "node-a", "/etc/hermes/config.json")
	seedDesiredAndProbe(t, nodeStore, "node-b", "/etc/hermes/config.json")
	handler := newDevHandlerWithStore(t, nodeStore)

	for _, nodeID := range []string{"node-a", "node-b"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/config-apply", strings.NewReader(`{}`))
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusCreated)
	}
}

func TestCreateConfigApplyJobRequiresConfigPath(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	if err := nodeStore.SetDesiredConfig(context.Background(), protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set desired: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestCreateConfigApplyJobRequiresDesired(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`))
	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestCreateConfigApplyJobUnknownNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/missing/config-apply", strings.NewReader(`{}`))
	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestCreateRestartJobDryRunDefaultAndAudit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-restart")
	handler := newDevHandlerWithStore(t, nodeStore)

	body, err := json.Marshal(protocol.RestartRequest{
		RuntimeType: "hermes",
		Profile:     "default",
		Reason:      "operator test restart",
	})
	if err != nil {
		t.Fatalf("marshal restart request: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-restart/restart", bytes.NewReader(body))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode restart job: %v", err)
	}
	if job.NodeID != "node-restart" || job.Type != protocol.JobTypeRestart || job.Status != protocol.JobStatusPending {
		t.Fatalf("restart job = %#v, want pending restart for node-restart", job)
	}

	var payload protocol.RestartJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode restart payload: %v", err)
	}
	if payload.RuntimeType != "hermes" || payload.Profile != "default" || payload.Reason != "operator test restart" {
		t.Fatalf("restart payload = %#v, want target and reason", payload)
	}
	if !payload.DryRun {
		t.Fatalf("restart payload dryRun = false, want dry-run default")
	}

	auditRec := httptest.NewRecorder()
	handler.ServeHTTP(auditRec, httptest.NewRequest(http.MethodGet, "/api/audit?action=restart&nodeId=node-restart", nil))
	assertStatus(t, auditRec, http.StatusOK)
	var auditResp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(auditResp.Events) != 1 {
		t.Fatalf("audit events = %d, want 1: %#v", len(auditResp.Events), auditResp.Events)
	}
	event := auditResp.Events[0]
	if event.Action != audit.ActionRestart || event.TargetNode != "node-restart" {
		t.Fatalf("audit event = %#v, want restart for node-restart", event)
	}
	for _, fragment := range []string{"job=" + job.ID, "mode=dry-run", "type=hermes", "profile=default"} {
		if !strings.Contains(event.Detail, fragment) {
			t.Fatalf("audit detail = %q, want %q", event.Detail, fragment)
		}
	}
}

func TestCreateRestartJobLivePayload(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-restart")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-restart/restart", strings.NewReader(`{"runtimeType":"openclaw","live":true}`))
	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode restart job: %v", err)
	}
	var payload protocol.RestartJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode restart payload: %v", err)
	}
	if payload.DryRun {
		t.Fatalf("restart payload dryRun = true, want live payload")
	}
	if payload.RuntimeType != "openclaw" {
		t.Fatalf("restart payload runtimeType = %q, want openclaw", payload.RuntimeType)
	}
}

func TestCreateRestartJobRejectsUnknownNodeAndInvalidPayload(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "unknown node",
			path:       "/api/nodes/missing/restart",
			body:       `{}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "malformed JSON",
			path:       "/api/nodes/node-restart/restart",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unsupported runtime type",
			path:       "/api/nodes/node-restart/restart",
			body:       `{"runtimeType":"unknown"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown field",
			path:       "/api/nodes/node-restart/restart",
			body:       `{"runtimeType":"hermes","configPath":"/tmp/not-used"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			enrollTestNode(t, nodeStore, "node-restart")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
			assertStatus(t, rec, tt.wantStatus)
		})
	}
}

func TestCreateRollbackJobRequiresKnownBackupAndWritesAudit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-rollback")
	backup := seedRollbackBackup(t, nodeStore, "node-rollback", "plan_rollback", "/tmp/sideplane-test/config.json", "/tmp/sideplane-test/current.backup")
	handler := newDevHandlerWithStore(t, nodeStore)

	body, err := json.Marshal(protocol.RollbackRequest{
		RuntimeType: "hermes",
		BackupRef:   backup.Ref,
	})
	if err != nil {
		t.Fatalf("marshal rollback request: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-rollback/rollback", bytes.NewReader(body))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode rollback job: %v", err)
	}
	if job.Type != protocol.JobTypeRollback || job.NodeID != "node-rollback" {
		t.Fatalf("rollback job = %#v, want rollback for node-rollback", job)
	}
	var payload protocol.RollbackJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode rollback payload: %v", err)
	}
	if payload.BackupRef != backup.Ref || payload.BackupPath != backup.BackupPath || payload.ConfigPath != backup.ConfigPath {
		t.Fatalf("rollback payload = %#v, want server-derived backup metadata %#v", payload, backup)
	}
	if !payload.DryRun {
		t.Fatalf("rollback payload dryRun = false, want dry-run default")
	}

	auditRec := httptest.NewRecorder()
	handler.ServeHTTP(auditRec, httptest.NewRequest(http.MethodGet, "/api/audit?action=rollback&nodeId=node-rollback", nil))
	assertStatus(t, auditRec, http.StatusOK)
	var auditResp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(auditResp.Events) != 1 {
		t.Fatalf("audit events = %d, want 1: %#v", len(auditResp.Events), auditResp.Events)
	}
	event := auditResp.Events[0]
	if event.Action != audit.ActionRollback || event.TargetNode != "node-rollback" {
		t.Fatalf("audit event = %#v, want rollback for node-rollback", event)
	}
	for _, fragment := range []string{"job=" + job.ID, "mode=dry-run", "backupRef=" + backup.Ref} {
		if !strings.Contains(event.Detail, fragment) {
			t.Fatalf("audit detail = %q, want %q", event.Detail, fragment)
		}
	}
}

func TestListNodeBackupsAPI(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-backups")
	first := seedRollbackBackup(t, nodeStore, "node-backups", "plan_first", "/tmp/sideplane-test/config.json", "/tmp/sideplane-test/first.backup")
	time.Sleep(time.Millisecond)
	second := seedRollbackBackup(t, nodeStore, "node-backups", "plan_second", "/tmp/sideplane-test/config.json", "/tmp/sideplane-test/second.backup")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-backups/backups?limit=1", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var resp protocol.ListRollbackBackupsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode backups response: %v", err)
	}
	if resp.Total != 2 || resp.Limit != 1 || len(resp.Backups) != 1 {
		t.Fatalf("backup page = %#v, want total 2 limit 1 one item", resp)
	}
	if resp.Backups[0].Ref != second.Ref || resp.Backups[0].SourceJobID != second.SourceJobID {
		t.Fatalf("first backup = %#v, want newest second backup %#v", resp.Backups[0], second)
	}
	if resp.Backups[0].RuntimeType != "hermes" || resp.Backups[0].ConfigHash != "sha256:plan_second" {
		t.Fatalf("backup metadata = %#v, want hermes and config hash", resp.Backups[0])
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal backups response: %v", err)
	}
	for _, forbidden := range []string{first.BackupPath, first.ConfigPath, second.BackupPath, second.ConfigPath} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("backup inventory leaked sidecar-local path %q: %s", forbidden, payload)
		}
	}
}

func TestListNodeBackupsAPIEmptyAndAuth(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-empty-backups")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-empty-backups/backups", nil)
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-empty-backups/backups", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var resp protocol.ListRollbackBackupsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode empty backups response: %v", err)
	}
	if resp.Total != 0 || len(resp.Backups) != 0 || resp.Limit != defaultBackupListLimit {
		t.Fatalf("empty backups response = %#v, want empty default page", resp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/missing/backups", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusNotFound, "not_found", "node not found")
}

func TestCreateRollbackJobRejectsMissingOrUnknownBackup(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "missing backup ref",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown backup",
			body:       `{"backupRef":"config_apply:missing:plan"}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown field path rejected",
			body:       `{"backupRef":"config_apply:missing:plan","backupPath":"/tmp/not-accepted"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			enrollTestNode(t, nodeStore, "node-rollback")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-rollback/rollback", strings.NewReader(tt.body))
			newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)
			assertStatus(t, rec, tt.wantStatus)
		})
	}
}

func TestCreateRollbackJobRejectsUnknownNode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/missing/rollback", strings.NewReader(`{"backupRef":"config_apply:job:plan"}`))
	newDevHandlerWithStore(t, store.NewMemoryNodeStore()).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestMutatingOperatorAPIsRejectWhenOperatorTokenNotConfigured(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, nodeStore store.Store)
		req   *http.Request
	}{
		{
			name: "enrollment token",
			req:  httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`)),
		},
		{
			name: "node job",
			setup: func(t *testing.T, nodeStore store.Store) {
				t.Helper()
				enrollTestNode(t, nodeStore, "node-jobs")
			},
			req: httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`)),
		},
		{
			name: "config apply",
			setup: func(t *testing.T, nodeStore store.Store) {
				t.Helper()
				enrollTestNode(t, nodeStore, "node-apply")
				seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
			},
			req: httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`)),
		},
		{
			name: "restart",
			setup: func(t *testing.T, nodeStore store.Store) {
				t.Helper()
				enrollTestNode(t, nodeStore, "node-restart")
			},
			req: httptest.NewRequest(http.MethodPost, "/api/nodes/node-restart/restart", strings.NewReader(`{}`)),
		},
		{
			name: "rollback",
			setup: func(t *testing.T, nodeStore store.Store) {
				t.Helper()
				enrollTestNode(t, nodeStore, "node-rollback")
				seedRollbackBackup(t, nodeStore, "node-rollback", "plan_rollback", "/tmp/sideplane-test/config.json", "/tmp/sideplane-test/current.backup")
			},
			req: httptest.NewRequest(http.MethodPost, "/api/nodes/node-rollback/rollback", strings.NewReader(`{"backupRef":"config_apply:job_rollback:plan_rollback"}`)),
		},
		{
			name: "desired config",
			req:  httptest.NewRequest(http.MethodPut, "/api/config/desired", strings.NewReader(`{}`)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			if tt.setup != nil {
				tt.setup(t, nodeStore)
			}

			rec := httptest.NewRecorder()
			NewHandlerWithStore(nodeStore).ServeHTTP(rec, tt.req)

			assertStatus(t, rec, http.StatusUnauthorized)
		})
	}
}

func TestCreateEnrollmentTokenRequiresConfiguredOperatorToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:       "missing",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "wrong",
			authorization: "Bearer wrong-token",
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "correct",
			authorization: "Bearer dev-token",
			wantStatus:    http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
			if err != nil {
				t.Fatalf("build handler: %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}

			handler.ServeHTTP(rec, req)

			assertStatus(t, rec, tt.wantStatus)
		})
	}
}

func TestCreateEnrollmentTokenAcceptsNamedOperatorToken(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	operatorToken, err := nodeStore.CreateOperatorToken(context.Background(), "ops laptop", protocol.OperatorTokenScopeAdmin, now)
	if err != nil {
		t.Fatalf("create operator token: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:     nodeStore,
		Freshness: DefaultFreshnessPolicy(),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+operatorToken.Token)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	tokens, err := nodeStore.ListOperatorTokens(context.Background())
	if err != nil {
		t.Fatalf("list operator tokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].LastUsedAt == nil {
		t.Fatalf("operator token lastUsedAt not recorded: %+v", tokens)
	}
}

func TestOperatorTokenManagementEndpointsRequireAuth(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "bootstrap-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	tests := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "create",
			req:  httptest.NewRequest(http.MethodPost, "/api/operator-tokens", strings.NewReader(`{"name":"ops"}`)),
		},
		{
			name: "list",
			req:  httptest.NewRequest(http.MethodGet, "/api/operator-tokens", nil),
		},
		{
			name: "revoke",
			req:  httptest.NewRequest(http.MethodDelete, "/api/operator-tokens/optok_missing", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tt.req)
			assertStatus(t, rec, http.StatusUnauthorized)
		})
	}
}

func TestOperatorTokenManagementEndpointsCreateListRevokeAndAudit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "bootstrap-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	created := doJSONRequest[protocol.CreateOperatorTokenResponse](t, server.Client(), http.MethodPost, server.URL+"/api/operator-tokens", "bootstrap-token", protocol.CreateOperatorTokenRequest{Name: "ops laptop"})
	if created.Token == "" || created.OperatorToken.ID == "" || created.OperatorToken.Name != "ops laptop" {
		t.Fatalf("created operator token = %+v, want plaintext and metadata", created)
	}

	doJSONRequest[protocol.CreateEnrollmentTokenResponse](t, server.Client(), http.MethodPost, server.URL+"/api/enrollment-tokens", created.Token, protocol.CreateEnrollmentTokenRequest{})

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/operator-tokens", nil)
	if err != nil {
		t.Fatalf("build list request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer bootstrap-token")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("list operator tokens: %v", err)
	}
	listBody, err := io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); closeErr != nil {
		t.Fatalf("close list response: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read list response: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", res.StatusCode, listBody)
	}
	if bytes.Contains(listBody, []byte(created.Token)) {
		t.Fatalf("operator token list exposed plaintext token")
	}
	var listResp protocol.ListOperatorTokensResponse
	if err := json.Unmarshal(listBody, &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Tokens) != 1 || listResp.Tokens[0].ID != created.OperatorToken.ID || listResp.Tokens[0].LastUsedAt == nil {
		t.Fatalf("operator token list = %+v, want created token with lastUsedAt", listResp.Tokens)
	}

	revoked := doJSONRequest[protocol.RevokeOperatorTokenResponse](t, server.Client(), http.MethodDelete, server.URL+"/api/operator-tokens/"+created.OperatorToken.ID, "bootstrap-token", nil)
	if revoked.OperatorToken.ID != created.OperatorToken.ID || revoked.OperatorToken.RevokedAt == nil {
		t.Fatalf("revoked operator token = %+v, want revoked metadata", revoked.OperatorToken)
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/enrollment-tokens", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build revoked-token request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+created.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("use revoked operator token: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked operator token status = %d, want 401", res.StatusCode)
	}

	events, err := nodeStore.ListAuditEventsFiltered(context.Background(), store.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	var sawCreate, sawList, sawRevoke bool
	for _, event := range events {
		if strings.Contains(event.Detail, created.Token) {
			t.Fatalf("audit event leaked plaintext token: %#v", event)
		}
		switch event.Action {
		case audit.ActionOperatorTokenCreate:
			sawCreate = true
		case audit.ActionOperatorTokenList:
			sawList = true
		case audit.ActionOperatorTokenRevoke:
			sawRevoke = true
		}
	}
	if !sawCreate || !sawList || !sawRevoke {
		t.Fatalf("operator token audit actions create=%t list=%t revoke=%t events=%#v", sawCreate, sawList, sawRevoke, events)
	}
}

func TestOperatorTokenScopeEnforcement(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "bootstrap-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()

	readonly := doJSONRequest[protocol.CreateOperatorTokenResponse](t, client, http.MethodPost, server.URL+"/api/operator-tokens", "bootstrap-token", protocol.CreateOperatorTokenRequest{Name: "viewer", Scope: protocol.OperatorTokenScopeReadonly})
	if readonly.OperatorToken.Scope != protocol.OperatorTokenScopeReadonly {
		t.Fatalf("created scope = %q, want readonly", readonly.OperatorToken.Scope)
	}
	admin := doJSONRequest[protocol.CreateOperatorTokenResponse](t, client, http.MethodPost, server.URL+"/api/operator-tokens", "bootstrap-token", protocol.CreateOperatorTokenRequest{Name: "ops", Scope: protocol.OperatorTokenScopeAdmin})

	status := func(method, path, token string, body any) int {
		t.Helper()
		var payload bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&payload).Encode(body); err != nil {
				t.Fatalf("encode body: %v", err)
			}
		}
		req, err := http.NewRequest(method, server.URL+path, &payload)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	// readonly passes read endpoints (GET list of operator tokens).
	if code := status(http.MethodGet, "/api/operator-tokens", readonly.Token, nil); code != http.StatusOK {
		t.Fatalf("readonly GET operator-tokens status = %d, want 200", code)
	}
	// readonly is forbidden on mutating endpoints.
	if code := status(http.MethodPost, "/api/enrollment-tokens", readonly.Token, protocol.CreateEnrollmentTokenRequest{}); code != http.StatusForbidden {
		t.Fatalf("readonly POST enrollment-tokens status = %d, want 403", code)
	}
	if code := status(http.MethodPost, "/api/operator-tokens", readonly.Token, protocol.CreateOperatorTokenRequest{Name: "escalation"}); code != http.StatusForbidden {
		t.Fatalf("readonly POST operator-tokens status = %d, want 403", code)
	}
	// admin named token has full access.
	if code := status(http.MethodPost, "/api/enrollment-tokens", admin.Token, protocol.CreateEnrollmentTokenRequest{}); code != http.StatusCreated {
		t.Fatalf("admin POST enrollment-tokens status = %d, want 201", code)
	}

	// admin mutations record the acting token id in the audit detail.
	events, err := nodeStore.ListAuditEventsFiltered(context.Background(), store.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	sawTokenAttribution := false
	for _, event := range events {
		if event.Action == audit.ActionEnrollmentTokenCreate && strings.Contains(event.Detail, "actor_id="+admin.OperatorToken.ID) {
			sawTokenAttribution = true
		}
	}
	if !sawTokenAttribution {
		t.Fatalf("audit events missing acting token attribution for %s; events=%#v", admin.OperatorToken.ID, events)
	}
}

func TestBulkJobCreationBySelectorAndNodeIDs(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		enrollTestNode(t, nodeStore, id)
	}
	if err := nodeStore.SetNodeLabels(ctx, "node-a", map[string]string{"role": "canary"}); err != nil {
		t.Fatalf("label node-a: %v", err)
	}
	if err := nodeStore.SetNodeLabels(ctx, "node-b", map[string]string{"role": "canary"}); err != nil {
		t.Fatalf("label node-b: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	bySelector := doJSONRequest[protocol.BulkJobResponse](t, server.Client(), http.MethodPost, server.URL+"/api/jobs/bulk", "dev-token", protocol.BulkJobRequest{
		Selector: map[string]string{"role": "canary"},
		Type:     protocol.JobTypeDeepProbe,
	})
	if bySelector.Created != 2 || len(bySelector.Jobs) != 2 {
		t.Fatalf("selector bulk = %+v, want 2 created jobs", bySelector)
	}
	matched := map[string]bool{}
	for _, result := range bySelector.Jobs {
		if result.JobID == "" {
			t.Fatalf("bulk result missing job id: %+v", result)
		}
		matched[result.NodeID] = true
	}
	if !matched["node-a"] || !matched["node-b"] || matched["node-c"] {
		t.Fatalf("selector matched = %+v, want node-a and node-b only", matched)
	}

	// node-c via explicit nodeIds.
	byIDs := doJSONRequest[protocol.BulkJobResponse](t, server.Client(), http.MethodPost, server.URL+"/api/jobs/bulk", "dev-token", protocol.BulkJobRequest{
		NodeIDs: []string{"node-c"},
		Type:    protocol.JobTypeDeepProbe,
	})
	if byIDs.Created != 1 || byIDs.Jobs[0].NodeID != "node-c" || byIDs.Jobs[0].JobID == "" {
		t.Fatalf("nodeIds bulk = %+v, want node-c job created", byIDs)
	}

	// Re-running the selector now conflicts with the active probe jobs.
	conflict := doJSONRequest[protocol.BulkJobResponse](t, server.Client(), http.MethodPost, server.URL+"/api/jobs/bulk", "dev-token", protocol.BulkJobRequest{
		Selector: map[string]string{"role": "canary"},
		Type:     protocol.JobTypeDeepProbe,
	})
	if conflict.Created != 0 {
		t.Fatalf("conflicting bulk created = %d, want 0", conflict.Created)
	}
	for _, result := range conflict.Jobs {
		if result.Error == "" {
			t.Fatalf("conflicting bulk result missing error: %+v", result)
		}
	}

	events, err := nodeStore.ListAuditEventsFiltered(ctx, store.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	sawBulk := false
	for _, event := range events {
		if event.Action == audit.ActionJobBulkCreate {
			sawBulk = true
		}
	}
	if !sawBulk {
		t.Fatalf("audit events missing %s; events=%#v", audit.ActionJobBulkCreate, events)
	}
}

func TestSidecarOutdatedFlagAndMetric(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	now := time.Now().UTC()
	enrollTestNode(t, nodeStore, "node-a")
	if _, err := nodeStore.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-a", SidecarVersion: "v1.0.0"}, now); err != nil {
		t.Fatalf("heartbeat node-a: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	// No expected version configured: not outdated.
	nodes := doJSONRequest[protocol.ListNodesResponse](t, server.Client(), http.MethodGet, server.URL+"/api/nodes", "", nil)
	if len(nodes.Nodes) != 1 || nodes.Nodes[0].SidecarOutdated {
		t.Fatalf("nodes = %+v, want node not flagged when no expected version", nodes.Nodes)
	}

	// Set an expected version that differs from the node's.
	updated := doJSONRequest[protocol.ServerSettings](t, server.Client(), http.MethodPut, server.URL+"/api/settings", "dev-token", protocol.UpdateServerSettingsRequest{ExpectedSidecarVersion: "v1.1.0"})
	if updated.ExpectedSidecarVersion != "v1.1.0" {
		t.Fatalf("update settings = %+v, want v1.1.0", updated)
	}
	nodes = doJSONRequest[protocol.ListNodesResponse](t, server.Client(), http.MethodGet, server.URL+"/api/nodes", "", nil)
	if len(nodes.Nodes) != 1 || !nodes.Nodes[0].SidecarOutdated {
		t.Fatalf("nodes = %+v, want node flagged outdated", nodes.Nodes)
	}

	// Metrics gauge reflects the outdated node.
	metricsRes, err := server.Client().Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	metricsBody, _ := io.ReadAll(metricsRes.Body)
	metricsRes.Body.Close()
	if !strings.Contains(string(metricsBody), "sideplane_fleet_sidecar_outdated 1") {
		t.Fatalf("metrics missing outdated gauge=1; body=%s", metricsBody)
	}

	// Matching version clears the flag.
	doJSONRequest[protocol.ServerSettings](t, server.Client(), http.MethodPut, server.URL+"/api/settings", "dev-token", protocol.UpdateServerSettingsRequest{ExpectedSidecarVersion: "v1.0.0"})
	nodes = doJSONRequest[protocol.ListNodesResponse](t, server.Client(), http.MethodGet, server.URL+"/api/nodes", "", nil)
	if nodes.Nodes[0].SidecarOutdated {
		t.Fatalf("node still flagged outdated after matching version")
	}
}

func TestServerSettingsUpdateRequiresOperatorAuth(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var payload bytes.Buffer
	_ = json.NewEncoder(&payload).Encode(protocol.UpdateServerSettingsRequest{ExpectedSidecarVersion: "v9"})
	req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/settings", &payload)
	req.Header.Set("Content-Type", "application/json")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("put settings: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestAuditExportFormatsAndFilter(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	if _, err := nodeStore.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "job.create", TargetNode: "node-a", Detail: "deep_probe", CreatedAt: now}); err != nil {
		t.Fatalf("append audit a: %v", err)
	}
	if _, err := nodeStore.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "restart", TargetNode: "node-b", Detail: "token=supersecret", CreatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("append audit b: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	getExport := func(query string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/audit/export"+query, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer dev-token")
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("export request: %v", err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(body)
	}

	// ndjson: one JSON object per line, secret redacted.
	res, body := getExport("?format=ndjson")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ndjson status = %d, body=%s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Fatalf("ndjson content-type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines = %d, want 2; body=%s", len(lines), body)
	}
	for _, line := range lines {
		var event protocol.AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode ndjson line %q: %v", line, err)
		}
	}
	if strings.Contains(body, "supersecret") {
		t.Fatalf("ndjson export leaked secret: %s", body)
	}

	// csv: header + rows, secret redacted.
	res, body = getExport("?format=csv")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("csv status = %d, body=%s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("csv content-type = %q", ct)
	}
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 || records[0][0] != "id" {
		t.Fatalf("csv records = %+v, want header + 2 rows", records)
	}
	if strings.Contains(body, "supersecret") {
		t.Fatalf("csv export leaked secret: %s", body)
	}

	// filter passthrough: only node-a action job.create.
	_, filtered := getExport("?format=ndjson&action=job.create&nodeId=node-a")
	filteredLines := strings.Split(strings.TrimSpace(filtered), "\n")
	if len(filteredLines) != 1 {
		t.Fatalf("filtered lines = %d, want 1; body=%s", len(filteredLines), filtered)
	}
	var filteredEvent protocol.AuditEvent
	if err := json.Unmarshal([]byte(filteredLines[0]), &filteredEvent); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if filteredEvent.Action != "job.create" || filteredEvent.TargetNode != "node-a" {
		t.Fatalf("filtered event = %+v, want node-a job.create", filteredEvent)
	}

	// invalid format -> 400.
	res, _ = getExport("?format=xml")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid format status = %d, want 400", res.StatusCode)
	}
}

func TestAuditExportRequiresOperatorAuth(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	res, err := server.Client().Get(server.URL + "/api/audit/export?format=ndjson")
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated export status = %d, want 401", res.StatusCode)
	}
}

func TestAlertWebhookCRUDEndpoints(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	created := doJSONRequest[protocol.CreateAlertWebhookResponse](t, server.Client(), http.MethodPost, server.URL+"/api/webhooks", "dev-token", protocol.CreateAlertWebhookRequest{
		URL:    "https://hooks.example.com/sp",
		Events: []protocol.AlertEventType{protocol.AlertEventRolloutPaused, protocol.AlertEventRolloutFailed},
		Sign:   true,
	})
	if created.Webhook.ID == "" || !created.Webhook.HasSecret {
		t.Fatalf("created webhook = %+v, want id and hasSecret", created.Webhook)
	}
	if created.Secret == "" {
		t.Fatalf("create response missing one-time signing secret")
	}

	// List never exposes the secret.
	listed := doJSONRequest[protocol.ListAlertWebhooksResponse](t, server.Client(), http.MethodGet, server.URL+"/api/webhooks", "dev-token", nil)
	if len(listed.Webhooks) != 1 || listed.Webhooks[0].ID != created.Webhook.ID {
		t.Fatalf("listed webhooks = %+v, want created webhook", listed.Webhooks)
	}

	// Verify the secret never leaks via the list endpoint body.
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/webhooks", nil)
	if err != nil {
		t.Fatalf("build list request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer dev-token")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	listBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if bytes.Contains(listBody, []byte(created.Secret)) {
		t.Fatalf("list webhooks leaked signing secret")
	}

	// Delete.
	delReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/webhooks/"+created.Webhook.ID, nil)
	if err != nil {
		t.Fatalf("build delete request: %v", err)
	}
	delReq.Header.Set("Authorization", "Bearer dev-token")
	delRes, err := server.Client().Do(delReq)
	if err != nil {
		t.Fatalf("delete webhook: %v", err)
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
		if bytes.Contains([]byte(event.Detail), []byte(created.Secret)) {
			t.Fatalf("audit event leaked signing secret: %#v", event)
		}
		switch event.Action {
		case audit.ActionAlertWebhookCreate:
			sawCreate = true
		case audit.ActionAlertWebhookDelete:
			sawDelete = true
		}
	}
	if !sawCreate || !sawDelete {
		t.Fatalf("audit actions create=%t delete=%t", sawCreate, sawDelete)
	}
}

func TestAlertWebhookCreateRejectsBadURL(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(protocol.CreateAlertWebhookRequest{URL: "ftp://x", Events: []protocol.AlertEventType{protocol.AlertEventNodeOffline}}); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/webhooks", &payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestBulkNodeLabelAssignmentMergesAndAudits(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		enrollTestNode(t, nodeStore, id)
	}
	if err := nodeStore.SetNodeLabels(ctx, "node-a", map[string]string{"zone": "lab"}); err != nil {
		t.Fatalf("seed node-a labels: %v", err)
	}
	if err := nodeStore.SetNodeLabels(ctx, "node-b", map[string]string{"zone": "lab"}); err != nil {
		t.Fatalf("seed node-b labels: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp := doJSONRequest[protocol.BulkNodeLabelsResponse](t, server.Client(), http.MethodPut, server.URL+"/api/nodes/labels", "dev-token", protocol.BulkNodeLabelsRequest{
		Selector: map[string]string{"zone": "lab"},
		Labels:   map[string]string{"role": "canary"},
	})
	if resp.Updated != 2 || len(resp.NodeIDs) != 2 {
		t.Fatalf("bulk labels response = %+v, want 2 updated", resp)
	}

	// Existing zone label is preserved, role is added.
	labelsA, err := nodeStore.GetNodeLabels(ctx, "node-a")
	if err != nil {
		t.Fatalf("get node-a labels: %v", err)
	}
	if labelsA["zone"] != "lab" || labelsA["role"] != "canary" {
		t.Fatalf("node-a labels = %+v, want merged zone+role", labelsA)
	}
	// node-c was not matched.
	labelsC, err := nodeStore.GetNodeLabels(ctx, "node-c")
	if err != nil {
		t.Fatalf("get node-c labels: %v", err)
	}
	if labelsC["role"] != "" {
		t.Fatalf("node-c labels = %+v, want untouched", labelsC)
	}

	events, err := nodeStore.ListAuditEventsFiltered(ctx, store.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	sawBulk := false
	for _, event := range events {
		if event.Action == audit.ActionNodeLabelsBulkUpdate {
			sawBulk = true
		}
	}
	if !sawBulk {
		t.Fatalf("audit events missing %s; events=%#v", audit.ActionNodeLabelsBulkUpdate, events)
	}
}

func TestBulkNodeLabelAssignmentValidation(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-a")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	cases := []struct {
		name string
		body protocol.BulkNodeLabelsRequest
		want int
	}{
		{name: "missing labels", body: protocol.BulkNodeLabelsRequest{NodeIDs: []string{"node-a"}}, want: http.StatusBadRequest},
		{name: "selector and nodeIds", body: protocol.BulkNodeLabelsRequest{NodeIDs: []string{"node-a"}, Selector: map[string]string{"a": "b"}, Labels: map[string]string{"role": "x"}}, want: http.StatusBadRequest},
		{name: "neither selector nor nodeIds", body: protocol.BulkNodeLabelsRequest{Labels: map[string]string{"role": "x"}}, want: http.StatusBadRequest},
		{name: "missing node", body: protocol.BulkNodeLabelsRequest{NodeIDs: []string{"ghost"}, Labels: map[string]string{"role": "x"}}, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload bytes.Buffer
			if err := json.NewEncoder(&payload).Encode(tc.body); err != nil {
				t.Fatalf("encode body: %v", err)
			}
			req, err := http.NewRequest(http.MethodPut, server.URL+"/api/nodes/labels", &payload)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer dev-token")
			req.Header.Set("Content-Type", "application/json")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestBulkJobCreationValidation(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-a")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	cases := []struct {
		name string
		body protocol.BulkJobRequest
		want int
	}{
		{name: "missing type", body: protocol.BulkJobRequest{NodeIDs: []string{"node-a"}}, want: http.StatusBadRequest},
		{name: "unsupported type", body: protocol.BulkJobRequest{NodeIDs: []string{"node-a"}, Type: protocol.JobTypeRestart}, want: http.StatusBadRequest},
		{name: "selector and nodeIds", body: protocol.BulkJobRequest{NodeIDs: []string{"node-a"}, Selector: map[string]string{"role": "x"}, Type: protocol.JobTypeDeepProbe}, want: http.StatusBadRequest},
		{name: "neither selector nor nodeIds", body: protocol.BulkJobRequest{Type: protocol.JobTypeDeepProbe}, want: http.StatusBadRequest},
		{name: "missing node", body: protocol.BulkJobRequest{NodeIDs: []string{"ghost"}, Type: protocol.JobTypeDeepProbe}, want: http.StatusNotFound},
		{name: "empty selector match", body: protocol.BulkJobRequest{Selector: map[string]string{"role": "none"}, Type: protocol.JobTypeDeepProbe}, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload bytes.Buffer
			if err := json.NewEncoder(&payload).Encode(tc.body); err != nil {
				t.Fatalf("encode body: %v", err)
			}
			req, err := http.NewRequest(http.MethodPost, server.URL+"/api/jobs/bulk", &payload)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer dev-token")
			req.Header.Set("Content-Type", "application/json")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestDesiredConfigPutWritesAuditEvent(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/config/desired", strings.NewReader(`{"global":{"provider":"openai","model":"gpt-5"}}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var resp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("audit event count = %d, want 1: %#v", len(resp.Events), resp.Events)
	}
	event := resp.Events[0]
	if event.Actor != audit.ActorOperator || event.Action != audit.ActionDesiredConfigUpdate {
		t.Fatalf("audit event = %#v, want operator desired update", event)
	}
	if !strings.HasPrefix(event.Detail, "desiredHash=sha256:") {
		t.Fatalf("audit detail = %q, want desired hash", event.Detail)
	}
	for _, forbidden := range []string{"openai", "gpt-5"} {
		if strings.Contains(event.Detail, forbidden) {
			t.Fatalf("audit detail leaked desired value %q: %s", forbidden, event.Detail)
		}
	}
}

func TestDesiredConfigPutRejectsUnsafeProviderModelValues(t *testing.T) {
	handler := newDevHandler(t)
	tests := []struct {
		name  string
		value string
	}{
		{name: "newline", value: "gpt-5\nmodel: hacked"},
		{name: "comment", value: "gpt-5#comment"},
		{name: "colon", value: "gpt-5:bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(protocol.DesiredConfig{
				Global: protocol.ProviderModelConfig{Provider: "openai", Model: tt.value},
			})
			if err != nil {
				t.Fatalf("marshal desired: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/config/desired", bytes.NewReader(payload))
			handler.ServeHTTP(rec, req)
			assertStatus(t, rec, http.StatusBadRequest)
			if !strings.Contains(rec.Body.String(), "invalid desired config") {
				t.Fatalf("response = %q, want invalid desired config", rec.Body.String())
			}
		})
	}
}

func TestDesiredConfigHistoryRequiresAuth(t *testing.T) {
	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(store.NewMemoryNodeStore(), DefaultFreshnessPolicy(), "dev-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	tests := []struct {
		name string
		req  *http.Request
	}{
		{name: "history", req: httptest.NewRequest(http.MethodGet, "/api/config/desired/history", nil)},
		{name: "revert", req: httptest.NewRequest(http.MethodPost, "/api/config/desired/revert", strings.NewReader(`{"historyId":"deshist_1"}`))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tt.req)
			assertStatus(t, rec, http.StatusUnauthorized)
		})
	}
}

func TestDesiredConfigHistoryListAndRevert(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	for _, payload := range []string{
		`{"global":{"provider":"openai","model":"gpt-4o"}}`,
		`{"global":{"provider":"anthropic","model":"claude-sonnet-4"}}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/config/desired", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer dev-token")
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusOK)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config/desired/history?limit=1&offset=1", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var historyResp protocol.ListDesiredConfigHistoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&historyResp); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if historyResp.Total != 2 || historyResp.Limit != 1 || historyResp.Offset != 1 || len(historyResp.History) != 1 {
		t.Fatalf("history response = %+v, want page 1 of total 2", historyResp)
	}
	firstHistory := historyResp.History[0]
	if firstHistory.Config.Global.Model != "gpt-4o" || !strings.HasPrefix(firstHistory.DesiredHash, "sha256:") {
		t.Fatalf("first history = %+v, want gpt-4o with hash", firstHistory)
	}

	body, err := json.Marshal(protocol.RevertDesiredConfigRequest{HistoryID: firstHistory.ID})
	if err != nil {
		t.Fatalf("marshal revert request: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/config/desired/revert", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var revertResp protocol.RevertDesiredConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&revertResp); err != nil {
		t.Fatalf("decode revert response: %v", err)
	}
	if revertResp.Desired.Global.Model != "gpt-4o" || revertResp.History.ID == firstHistory.ID {
		t.Fatalf("revert response = %+v, want new gpt-4o history entry", revertResp)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/desired", nil))
	assertStatus(t, rec, http.StatusOK)
	var desired protocol.DesiredConfig
	if err := json.NewDecoder(rec.Body).Decode(&desired); err != nil {
		t.Fatalf("decode desired response: %v", err)
	}
	if desired.Global.Provider != "openai" || desired.Global.Model != "gpt-4o" {
		t.Fatalf("desired config = %+v, want reverted gpt-4o", desired)
	}

	events, err := nodeStore.ListAuditEventsFiltered(context.Background(), store.AuditFilter{Action: audit.ActionDesiredConfigRevert, Limit: 10})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Detail, "historyId="+firstHistory.ID) {
		t.Fatalf("revert audit events = %+v, want historyId detail", events)
	}
	for _, forbidden := range []string{"openai", "gpt-4o"} {
		if strings.Contains(events[0].Detail, forbidden) {
			t.Fatalf("revert audit detail leaked desired value %q: %s", forbidden, events[0].Detail)
		}
	}
}

func TestEffectiveConfigPreviewDoesNotPersistDesiredConfig(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-preview")
	seedDesiredAndProfileProbe(t, nodeStore, "node-preview", "/etc/hermes/config.json", "default")
	handler := newDevHandlerWithStore(t, nodeStore)

	beforeRec := httptest.NewRecorder()
	handler.ServeHTTP(beforeRec, httptest.NewRequest(http.MethodGet, "/api/config/desired", nil))
	assertStatus(t, beforeRec, http.StatusOK)
	var before protocol.DesiredConfig
	if err := json.NewDecoder(beforeRec.Body).Decode(&before); err != nil {
		t.Fatalf("decode before desired: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config/effective/preview", strings.NewReader(`{"nodeId":"node-preview","runtimeType":"hermes","profile":"default","desired":{"provider":"local","model":"qwen3"}}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var preview protocol.EffectiveConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Effective.Provider != "local" || preview.Effective.Model != "qwen3" {
		t.Fatalf("preview effective = %+v, want local/qwen3", preview.Effective)
	}

	afterRec := httptest.NewRecorder()
	handler.ServeHTTP(afterRec, httptest.NewRequest(http.MethodGet, "/api/config/desired", nil))
	assertStatus(t, afterRec, http.StatusOK)
	var after protocol.DesiredConfig
	if err := json.NewDecoder(afterRec.Body).Decode(&after); err != nil {
		t.Fatalf("decode after desired: %v", err)
	}
	if after.Global != before.Global || len(after.NodeOverrides) != len(before.NodeOverrides) || len(after.RuntimeProfileOverrides) != len(before.RuntimeProfileOverrides) || len(after.NodeRuntimeProfileOverrides) != len(before.NodeRuntimeProfileOverrides) {
		t.Fatalf("preview mutated desired config: before=%#v after=%#v", before, after)
	}
}

func TestEffectiveConfigPreviewRejectsUnsafeProviderModelValues(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-preview")
	seedDesiredAndProfileProbe(t, nodeStore, "node-preview", "/etc/hermes/config.json", "default")
	handler := newDevHandlerWithStore(t, nodeStore)

	payload, err := json.Marshal(protocol.EffectiveConfigPreviewRequest{
		NodeID:      "node-preview",
		RuntimeType: "hermes",
		Profile:     "default",
		Desired:     protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5#comment"},
	})
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config/effective/preview", bytes.NewReader(payload))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid desired provider/model") {
		t.Fatalf("response = %q, want invalid desired provider/model", rec.Body.String())
	}
}

func TestCreateJobAPIAllowsLocalDevWhenOperatorTokenNotConfigured(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)
}

func TestDeleteNodeAPIRequiresOperatorAndRemovesNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-delete")
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-delete", time.Now().UTC()); err != nil {
		t.Fatalf("create job: %v", err)
	}
	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/node-delete", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnauthorized)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/nodes/node-delete", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusNoContent)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	nodesResp := decodeListNodesResponse(t, rec)
	if len(nodesResp.Nodes) != 0 {
		t.Fatalf("nodes length = %d, want 0: %#v", len(nodesResp.Nodes), nodesResp.Nodes)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var auditResp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(auditResp.Events) != 1 {
		t.Fatalf("audit event count = %d, want 1: %#v", len(auditResp.Events), auditResp.Events)
	}
	event := auditResp.Events[0]
	if event.Action != audit.ActionNodeDelete || event.TargetNode != "node-delete" {
		t.Fatalf("audit event = %#v, want node.delete for node-delete", event)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/nodes/node-delete", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestCreateJobAPIRejectsWhenOperatorTokenNotConfigured(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestCreateJobAPIRequiresConfiguredOperatorToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:       "missing",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "wrong",
			authorization: "Bearer wrong-token",
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "correct",
			authorization: "Bearer dev-token",
			wantStatus:    http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			enrollTestNode(t, nodeStore, "node-jobs")

			handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
			if err != nil {
				t.Fatalf("build handler: %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}

			handler.ServeHTTP(rec, req)

			assertStatus(t, rec, tt.wantStatus)
		})
	}
}

func TestListNodeJobsProtectsResultJSONWithConfiguredOperatorToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantResult    bool
	}{
		{
			name: "missing token gets summary only",
		},
		{
			name:          "wrong token gets summary only",
			authorization: "Bearer wrong-token",
		},
		{
			name:          "correct token gets result details",
			authorization: "Bearer dev-token",
			wantResult:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			enrollTestNode(t, nodeStore, "node-jobs")
			job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC())
			if err != nil {
				t.Fatalf("create job: %v", err)
			}
			if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", time.Now().UTC()); err != nil {
				t.Fatalf("claim job: %v", err)
			}
			if err := nodeStore.CompleteJob(context.Background(), job.ID, protocol.JobResultRequest{
				Status:     protocol.JobStatusCompleted,
				ResultJSON: `{"apiKey":"sk-test-secret","status":"complete"}`,
			}, time.Now().UTC()); err != nil {
				t.Fatalf("complete job: %v", err)
			}

			handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
			if err != nil {
				t.Fatalf("build handler: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}

			handler.ServeHTTP(rec, req)

			assertStatus(t, rec, http.StatusOK)
			body := rec.Body.String()
			var jobs []protocol.Job
			if err := json.Unmarshal([]byte(body), &jobs); err != nil {
				t.Fatalf("decode jobs: %v", err)
			}
			if len(jobs) != 1 {
				t.Fatalf("len(jobs) = %d, want 1", len(jobs))
			}
			if tt.wantResult {
				if strings.Contains(jobs[0].ResultJSON, "sk-test-secret") {
					t.Fatalf("resultJson leaked secret: %q", jobs[0].ResultJSON)
				}
				if !strings.Contains(jobs[0].ResultJSON, spconfig.RedactedValue) {
					t.Fatalf("resultJson = %q, want redacted detailed result", jobs[0].ResultJSON)
				}
				return
			}
			if jobs[0].ResultJSON != "" {
				t.Fatalf("resultJson = %q, want empty summary", jobs[0].ResultJSON)
			}
			for _, forbidden := range []string{"sk-test-secret", "apiKey"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("unauthenticated jobs response leaked %q: %s", forbidden, body)
				}
			}
		})
	}
}

func TestCreateJobAPIRejectsMalformedJSON(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":`))

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestJSONAPIBodySizeLimits(t *testing.T) {
	t.Run("heartbeat uses default limit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		body := `{"nodeId":"` + strings.Repeat("x", int(defaultJSONBodyLimit)+1) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(body))

		NewHandler().ServeHTTP(rec, req)

		assertAPIError(t, rec, http.StatusRequestEntityTooLarge, "request_entity_too_large", "request body too large")
	})

	t.Run("operator job create uses default limit", func(t *testing.T) {
		nodeStore := store.NewMemoryNodeStore()
		enrollTestNode(t, nodeStore, "node-large")

		rec := httptest.NewRecorder()
		body := `{"type":"deep_probe","payloadJson":"` + strings.Repeat("x", int(defaultJSONBodyLimit)+1) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-large/jobs", strings.NewReader(body))

		newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

		assertAPIError(t, rec, http.StatusRequestEntityTooLarge, "request_entity_too_large", "request body too large")
	})

	t.Run("config apply uses large limit", func(t *testing.T) {
		nodeStore := store.NewMemoryNodeStore()

		rec := httptest.NewRecorder()
		body := `{"profile":"` + strings.Repeat("x", int(defaultJSONBodyLimit)+1) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-large/config-apply", strings.NewReader(body))

		newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

		if rec.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("config apply returned 413 for a body under the large limit: %s", rec.Body.String())
		}
	})

	t.Run("sidecar job result uses large limit", func(t *testing.T) {
		nodeStore := store.NewMemoryNodeStore()
		credential := enrollTestNode(t, nodeStore, "node-large")
		job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-large", time.Now().UTC())
		if err != nil {
			t.Fatalf("create job: %v", err)
		}
		if _, err := nodeStore.ClaimNextJob(context.Background(), "node-large", time.Now().UTC()); err != nil {
			t.Fatalf("claim job: %v", err)
		}

		rec := httptest.NewRecorder()
		body := `{"status":"completed","resultJson":"` + strings.Repeat("x", int(largeJSONBodyLimit)+1) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+credential)

		NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

		assertAPIError(t, rec, http.StatusRequestEntityTooLarge, "request_entity_too_large", "request body too large")
	})
}

func TestCreateJobAPIRejectsUnsupportedType(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"bad"}`))

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestCreateJobAPIRejectsUnknownNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/missing-node/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusNotFound)
}

func TestCreateJobAPIRejectsDuplicatePendingDeepProbe(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusConflict)
}

func TestCreateJobAPIRejectsDuplicateClaimedDeepProbe(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusConflict)
}

func TestListNodeJobsAPI(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")
	enrollTestNode(t, nodeStore, "other-node")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	older, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create older job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", now.Add(30*time.Second)); err != nil {
		t.Fatalf("claim older job: %v", err)
	}
	if err := nodeStore.CompleteJob(context.Background(), older.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(45*time.Second)); err != nil {
		t.Fatalf("complete older job: %v", err)
	}
	newer, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("create newer job: %v", err)
	}
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "other-node", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("create other job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs length = %d, want 2", len(jobs))
	}
	if jobs[0].ID != newer.ID || jobs[1].ID != older.ID {
		t.Fatalf("jobs order = [%q, %q], want [%q, %q]", jobs[0].ID, jobs[1].ID, newer.ID, older.ID)
	}
}

func TestListNodeJobsAPIWithLimitAndStatusFilter(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	older, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create older job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", now.Add(time.Second)); err != nil {
		t.Fatalf("claim older job: %v", err)
	}
	if err := nodeStore.CompleteJob(context.Background(), older.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete older job: %v", err)
	}
	newer, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create newer job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", now.Add(4*time.Second)); err != nil {
		t.Fatalf("claim newer job: %v", err)
	}
	if err := nodeStore.CompleteJob(context.Background(), newer.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(5*time.Second)); err != nil {
		t.Fatalf("complete newer job: %v", err)
	}
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(6*time.Second)); err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs?limit=1&status=completed", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != newer.ID {
		t.Fatalf("jobs = %#v, want newest completed job %s", jobs, newer.ID)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs?status=unknown", nil)
	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestListNodeJobsAPIOmitsUnsetTimestamps(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if _, ok := jobs[0]["claimedAt"]; ok {
		t.Fatalf("pending job response includes claimedAt: %#v", jobs[0])
	}
	if _, ok := jobs[0]["finishedAt"]; ok {
		t.Fatalf("pending job response includes finishedAt: %#v", jobs[0])
	}
}

func TestListNodeJobsAPIIncludesFinishedTimestamps(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-jobs")

	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC())
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+credential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer "+credential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if _, ok := jobs[0]["claimedAt"]; !ok {
		t.Fatalf("completed job response omits claimedAt: %#v", jobs[0])
	}
	if _, ok := jobs[0]["finishedAt"]; !ok {
		t.Fatalf("completed job response omits finishedAt: %#v", jobs[0])
	}
}

func TestAuditAPIRecordsFleetOperations(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var tokenResp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	var enrollBody bytes.Buffer
	if err := json.NewEncoder(&enrollBody).Encode(protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: "node-audit",
	}); err != nil {
		t.Fatalf("encode enroll request: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/enroll", &enrollBody)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var enrollResp protocol.EnrollNodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&enrollResp); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-audit/jobs", strings.NewReader(`{"type":"deep_probe"}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-audit", nil)
	req.Header.Set("Authorization", "Bearer "+enrollResp.NodeCredential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer "+enrollResp.NodeCredential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var resp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("audit event count = %d, want 4: %#v", len(resp.Events), resp.Events)
	}
	gotActions := map[string]bool{}
	for _, event := range resp.Events {
		gotActions[event.Action] = true
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal audit event: %v", err)
		}
		if strings.Contains(string(payload), tokenResp.Token) || strings.Contains(string(payload), enrollResp.NodeCredential) {
			t.Fatalf("audit event leaked credential material: %s", payload)
		}
	}
	for _, action := range []string{"enrollment.token.create", "node.enroll", "job.create", "job.complete"} {
		if !gotActions[action] {
			t.Fatalf("missing audit action %q in %#v", action, resp.Events)
		}
	}
}

func TestOperatorWorkflowEndToEnd(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "operator-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()

	tokenResp := doJSONRequest[protocol.CreateEnrollmentTokenResponse](t, client, http.MethodPost, server.URL+"/api/enrollment-tokens", "operator-token", protocol.CreateEnrollmentTokenRequest{})
	enrollResp := doJSONRequest[protocol.EnrollNodeResponse](t, client, http.MethodPost, server.URL+"/api/enroll", "", protocol.EnrollNodeRequest{
		Token:    tokenResp.Token,
		NodeID:   "node-e2e",
		Hostname: "worker-e2e",
	})
	if enrollResp.NodeID != "node-e2e" || enrollResp.NodeCredential == "" {
		t.Fatalf("enroll response = %#v, want node id and credential", enrollResp)
	}

	heartbeatResp := doJSONRequest[protocol.HeartbeatResponse](t, client, http.MethodPost, server.URL+"/api/heartbeat", enrollResp.NodeCredential, protocol.HeartbeatRequest{
		NodeID:         enrollResp.NodeID,
		Hostname:       "worker-e2e",
		SidecarVersion: "test-version",
		Runtimes: []protocol.RuntimeStatus{{
			Name:       "hermes",
			Type:       "hermes",
			State:      "present",
			Provider:   "anthropic",
			Model:      "claude-3.7-sonnet",
			ConfigHash: "sha256:actual",
		}},
	})
	if !heartbeatResp.Accepted {
		t.Fatalf("heartbeat accepted = false")
	}

	nodesResp := doJSONRequest[protocol.ListNodesResponse](t, client, http.MethodGet, server.URL+"/api/nodes", "", nil)
	if len(nodesResp.Nodes) != 1 || nodesResp.Nodes[0].NodeID != "node-e2e" || nodesResp.Nodes[0].State != protocol.NodeStateFresh {
		t.Fatalf("nodes = %#v, want fresh node-e2e", nodesResp.Nodes)
	}

	probeJob := doJSONRequest[protocol.Job](t, client, http.MethodPost, server.URL+"/api/nodes/node-e2e/jobs", "operator-token", protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe})
	if probeJob.Type != protocol.JobTypeDeepProbe || probeJob.Status != protocol.JobStatusPending {
		t.Fatalf("probe job = %#v, want pending deep_probe", probeJob)
	}
	claimedProbe := doJSONRequest[protocol.Job](t, client, http.MethodGet, server.URL+"/api/sidecar/jobs/next?nodeId=node-e2e", enrollResp.NodeCredential, nil)
	if claimedProbe.ID != probeJob.ID || claimedProbe.Status != protocol.JobStatusClaimed {
		t.Fatalf("claimed probe = %#v, want %s claimed", claimedProbe, probeJob.ID)
	}

	probeResult, err := json.Marshal(protocol.DeepProbeResult{
		Runtimes: []protocol.RuntimeStatus{{
			Name:       "hermes",
			Type:       "hermes",
			State:      "present",
			Provider:   "anthropic",
			Model:      "claude-3.7-sonnet",
			ConfigHash: "sha256:actual",
		}},
		ConfigSnapshots: []protocol.RuntimeConfigSnapshot{{
			RuntimeName: "hermes",
			RuntimeType: "hermes",
			ConfigPath:  "/tmp/sideplane-test/hermes/config.yaml",
			Source:      "file",
			Provider:    "anthropic",
			Model:       "claude-3.7-sonnet",
			ConfigHash:  "sha256:actual",
		}},
	})
	if err != nil {
		t.Fatalf("marshal probe result: %v", err)
	}
	submitJobResult(t, client, server.URL, probeJob.ID, enrollResp.NodeCredential, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(probeResult),
	})

	doJSONRequest[protocol.DesiredConfig](t, client, http.MethodPut, server.URL+"/api/config/desired", "operator-token", protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	})
	applyJob := doJSONRequest[protocol.Job](t, client, http.MethodPost, server.URL+"/api/nodes/node-e2e/config-apply", "operator-token", protocol.ConfigApplyRequest{})
	if applyJob.Type != protocol.JobTypeConfigApply || applyJob.Status != protocol.JobStatusPending {
		t.Fatalf("apply job = %#v, want pending config_apply", applyJob)
	}
	claimedApply := doJSONRequest[protocol.Job](t, client, http.MethodGet, server.URL+"/api/sidecar/jobs/next?nodeId=node-e2e", enrollResp.NodeCredential, nil)
	if claimedApply.ID != applyJob.ID || claimedApply.Status != protocol.JobStatusClaimed {
		t.Fatalf("claimed apply = %#v, want %s claimed", claimedApply, applyJob.ID)
	}
	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(claimedApply.PayloadJSON), &signed); err != nil {
		t.Fatalf("decode signed apply plan: %v", err)
	}
	if signed.Plan.Mode != protocol.ConfigPlanModeDryRun || !signed.Plan.Body.DryRun {
		t.Fatalf("signed plan mode=%q dryRun=%t, want dry-run", signed.Plan.Mode, signed.Plan.Body.DryRun)
	}
	if signed.Plan.Body.Profile != "/tmp/sideplane-test/hermes/config.yaml" {
		t.Fatalf("signed plan profile = %q, want fake config path", signed.Plan.Body.Profile)
	}

	applyResult, err := json.Marshal(protocol.ConfigApplyResult{
		PlanID: signed.Plan.ID,
		DryRun: true,
		Steps: []protocol.ConfigApplyStep{
			{Name: "validated", Status: "completed"},
			{Name: "replaced", Status: "skipped", Detail: "dry-run"},
		},
	})
	if err != nil {
		t.Fatalf("marshal apply result: %v", err)
	}
	submitJobResult(t, client, server.URL, applyJob.ID, enrollResp.NodeCredential, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(applyResult),
	})

	auditResp := doJSONRequest[protocol.ListAuditEventsResponse](t, client, http.MethodGet, server.URL+"/api/audit", "", nil)
	wantActions := map[string]bool{
		audit.ActionEnrollmentTokenCreate: false,
		audit.ActionNodeEnroll:            false,
		audit.ActionJobCreate:             false,
		audit.ActionJobComplete:           false,
		audit.ActionConfigApply:           false,
		audit.ActionDesiredConfigUpdate:   false,
	}
	for _, event := range auditResp.Events {
		if _, ok := wantActions[event.Action]; ok {
			wantActions[event.Action] = true
		}
	}
	for action, seen := range wantActions {
		if !seen {
			t.Fatalf("audit action %q not recorded in %#v", action, auditResp.Events)
		}
	}
}

func TestAuditAPIFiltersByNodeActionAndLimit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for _, event := range []protocol.AuditEvent{
		{Actor: audit.ActorOperator, Action: audit.ActionJobCreate, TargetNode: "node-a", CreatedAt: now},
		{Actor: audit.ActorOperator, Action: audit.ActionJobCreate, TargetNode: "node-b", CreatedAt: now.Add(time.Minute)},
		{Actor: audit.ActorSidecar, Action: audit.ActionJobFail, TargetNode: "node-a", CreatedAt: now.Add(2 * time.Minute)},
	} {
		if _, err := nodeStore.AppendAuditEvent(context.Background(), event); err != nil {
			t.Fatalf("append audit: %v", err)
		}
	}
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/audit?nodeId=node-a&action=job.create", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var resp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].TargetNode != "node-a" || resp.Events[0].Action != audit.ActionJobCreate {
		t.Fatalf("filtered events = %#v, want node-a job.create only", resp.Events)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit?action=job.create&limit=1", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	resp = protocol.ListAuditEventsResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode limited audit response: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].TargetNode != "node-b" {
		t.Fatalf("limited events = %#v, want newest job.create event", resp.Events)
	}
}

func TestListNodeJobsAPISurfacesTimedOutJob(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-timeout")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimed == nil {
		t.Fatalf("claimed job is nil")
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", claimed.ClaimExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("advance timeout: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-timeout/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if jobs[0].ID != job.ID {
		t.Fatalf("job ID = %q, want %q", jobs[0].ID, job.ID)
	}
	if jobs[0].Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", jobs[0].Status)
	}
	if jobs[0].Error != "job claim timed out" {
		t.Fatalf("job error = %q, want timeout error", jobs[0].Error)
	}
}

func TestTimedOutConfigApplyRecordsMetricsAndAuditOnce(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-timeout")
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForHTTPTest(t, "hermes", "/etc/hermes/config.yaml"),
	}, "node-timeout", now)
	if err != nil {
		t.Fatalf("create config_apply: %v", err)
	}
	claimed, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", claimed.ClaimExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("advance timeout: %v", err)
	}

	handler := newDevHandlerWithStore(t, nodeStore)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-timeout/jobs", nil)
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusOK)
	}

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := metricsRec.Body.String(); !strings.Contains(got, `sideplane_jobs_failed_total{type="config_apply"} 1`) {
		t.Fatalf("metrics missing one config_apply failure for timeout:\n%s", got)
	}

	auditRec := httptest.NewRecorder()
	handler.ServeHTTP(auditRec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	assertStatus(t, auditRec, http.StatusOK)
	var auditResp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	var timeoutEvents int
	for _, event := range auditResp.Events {
		if event.Action == audit.ActionJobFail && event.TargetNode == "node-timeout" && strings.Contains(event.Detail, "config_apply timeout") {
			timeoutEvents++
		}
	}
	if timeoutEvents != 1 {
		t.Fatalf("timeout audit events = %d, want 1; job=%s events=%#v", timeoutEvents, job.ID, auditResp.Events)
	}
}

func TestLateConfigApplyResultAfterTimeoutIsRecorded(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-late")
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForHTTPTest(t, "hermes", "/etc/hermes/config.yaml"),
	}, "node-late", now)
	if err != nil {
		t.Fatalf("create config_apply: %v", err)
	}
	claimed, err := nodeStore.ClaimNextJob(context.Background(), "node-late", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-late", claimed.ClaimExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("advance timeout: %v", err)
	}

	handler := newDevHandlerWithStore(t, nodeStore)
	resultJSON := `{"steps":[{"name":"health_checked","status":"completed"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":`+strconv.Quote(resultJSON)+`}`))
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "accepted_late") {
		t.Fatalf("late result response = %s, want accepted_late", rec.Body.String())
	}

	got, err := nodeStore.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.ResultJSON != resultJSON {
		t.Fatalf("late result JSON = %q, want %q", got.ResultJSON, resultJSON)
	}
	if !strings.Contains(got.Error, "late sidecar result status=completed") {
		t.Fatalf("late result error detail = %q", got.Error)
	}

	auditRec := httptest.NewRecorder()
	handler.ServeHTTP(auditRec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	assertStatus(t, auditRec, http.StatusOK)
	var auditResp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	var sawTimeout, sawLate bool
	for _, event := range auditResp.Events {
		if event.TargetNode != "node-late" || event.Action != audit.ActionJobFail {
			continue
		}
		if strings.Contains(event.Detail, "config_apply timeout") {
			sawTimeout = true
		}
		if strings.Contains(event.Detail, "config_apply late_result_after_timeout") {
			sawLate = true
		}
	}
	if !sawTimeout || !sawLate {
		t.Fatalf("audit events missing timeout=%t late=%t: %#v", sawTimeout, sawLate, auditResp.Events)
	}
}

func TestSidecarClaimsOnlyOwnNodeJobs(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	nodeACredential := enrollTestNode(t, nodeStore, "node-a")
	enrollTestNode(t, nodeStore, "node-b")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-b", now); err != nil {
		t.Fatalf("create node-b job: %v", err)
	}
	jobA, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-a", now.Add(time.Second))
	if err != nil {
		t.Fatalf("create node-a job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-a", nil)
	req.Header.Set("Authorization", "Bearer "+nodeACredential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if job.ID != jobA.ID {
		t.Fatalf("claimed job = %q, want node-a job %q", job.ID, jobA.ID)
	}
	if job.NodeID != "node-a" {
		t.Fatalf("claimed nodeId = %q, want node-a", job.NodeID)
	}
}

func TestSidecarJobPollingRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-auth")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-auth", nil)
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestSidecarJobResultRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-result")
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-result", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-result", time.Now().UTC()); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestSidecarFailedConfigApplyResultPersistsResultJSON(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-result")
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply}, "node-result", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-result", time.Now().UTC()); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	resultJSON := `{"steps":[{"name":"rolled_back","status":"completed"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"failed","error":"apply failed","resultJson":`+strconv.Quote(resultJSON)+`}`))
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-result/jobs", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if jobs[0].Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", jobs[0].Status)
	}
	if jobs[0].ResultJSON != resultJSON {
		t.Fatalf("result JSON = %q, want %q", jobs[0].ResultJSON, resultJSON)
	}
	if !configApplyRolledBack(jobs[0].ResultJSON) {
		t.Fatalf("result JSON did not expose rollback completion: %s", jobs[0].ResultJSON)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ok")
}

func TestSecurityHeaders(t *testing.T) {
	handler := NewHandler()
	for _, path := range []string{"/healthz", "/api/nodes"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)

			handler.ServeHTTP(rec, req)

			assertStatus(t, rec, http.StatusOK)
			assertSecurityHeaders(t, rec)
		})
	}
}

func TestRequestLoggingMiddleware(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:     store.NewMemoryNodeStore(),
		Freshness: DefaultFreshnessPolicy(),
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("logged method = %#v, want %s", entry["method"], http.MethodGet)
	}
	if entry["path"] != "/healthz" {
		t.Fatalf("logged path = %#v, want /healthz", entry["path"])
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Fatalf("logged status = %#v, want %d", entry["status"], http.StatusOK)
	}
}

func TestJobLifecycleLoggingIncludesStructuredContext(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-logs")
	var logs bytes.Buffer
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       DefaultFreshnessPolicy(),
		AllowUnauthenticatedOperatorAPI: true,
		Logger:                          slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-logs/jobs", strings.NewReader(`{"type":"deep_probe","payloadJson":"{\"token\":\"secret-value\"}"}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)
	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-logs", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{\"token\":\"secret-value\"}"}`))
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	body := logs.String()
	for _, want := range []string{
		`"msg":"job created"`,
		`"msg":"job claimed"`,
		`"msg":"job result recorded"`,
		`"job_id":"` + job.ID + `"`,
		`"node_id":"node-logs"`,
		`"type":"deep_probe"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "secret-value") || strings.Contains(body, "payloadJson") || strings.Contains(body, "resultJson") {
		t.Fatalf("job logs exposed payload/result details:\n%s", body)
	}
}

func TestReadyz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ready")
}

func TestReadyzReportsStoreFailure(t *testing.T) {
	handler, err := NewHandlerWithStoreAndFreshnessPolicy(staticNodeStore{
		checkErr: errors.New("database unavailable"),
	}, DefaultFreshnessPolicy())
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	handler.ServeHTTP(rec, req)

	assertAPIError(t, rec, http.StatusServiceUnavailable, "service_unavailable", "store is not ready")
}

func TestMetricsExposesCounters(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"sideplane_build_info",
		"sideplane_heartbeats_total",
		"sideplane_jobs_created_total",
		"sideplane_sidecar_job_claims_total",
		"sideplane_jobs_completed_total",
		"sideplane_jobs_failed_total",
		"sideplane_job_late_results_total",
		"sideplane_config_apply_rolled_back_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\n%s", want, body)
		}
	}
}

func TestMetricsCountsHeartbeatsClaimsAndLateResults(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-metrics")
	handler := NewHandlerWithStore(nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-metrics","hostname":"worker-a"}`))
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-metrics"}`))
	req.Header.Set("Authorization", "Bearer wrong-credential")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnauthorized)

	now := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-metrics", now); err != nil {
		t.Fatalf("create deep probe: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-metrics", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForHTTPTest(t, "hermes", "/etc/hermes/config.yaml"),
	}, "node-metrics", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("create config_apply: %v", err)
	}
	claimed, err := nodeStore.ClaimNextJob(context.Background(), "node-metrics", job.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claimed job = %#v, want config_apply %s", claimed, job.ID)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-metrics", claimed.ClaimExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("advance config_apply timeout: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	for _, want := range []string{
		`sideplane_heartbeats_total{status="accepted"} 1`,
		`sideplane_heartbeats_total{status="rejected"} 1`,
		`sideplane_sidecar_job_claims_total{type="deep_probe"} 1`,
		`sideplane_job_late_results_total{type="config_apply",status="completed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\n%s", want, body)
		}
	}
}

func TestMetricsCountsConfigApplyCreation(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-apply")
	seedDesiredAndProbe(t, nodeStore, "node-apply", "/etc/hermes/config.json")
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/nodes/node-apply/config-apply", strings.NewReader(`{}`)))
	assertStatus(t, rec, http.StatusCreated)

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := metricsRec.Body.String(); !strings.Contains(got, `sideplane_jobs_created_total{type="config_apply"} 1`) {
		t.Errorf("expected config_apply creation counter, got:\n%s", got)
	}
}

func TestMetricsExposeFleetGauges(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	heartbeats := map[string]time.Time{
		"node-fresh-drift": now.Add(-time.Minute),
		"node-stale":       now.Add(-3 * time.Minute),
		"node-offline":     now.Add(-11 * time.Minute),
	}
	for nodeID, observedAt := range heartbeats {
		enrollTestNode(t, nodeStore, nodeID)
		if _, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Hostname: nodeID,
		}, observedAt); err != nil {
			t.Fatalf("record heartbeat for %s: %v", nodeID, err)
		}
	}
	if err := nodeStore.SetDesiredConfig(context.Background(), protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}, now); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	seedRuntimeConfigSnapshot(t, nodeStore, "node-fresh-drift", "anthropic", "claude-3-7-sonnet")
	seedRuntimeConfigSnapshot(t, nodeStore, "node-stale", "openai", "gpt-4o")

	handler, err := NewHandlerWithStoreAndFreshnessPolicy(nodeStore, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{
		`sideplane_fleet_nodes{state="fresh"} 1`,
		`sideplane_fleet_nodes{state="stale"} 1`,
		`sideplane_fleet_nodes{state="offline"} 1`,
		`sideplane_fleet_nodes_drifted 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\n%s", want, body)
		}
	}
}

func TestPublicSigningKeyAPI(t *testing.T) {
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPair: keyPair,
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/signing-key", nil)
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	var resp protocol.PublicSigningKeyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode signing key response: %v", err)
	}
	if resp.Algorithm != "ed25519" {
		t.Fatalf("algorithm = %q, want ed25519", resp.Algorithm)
	}
	if resp.PublicKey != spcrypto.PublicKeyString(keyPair.PublicKey) {
		t.Fatalf("public key response mismatch")
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(payload), "private") {
		t.Fatalf("signing key response mentions private key: %s", payload)
	}
}

func TestEventsStreamReceivesNodeJobAndRolloutEvents(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-events")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "operator-token",
		Events:        NewEventHub(),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("build events request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer operator-token")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", res.StatusCode)
	}
	if contentType := res.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	reader := bufio.NewReader(res.Body)

	doJSONRequest[protocol.HeartbeatResponse](t, server.Client(), http.MethodPost, server.URL+"/api/heartbeat", credential, protocol.HeartbeatRequest{
		NodeID: "node-events",
	})
	nodeEvent := readSSEMessage(t, reader)
	if nodeEvent.name != "node" {
		t.Fatalf("event name = %q, want node", nodeEvent.name)
	}
	var nodePayload map[string]string
	if err := json.Unmarshal([]byte(nodeEvent.data), &nodePayload); err != nil {
		t.Fatalf("decode node event: %v", err)
	}
	if nodePayload["nodeId"] != "node-events" || nodePayload["state"] != "fresh" {
		t.Fatalf("node event = %#v, want node-events/fresh", nodePayload)
	}

	job := doJSONRequest[protocol.Job](t, server.Client(), http.MethodPost, server.URL+"/api/nodes/node-events/jobs", "operator-token", protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe})
	jobEvent := readSSEMessage(t, reader)
	if jobEvent.name != "job" {
		t.Fatalf("event name = %q, want job", jobEvent.name)
	}
	var jobPayload map[string]string
	if err := json.Unmarshal([]byte(jobEvent.data), &jobPayload); err != nil {
		t.Fatalf("decode job event: %v", err)
	}
	if jobPayload["jobId"] != job.ID || jobPayload["nodeId"] != "node-events" || jobPayload["status"] != "pending" {
		t.Fatalf("job event = %#v, want created job pending", jobPayload)
	}

	rolloutResp := doJSONRequest[protocol.CreateRolloutResponse](t, server.Client(), http.MethodPost, server.URL+"/api/rollouts", "operator-token", protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
		NodeIDs:     []string{"node-events"},
		RuntimeType: "hermes",
		Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:   1,
	}})
	rolloutEvent := readSSEMessage(t, reader)
	if rolloutEvent.name != "rollout" {
		t.Fatalf("event name = %q, want rollout", rolloutEvent.name)
	}
	var rolloutPayload map[string]string
	if err := json.Unmarshal([]byte(rolloutEvent.data), &rolloutPayload); err != nil {
		t.Fatalf("decode rollout event: %v", err)
	}
	if rolloutPayload["rolloutId"] != rolloutResp.Rollout.ID || rolloutPayload["state"] != "pending" {
		t.Fatalf("rollout event = %#v, want pending rollout", rolloutPayload)
	}
}

func TestEventsStreamDisconnectRemovesClient(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	hub := NewEventHub()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "operator-token",
		Events:        hub,
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("build events request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer operator-token")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", res.StatusCode)
	}
	waitForEventHubClients(t, hub, 1)
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close event stream: %v", err)
	}
	waitForEventHubClients(t, hub, 0)
}

func TestEventsStreamAcceptsOneTimeTicket(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "operator-token",
		Events:        NewEventHub(),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ticketResp := doJSONRequest[eventTicketResponse](t, server.Client(), http.MethodPost, server.URL+"/api/events/tickets", "operator-token", map[string]string{})
	if ticketResp.Ticket == "" || ticketResp.ExpiresAt.IsZero() {
		t.Fatalf("ticket response = %+v, want ticket and expiry", ticketResp)
	}

	res, err := server.Client().Get(server.URL + "/api/events?ticket=" + ticketResp.Ticket)
	if err != nil {
		t.Fatalf("open event stream with ticket: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ticket stream status = %d, want 200", res.StatusCode)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close ticket stream: %v", err)
	}

	second, err := server.Client().Get(server.URL + "/api/events?ticket=" + ticketResp.Ticket)
	if err != nil {
		t.Fatalf("open event stream with reused ticket: %v", err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused ticket status = %d, want 401", second.StatusCode)
	}
}

func TestEventHubDropsSlowClient(t *testing.T) {
	hub := NewEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = hub.subscribe(ctx)

	for i := 0; i < eventClientBuffer+1; i++ {
		hub.publish("job", map[string]string{"jobId": "job-slow", "status": "pending"})
	}
	if got := hub.clientCount(); got != 0 {
		t.Fatalf("client count = %d, want slow client dropped", got)
	}
}

func TestHandlerLoadsPersistedSigningKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "signing-key.json")
	first, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("build first handler: %v", err)
	}
	second, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("build second handler: %v", err)
	}
	firstKey := readSigningKeyForTest(t, first)
	secondKey := readSigningKeyForTest(t, second)
	if firstKey != secondKey {
		t.Fatalf("persisted public key changed")
	}
}

func TestHeartbeatRecordsNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-1")

	body := protocol.HeartbeatRequest{
		NodeID:         "node-1",
		Hostname:       "worker-a",
		SidecarVersion: "dev",
		SentAt:         time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC),
		Runtimes: []protocol.RuntimeStatus{
			{
				Name:       "default",
				Type:       "hermes",
				State:      "running",
				Provider:   "openai",
				Model:      "gpt-5",
				ConfigHash: "sha256:abc",
			},
		},
		ConfigHash: "sha256:node",
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode heartbeat: %v", err)
	}

	handler := NewHandlerWithStore(nodeStore)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", &buf)
	req.Header.Set("Authorization", "Bearer "+credential)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var heartbeatResp protocol.HeartbeatResponse
	if err := json.NewDecoder(rec.Body).Decode(&heartbeatResp); err != nil {
		t.Fatalf("decode heartbeat response: %v", err)
	}
	if !heartbeatResp.Accepted {
		t.Fatalf("accepted = false, want true")
	}
	if heartbeatResp.Node.NodeID != "node-1" {
		t.Fatalf("nodeId = %q, want node-1", heartbeatResp.Node.NodeID)
	}
	if heartbeatResp.Node.State != protocol.NodeStateFresh {
		t.Fatalf("node state = %q, want fresh", heartbeatResp.Node.State)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	nodesResp := decodeListNodesResponse(t, rec)
	nodes := nodesResp.Nodes
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].NodeID != "node-1" {
		t.Fatalf("nodes[0].nodeId = %q, want node-1", nodes[0].NodeID)
	}
	if nodes[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("runtime type = %q, want hermes", nodes[0].Runtimes[0].Type)
	}
}

func TestNodesAPIPaginatesAndValidatesQuery(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, nodeID := range []string{"node-c", "node-a", "node-b"} {
		if _, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
		}, now); err != nil {
			t.Fatalf("record %s heartbeat: %v", nodeID, err)
		}
	}
	handler := newDevHandlerWithStore(t, nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes?limit=1&offset=1", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	resp := decodeListNodesResponse(t, rec)
	if resp.Total != 3 || resp.Limit != 1 || resp.Offset != 1 {
		t.Fatalf("page metadata = total:%d limit:%d offset:%d, want 3/1/1", resp.Total, resp.Limit, resp.Offset)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].NodeID != "node-b" {
		t.Fatalf("nodes = %#v, want node-b", resp.Nodes)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes?limit=2000", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	resp = decodeListNodesResponse(t, rec)
	if resp.Limit != store.MaxNodeListLimit || resp.Offset != 0 || resp.Total != 3 {
		t.Fatalf("capped metadata = total:%d limit:%d offset:%d, want 3/%d/0", resp.Total, resp.Limit, resp.Offset, store.MaxNodeListLimit)
	}

	for _, path := range []string{"/api/nodes?limit=0", "/api/nodes?limit=bad", "/api/nodes?offset=-1"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusBadRequest)
	}
}

func TestNodeLabelsAPISetGetSelectorAndAudit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	for _, nodeID := range []string{"node-a", "node-b", "node-c"} {
		enrollTestNode(t, nodeStore, nodeID)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	for _, tt := range []struct {
		nodeID string
		labels map[string]string
	}{
		{nodeID: "node-a", labels: map[string]string{"role": "canary", "zone": "lab"}},
		{nodeID: "node-b", labels: map[string]string{"role": "stable", "zone": "lab"}},
		{nodeID: "node-c", labels: map[string]string{"role": "canary", "zone": "vps"}},
	} {
		body, _ := json.Marshal(protocol.NodeLabelsRequest{Labels: tt.labels})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/nodes/"+tt.nodeID+"/labels", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer dev-token")
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusOK)
		var resp protocol.NodeLabelsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode set labels response: %v", err)
		}
		if resp.NodeID != tt.nodeID || resp.Labels["role"] != tt.labels["role"] {
			t.Fatalf("set labels response = %#v, want node %s role %s", resp, tt.nodeID, tt.labels["role"])
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-a/labels", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var labelsResp protocol.NodeLabelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&labelsResp); err != nil {
		t.Fatalf("decode labels response: %v", err)
	}
	if labelsResp.Labels["role"] != "canary" || labelsResp.Labels["zone"] != "lab" {
		t.Fatalf("labels response = %#v, want node-a canary lab", labelsResp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes?selector=role=canary,zone=lab", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	nodesResp := decodeListNodesResponse(t, rec)
	if nodesResp.Total != 1 || len(nodesResp.Nodes) != 1 || nodesResp.Nodes[0].NodeID != "node-a" {
		t.Fatalf("selector response = total:%d %#v, want node-a only", nodesResp.Total, nodesResp.Nodes)
	}
	if nodesResp.Nodes[0].Labels["role"] != "canary" {
		t.Fatalf("selected node labels = %#v, want canary label", nodesResp.Nodes[0].Labels)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes?selector=role=missing", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	nodesResp = decodeListNodesResponse(t, rec)
	if nodesResp.Total != 0 || len(nodesResp.Nodes) != 0 {
		t.Fatalf("missing selector response = total:%d %#v, want none", nodesResp.Total, nodesResp.Nodes)
	}

	events, err := nodeStore.ListAuditEventsFiltered(context.Background(), store.AuditFilter{
		Action: audit.ActionNodeLabelsUpdate,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 3 || events[0].TargetNode == "" {
		t.Fatalf("label audit events = %#v, want three node-scoped events", events)
	}
}

func TestNodeLabelsAPIRequiresAuthAndValidatesSelector(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-labels")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-labels/labels", nil)
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/nodes/node-labels/labels", strings.NewReader(`{"labels":{"bad\nkey":"value"}}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "bad_request", "label key and value must not contain control characters")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes?selector=role", nil)
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "bad_request", "selector entries must use key=value")
}

func TestRolloutAPICreateListGetActionsAndAudit(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	for _, nodeID := range []string{"node-a", "node-b", "node-c"} {
		enrollTestNode(t, nodeStore, nodeID)
	}
	if err := nodeStore.SetNodeLabels(context.Background(), "node-a", map[string]string{"role": "canary"}); err != nil {
		t.Fatalf("set node-a labels: %v", err)
	}
	if err := nodeStore.SetNodeLabels(context.Background(), "node-b", map[string]string{"role": "stable"}); err != nil {
		t.Fatalf("set node-b labels: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	createBody, _ := json.Marshal(protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
		Selector:    map[string]string{"role": "canary"},
		RuntimeType: "hermes",
		Profile:     "default",
		Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rollouts", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)
	var created protocol.CreateRolloutResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created rollout: %v", err)
	}
	if created.Rollout.ID == "" || created.Rollout.State != protocol.RolloutStatePending {
		t.Fatalf("created rollout = %#v, want pending with ID", created.Rollout)
	}
	if len(created.Rollout.Spec.NodeIDs) != 1 || created.Rollout.Spec.NodeIDs[0] != "node-a" {
		t.Fatalf("resolved node IDs = %#v, want node-a", created.Rollout.Spec.NodeIDs)
	}
	if created.Rollout.Spec.BatchSize != 1 || created.Rollout.Spec.HealthTimeout != rolloutengine.DefaultHealthTimeout {
		t.Fatalf("defaulted spec = %#v, want batch 1 health timeout", created.Rollout.Spec)
	}

	explicitBody, _ := json.Marshal(protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
		NodeIDs:     []string{"node-b", "node-c"},
		RuntimeType: "openclaw",
		Target:      protocol.ProviderModelConfig{Provider: "anthropic", Model: "claude-sonnet-4"},
		BatchSize:   2,
		Live:        true,
	}})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rollouts", bytes.NewReader(explicitBody))
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/rollouts?limit=1", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var list protocol.ListRolloutsResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode rollout list: %v", err)
	}
	if list.Total != 2 || list.Limit != 1 || len(list.Rollouts) != 1 {
		t.Fatalf("rollout list = %#v, want total 2 limit 1", list)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/rollouts/"+created.Rollout.ID, nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var got protocol.GetRolloutResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode rollout get: %v", err)
	}
	if got.Rollout.ID != created.Rollout.ID {
		t.Fatalf("got rollout ID = %q, want %q", got.Rollout.ID, created.Rollout.ID)
	}

	for _, action := range []protocol.RolloutAction{protocol.RolloutActionPause, protocol.RolloutActionResume, protocol.RolloutActionAbort} {
		body, _ := json.Marshal(protocol.RolloutActionRequest{Action: action})
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/rollouts/"+created.Rollout.ID+"/actions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer dev-token")
		handler.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusOK)
	}
	final, err := nodeStore.GetRollout(context.Background(), created.Rollout.ID)
	if err != nil {
		t.Fatalf("get final rollout: %v", err)
	}
	if final == nil || final.State != protocol.RolloutStateAborted {
		t.Fatalf("final rollout = %#v, want aborted", final)
	}
	events, err := nodeStore.ListAuditEventsFiltered(context.Background(), store.AuditFilter{Action: audit.ActionRolloutCreate, Limit: 10})
	if err != nil {
		t.Fatalf("list rollout create audit: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("rollout create audit events = %#v, want two", events)
	}
}

func TestRolloutAPIValidationAndAuth(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-a")
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "dev-token",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	valid := protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
		NodeIDs:     []string{"node-a"},
		RuntimeType: "hermes",
		Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
	}}
	body, _ := json.Marshal(valid)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rollouts", bytes.NewReader(body))
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))

	tests := []struct {
		name string
		req  protocol.CreateRolloutRequest
	}{
		{
			name: "empty target set",
			req: protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
				Selector:    map[string]string{"role": "missing"},
				RuntimeType: "hermes",
				Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			}},
		},
		{
			name: "selector and node IDs",
			req: protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
				Selector:    map[string]string{"role": "canary"},
				NodeIDs:     []string{"node-a"},
				RuntimeType: "hermes",
				Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			}},
		},
		{
			name: "invalid provider model",
			req: protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
				NodeIDs:     []string{"node-a"},
				RuntimeType: "hermes",
				Target:      protocol.ProviderModelConfig{Provider: "bad\nprovider", Model: "gpt-5"},
			}},
		},
		{
			name: "unknown node",
			req: protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
				NodeIDs:     []string{"missing"},
				RuntimeType: "hermes",
				Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.req)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/rollouts", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer dev-token")
			handler.ServeHTTP(rec, req)
			if rec.Code < 400 {
				t.Fatalf("status = %d, want failure; body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/rollouts", nil)
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))
}

func TestHeartbeatRequiresAuthorization(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-1"}`))

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestHeartbeatRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-1")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-1"}`))
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestHeartbeatRejectsCredentialNodeMismatch(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	nodeACredential := enrollTestNode(t, nodeStore, "node-a")
	enrollTestNode(t, nodeStore, "node-b")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-b"}`))
	req.Header.Set("Authorization", "Bearer "+nodeACredential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestEnrollmentAPIsCreateTokenAndEnrollNode(t *testing.T) {
	handler := newDevHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	var tokenResp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode enrollment token response: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatalf("token is empty")
	}
	if tokenResp.ExpiresAt.IsZero() {
		t.Fatalf("expiresAt is zero")
	}

	var enrollBody bytes.Buffer
	if err := json.NewEncoder(&enrollBody).Encode(protocol.EnrollNodeRequest{
		Token:    tokenResp.Token,
		NodeID:   "node-enrolled",
		Hostname: "worker-enrolled",
	}); err != nil {
		t.Fatalf("encode enroll request: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/enroll", &enrollBody)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var enrollResp protocol.EnrollNodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&enrollResp); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}
	if enrollResp.NodeID != "node-enrolled" {
		t.Fatalf("nodeId = %q, want node-enrolled", enrollResp.NodeID)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("nodeCredential is empty")
	}
}

func TestHeartbeatRequiresNodeID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"hostname":"worker-a"}`))

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestNodesApplyFreshnessPolicy(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	heartbeats := map[string]time.Time{
		"node-fresh":   now.Add(-time.Minute),
		"node-stale":   now.Add(-3 * time.Minute),
		"node-offline": now.Add(-11 * time.Minute),
	}
	for nodeID, observedAt := range heartbeats {
		_, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Hostname: nodeID,
		}, observedAt)
		if err != nil {
			t.Fatalf("record heartbeat for %s: %v", nodeID, err)
		}
	}

	handler, err := NewHandlerWithStoreAndFreshnessPolicy(nodeStore, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	nodesResp := decodeListNodesResponse(t, rec)

	got := make(map[string]protocol.NodeState)
	for _, node := range nodesResp.Nodes {
		got[node.NodeID] = node.State
	}
	want := map[string]protocol.NodeState{
		"node-fresh":   protocol.NodeStateFresh,
		"node-stale":   protocol.NodeStateStale,
		"node-offline": protocol.NodeStateOffline,
	}
	for nodeID, wantState := range want {
		if got[nodeID] != wantState {
			t.Fatalf("node %s state = %q, want %q", nodeID, got[nodeID], wantState)
		}
	}
}

func TestNodesReportConfigDrift(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	for _, nodeID := range []string{"node-drift", "node-match", "node-nosnapshot", "node-unknown"} {
		enrollTestNode(t, nodeStore, nodeID)
	}
	if err := nodeStore.SetDesiredConfig(context.Background(), protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	seedRuntimeConfigSnapshot(t, nodeStore, "node-drift", "anthropic", "claude-3-7-sonnet")
	seedRuntimeConfigSnapshot(t, nodeStore, "node-match", "openai", "gpt-4o")
	seedRuntimeConfigSnapshot(t, nodeStore, "node-unknown", "", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	newDevHandlerWithStore(t, nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	nodesResp := decodeListNodesResponse(t, rec)
	got := make(map[string]bool, len(nodesResp.Nodes))
	for _, node := range nodesResp.Nodes {
		got[node.NodeID] = node.Drift
	}
	want := map[string]bool{
		"node-drift":      true,
		"node-match":      false,
		"node-nosnapshot": false,
		"node-unknown":    false,
	}
	for nodeID, wantDrift := range want {
		if got[nodeID] != wantDrift {
			t.Fatalf("node %s drift = %t, want %t", nodeID, got[nodeID], wantDrift)
		}
	}
}

func TestNodesTreatZeroHeartbeatAsOffline(t *testing.T) {
	handler, err := NewHandlerWithStoreAndFreshnessPolicy(staticNodeStore{
		nodes: []protocol.NodeStatus{
			{
				NodeID:          "node-zero",
				State:           protocol.NodeStateFresh,
				LastHeartbeatAt: time.Time{},
			},
		},
	}, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	nodesResp := decodeListNodesResponse(t, rec)
	nodes := nodesResp.Nodes
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].State != protocol.NodeStateOffline {
		t.Fatalf("node state = %q, want offline", nodes[0].State)
	}
}

func TestStatusEndpointsRejectNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestHeartbeatRejectsNonPOST(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/heartbeat", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", got, http.MethodPost)
	}
}

func TestNodesRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestAPIEndpointsReturnStructuredJSONErrors(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-errors")
	devHandler := newDevHandlerWithStore(t, nodeStore)

	tests := []struct {
		name        string
		handler     http.Handler
		req         *http.Request
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "operator auth failure",
			handler:     NewHandlerWithStore(nodeStore),
			req:         httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`)),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: http.StatusText(http.StatusUnauthorized),
		},
		{
			name:        "sidecar auth failure",
			handler:     NewHandlerWithStore(nodeStore),
			req:         httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-errors", nil),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: http.StatusText(http.StatusUnauthorized),
		},
		{
			name:        "validation failure",
			handler:     devHandler,
			req:         httptest.NewRequest(http.MethodGet, "/api/nodes/node-errors/jobs?status=unknown", nil),
			wantStatus:  http.StatusBadRequest,
			wantCode:    "bad_request",
			wantMessage: `unsupported job status "unknown"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			tt.handler.ServeHTTP(rec, tt.req)

			assertAPIError(t, rec, tt.wantStatus, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestAPIErrorRedactsSecretFragments(t *testing.T) {
	rec := httptest.NewRecorder()

	writeAPIError(rec, http.StatusBadRequest, "token=secret-token status=bad")

	assertAPIError(t, rec, http.StatusBadRequest, "bad_request", "token=[REDACTED] status=bad")
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("API error leaked secret: %s", rec.Body.String())
	}
}

func TestAuditEventRedactionRedactsNestedJSONDetails(t *testing.T) {
	events := redactAuditEvents([]protocol.AuditEvent{{
		ID:     "audit_1",
		Actor:  audit.ActorSidecar,
		Action: audit.ActionJobFail,
		Detail: `{"token":"secret-token","nested":{"apiKey":"sk-test"},"status":"failed"}`,
	}})

	payload, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal audit events: %v", err)
	}
	for _, forbidden := range []string{"secret-token", "sk-test"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("redacted audit event leaked %q: %s", forbidden, payload)
		}
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(events[0].Detail), &detail); err != nil {
		t.Fatalf("decode redacted detail: %v", err)
	}
	if detail["status"] != "failed" {
		t.Fatalf("redacted audit event detail = %#v, want harmless status preserved", detail)
	}
}

func doJSONRequest[T any](t *testing.T, client *http.Client, method string, url string, bearerToken string, body any) T {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode request body: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("%s %s status = %d, want 2xx", method, url, res.StatusCode)
	}

	var out T
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s %s response: %v", method, url, err)
	}
	return out
}

func submitJobResult(t *testing.T, client *http.Client, serverURL string, jobID string, credential string, result protocol.JobResultRequest) {
	t.Helper()
	resp := doJSONRequest[map[string]string](t, client, http.MethodPost, serverURL+"/api/sidecar/jobs/"+jobID+"/result", credential, result)
	if resp["status"] != "accepted" {
		t.Fatalf("job result response = %#v, want accepted", resp)
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func decodeListNodesResponse(t *testing.T, rec *httptest.ResponseRecorder) protocol.ListNodesResponse {
	t.Helper()
	var resp protocol.ListNodesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	return resp
}

func assertAPIError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string, wantMessage string) {
	t.Helper()
	assertStatus(t, rec, wantStatus)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var apiErr protocol.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if apiErr.Code != wantCode || apiErr.Message != wantMessage {
		t.Fatalf("API error = %#v, want code=%q message=%q", apiErr, wantCode, wantMessage)
	}
}

func assertJSONStatus(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != want {
		t.Fatalf("status body = %q, want %q", body.Status, want)
	}
}

func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Content-Security-Policy": contentSecurityPolicy,
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}

func readSigningKeyForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/signing-key", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var resp protocol.PublicSigningKeyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode signing key response: %v", err)
	}
	return resp.PublicKey
}

type sseTestMessage struct {
	name string
	data string
}

func readSSEMessage(t *testing.T, reader *bufio.Reader) sseTestMessage {
	t.Helper()
	type result struct {
		message sseTestMessage
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		message, err := readSSEMessageSync(reader)
		ch <- result{message: message, err: err}
	}()

	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read SSE message: %v", got.err)
		}
		return got.message
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE message")
		return sseTestMessage{}
	}
}

func readSSEMessageSync(reader *bufio.Reader) (sseTestMessage, error) {
	var message sseTestMessage
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return sseTestMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if message.name != "" || message.data != "" {
				return message, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			message.name = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			message.data = strings.TrimSpace(value)
		}
	}
}

func waitForEventHubClients(t *testing.T, hub *EventHub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := hub.clientCount(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event hub client count = %d, want %d", hub.clientCount(), want)
}

func enrollTestNode(t *testing.T, nodeStore store.Store, nodeID string) string {
	t.Helper()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	tokenResp, err := nodeStore.CreateEnrollmentToken(context.Background(), now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	enrollResp, err := nodeStore.EnrollNode(context.Background(), protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: nodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("enroll test node %q: %v", nodeID, err)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("node credential is empty")
	}
	return enrollResp.NodeCredential
}

type staticNodeStore struct {
	nodes    []protocol.NodeStatus
	checkErr error
}

func (s staticNodeStore) Check(context.Context) error {
	return s.checkErr
}

func (s staticNodeStore) RecordHeartbeat(context.Context, protocol.HeartbeatRequest, time.Time) (protocol.NodeStatus, error) {
	return protocol.NodeStatus{}, nil
}

func (s staticNodeStore) ListNodes(context.Context) ([]protocol.NodeStatus, error) {
	nodes := append([]protocol.NodeStatus(nil), s.nodes...)
	return nodes, nil
}

func (s staticNodeStore) ListNodesFiltered(ctx context.Context, filter store.NodeFilter) (store.NodeList, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return store.NodeList{}, err
	}
	filter = store.NormalizeNodeFilter(filter)
	total := len(nodes)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}
	return store.NodeList{
		Nodes:  nodes[start:end],
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}, nil
}

func (s staticNodeStore) NodeExists(context.Context, string) (bool, error) {
	return false, nil
}

func (s staticNodeStore) SetNodeLabels(context.Context, string, map[string]string) error {
	return nil
}

func (s staticNodeStore) GetNodeLabels(context.Context, string) (map[string]string, error) {
	return nil, nil
}

func (s staticNodeStore) DeleteNode(context.Context, string) error {
	return nil
}

func (s staticNodeStore) PruneHeartbeats(context.Context, int) (int64, error) {
	return 0, nil
}

func (s staticNodeStore) CreateEnrollmentToken(context.Context, time.Time, time.Time) (protocol.CreateEnrollmentTokenResponse, error) {
	return protocol.CreateEnrollmentTokenResponse{}, nil
}

func (s staticNodeStore) EnrollNode(context.Context, protocol.EnrollNodeRequest, time.Time) (protocol.EnrollNodeResponse, error) {
	return protocol.EnrollNodeResponse{}, nil
}

func (s staticNodeStore) VerifyNodeCredential(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s staticNodeStore) CreateOperatorToken(context.Context, string, protocol.OperatorTokenScope, time.Time) (protocol.CreateOperatorTokenResponse, error) {
	return protocol.CreateOperatorTokenResponse{}, nil
}

func (s staticNodeStore) ListOperatorTokens(context.Context) ([]protocol.OperatorToken, error) {
	return nil, nil
}

func (s staticNodeStore) RevokeOperatorToken(context.Context, string, time.Time) (protocol.OperatorToken, error) {
	return protocol.OperatorToken{}, nil
}

func (s staticNodeStore) VerifyOperatorToken(context.Context, string) (string, protocol.OperatorTokenScope, bool, error) {
	return "", "", false, nil
}

func (s staticNodeStore) UpdateOperatorTokenLastUsed(context.Context, string, time.Time) error {
	return nil
}

func (s staticNodeStore) GetServerSettings(context.Context) (protocol.ServerSettings, error) {
	return protocol.ServerSettings{}, nil
}

func (s staticNodeStore) SetExpectedSidecarVersion(context.Context, string) error {
	return nil
}

func (s staticNodeStore) CreateAlertWebhook(context.Context, protocol.CreateAlertWebhookRequest, time.Time) (protocol.AlertWebhook, error) {
	return protocol.AlertWebhook{}, nil
}

func (s staticNodeStore) ListAlertWebhooks(context.Context) ([]protocol.AlertWebhook, error) {
	return nil, nil
}

func (s staticNodeStore) DeleteAlertWebhook(context.Context, string) error {
	return nil
}

func (s staticNodeStore) ListAlertWebhookTargets(context.Context, protocol.AlertEventType) ([]store.AlertWebhookTarget, error) {
	return nil, nil
}

func (s staticNodeStore) CreateJob(context.Context, protocol.CreateJobRequest, string, time.Time) (protocol.Job, error) {
	return protocol.Job{}, nil
}

func (s staticNodeStore) GetJob(context.Context, string) (*protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) ClaimNextJob(context.Context, string, time.Time) (*protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) CompleteJob(context.Context, string, protocol.JobResultRequest, time.Time) error {
	return nil
}

func (s staticNodeStore) FailJob(context.Context, string, protocol.JobResultRequest, time.Time) error {
	return nil
}

func (s staticNodeStore) ListNodeJobs(context.Context, string) ([]protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) ListNodeJobsFiltered(context.Context, string, store.JobFilter) ([]protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) PruneTerminalJobs(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s staticNodeStore) CreateRollout(context.Context, protocol.Rollout) (protocol.Rollout, error) {
	return protocol.Rollout{}, nil
}

func (s staticNodeStore) GetRollout(context.Context, string) (*protocol.Rollout, error) {
	return nil, nil
}

func (s staticNodeStore) ListRollouts(context.Context, store.RolloutFilter) (store.RolloutList, error) {
	return store.RolloutList{}, nil
}

func (s staticNodeStore) UpdateRollout(context.Context, protocol.Rollout) error {
	return nil
}

func (s staticNodeStore) PruneTerminalRollouts(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s staticNodeStore) AppendAuditEvent(context.Context, protocol.AuditEvent) (protocol.AuditEvent, error) {
	return protocol.AuditEvent{}, nil
}

func (s staticNodeStore) ListAuditEvents(context.Context, int) ([]protocol.AuditEvent, error) {
	return nil, nil
}

func (s staticNodeStore) ListAuditEventsFiltered(context.Context, store.AuditFilter) ([]protocol.AuditEvent, error) {
	return nil, nil
}

func (s staticNodeStore) PruneAuditEvents(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s staticNodeStore) GetDesiredConfig(context.Context) (protocol.DesiredConfig, error) {
	return protocol.DesiredConfig{}, nil
}

func (s staticNodeStore) SetDesiredConfig(context.Context, protocol.DesiredConfig, time.Time) error {
	return nil
}

func (s staticNodeStore) ListDesiredConfigHistory(context.Context, store.DesiredConfigHistoryFilter) (store.DesiredConfigHistoryList, error) {
	return store.DesiredConfigHistoryList{}, nil
}

func (s staticNodeStore) RevertDesiredConfig(context.Context, string) (protocol.DesiredConfigHistoryEntry, error) {
	return protocol.DesiredConfigHistoryEntry{}, nil
}
