package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// ConfigApplyExecutor executes signed config_apply jobs.
type ConfigApplyExecutor struct {
	PublicKey string
	WorkDir   string
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, os.FileMode) error
	MkdirAll  func(string, os.FileMode) error
	Now       func() time.Time
}

// Execute verifies and dry-runs a config apply plan.
func (e ConfigApplyExecutor) Execute(_ context.Context, signedPlan protocol.SignedConfigPlan) (protocol.ConfigApplyResult, error) {
	result := protocol.ConfigApplyResult{PlanID: signedPlan.Plan.ID, DryRun: signedPlan.Plan.Body.DryRun}
	addStep := func(name string, status string, detail string) {
		result.Steps = append(result.Steps, protocol.ConfigApplyStep{Name: name, Status: status, Detail: detail})
	}
	addStep("plan_received", "completed", signedPlan.Plan.Schema)

	publicKey, err := spcrypto.ParsePublicKey(e.PublicKey)
	if err != nil {
		addStep("signature_verified", "failed", err.Error())
		return result, err
	}
	if err := protocol.VerifySignedConfigPlan(signedPlan, publicKey); err != nil {
		addStep("signature_verified", "failed", err.Error())
		return result, err
	}
	addStep("signature_verified", "completed", "ed25519")

	if signedPlan.Plan.Mode != protocol.ConfigPlanModeDryRun || !signedPlan.Plan.Body.DryRun {
		err := errors.New("only dry-run config_apply is enabled")
		addStep("validated", "failed", err.Error())
		return result, err
	}
	if signedPlan.Plan.Body.RuntimeType != "hermes" {
		err := fmt.Errorf("unsupported runtime type: %s", signedPlan.Plan.Body.RuntimeType)
		addStep("validated", "failed", err.Error())
		return result, err
	}

	configPath := strings.TrimSpace(signedPlan.Plan.Body.Profile)
	if configPath == "" {
		return result, errors.New("plan profile must contain the read-only config path for dry-run apply")
	}
	readFile := e.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	contents, err := readFile(configPath)
	if err != nil {
		addStep("backup_created", "failed", err.Error())
		return result, fmt.Errorf("read current config: %w", err)
	}

	workDir := strings.TrimSpace(e.WorkDir)
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "sideplane-apply")
	}
	now := e.Now
	if now == nil {
		now = time.Now
	}
	runDir := filepath.Join(workDir, signedPlan.Plan.ID+"-"+now().UTC().Format("20060102T150405Z"))
	mkdirAll := e.MkdirAll
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	if err := mkdirAll(runDir, 0o700); err != nil {
		addStep("backup_created", "failed", err.Error())
		return result, fmt.Errorf("create apply work dir: %w", err)
	}
	writeFile := e.WriteFile
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	backupPath := filepath.Join(runDir, "current.backup")
	if err := writeFile(backupPath, contents, 0o600); err != nil {
		addStep("backup_created", "failed", err.Error())
		return result, fmt.Errorf("write backup: %w", err)
	}
	result.BackupPath = backupPath
	addStep("backup_created", "completed", "read-only copy")

	rendered, err := renderHermesDesiredConfig(signedPlan.Plan.Body.Desired)
	if err != nil {
		addStep("temp_written", "failed", err.Error())
		return result, err
	}
	tempPath := filepath.Join(runDir, "desired.json")
	if err := writeFile(tempPath, rendered, 0o600); err != nil {
		addStep("temp_written", "failed", err.Error())
		return result, fmt.Errorf("write temp config: %w", err)
	}
	result.TempPath = tempPath
	addStep("temp_written", "completed", "sidecar temp path")

	if err := ValidateHermesConfigBytes(rendered); err != nil {
		addStep("validated", "failed", err.Error())
		return result, err
	}
	addStep("validated", "completed", "hermes provider/model config")
	return result, nil
}

func renderHermesDesiredConfig(desired protocol.ProviderModelConfig) ([]byte, error) {
	if strings.TrimSpace(desired.Provider) == "" {
		return nil, errors.New("desired provider is required")
	}
	if strings.TrimSpace(desired.Model) == "" {
		return nil, errors.New("desired model is required")
	}
	payload := map[string]protocol.ProviderModelConfig{"runtime": {
		Provider: strings.TrimSpace(desired.Provider),
		Model:    strings.TrimSpace(desired.Model),
	}}
	return json.MarshalIndent(payload, "", "  ")
}

// ValidateHermesConfigBytes validates the dry-run rendered Hermes config.
func ValidateHermesConfigBytes(contents []byte) error {
	var payload struct {
		Runtime protocol.ProviderModelConfig `json:"runtime"`
	}
	if err := json.Unmarshal(contents, &payload); err != nil {
		return fmt.Errorf("parse hermes temp config: %w", err)
	}
	if strings.TrimSpace(payload.Runtime.Provider) == "" {
		return errors.New("hermes provider is required")
	}
	if strings.TrimSpace(payload.Runtime.Model) == "" {
		return errors.New("hermes model is required")
	}
	return nil
}

func (p *JobPoller) executeConfigApply(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	var signedPlan protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signedPlan); err != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("parse config_apply payload: %v", err)}
	}
	executor := ConfigApplyExecutor{PublicKey: p.publicKey, WorkDir: p.applyWorkDir}
	result, err := executor.Execute(ctx, signedPlan)
	payload, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("marshal config_apply result: %v", marshalErr)}
	}
	if err != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, ResultJSON: string(payload), Error: err.Error()}
	}
	return protocol.JobResultRequest{Status: protocol.JobStatusCompleted, ResultJSON: string(payload)}
}
