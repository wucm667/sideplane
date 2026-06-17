package sidecar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// defaultApplyRetention bounds how many apply run directories are kept.
const defaultApplyRetention = 20

// ConfigApplyExecutor executes signed config_apply jobs.
//
// Dry-run is always available. The live branch (atomic replace + rollback) is
// reachable only when AllowLiveApply is true AND the plan explicitly requests
// live mode. With AllowLiveApply false the live branch is never entered.
type ConfigApplyExecutor struct {
	PublicKey      string
	WorkDir        string
	AllowLiveApply bool
	// Controller restarts the runtime and verifies its health after a live
	// replace. When nil, the restart and health-check steps are skipped. A
	// failure in either step triggers a rollback to the backup.
	Controller adapters.ServiceController
	ReadFile   func(string) ([]byte, error)
	WriteFile  func(string, []byte, os.FileMode) error
	MkdirAll   func(string, os.FileMode) error
	Now        func() time.Time
}

// Execute verifies a signed config apply plan and runs it in dry-run mode, or
// in live mode when explicitly permitted.
func (e ConfigApplyExecutor) Execute(ctx context.Context, signedPlan protocol.SignedConfigPlan) (protocol.ConfigApplyResult, error) {
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

	live, err := planExecutionMode(signedPlan.Plan)
	if err != nil {
		addStep("validated", "failed", err.Error())
		return result, err
	}
	if live && !e.AllowLiveApply {
		err := errors.New("live config apply is disabled by sidecar policy (--allow-live-apply off)")
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

	rendered, err := hermes.RenderDesiredModel(contents, signedPlan.Plan.Body.Desired)
	if err != nil {
		addStep("temp_written", "failed", err.Error())
		return result, err
	}
	tempPath := filepath.Join(runDir, "desired"+configExt(configPath))
	if err := writeFile(tempPath, rendered, 0o600); err != nil {
		addStep("temp_written", "failed", err.Error())
		return result, fmt.Errorf("write temp config: %w", err)
	}
	result.TempPath = tempPath
	addStep("temp_written", "completed", "sidecar temp path")

	if err := hermes.ValidateModelConfig(rendered, signedPlan.Plan.Body.Desired); err != nil {
		addStep("validated", "failed", err.Error())
		return result, err
	}
	addStep("validated", "completed", "hermes provider/model config")

	if !live {
		addStep("replaced", "skipped", "dry-run")
		addStep("restarted", "skipped", "dry-run")
		addStep("health_checked", "skipped", "dry-run")
		pruneApplyRuns(workDir, defaultApplyRetention)
		return result, nil
	}

	// Live branch. Reachable only when AllowLiveApply is set AND the plan
	// requested live mode. Atomic rename keeps the live file intact on a write
	// failure; a failure after replace rolls back to the backup byte-for-byte.
	if err := atomicReplaceFile(configPath, rendered); err != nil {
		addStep("replaced", "failed", err.Error())
		return result, fmt.Errorf("atomic replace: %w", err)
	}
	if err := verifyWritten(readFile, configPath, rendered); err != nil {
		addStep("replaced", "failed", err.Error())
		return result, e.rollback(addStep, configPath, contents, err)
	}
	addStep("replaced", "completed", "atomic rename")

	if e.Controller == nil {
		addStep("restarted", "skipped", "no controller")
		addStep("health_checked", "skipped", "no controller")
		pruneApplyRuns(workDir, defaultApplyRetention)
		return result, nil
	}

	if err := e.Controller.Restart(ctx); err != nil {
		addStep("restarted", "failed", err.Error())
		return result, e.rollback(addStep, configPath, contents, err)
	}
	addStep("restarted", "completed", "")

	if err := e.Controller.HealthCheck(ctx); err != nil {
		addStep("health_checked", "failed", err.Error())
		return result, e.rollback(addStep, configPath, contents, err)
	}
	addStep("health_checked", "completed", "")

	pruneApplyRuns(workDir, defaultApplyRetention)
	return result, nil
}

// rollback restores the backup contents over the live config and records the
// outcome. It returns an error wrapping the triggering cause.
func (e ConfigApplyExecutor) rollback(addStep func(string, string, string), configPath string, backup []byte, cause error) error {
	if rbErr := atomicReplaceFile(configPath, backup); rbErr != nil {
		addStep("rolled_back", "failed", rbErr.Error())
		return fmt.Errorf("apply failed (%v) and rollback failed: %w", cause, rbErr)
	}
	addStep("rolled_back", "completed", "restored backup")
	return fmt.Errorf("apply failed, rolled back: %w", cause)
}

// planExecutionMode reports whether a plan requests the live branch and rejects
// inconsistent plans.
func planExecutionMode(plan protocol.ConfigPlan) (live bool, err error) {
	switch {
	case plan.Mode == protocol.ConfigPlanModeDryRun && plan.Body.DryRun:
		return false, nil
	case plan.Mode == protocol.ConfigPlanModeLive && !plan.Body.DryRun:
		return true, nil
	default:
		return false, fmt.Errorf("inconsistent plan mode %q with dryRun=%t", plan.Mode, plan.Body.DryRun)
	}
}

// atomicReplaceFile writes contents to a sibling temp file and renames it over
// path, so readers never observe a partial config.
func atomicReplaceFile(path string, contents []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sideplane-apply-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	cleanup = false
	return nil
}

// verifyWritten confirms the on-disk config matches the intended bytes.
func verifyWritten(readFile func(string) ([]byte, error), path string, want []byte) error {
	got, err := readFile(path)
	if err != nil {
		return fmt.Errorf("read back config: %w", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(want) {
		return errors.New("written config hash mismatch")
	}
	return nil
}

// pruneApplyRuns keeps only the newest keep run directories under workDir.
func pruneApplyRuns(workDir string, keep int) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" || keep <= 0 {
		return
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) <= keep {
		return
	}
	sort.Strings(dirs) // run dir names carry a sortable UTC timestamp suffix
	for _, name := range dirs[:len(dirs)-keep] {
		_ = os.RemoveAll(filepath.Join(workDir, name))
	}
}

// configExt returns the file extension of the config path so the temp file
// keeps the same format suffix (e.g. .yaml).
func configExt(path string) string {
	if ext := filepath.Ext(path); ext != "" {
		return ext
	}
	return ".tmp"
}

func (p *JobPoller) executeConfigApply(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	var signedPlan protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signedPlan); err != nil {
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("parse config_apply payload: %v", err)}
	}
	executor := ConfigApplyExecutor{PublicKey: p.publicKey, WorkDir: p.applyWorkDir, AllowLiveApply: p.allowLiveApply, Controller: p.controller}
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
