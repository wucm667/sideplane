package protocol

import "strings"

const rollbackBackupRefPrefix = "config_apply"

// RollbackBackupRef builds the stable operator-facing reference for a
// sidecar-reported backup. The source job ID scopes the ref to a backup the
// server already observed; the plan ID makes it easier to audit by eye.
func RollbackBackupRef(sourceJobID, planID string) string {
	sourceJobID = strings.TrimSpace(sourceJobID)
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return rollbackBackupRefPrefix + ":" + sourceJobID
	}
	return rollbackBackupRefPrefix + ":" + sourceJobID + ":" + planID
}
