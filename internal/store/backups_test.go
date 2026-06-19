package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestRollbackBackupFromJobDerivesLegacyConfigApplyResult(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	resultJSON := configApplyResultForBackupTest(t, protocol.ConfigApplyResult{
		PlanID:     "plan_123",
		DryRun:     false,
		BackupPath: "/tmp/sideplane-test/current.backup",
		Steps:      []protocol.ConfigApplyStep{{Name: "backup_created", Status: "completed"}},
	})

	backup, ok := RollbackBackupFromJob(protocol.Job{
		ID:          "job_apply",
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusCompleted,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/tmp/sideplane-test/config.json"),
		ResultJSON:  resultJSON,
		CreatedAt:   now.Add(-time.Minute),
		FinishedAt:  now,
	})
	if !ok {
		t.Fatal("rollback backup not derived")
	}
	if backup.Ref != "config_apply:job_apply:plan_123" {
		t.Fatalf("backup ref = %q, want config_apply:job_apply:plan_123", backup.Ref)
	}
	if backup.BackupPath != "/tmp/sideplane-test/current.backup" || backup.ConfigPath != "/tmp/sideplane-test/config.json" {
		t.Fatalf("backup paths = %#v, want derived backup/config paths", backup)
	}
	if backup.RuntimeType != "hermes" || backup.PlanID != "plan_123" || backup.SourceJobID != "job_apply" {
		t.Fatalf("backup metadata = %#v, want hermes/plan/source", backup)
	}
	if !backup.CreatedAt.Equal(now) {
		t.Fatalf("createdAt = %s, want finishedAt %s", backup.CreatedAt, now)
	}
}

func TestRollbackBackupFromJobPrefersStructuredBackupMetadata(t *testing.T) {
	metadata := &protocol.RollbackBackup{
		Ref:         "config_apply:job_apply:plan_structured",
		SourceJobID: "job_apply",
		PlanID:      "plan_structured",
		RuntimeType: "openclaw",
		Profile:     "work",
		ConfigPath:  "/tmp/sideplane-test/openclaw.json",
		BackupPath:  "/tmp/sideplane-test/backup",
	}
	backup, ok := RollbackBackupFromJob(protocol.Job{
		ID:          "job_apply",
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusCompleted,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/tmp/sideplane-test/config.json"),
		ResultJSON: configApplyResultForBackupTest(t, protocol.ConfigApplyResult{
			PlanID:     "plan_legacy",
			DryRun:     false,
			BackupPath: "/tmp/sideplane-test/legacy-backup",
			Backup:     metadata,
			Steps:      []protocol.ConfigApplyStep{{Name: "backup_created", Status: "completed"}},
		}),
		CreatedAt: time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	})
	if !ok {
		t.Fatal("rollback backup not derived")
	}
	if backup.Ref != metadata.Ref || backup.RuntimeType != "openclaw" || backup.Profile != "work" {
		t.Fatalf("backup = %#v, want structured metadata", backup)
	}
}

func TestRollbackBackupFromJobRejectsUnknownOrIncompleteJobs(t *testing.T) {
	tests := []struct {
		name string
		job  protocol.Job
	}{
		{
			name: "non config apply",
			job: protocol.Job{
				Type:   protocol.JobTypeDeepProbe,
				Status: protocol.JobStatusCompleted,
			},
		},
		{
			name: "pending apply",
			job: protocol.Job{
				Type:   protocol.JobTypeConfigApply,
				Status: protocol.JobStatusPending,
			},
		},
		{
			name: "missing backup path",
			job: protocol.Job{
				ID:          "job_apply",
				Type:        protocol.JobTypeConfigApply,
				Status:      protocol.JobStatusCompleted,
				PayloadJSON: configApplyPayloadForTest(t, "hermes", "/tmp/sideplane-test/config.json"),
				ResultJSON:  configApplyResultForBackupTest(t, protocol.ConfigApplyResult{PlanID: "plan_123", DryRun: false}),
			},
		},
		{
			name: "missing config path",
			job: protocol.Job{
				ID:         "job_apply",
				Type:       protocol.JobTypeConfigApply,
				Status:     protocol.JobStatusCompleted,
				ResultJSON: configApplyResultForBackupTest(t, protocol.ConfigApplyResult{PlanID: "plan_123", DryRun: false, BackupPath: "/tmp/backup"}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if backup, ok := RollbackBackupFromJob(tt.job); ok {
				t.Fatalf("backup = %#v, want rejected", backup)
			}
		})
	}
}

func configApplyResultForBackupTest(t *testing.T, result protocol.ConfigApplyResult) string {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal config apply result: %v", err)
	}
	return string(payload)
}
