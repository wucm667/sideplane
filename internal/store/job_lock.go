package store

import (
	"encoding/json"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func activeJobConflict(candidate protocol.Job, existing protocol.Job) bool {
	if candidate.NodeID != existing.NodeID || candidate.Type != existing.Type || !jobStatusIsActive(existing.Status) {
		return false
	}
	switch candidate.Type {
	case protocol.JobTypeDeepProbe:
		return true
	case protocol.JobTypeConfigApply:
		candidateRuntime, candidatePath, candidateOK := configApplyLockFields(candidate.PayloadJSON)
		existingRuntime, existingPath, existingOK := configApplyLockFields(existing.PayloadJSON)
		if !candidateOK || !existingOK {
			return true
		}
		return candidateRuntime == existingRuntime && candidatePath == existingPath
	default:
		return false
	}
}

func configApplyLockFields(payloadJSON string) (runtimeType string, configPath string, ok bool) {
	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(payloadJSON), &signed); err != nil {
		return "", "", false
	}
	runtimeType = strings.TrimSpace(signed.Plan.Body.RuntimeType)
	configPath = strings.TrimSpace(signed.Plan.Body.Profile)
	if runtimeType == "" || configPath == "" {
		return "", "", false
	}
	return runtimeType, configPath, true
}
