package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestRollbackDryRunReadsBackupWithoutRestoring(t *testing.T) {
	dir := t.TempDir()
	configPath, original := writeHermesConfig(t, dir)
	backupPath := writeRollbackBackup(t, dir, "backup.yaml")
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  backupPath,
		DryRun:      true,
	})

	result := (&JobPoller{}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusCompleted {
		t.Fatalf("rollback status = %q, want completed; error=%q", result.Status, result.Error)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(original) {
		t.Fatalf("dry-run mutated config: %q", gotConfig)
	}
	decoded := decodeRollbackResultForTest(t, result.ResultJSON)
	if decoded.HealthStatus != "skipped" {
		t.Fatalf("health status = %q, want skipped", decoded.HealthStatus)
	}
	if step, ok := findStep(t, decoded.Steps, "restored"); !ok || step.Status != "skipped" {
		t.Fatalf("restored step = %#v, want skipped", step)
	}
}

func TestRollbackLiveRejectedWithoutAllowLive(t *testing.T) {
	dir := t.TempDir()
	configPath, original := writeHermesConfig(t, dir)
	backupPath := writeRollbackBackup(t, dir, "backup.yaml")
	controller := &fakeServiceController{}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  backupPath,
		DryRun:      false,
	})

	result := (&JobPoller{controller: controller}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("rollback status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "disabled") {
		t.Fatalf("rollback error = %q, want disabled policy", result.Error)
	}
	if controller.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", controller.restartCalls)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(original) {
		t.Fatalf("policy rejection mutated config: %q", gotConfig)
	}
}

func TestRollbackLiveRestoresBackupAndCallsControllerOnce(t *testing.T) {
	dir := t.TempDir()
	configPath, _ := writeHermesConfig(t, dir)
	backupPath := writeRollbackBackup(t, dir, "backup.yaml")
	backupContents, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	controller := &fakeServiceController{}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  backupPath,
		DryRun:      false,
	})

	result := (&JobPoller{allowLiveApply: true, controller: controller}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusCompleted {
		t.Fatalf("rollback status = %q, want completed; error=%q", result.Status, result.Error)
	}
	if controller.restartCalls != 1 || controller.healthCalls != 1 {
		t.Fatalf("controller calls restart=%d health=%d, want 1/1", controller.restartCalls, controller.healthCalls)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(backupContents) {
		t.Fatalf("restored config = %q, want backup %q", gotConfig, backupContents)
	}
	decoded := decodeRollbackResultForTest(t, result.ResultJSON)
	if decoded.HealthStatus != "healthy" {
		t.Fatalf("health status = %q, want healthy", decoded.HealthStatus)
	}
}

func TestRollbackHealthFailureReturnsFailedWithoutRecursiveRollback(t *testing.T) {
	dir := t.TempDir()
	configPath, _ := writeHermesConfig(t, dir)
	backupPath := writeRollbackBackup(t, dir, "backup.yaml")
	backupContents, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	controller := &fakeServiceController{healthErr: errors.New("runtime stayed unhealthy")}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  backupPath,
		DryRun:      false,
	})

	result := (&JobPoller{allowLiveApply: true, controller: controller}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("rollback status = %q, want failed", result.Status)
	}
	if controller.restartCalls != 1 || controller.healthCalls != 1 {
		t.Fatalf("controller calls restart=%d health=%d, want 1/1", controller.restartCalls, controller.healthCalls)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(backupContents) {
		t.Fatalf("restored config = %q, want backup %q", gotConfig, backupContents)
	}
	decoded := decodeRollbackResultForTest(t, result.ResultJSON)
	if decoded.HealthStatus != "unhealthy" {
		t.Fatalf("health status = %q, want unhealthy", decoded.HealthStatus)
	}
	if _, ok := findStep(t, decoded.Steps, "rolled_back"); ok {
		t.Fatal("rollback job recorded recursive rolled_back step")
	}
}

func rollbackJobForTest(t *testing.T, payload protocol.RollbackJobPayload) protocol.Job {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal rollback payload: %v", err)
	}
	return protocol.Job{
		ID:          "job_rollback",
		NodeID:      "node-rollback",
		Type:        protocol.JobTypeRollback,
		Status:      protocol.JobStatusClaimed,
		PayloadJSON: string(payloadJSON),
	}
}

func writeRollbackBackup(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	contents := []byte("model:\n  default: gpt-4o\n  provider: openai\nproviders: {}\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write rollback backup: %v", err)
	}
	return path
}

func decodeRollbackResultForTest(t *testing.T, resultJSON string) protocol.RollbackJobResult {
	t.Helper()
	var result protocol.RollbackJobResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode rollback result: %v", err)
	}
	return result
}
