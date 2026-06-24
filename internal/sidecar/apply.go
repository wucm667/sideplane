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
	"syscall"
	"time"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	// defaultApplyRetention bounds how many apply run directories are kept.
	defaultApplyRetention = 20
	// maxConfigPlanAge is the conservative replay-resistance window for plans.
	maxConfigPlanAge = 15 * time.Minute
	// maxConfigPlanFutureSkew allows small server/sidecar clock differences.
	maxConfigPlanFutureSkew = 2 * time.Minute
)

// ConfigApplyExecutor executes signed config_apply jobs.
//
// Dry-run is always available. The live branch (atomic replace + rollback) is
// reachable only when AllowLiveApply is true AND the plan explicitly requests
// live mode. With AllowLiveApply false the live branch is never entered.
type ConfigApplyExecutor struct {
	NodeID            string
	PublicKey         string
	WorkDir           string
	EnvPath           string
	AllowedConfigDirs []string
	AllowLiveApply    bool
	// Controller restarts the runtime and verifies its health after a live
	// replace. Live apply requires a controller so replacement is followed by
	// restart and health-check; a failure in either step triggers rollback.
	Controller      adapters.ServiceController
	ReadFile        func(string) ([]byte, error)
	WriteFile       func(string, []byte, os.FileMode) error
	MkdirAll        func(string, os.FileMode) error
	Now             func() time.Time
	FetchEnvSecrets func(context.Context, protocol.SignedConfigPlan) (map[string]string, error)
	replaceFile     func(string, []byte, os.FileInfo) error
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

	now := e.Now
	if now == nil {
		now = time.Now
	}
	observedAt := now().UTC()
	if err := e.validateSignedPlan(signedPlan.Plan, observedAt); err != nil {
		addStep("validated", "failed", err.Error())
		return result, err
	}

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
	managedEnvNames := managedProviderEnvNames(signedPlan.Plan.Body.Providers)

	configPath := strings.TrimSpace(signedPlan.Plan.Body.Profile)
	if configPath == "" {
		return result, errors.New("plan profile must contain the read-only config path for dry-run apply")
	}
	if live {
		if e.Controller == nil {
			err := errors.New("live config apply requires a restart/health controller")
			addStep("validated", "failed", err.Error())
			return result, err
		}
		if err := validateLiveConfigPath(configPath, e.AllowedConfigDirs); err != nil {
			addStep("validated", "failed", err.Error())
			return result, err
		}
		if len(managedEnvNames) > 0 {
			envPath := strings.TrimSpace(e.EnvPath)
			if envPath == "" {
				err := errors.New("managed provider secrets require a Hermes env path")
				addStep("validated", "failed", err.Error())
				return result, err
			}
			if sameCleanAbsPath(envPath, configPath) {
				err := errors.New("Hermes env path must not be the same file as the config path")
				addStep("validated", "failed", err.Error())
				return result, err
			}
			if err := validateLiveEnvPath(envPath, e.AllowedConfigDirs); err != nil {
				addStep("validated", "failed", err.Error())
				return result, err
			}
		}
	}
	workDir := strings.TrimSpace(e.WorkDir)
	if workDir == "" {
		workDir = defaultApplyWorkDir()
	}
	if err := rejectDuplicatePlanRun(workDir, signedPlan.Plan.ID); err != nil {
		addStep("validated", "failed", err.Error())
		return result, err
	}
	readFile := e.ReadFile
	if readFile == nil {
		readFile = func(path string) ([]byte, error) {
			return readConfigFile(path, live)
		}
	}
	contents, err := readFile(configPath)
	if err != nil {
		addStep("backup_created", "failed", err.Error())
		return result, fmt.Errorf("read current config: %w", err)
	}

	runDir := filepath.Join(workDir, signedPlan.Plan.ID+"-"+observedAt.Format("20060102T150405Z"))
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
	if len(signedPlan.Plan.Body.Providers) > 0 {
		rendered, err = hermes.RenderCustomProviders(rendered, signedPlan.Plan.Body.Providers)
		if err != nil {
			addStep("temp_written", "failed", err.Error())
			return result, err
		}
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
	validateDetail := "hermes provider/model config"
	if len(signedPlan.Plan.Body.Providers) > 0 {
		if err := hermes.ValidateCustomProviders(rendered, signedPlan.Plan.Body.Providers); err != nil {
			addStep("validated", "failed", err.Error())
			return result, err
		}
		validateDetail = "hermes provider/model config and provider catalog"
	}
	addStep("validated", "completed", validateDetail)

	if !live {
		if len(managedEnvNames) > 0 {
			addStep("managed_env", "skipped", "dry-run would write managed envs: "+strings.Join(managedEnvNames, ", "))
		}
		addStep("replaced", "skipped", "dry-run")
		addStep("restarted", "skipped", "dry-run")
		addStep("health_checked", "skipped", "dry-run")
		pruneApplyRuns(workDir, defaultApplyRetention)
		return result, nil
	}

	// Live branch. Reachable only when AllowLiveApply is set AND the plan
	// requested live mode. Atomic rename keeps the live file intact on a write
	// failure; a failure after replace rolls back to the backup byte-for-byte.
	// The original file's mode and ownership are preserved so a sidecar running
	// as root does not leave the config owned by root for a non-root runtime.
	origInfo, err := os.Stat(configPath)
	if err != nil {
		addStep("replaced", "failed", err.Error())
		return result, fmt.Errorf("stat current config: %w", err)
	}
	var envState *managedEnvApply
	if len(managedEnvNames) > 0 {
		fetch := e.FetchEnvSecrets
		if fetch == nil {
			err := errors.New("managed provider secrets require a sidecar secret fetcher")
			addStep("managed_env_fetched", "failed", err.Error())
			return result, err
		}
		secrets, err := fetch(ctx, signedPlan)
		if err != nil {
			addStep("managed_env_fetched", "failed", err.Error())
			return result, err
		}
		if missing := missingManagedEnvNames(managedEnvNames, secrets); len(missing) > 0 {
			err := fmt.Errorf("managed provider secret not set for env %s", strings.Join(missing, ", "))
			addStep("managed_env_fetched", "failed", err.Error())
			return result, err
		}
		addStep("managed_env_fetched", "completed", "received managed envs: "+strings.Join(managedEnvNames, ", "))
		envState, err = e.prepareManagedEnv(strings.TrimSpace(e.EnvPath), runDir, secrets, writeFile)
		if err != nil {
			if errors.Is(err, errManagedEnvBackup) {
				addStep("managed_env_backup_created", "failed", err.Error())
			} else {
				addStep("managed_env_temp_written", "failed", err.Error())
			}
			return result, err
		}
		if envState.existed {
			addStep("managed_env_backup_created", "completed", "read-only copy")
		} else {
			addStep("managed_env_backup_created", "completed", "absent")
		}
		addStep("managed_env_temp_written", "completed", "sidecar temp path")
	}
	replaceFile := e.replaceFile
	if replaceFile == nil {
		replaceFile = atomicReplaceFile
	}
	if err := replaceFile(configPath, rendered, origInfo); err != nil {
		addStep("replaced", "failed", err.Error())
		if configWasMutated(err) {
			return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
		}
		return result, fmt.Errorf("atomic replace: %w", err)
	}
	if err := verifyWritten(readFile, configPath, rendered); err != nil {
		addStep("replaced", "failed", err.Error())
		return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
	}
	addStep("replaced", "completed", "atomic rename")
	if envState != nil {
		if err := replaceManagedEnv(envState, origInfo); err != nil {
			addStep("managed_env_replaced", "failed", err.Error())
			return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
		}
		addStep("managed_env_replaced", "completed", "atomic rename")
	}

	if e.Controller == nil {
		err := errors.New("live config apply requires a restart/health controller")
		addStep("restarted", "failed", err.Error())
		return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
	}

	if err := e.Controller.Restart(ctx); err != nil {
		addStep("restarted", "failed", err.Error())
		return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
	}
	addStep("restarted", "completed", "")

	if err := e.Controller.HealthCheck(ctx); err != nil {
		addStep("health_checked", "failed", err.Error())
		return result, e.rollbackBoth(addStep, configPath, contents, origInfo, envState, err)
	}
	addStep("health_checked", "completed", "")

	pruneApplyRuns(workDir, defaultApplyRetention)
	return result, nil
}

// rollback restores the backup contents over the live config and records the
// outcome. It returns an error wrapping the triggering cause.
func (e ConfigApplyExecutor) rollback(addStep func(string, string, string), configPath string, backup []byte, orig os.FileInfo, cause error) error {
	if rbErr := atomicReplaceFile(configPath, backup, orig); rbErr != nil {
		addStep("rolled_back", "failed", rbErr.Error())
		return fmt.Errorf("apply failed (%v) and rollback failed: %w", cause, rbErr)
	}
	addStep("rolled_back", "completed", "restored backup")
	return fmt.Errorf("apply failed, rolled back: %w", cause)
}

func (e ConfigApplyExecutor) rollbackBoth(addStep func(string, string, string), configPath string, backup []byte, orig os.FileInfo, envState *managedEnvApply, cause error) error {
	if envState == nil {
		return e.rollback(addStep, configPath, backup, orig, cause)
	}
	failures := []string{}
	if rbErr := atomicReplaceFile(configPath, backup, orig); rbErr != nil {
		failures = append(failures, "config: "+rbErr.Error())
	}
	if rbErr := restoreManagedEnvBackup(envState); rbErr != nil {
		failures = append(failures, "env: "+rbErr.Error())
	}
	if len(failures) > 0 {
		detail := strings.Join(failures, "; ")
		addStep("rolled_back", "failed", detail)
		return fmt.Errorf("apply failed (%v) and rollback failed: %s", cause, detail)
	}
	addStep("rolled_back", "completed", "restored config and managed env backups")
	return fmt.Errorf("apply failed, rolled back: %w", cause)
}

var errManagedEnvBackup = errors.New("managed env backup failed")

type managedEnvApply struct {
	path     string
	backup   []byte
	info     os.FileInfo
	existed  bool
	rendered []byte
	tempPath string
}

func (e ConfigApplyExecutor) prepareManagedEnv(envPath string, runDir string, secrets map[string]string, writeFile func(string, []byte, os.FileMode) error) (*managedEnvApply, error) {
	state := &managedEnvApply{path: envPath}
	info, err := os.Lstat(envPath)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: env path %q is not a regular file", errManagedEnvBackup, envPath)
		}
		contents, err := os.ReadFile(envPath)
		if err != nil {
			return nil, fmt.Errorf("%w: read env file: %v", errManagedEnvBackup, err)
		}
		state.backup = contents
		state.info = info
		state.existed = true
		backupPath := filepath.Join(runDir, "current.env.backup")
		if err := writeFile(backupPath, contents, 0o600); err != nil {
			return nil, fmt.Errorf("%w: write env backup: %v", errManagedEnvBackup, err)
		}
	case errors.Is(err, os.ErrNotExist):
		state.backup = nil
		state.info = nil
		state.existed = false
	default:
		return nil, fmt.Errorf("%w: inspect env path: %v", errManagedEnvBackup, err)
	}

	rendered, err := hermes.RenderManagedEnv(state.backup, secrets)
	if err != nil {
		return nil, err
	}
	tempPath := filepath.Join(runDir, "desired.env")
	if err := writeFile(tempPath, rendered, 0o600); err != nil {
		return nil, fmt.Errorf("write temp env: %w", err)
	}
	state.rendered = rendered
	state.tempPath = tempPath
	return state, nil
}

