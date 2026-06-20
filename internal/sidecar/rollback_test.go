package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

	result := (&JobPoller{applyWorkDir: dir}).executeJob(context.Background(), &job)
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

	result := (&JobPoller{applyWorkDir: dir, controller: controller}).executeJob(context.Background(), &job)
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

func TestRollbackRejectsBackupPathTraversalOutsideWorkDir(t *testing.T) {
	baseDir := t.TempDir()
	workDir := filepath.Join(baseDir, "work")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	configPath, original := writeHermesConfig(t, baseDir)
	backupPath := writeRollbackBackup(t, outsideDir, "backup.yaml")
	traversalBackupPath := filepath.Join(workDir, "..", "outside", filepath.Base(backupPath))
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  traversalBackupPath,
		DryRun:      true,
	})

	result := (&JobPoller{applyWorkDir: workDir}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("rollback status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "outside allowed directories") {
		t.Fatalf("rollback error = %q, want outside allowed directories", result.Error)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(original) {
		t.Fatalf("backup path rejection mutated config: %q", gotConfig)
	}
}

func TestRollbackRejectsBackupSymlinkComponent(t *testing.T) {
	baseDir := t.TempDir()
	workDir := filepath.Join(baseDir, "work")
	actualDir := filepath.Join(baseDir, "actual")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.MkdirAll(actualDir, 0o700); err != nil {
		t.Fatalf("mkdir actual dir: %v", err)
	}
	configPath, _ := writeHermesConfig(t, baseDir)
	backupPath := writeRollbackBackup(t, actualDir, "backup.yaml")
	linkDir := filepath.Join(workDir, "link")
	if err := os.Symlink(actualDir, linkDir); err != nil {
		t.Fatalf("symlink backup dir: %v", err)
	}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  configPath,
		BackupPath:  filepath.Join(linkDir, filepath.Base(backupPath)),
		DryRun:      true,
	})

	result := (&JobPoller{applyWorkDir: workDir}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("rollback status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "symlink component") {
		t.Fatalf("rollback error = %q, want symlink component", result.Error)
	}
}

func TestRollbackRejectsNonRegularBackupPath(t *testing.T) {
	workDir := t.TempDir()
	configPath, _ := writeHermesConfig(t, workDir)
	directoryPath := filepath.Join(workDir, "backup-dir")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	fifoPath := filepath.Join(workDir, "backup.fifo")
	fifoAvailable := true
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		fifoAvailable = false
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "directory", path: directoryPath},
	}
	if fifoAvailable {
		tests = append(tests, struct {
			name string
			path string
		}{name: "fifo", path: fifoPath})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := rollbackJobForTest(t, protocol.RollbackJobPayload{
				RuntimeType: "hermes",
				BackupRef:   "config_apply:job_apply:plan_rollback",
				ConfigPath:  configPath,
				BackupPath:  tt.path,
				DryRun:      true,
			})

			result := (&JobPoller{applyWorkDir: workDir}).executeJob(context.Background(), &job)
			if result.Status != protocol.JobStatusFailed {
				t.Fatalf("rollback status = %q, want failed", result.Status)
			}
			if !strings.Contains(result.Error, "not a regular file") {
				t.Fatalf("rollback error = %q, want not regular", result.Error)
			}
		})
	}
}

func TestRollbackLiveRejectsConfigPathOutsideAllowedDir(t *testing.T) {
	baseDir := t.TempDir()
	allowedDir := filepath.Join(baseDir, "allowed")
	outsideDir := filepath.Join(baseDir, "outside")
	workDir := filepath.Join(baseDir, "work")
	for _, dir := range []string{allowedDir, outsideDir, workDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	configPath, original := writeHermesConfig(t, outsideDir)
	backupPath := writeRollbackBackup(t, workDir, "backup.yaml")
	controller := &fakeServiceController{}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "hermes",
		BackupRef:   "config_apply:job_apply:plan_rollback",
		ConfigPath:  filepath.Join(allowedDir, "..", "outside", filepath.Base(configPath)),
		BackupPath:  backupPath,
		DryRun:      false,
	})

	result := (&JobPoller{applyWorkDir: workDir, allowedConfigDirs: []string{allowedDir}, allowLiveApply: true, controller: controller}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("rollback status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "outside allowed directories") {
		t.Fatalf("rollback error = %q, want outside allowed directories", result.Error)
	}
	if controller.restartCalls != 0 || controller.healthCalls != 0 {
		t.Fatalf("controller calls restart=%d health=%d, want 0/0", controller.restartCalls, controller.healthCalls)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(original) {
		t.Fatalf("config path rejection mutated config: %q", gotConfig)
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

	result := (&JobPoller{applyWorkDir: dir, allowLiveApply: true, controller: controller}).executeJob(context.Background(), &job)
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

func TestRollbackLiveUsesRuntimeSpecificOpenClawController(t *testing.T) {
	dir := t.TempDir()
	configPath, _ := writeOpenClawConfig(t, dir, "openclaw-config.json", "gpt-4o")
	backupPath, backupContents := writeOpenClawConfig(t, dir, "openclaw-backup.json", "gpt-4o-mini")
	hermesController := &fakeServiceController{}
	openclawController := &fakeServiceController{}
	job := rollbackJobForTest(t, protocol.RollbackJobPayload{
		RuntimeType: "openclaw",
		BackupRef:   "config_apply:job_apply:plan_openclaw",
		ConfigPath:  configPath,
		BackupPath:  backupPath,
		DryRun:      false,
	})

	result := (&JobPoller{
		applyWorkDir:   dir,
		allowLiveApply: true,
		controller:     hermesController,
		controllerResolver: fakeControllerResolver{
			"openclaw": openclawController,
		},
	}).executeJob(context.Background(), &job)
	if result.Status != protocol.JobStatusCompleted {
		t.Fatalf("rollback status = %q, want completed; error=%q", result.Status, result.Error)
	}
	if openclawController.restartCalls != 1 || openclawController.healthCalls != 1 {
		t.Fatalf("openclaw controller calls restart=%d health=%d, want 1/1", openclawController.restartCalls, openclawController.healthCalls)
	}
	if hermesController.restartCalls != 0 || hermesController.healthCalls != 0 {
		t.Fatalf("hermes controller calls restart=%d health=%d, want 0/0", hermesController.restartCalls, hermesController.healthCalls)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(gotConfig) != string(backupContents) {
		t.Fatalf("restored config = %q, want backup %q", gotConfig, backupContents)
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

	result := (&JobPoller{applyWorkDir: dir, allowLiveApply: true, controller: controller}).executeJob(context.Background(), &job)
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

func writeOpenClawConfig(t *testing.T, dir string, name string, model string) (string, []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	contents := []byte(fmt.Sprintf(`{"provider":"openai","model":"%s"}`+"\n", model))
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write openclaw config: %v", err)
	}
	return path, contents
}

func decodeRollbackResultForTest(t *testing.T, resultJSON string) protocol.RollbackJobResult {
	t.Helper()
	var result protocol.RollbackJobResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode rollback result: %v", err)
	}
	return result
}
