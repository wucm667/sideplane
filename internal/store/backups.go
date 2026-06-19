package store

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// RollbackBackupFromJob derives rollback metadata from a server-known config
// apply job. The backup path and config target must both come from prior
// sidecar/server records; operator requests only supply the resulting Ref.
func RollbackBackupFromJob(job protocol.Job) (protocol.RollbackBackup, bool) {
	if job.Type != protocol.JobTypeConfigApply {
		return protocol.RollbackBackup{}, false
	}
	if job.Status != protocol.JobStatusCompleted && job.Status != protocol.JobStatusFailed {
		return protocol.RollbackBackup{}, false
	}
	if strings.TrimSpace(job.ResultJSON) == "" {
		return protocol.RollbackBackup{}, false
	}

	var result protocol.ConfigApplyResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		return protocol.RollbackBackup{}, false
	}

	backup := protocol.RollbackBackup{}
	if result.Backup != nil {
		backup = *result.Backup
	}
	backup.SourceJobID = firstNonEmpty(backup.SourceJobID, job.ID)
	backup.PlanID = firstNonEmpty(backup.PlanID, result.PlanID)
	backup.BackupPath = firstNonEmpty(backup.BackupPath, result.BackupPath)
	backup.CreatedAt = firstNonZeroTime(backup.CreatedAt, job.FinishedAt, job.CreatedAt)

	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signed); err == nil {
		backup.RuntimeType = firstNonEmpty(backup.RuntimeType, signed.Plan.Body.RuntimeType)
		backup.ConfigPath = firstNonEmpty(backup.ConfigPath, signed.Plan.Body.Profile)
	}
	backup.Ref = firstNonEmpty(backup.Ref, protocol.RollbackBackupRef(backup.SourceJobID, backup.PlanID))

	if strings.TrimSpace(backup.Ref) == "" || strings.TrimSpace(backup.SourceJobID) == "" || strings.TrimSpace(backup.BackupPath) == "" || strings.TrimSpace(backup.ConfigPath) == "" {
		return protocol.RollbackBackup{}, false
	}
	return backup, true
}

// ListRollbackBackups derives known rollback backups newest-first from jobs
// already returned by the store.
func ListRollbackBackups(jobs []protocol.Job) []protocol.RollbackBackup {
	backups := make([]protocol.RollbackBackup, 0, len(jobs))
	for _, job := range jobs {
		backup, ok := RollbackBackupFromJob(job)
		if ok {
			backups = append(backups, backup)
		}
	}
	return backups
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