func replaceManagedEnv(state *managedEnvApply, configInfo os.FileInfo) error {
	if state == nil {
		return nil
	}
	if err := atomicReplaceFileWithModeOwner(state.path, state.rendered, 0o600, configInfo); err != nil {
		return fmt.Errorf("atomic replace env: %w", err)
	}
	if err := verifyManagedEnvWritten(state.path, state.rendered); err != nil {
		return err
	}
	return nil
}

func restoreManagedEnvBackup(state *managedEnvApply) error {
	if state == nil {
		return nil
	}
	if state.existed {
		return atomicReplaceFile(state.path, state.backup, state.info)
	}
	if err := os.Remove(state.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete env file created during failed apply: %w", err)
	}
	return nil
}

func verifyManagedEnvWritten(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read back env: %w", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(want) {
		return errors.New("written env hash mismatch")
	}
	return nil
}

func missingManagedEnvNames(names []string, secrets map[string]string) []string {
	missing := []string{}
	for _, name := range names {
		if _, ok := secrets[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func managedProviderEnvNames(providers []protocol.ProviderDefinition) []string {
	seen := map[string]struct{}{}
	for _, provider := range providers {
		if !provider.APIKeyManaged {
			continue
		}
		envName := strings.TrimSpace(provider.APIKeyEnv)
		if envName == "" {
			continue
		}
		seen[envName] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type mutatedConfigError struct {
	err error
}

func (e mutatedConfigError) Error() string {
	return e.err.Error()
}

func (e mutatedConfigError) Unwrap() error {
	return e.err
}

func configWasMutated(err error) bool {
	var mutated mutatedConfigError
	return errors.As(err, &mutated)
}

func validateLiveConfigPath(path string, allowedDirs []string) error {
	if err := rejectPathOutsideAllowedDirs(path, allowedDirs, "config path"); err != nil {
		return err
	}
	if len(nonEmptyPaths(allowedDirs)) > 0 {
		if err := rejectSymlinkComponentsUnderAllowedDirs(path, allowedDirs, "config path"); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect config path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("live apply refuses symlink config path %q until target resolution is implemented", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config path %q is not a regular file", path)
	}
	return nil
}

func validateLiveEnvPath(path string, allowedDirs []string) error {
	if err := rejectPathOutsideAllowedDirs(path, allowedDirs, "Hermes env path"); err != nil {
		return err
	}
	if len(nonEmptyPaths(allowedDirs)) > 0 {
		parent := filepath.Dir(filepath.Clean(path))
		if err := rejectSymlinkComponentsUnderAllowedDirs(parent, allowedDirs, "Hermes env path parent"); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("live apply refuses symlink Hermes env path %q until target resolution is implemented", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Hermes env path %q is not a regular file", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Hermes env path: %w", err)
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect Hermes env parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("live apply refuses symlink Hermes env parent %q until target resolution is implemented", parent)
	}
	if !parentInfo.Mode().IsDir() {
		return fmt.Errorf("Hermes env parent %q is not a directory", parent)
	}
	return nil
}

func sameCleanAbsPath(left string, right string) bool {
	cleanLeft, err := cleanAbsPath(left, "Hermes env path")
	if err != nil {
		return false
	}
	cleanRight, err := cleanAbsPath(right, "config path")
	if err != nil {
		return false
	}
	return cleanLeft == cleanRight
}

func rejectDuplicatePlanRun(workDir string, planID string) error {
	entries, err := os.ReadDir(workDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect apply work dir for replay: %w", err)
	}
	prefix := planID + "-"
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			return fmt.Errorf("duplicate config plan id %q already has an apply run", planID)
		}
	}
	return nil
}

func (e ConfigApplyExecutor) validateSignedPlan(plan protocol.ConfigPlan, now time.Time) error {
	if strings.TrimSpace(plan.ID) == "" {
		return errors.New("plan id is required")
	}
	if !safePathToken(plan.ID) {
		return fmt.Errorf("plan id %q is not a safe path token", plan.ID)
	}
	if plan.Schema != protocol.ConfigPlanSchema {
		return fmt.Errorf("unsupported plan schema %q", plan.Schema)
	}
	if plan.Version != protocol.ConfigPlanVersion {
		return fmt.Errorf("unsupported plan version %d", plan.Version)
	}
	nodeID := strings.TrimSpace(e.NodeID)
	if nodeID == "" {
		return errors.New("sidecar node id is required to validate plan target")
	}
	targetNodeID := strings.TrimSpace(plan.TargetNodeID)
	if targetNodeID != nodeID {
		return fmt.Errorf("plan target node %q does not match sidecar node %q", targetNodeID, nodeID)
	}
	if plan.CreatedAt.IsZero() {
		return errors.New("plan createdAt is required")
	}
	createdAt := plan.CreatedAt.UTC()
	if createdAt.After(now.Add(maxConfigPlanFutureSkew)) {
		return fmt.Errorf("plan createdAt %s is too far in the future", createdAt.Format(time.RFC3339))
	}
	if now.Sub(createdAt) > maxConfigPlanAge {
		return fmt.Errorf("plan expired: createdAt %s is older than %s", createdAt.Format(time.RFC3339), maxConfigPlanAge)
	}
	if err := spconfig.ValidateProviderModelSelection(plan.Body.Desired); err != nil {
		return fmt.Errorf("invalid desired provider/model: %w", err)
	}
	if len(plan.Body.Providers) > 0 {
		if err := spconfig.ValidateDesiredConfigValues(protocol.DesiredConfig{GlobalProviders: plan.Body.Providers}); err != nil {
			return fmt.Errorf("invalid desired provider catalog: %w", err)
		}
	}
	return nil
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
// path, so readers never observe a partial config. When orig is non-nil, the
// original file's permission bits and ownership are preserved so that a sidecar
// running as root does not leave the config owned by root.
func atomicReplaceFile(path string, contents []byte, orig os.FileInfo) error {
	mode := os.FileMode(0o600)
	if orig != nil {
		mode = orig.Mode().Perm()
	}
	return atomicReplaceFileWithModeOwner(path, contents, mode, orig)
}

func atomicReplaceFileWithModeOwner(path string, contents []byte, mode os.FileMode, owner os.FileInfo) error {
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
	if uid, gid, ok := fileOwner(owner); ok {
		if err := os.Chown(tmpName, uid, gid); err != nil {
			return fmt.Errorf("restore temp ownership: %w", err)
		}
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	cleanup = false
	return nil
}

// fileOwner returns the uid/gid of a stat result, when available on this OS.
func fileOwner(info os.FileInfo) (uid, gid int, ok bool) {
	if info == nil {
		return 0, 0, false
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid), true
	}
	return 0, 0, false
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

func defaultApplyWorkDir() string {
	return filepath.Join(os.TempDir(), "sideplane-apply")
}

func readConfigFile(path string, live bool) ([]byte, error) {
	if live {
		return readRegularFile(path, "config path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect config path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("config path %q is not a regular file", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return contents, nil
}

func rejectPathOutsideAllowedDirs(path string, allowedDirs []string, label string) error {
	if len(nonEmptyPaths(allowedDirs)) == 0 {
		return nil
	}
	_, err := matchingAllowedDir(path, allowedDirs, label)
	return err
}

func matchingAllowedDir(path string, allowedDirs []string, label string) (string, error) {
	cleanPath, err := cleanAbsPath(path, label)
	if err != nil {
		return "", err
	}
	for _, allowed := range allowedDirs {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		cleanAllowed, err := cleanAbsPath(allowed, "allowed directory")
		if err != nil {
			return "", err
		}
		if pathInsideDir(cleanPath, cleanAllowed) {
			return cleanAllowed, nil
		}
	}
	return "", fmt.Errorf("%s %q is outside allowed directories", label, path)
}

func pathInsideDir(cleanPath string, cleanDir string) bool {
	rel, err := filepath.Rel(cleanDir, cleanPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func nonEmptyPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			out = append(out, path)
		}
	}
	return out
}

func cleanAbsPath(path string, label string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%s %q must be absolute", label, path)
	}
	return cleaned, nil
}

func rejectSymlinkComponentsUnderAllowedDirs(path string, allowedDirs []string, label string) error {
	cleaned, err := cleanAbsPath(path, label)
	if err != nil {
		return err
	}
	root, err := matchingAllowedDir(path, allowedDirs, label)
	if err != nil {
		return err
	}
	for current := cleaned; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s %q includes symlink component %q", label, path, current)
		}
		if current == root {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("%s %q is outside allowed directories", label, path)
		}
	}
}

func safePathToken(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func (p *JobPoller) executeConfigApply(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	logger := p.jobLogger()
	var signedPlan protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signedPlan); err != nil {
		logger.Warn("config_apply payload rejected", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "error", err)
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("parse config_apply payload: %v", err)}
	}
	logger.Info("config_apply execution started", "job_id", job.ID, "node_id", p.nodeID, "plan_id", signedPlan.Plan.ID, "mode", signedPlan.Plan.Mode, "dry_run", signedPlan.Plan.Body.DryRun)
	executor := ConfigApplyExecutor{NodeID: p.nodeID, PublicKey: p.publicKey, WorkDir: p.applyWorkDir, EnvPath: p.envPath, AllowedConfigDirs: p.allowedConfigDirs, AllowLiveApply: p.allowLiveApply, Controller: p.controllerForRuntime(signedPlan.Plan.Body.RuntimeType), FetchEnvSecrets: p.fetchConfigApplySecrets}
	result, err := executor.Execute(ctx, signedPlan)
	payload, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		logger.Warn("config_apply result marshal failed", "job_id", job.ID, "node_id", p.nodeID, "plan_id", signedPlan.Plan.ID, "mode", signedPlan.Plan.Mode, "error", marshalErr)
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: fmt.Sprintf("marshal config_apply result: %v", marshalErr)}
	}
	if err != nil {
		logger.Warn("config_apply execution failed", "job_id", job.ID, "node_id", p.nodeID, "plan_id", signedPlan.Plan.ID, "mode", signedPlan.Plan.Mode, "dry_run", signedPlan.Plan.Body.DryRun, "error", err)
		return protocol.JobResultRequest{Status: protocol.JobStatusFailed, ResultJSON: string(payload), Error: err.Error()}
	}
	logger.Info("config_apply execution completed", "job_id", job.ID, "node_id", p.nodeID, "plan_id", signedPlan.Plan.ID, "mode", signedPlan.Plan.Mode, "dry_run", signedPlan.Plan.Body.DryRun)
	return protocol.JobResultRequest{Status: protocol.JobStatusCompleted, ResultJSON: string(payload)}
}
