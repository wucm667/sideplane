package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	"github.com/wucm667/sideplane/pkg/adapters/openclaw"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func (p *JobPoller) executeRollback(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	var payload protocol.RollbackJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("invalid rollback payload: %v", err)}
	}

	result := protocol.RollbackJobResult{BackupRef: strings.TrimSpace(payload.BackupRef), HealthStatus: "not_checked"}
	addStep := func(name, status, detail string) {
		result.Steps = append(result.Steps, protocol.ConfigApplyStep{Name: name, Status: status, Detail: detail})
	}
	addStep("payload_received", "completed", rollbackTargetDetail(payload))

	if err := validateRollbackPayload(payload); err != nil {
		addStep("validated", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	runtimeType := strings.TrimSpace(payload.RuntimeType)
	if runtimeType == "" {
		runtimeType = "hermes"
	}
	if runtimeType != "hermes" && runtimeType != "openclaw" {
		err := fmt.Sprintf("unsupported rollback runtime type: %s", runtimeType)
		addStep("validated", "failed", err)
		return marshalRollbackResult(protocol.JobStatusFailed, result, err)
	}
	controller := p.controllerForRuntime(runtimeType)
	if !payload.DryRun {
		if !p.allowLiveApply {
			err := "live rollback is disabled by sidecar policy (--allow-live-apply off)"
			addStep("validated", "failed", err)
			return marshalRollbackResult(protocol.JobStatusFailed, result, err)
		}
		if controller == nil {
			err := "live rollback requires a configured service controller"
			addStep("validated", "failed", err)
			return marshalRollbackResult(protocol.JobStatusFailed, result, err)
		}
		if err := validateLiveConfigPath(payload.ConfigPath, p.allowedConfigDirs); err != nil {
			addStep("validated", "failed", err.Error())
			return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
		}
	}

	workDir := strings.TrimSpace(p.applyWorkDir)
	if workDir == "" {
		workDir = defaultApplyWorkDir()
	}
	if err := rejectPathOutsideAllowedDirs(payload.BackupPath, []string{workDir}, "backup path"); err != nil {
		addStep("backup_read", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	if err := rejectSymlinkComponentsUnderAllowedDirs(payload.BackupPath, []string{workDir}, "backup path"); err != nil {
		addStep("backup_read", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}

	backupContents, err := readRegularFile(payload.BackupPath, "backup path")
	if err != nil {
		addStep("backup_read", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	addStep("backup_read", "completed", "sidecar-reported backup")

	if err := validateRollbackBackup(runtimeType, backupContents); err != nil {
		addStep("validated", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	addStep("validated", "completed", runtimeType+" backup config")

	if payload.DryRun {
		addStep("restored", "skipped", "dry-run")
		addStep("restarted", "skipped", "dry-run")
		addStep("health_checked", "skipped", "dry-run")
		result.HealthStatus = "skipped"
		return marshalRollbackResult(protocol.JobStatusCompleted, result, "")
	}

	origInfo, err := os.Stat(payload.ConfigPath)
	if err != nil {
		addStep("restored", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	if !origInfo.Mode().IsRegular() {
		err := fmt.Sprintf("config path %q is not a regular file", payload.ConfigPath)
		addStep("restored", "failed", err)
		return marshalRollbackResult(protocol.JobStatusFailed, result, err)
	}
	if err := atomicReplaceFile(payload.ConfigPath, backupContents, origInfo); err != nil {
		addStep("restored", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	if err := verifyWritten(os.ReadFile, payload.ConfigPath, backupContents); err != nil {
		addStep("restored", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	addStep("restored", "completed", "backup restored")

	if err := controller.Restart(ctx); err != nil {
		addStep("restarted", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	addStep("restarted", "completed", "")

	if err := controller.HealthCheck(ctx); err != nil {
		result.HealthStatus = "unhealthy"
		addStep("health_checked", "failed", err.Error())
		return marshalRollbackResult(protocol.JobStatusFailed, result, err.Error())
	}
	result.HealthStatus = "healthy"
	addStep("health_checked", "completed", "")
	return marshalRollbackResult(protocol.JobStatusCompleted, result, "")
}

func validateRollbackPayload(payload protocol.RollbackJobPayload) error {
	if strings.TrimSpace(payload.BackupRef) == "" {
		return errors.New("rollback backupRef is required")
	}
	if strings.TrimSpace(payload.ConfigPath) == "" {
		return errors.New("rollback configPath is required")
	}
	if strings.TrimSpace(payload.BackupPath) == "" {
		return errors.New("rollback backupPath is required")
	}
	return nil
}

func readRegularFile(path string, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q is not a regular file", label, path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return contents, nil
}

func validateRollbackBackup(runtimeType string, contents []byte) error {
	if len(contents) == 0 {
		return errors.New("backup config is empty")
	}
	if runtimeType == "hermes" {
		if _, _, ok := hermes.ModelFields(contents); !ok {
			return errors.New("hermes backup config is missing model provider/name")
		}
	}
	if runtimeType == "openclaw" {
		if _, _, ok := openclaw.ProviderModelFields(contents); !ok {
			return errors.New("openclaw backup config is missing provider/model")
		}
	}
	return nil
}

func marshalRollbackResult(status protocol.JobStatus, result protocol.RollbackJobResult, errText string) protocol.JobResultRequest {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("marshal rollback result: %v", err)}
	}
	return protocol.JobResultRequest{
		Status:     status,
		ResultJSON: string(resultJSON),
		Error:      errText,
	}
}

func rollbackTargetDetail(payload protocol.RollbackJobPayload) string {
	parts := []string{}
	if payload.RuntimeType != "" {
		parts = append(parts, "type="+payload.RuntimeType)
	}
	if payload.RuntimeName != "" {
		parts = append(parts, "name="+payload.RuntimeName)
	}
	if payload.Profile != "" {
		parts = append(parts, "profile="+payload.Profile)
	}
	if payload.BackupRef != "" {
		parts = append(parts, "backupRef="+payload.BackupRef)
	}
	if len(parts) == 0 {
		return "default target"
	}
	return strings.Join(parts, " ")
}
