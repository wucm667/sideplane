package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobJSONOmitUnsetTimestamps(t *testing.T) {
	job := Job{
		ID:        "job_pending",
		NodeID:    "node-1",
		Type:      JobTypeDeepProbe,
		Status:    JobStatusPending,
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal pending job: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal pending job: %v", err)
	}
	if _, ok := got["claimedAt"]; ok {
		t.Fatalf("pending job JSON includes claimedAt: %s", payload)
	}
	if _, ok := got["finishedAt"]; ok {
		t.Fatalf("pending job JSON includes finishedAt: %s", payload)
	}
}

func TestJobJSONIncludesSetTimestamps(t *testing.T) {
	job := Job{
		ID:         "job_completed",
		NodeID:     "node-1",
		Type:       JobTypeDeepProbe,
		Status:     JobStatusCompleted,
		CreatedAt:  time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		ClaimedAt:  time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 6, 16, 12, 2, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal completed job: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal completed job: %v", err)
	}
	if _, ok := got["claimedAt"]; !ok {
		t.Fatalf("completed job JSON omits claimedAt: %s", payload)
	}
	if _, ok := got["finishedAt"]; !ok {
		t.Fatalf("completed job JSON omits finishedAt: %s", payload)
	}
}

func TestRestartJobPayloadAndResultJSONRoundTrip(t *testing.T) {
	payload := RestartJobPayload{
		RuntimeType: "hermes",
		RuntimeName: "Hermes Agent",
		Profile:     "default",
		Reason:      "operator requested restart after config preview",
		DryRun:      true,
	}

	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal restart payload: %v", err)
	}

	var decodedPayload RestartJobPayload
	if err := json.Unmarshal(encodedPayload, &decodedPayload); err != nil {
		t.Fatalf("unmarshal restart payload: %v", err)
	}
	if decodedPayload != payload {
		t.Fatalf("restart payload round trip mismatch: %#v", decodedPayload)
	}

	result := RestartJobResult{
		Controller:   "fake-controller",
		HealthStatus: "healthy",
		Steps: []ConfigApplyStep{
			{Name: "plan", Status: "completed", Detail: "dry-run restart planned"},
			{Name: "restart", Status: "skipped", Detail: "dry-run"},
			{Name: "health_check", Status: "skipped", Detail: "dry-run"},
		},
	}

	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal restart result: %v", err)
	}

	var decodedResult RestartJobResult
	if err := json.Unmarshal(encodedResult, &decodedResult); err != nil {
		t.Fatalf("unmarshal restart result: %v", err)
	}
	if decodedResult.Controller != result.Controller || decodedResult.HealthStatus != result.HealthStatus {
		t.Fatalf("restart result scalar fields changed: %#v", decodedResult)
	}
	if len(decodedResult.Steps) != 3 || decodedResult.Steps[1].Status != "skipped" {
		t.Fatalf("restart result steps changed: %#v", decodedResult.Steps)
	}
}

func TestRollbackBackupAndJobPayloadJSONRoundTrip(t *testing.T) {
	backup := RollbackBackup{
		Ref:         RollbackBackupRef("job_apply", "plan_123"),
		SourceJobID: "job_apply",
		PlanID:      "plan_123",
		RuntimeType: "hermes",
		Profile:     "default",
		ConfigHash:  "sha256:before",
		ConfigPath:  "/tmp/sideplane-test/config.json",
		BackupPath:  "/tmp/sideplane-test/current.backup",
		CreatedAt:   time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	}
	result := ConfigApplyResult{
		PlanID:     "plan_123",
		DryRun:     false,
		BackupPath: backup.BackupPath,
		Backup:     &backup,
		Steps:      []ConfigApplyStep{{Name: "backup_created", Status: "completed"}},
	}

	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal config apply result with backup: %v", err)
	}
	var decodedResult ConfigApplyResult
	if err := json.Unmarshal(encodedResult, &decodedResult); err != nil {
		t.Fatalf("unmarshal config apply result with backup: %v", err)
	}
	if decodedResult.Backup == nil {
		t.Fatalf("decoded backup is nil")
	}
	if decodedResult.Backup.Ref != backup.Ref || decodedResult.Backup.ConfigPath != backup.ConfigPath {
		t.Fatalf("decoded backup = %#v, want ref/config path", decodedResult.Backup)
	}
	if decodedResult.Backup.ConfigHash != backup.ConfigHash {
		t.Fatalf("decoded backup configHash = %q, want %q", decodedResult.Backup.ConfigHash, backup.ConfigHash)
	}

	payload := RollbackJobPayload{
		RuntimeType: "hermes",
		Profile:     "default",
		BackupRef:   backup.Ref,
		ConfigPath:  backup.ConfigPath,
		BackupPath:  backup.BackupPath,
		DryRun:      true,
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal rollback payload: %v", err)
	}
	var decodedPayload RollbackJobPayload
	if err := json.Unmarshal(encodedPayload, &decodedPayload); err != nil {
		t.Fatalf("unmarshal rollback payload: %v", err)
	}
	if decodedPayload != payload {
		t.Fatalf("rollback payload round trip mismatch: %#v", decodedPayload)
	}
}

func TestRollbackBackupRefFallsBackWhenPlanMissing(t *testing.T) {
	if got := RollbackBackupRef(" job_apply ", " "); got != "config_apply:job_apply" {
		t.Fatalf("backup ref = %q, want config_apply:job_apply", got)
	}
}
