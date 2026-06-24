package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func livePlan(nodeID, configPath, provider, model string) protocol.ConfigPlan {
	p := dryRunPlan(nodeID, configPath, provider, model)
	p.Mode = protocol.ConfigPlanModeLive
	p.Body.DryRun = false
	return p
}

func withProviderCatalog(plan protocol.ConfigPlan) protocol.ConfigPlan {
	plan.Body.Providers = []protocol.ProviderDefinition{
		{Name: "anthropic", BaseURL: "https://api.anthropic.example/v1", APIKeyEnv: "ANTHROPIC_API_KEY"},
		{Name: "openai", BaseURL: "https://api.openai.example/v1", APIKeyEnv: "OPENAI_API_KEY"},
	}
	return plan
}

type stubController struct {
	restartErr error
	healthErr  error
	restarts   int
	healths    int
}

func (s *stubController) Restart(context.Context) error {
	s.restarts++
	return s.restartErr
}

func (s *stubController) HealthCheck(context.Context) error {
	s.healths++
	return s.healthErr
}

func newTestSigningKey(t *testing.T) (publicKey string, privateKey []byte) {
	t.Helper()
	kp, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return spcrypto.PublicKeyString(kp.PublicKey), kp.PrivateKey
}

func dryRunPlan(nodeID, configPath, provider, model string) protocol.ConfigPlan {
	return protocol.ConfigPlan{
		ID:           "plan-1",
		Schema:       protocol.ConfigPlanSchema,
		Version:      protocol.ConfigPlanVersion,
		CreatedAt:    time.Now().UTC(),
		TargetNodeID: nodeID,
		Mode:         protocol.ConfigPlanModeDryRun,
		Body: protocol.ConfigPlanBody{
			RuntimeType: "hermes",
			Profile:     configPath,
			Desired:     protocol.ProviderModelConfig{Provider: provider, Model: model},
			DryRun:      true,
		},
	}
}

func writeHermesConfig(t *testing.T, dir string) (path string, contents []byte) {
	t.Helper()
	path = filepath.Join(dir, "config.yaml")
	contents = []byte("model:\n  default: claude-3.7-sonnet\n  provider: anthropic\n  base_url: https://example.invalid/v1\nproviders: {}\ntoolsets:\n  shell:\n    provider: auto\n    model: ''\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path, contents
}

func findStep(t *testing.T, steps []protocol.ConfigApplyStep, name string) (protocol.ConfigApplyStep, bool) {
	t.Helper()
	for _, s := range steps {
		if s.Name == name {
			return s, true
		}
	}
	return protocol.ConfigApplyStep{}, false
}

func dirEntryCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	return len(entries)
}

func TestConfigApplyDryRunHappyPath(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, name := range []string{"plan_received", "signature_verified", "backup_created", "temp_written", "validated"} {
		s, ok := findStep(t, result.Steps, name)
		if !ok {
			t.Fatalf("missing step %q", name)
		}
		if s.Status != "completed" {
			t.Errorf("step %q status = %q, want completed (%s)", name, s.Status, s.Detail)
		}
	}
	for _, name := range []string{"replaced", "restarted", "health_checked"} {
		if s, ok := findStep(t, result.Steps, name); !ok || s.Status != "skipped" {
			t.Errorf("dry-run %s step = %+v, want skipped", name, s)
		}
	}
	if !result.DryRun {
		t.Error("result.DryRun = false, want true")
	}
	if result.BackupPath == "" || result.TempPath == "" {
		t.Fatalf("backup/temp paths not set: backup=%q temp=%q", result.BackupPath, result.TempPath)
	}

	// Backup must be a faithful read-only copy of the original config.
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != string(original) {
		t.Errorf("backup contents = %q, want %q", backup, original)
	}

	// Temp/backup must live under the sidecar work dir, never next to the live config.
	if !strings.HasPrefix(result.BackupPath, workDir) {
		t.Errorf("backup path %q not under work dir %q", result.BackupPath, workDir)
	}
	if !strings.HasPrefix(result.TempPath, workDir) {
		t.Errorf("temp path %q not under work dir %q", result.TempPath, workDir)
	}

	// The live config must be untouched and no new files written beside it.
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != string(original) {
		t.Error("live config was modified during dry-run apply")
	}
	if n := dirEntryCount(t, srcDir); n != 1 {
		t.Errorf("source dir entry count = %d, want 1 (no writes beside live config)", n)
	}
}

func TestConfigApplyDryRunRendersProviderCatalog(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(withProviderCatalog(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o")), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "completed" || !strings.Contains(s.Detail, "provider catalog") {
		t.Fatalf("validated step = %+v, want completed provider catalog validation", s)
	}
	rendered, err := os.ReadFile(result.TempPath)
	if err != nil {
		t.Fatalf("read temp config: %v", err)
	}
	provider, model, ok := hermes.ModelFields(rendered)
	if !ok || provider != "openai" || model != "gpt-4o" {
		t.Fatalf("temp model fields = (%q, %q, %t), want openai/gpt-4o", provider, model, ok)
	}
	if err := hermes.ValidateCustomProviders(rendered, signed.Plan.Body.Providers); err != nil {
		t.Fatalf("validate temp provider catalog: %v", err)
	}
	out := string(rendered)
	for _, want := range []string{"custom_providers:", "api_key: ${OPENAI_API_KEY}", "api_key: ${ANTHROPIC_API_KEY}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("temp config missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"sk-", "api_key: OPENAI_API_KEY", "api_key: ANTHROPIC_API_KEY"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("temp config leaked plaintext api_key material %q:\n%s", forbidden, out)
		}
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read live config after dry-run: %v", err)
	}
	if string(after) != string(original) {
		t.Fatal("live config was modified during dry-run catalog apply")
	}
}

func TestConfigApplyDryRunAllowsSymlinkReadOnly(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)
	linkPath := filepath.Join(srcDir, "config-link.yaml")
	if err := os.Symlink(cfgPath, linkPath); err != nil {
		t.Fatalf("symlink config: %v", err)
	}

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", linkPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err != nil {
		t.Fatalf("execute dry-run through symlink: %v", err)
	}
	if s, ok := findStep(t, result.Steps, "replaced"); !ok || s.Status != "skipped" {
		t.Errorf("replaced step = %+v, want skipped", s)
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dry-run changed symlink into a regular file")
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read target after dry-run: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("target after dry-run = %q, want original %q", after, original)
	}
}

func TestConfigApplyRejectsTamperedPlan(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}
	// Tamper after signing.
	signed.Plan.Body.Desired.Model = "tampered-model"

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected signature verification error, got nil")
	}
	s, ok := findStep(t, result.Steps, "signature_verified")
	if !ok || s.Status != "failed" {
		t.Errorf("signature_verified step = %+v, want failed", s)
	}
	if _, ok := findStep(t, result.Steps, "backup_created"); ok {
		t.Error("backup step reached despite signature failure")
	}
	if result.BackupPath != "" || result.TempPath != "" {
		t.Error("paths set despite signature failure")
	}
	// Nothing must be written: work dir empty, live config untouched.
	if n := dirEntryCount(t, workDir); n != 0 {
		t.Errorf("work dir entry count = %d, want 0", n)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(original) {
		t.Error("live config modified after rejected plan")
	}
}

func TestConfigApplyRejectsWrongKey(t *testing.T) {
	_, priv := newTestSigningKey(t)
	otherPub, _ := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: otherPub, WorkDir: t.TempDir()}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected verification error with wrong key, got nil")
	}
	if s, ok := findStep(t, result.Steps, "signature_verified"); !ok || s.Status != "failed" {
		t.Errorf("signature_verified step = %+v, want failed", s)
	}
}

func TestConfigApplyRejectsInvalidSignedPlanMetadata(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		mutate  func(*protocol.ConfigPlan)
		wantErr string
	}{
		{
			name: "target mismatch",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.TargetNodeID = "node-2"
			},
			wantErr: "does not match sidecar node",
		},
		{
			name: "schema mismatch",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.Schema = "sideplane.config-plan.v0"
			},
			wantErr: "unsupported plan schema",
		},
		{
			name: "version mismatch",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.Version = 99
			},
			wantErr: "unsupported plan version",
		},
		{
			name: "expired",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.CreatedAt = base.Add(-maxConfigPlanAge - time.Second)
			},
			wantErr: "plan expired",
		},
		{
			name: "future",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.CreatedAt = base.Add(maxConfigPlanFutureSkew + time.Second)
			},
			wantErr: "too far in the future",
		},
		{
			name: "invalid desired",
			mutate: func(plan *protocol.ConfigPlan) {
				plan.Body.Desired.Model = "gpt-5:bad"
			},
			wantErr: "invalid desired provider/model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workDir := t.TempDir()
			plan := dryRunPlan("node-1", filepath.Join(t.TempDir(), "missing.yaml"), "openai", "gpt-4o")
			plan.CreatedAt = base
			tt.mutate(&plan)
			signed, err := protocol.SignConfigPlan(plan, priv)
			if err != nil {
				t.Fatalf("sign plan: %v", err)
			}

			exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, Now: func() time.Time { return base }}
			result, err := exec.Execute(context.Background(), signed)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
			if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" {
				t.Fatalf("validated step = %+v, want failed", s)
			}
			if _, ok := findStep(t, result.Steps, "backup_created"); ok {
				t.Fatal("backup_created reached for invalid signed plan")
			}
			if result.BackupPath != "" || result.TempPath != "" {
				t.Fatalf("paths set for invalid signed plan: backup=%q temp=%q", result.BackupPath, result.TempPath)
			}
			if n := dirEntryCount(t, workDir); n != 0 {
				t.Fatalf("work dir entries = %d, want 0", n)
			}
		})
	}
}

func TestConfigApplyRejectsDuplicatePlanIDReplayBeforeMutation(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	plan := dryRunPlan("node-1", cfgPath, "openai", "gpt-4o")
	plan.CreatedAt = base
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, Now: func() time.Time { return base }}
	if _, err := exec.Execute(context.Background(), signed); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected duplicate plan rejection, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate config plan id") {
		t.Fatalf("error = %q, want duplicate plan id", err.Error())
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" {
		t.Fatalf("validated step = %+v, want failed", s)
	}
	if _, ok := findStep(t, result.Steps, "backup_created"); ok {
		t.Fatal("backup_created reached for duplicate plan replay")
	}
	if n := dirEntryCount(t, workDir); n != 1 {
		t.Fatalf("work dir entries = %d, want only the first apply run", n)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after duplicate rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("config after duplicate rejection = %q, want original %q", after, original)
	}
}

func TestConfigApplyRejectsUnsafePlanIDBeforeBackup(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	plan := dryRunPlan("node-1", cfgPath, "openai", "gpt-4o")
	plan.ID = "../escape"
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected unsafe plan ID rejection, got nil")
	}
	if !strings.Contains(err.Error(), "safe path token") {
		t.Fatalf("error = %q, want safe path token", err)
	}
	if _, ok := findStep(t, result.Steps, "backup_created"); ok {
		t.Fatal("backup_created reached for unsafe plan id")
	}
	if n := dirEntryCount(t, workDir); n != 0 {
		t.Fatalf("work dir entry count = %d, want 0", n)
	}
}

func TestConfigApplyRejectsLiveMode(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	plan := dryRunPlan("node-1", cfgPath, "openai", "gpt-4o")
	plan.Mode = protocol.ConfigPlanModeLive
	plan.Body.DryRun = false
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected live-mode rejection, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" {
		t.Errorf("validated step = %+v, want failed", s)
	}
	// Live mode must never read/back up/write anything.
	if result.BackupPath != "" || result.TempPath != "" {
		t.Error("paths set despite live-mode rejection")
	}
	if n := dirEntryCount(t, workDir); n != 0 {
		t.Errorf("work dir entry count = %d, want 0", n)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(original) {
		t.Error("live config modified despite live-mode rejection")
	}
}

func TestConfigApplyRejectsUnsupportedRuntime(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	plan := dryRunPlan("node-1", cfgPath, "openai", "gpt-4o")
	plan.Body.RuntimeType = "openclaw"
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir()}
	if _, err := exec.Execute(context.Background(), signed); err == nil {
		t.Fatal("expected unsupported runtime error, got nil")
	}
}

func TestConfigApplyLiveRejectsPathTraversalOutsideAllowedDir(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	baseDir := t.TempDir()
	allowedDir := filepath.Join(baseDir, "allowed")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(allowedDir, 0o700); err != nil {
		t.Fatalf("mkdir allowed: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	cfgPath, original := writeHermesConfig(t, outsideDir)
	traversalPath := filepath.Join(allowedDir, "..", "outside", filepath.Base(cfgPath))

	signed, err := protocol.SignConfigPlan(livePlan("node-1", traversalPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir(), AllowedConfigDirs: []string{allowedDir}, AllowLiveApply: true, Controller: &stubController{}}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected outside allowed dir rejection, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "outside allowed directories") {
		t.Fatalf("validated step = %+v, want outside allowed directories", s)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("outside config mutated: %q", after)
	}
}

func TestConfigApplyLiveRejectsSymlinkComponentBeforeMutation(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	baseDir := t.TempDir()
	targetDir := filepath.Join(baseDir, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	cfgPath, original := writeHermesConfig(t, targetDir)
	linkDir := filepath.Join(baseDir, "link")
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	linkConfigPath := filepath.Join(linkDir, filepath.Base(cfgPath))

	signed, err := protocol.SignConfigPlan(livePlan("node-1", linkConfigPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir(), AllowedConfigDirs: []string{baseDir}, AllowLiveApply: true, Controller: &stubController{}}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected symlink component rejection, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "symlink component") {
		t.Fatalf("validated step = %+v, want symlink component failure", s)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("target config mutated: %q", after)
	}
}

func TestConfigApplyRejectsNonRegularConfigPath(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	dir := t.TempDir()
	directoryPath := filepath.Join(dir, "config-dir")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	fifoPath := filepath.Join(dir, "config.fifo")
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
			signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", tt.path, "openai", "gpt-4o"), priv)
			if err != nil {
				t.Fatalf("sign plan: %v", err)
			}

			exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir()}
			result, err := exec.Execute(context.Background(), signed)
			if err == nil {
				t.Fatal("expected non-regular config path rejection, got nil")
			}
			if s, ok := findStep(t, result.Steps, "backup_created"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "not a regular file") {
				t.Fatalf("backup_created step = %+v, want not regular failure", s)
			}
		})
	}
}

func TestConfigApplyMissingConfigPath(t *testing.T) {
	pub, priv := newTestSigningKey(t)

	plan := dryRunPlan("node-1", "", "openai", "gpt-4o")
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir()}
	if _, err := exec.Execute(context.Background(), signed); err == nil {
		t.Fatal("expected missing config path error, got nil")
	}
}

func TestConfigApplyReadConfigError(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	missing := filepath.Join(srcDir, "does-not-exist.json")

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", missing, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir()}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
	if s, ok := findStep(t, result.Steps, "backup_created"); !ok || s.Status != "failed" {
		t.Errorf("backup_created step = %+v, want failed", s)
	}
}

func TestConfigApplyRejectsEmptyDesired(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	plan := dryRunPlan("node-1", cfgPath, "openai", "")
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir()}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected error for empty desired model, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" {
		t.Errorf("validated step = %+v, want failed", s)
	}
}

func TestConfigApplyBackupDirectoryPermissionFailureStopsBeforeWrite(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)
	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{
		NodeID:    "node-1",
		PublicKey: pub,
		WorkDir:   t.TempDir(),
		MkdirAll: func(string, os.FileMode) error {
			return os.ErrPermission
		},
	}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected backup directory permission error, got nil")
	}
	if s, ok := findStep(t, result.Steps, "backup_created"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "permission") {
		t.Fatalf("backup_created step = %+v, want permission failure", s)
	}
	if result.BackupPath != "" || result.TempPath != "" {
		t.Fatalf("paths set after backup permission failure: backup=%q temp=%q", result.BackupPath, result.TempPath)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after failure: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("config mutated after backup permission failure: %q", after)
	}
}

func TestExecuteConfigApplyJobCompletes(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(dryRunPlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed plan: %v", err)
	}

	p := &JobPoller{nodeID: "node-1", publicKey: pub, applyWorkDir: t.TempDir()}
	res := p.executeConfigApply(context.Background(), &protocol.Job{Type: protocol.JobTypeConfigApply, PayloadJSON: string(payload)})
	if res.Status != protocol.JobStatusCompleted {
		t.Fatalf("status = %q, want completed (err=%s)", res.Status, res.Error)
	}
	var decoded protocol.ConfigApplyResult
	if err := json.Unmarshal([]byte(res.ResultJSON), &decoded); err != nil {
		t.Fatalf("decode result json: %v", err)
	}
	if s, ok := findStep(t, decoded.Steps, "validated"); !ok || s.Status != "completed" {
		t.Errorf("validated step = %+v, want completed", s)
	}
}

func TestExecuteConfigApplyJobBadPayload(t *testing.T) {
	p := &JobPoller{}
	res := p.executeConfigApply(context.Background(), &protocol.Job{Type: protocol.JobTypeConfigApply, PayloadJSON: "not json"})
	if res.Status != protocol.JobStatusFailed {
		t.Errorf("status = %q, want failed", res.Status)
	}
}

func TestConfigApplyLiveReplacesConfig(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.DryRun {
		t.Error("result.DryRun = true for a live plan")
	}
	for _, name := range []string{"replaced", "restarted", "health_checked"} {
		if s, ok := findStep(t, result.Steps, name); !ok || s.Status != "completed" {
			t.Errorf("%s step = %+v, want completed", name, s)
		}
	}
	if controller.restarts != 1 || controller.healths != 1 {
		t.Errorf("controller calls: restarts=%d healths=%d, want 1/1", controller.restarts, controller.healths)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	provider, model, ok := hermes.ModelFields(after)
	if !ok || provider != "openai" || model != "gpt-4o" {
		t.Errorf("live config model fields = (%q, %q, %t), want (openai, gpt-4o, true)", provider, model, ok)
	}
	if string(after) == string(original) {
		t.Error("live config unchanged; expected live replacement")
	}
	if !strings.Contains(string(after), "base_url: https://example.invalid/v1") {
		t.Error("unrelated config (base_url) not preserved during live apply")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != string(original) {
		t.Errorf("backup = %q, want original %q", backup, original)
	}
}

func TestConfigApplyLiveReplacesConfigWithProviderCatalog(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, _ := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(withProviderCatalog(livePlan("node-1", cfgPath, "openai", "gpt-4o")), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if controller.restarts != 1 || controller.healths != 1 {
		t.Fatalf("controller calls: restarts=%d healths=%d, want 1/1", controller.restarts, controller.healths)
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "completed" || !strings.Contains(s.Detail, "provider catalog") {
		t.Fatalf("validated step = %+v, want provider catalog validation", s)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	provider, model, ok := hermes.ModelFields(after)
	if !ok || provider != "openai" || model != "gpt-4o" {
		t.Fatalf("live config model fields = (%q, %q, %t), want openai/gpt-4o", provider, model, ok)
	}
	if err := hermes.ValidateCustomProviders(after, signed.Plan.Body.Providers); err != nil {
		t.Fatalf("validate live provider catalog: %v", err)
	}
	out := string(after)
	if !strings.Contains(out, "api_key: ${OPENAI_API_KEY}") || strings.Contains(out, "api_key: OPENAI_API_KEY") {
		t.Fatalf("live config did not render env-only provider catalog:\n%s", out)
	}
}

func TestConfigApplyLiveRollsBackOnRestartFailure(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{restartErr: errors.New("simulated restart failure")}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected restart failure, got nil")
	}
	if s, ok := findStep(t, result.Steps, "replaced"); !ok || s.Status != "completed" {
		t.Errorf("replaced step = %+v, want completed before the restart failure", s)
	}
	if s, ok := findStep(t, result.Steps, "restarted"); !ok || s.Status != "failed" {
		t.Errorf("restarted step = %+v, want failed", s)
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Errorf("rolled_back step = %+v, want completed", s)
	}
	if controller.healths != 0 {
		t.Errorf("health check ran %d times after restart failure, want 0", controller.healths)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("config after rollback = %q, want original %q (byte-for-byte restore)", after, original)
	}
}

func TestConfigApplyLiveRollbackRestoresOriginalContentAndMode(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)
	if err := os.Chmod(cfgPath, 0o640); err != nil {
		t.Fatalf("chmod config: %v", err)
	}

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{restartErr: errors.New("simulated restart failure")}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected restart failure, got nil")
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Fatalf("rolled_back step = %+v, want completed", s)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rollback: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("config after rollback = %q, want original %q", after, original)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config after rollback: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode after rollback = %v, want 0640", info.Mode().Perm())
	}
}

func TestConfigApplyLiveRollsBackOnHealthFailure(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{healthErr: errors.New("unhealthy after restart")}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected health-check failure, got nil")
	}
	if s, ok := findStep(t, result.Steps, "restarted"); !ok || s.Status != "completed" {
		t.Errorf("restarted step = %+v, want completed", s)
	}
	if s, ok := findStep(t, result.Steps, "health_checked"); !ok || s.Status != "failed" {
		t.Errorf("health_checked step = %+v, want failed", s)
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Errorf("rolled_back step = %+v, want completed", s)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(original) {
		t.Errorf("config after rollback = %q, want original %q", after, original)
	}
}

func TestConfigApplyLiveProviderCatalogRollsBackOnHealthFailure(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(withProviderCatalog(livePlan("node-1", cfgPath, "openai", "gpt-4o")), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{healthErr: errors.New("unhealthy after catalog apply")}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected health-check failure, got nil")
	}
	if s, ok := findStep(t, result.Steps, "replaced"); !ok || s.Status != "completed" {
		t.Fatalf("replaced step = %+v, want completed before health failure", s)
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Fatalf("rolled_back step = %+v, want completed", s)
	}
	temp, err := os.ReadFile(result.TempPath)
	if err != nil {
		t.Fatalf("read temp config: %v", err)
	}
	if err := hermes.ValidateCustomProviders(temp, signed.Plan.Body.Providers); err != nil {
		t.Fatalf("temp provider catalog was not rendered before live failure: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rollback: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("config after rollback = %q, want original %q", after, original)
	}
}

func TestConfigApplyLiveRollsBackWhenReplaceMutatesThenFails(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{}
	exec := ConfigApplyExecutor{
		NodeID:         "node-1",
		PublicKey:      pub,
		WorkDir:        workDir,
		AllowLiveApply: true,
		Controller:     controller,
		replaceFile: func(path string, contents []byte, orig os.FileInfo) error {
			if err := atomicReplaceFile(path, contents, orig); err != nil {
				return err
			}
			mutated, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read mutated config: %v", err)
			}
			if string(mutated) == string(original) {
				t.Fatal("test hook did not mutate the live config before failing")
			}
			return mutatedConfigError{err: errors.New("simulated metadata failure after rename")}
		},
	}

	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected replace failure, got nil")
	}
	if s, ok := findStep(t, result.Steps, "replaced"); !ok || s.Status != "failed" {
		t.Errorf("replaced step = %+v, want failed", s)
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Errorf("rolled_back step = %+v, want completed", s)
	}
	if controller.restarts != 0 || controller.healths != 0 {
		t.Errorf("controller calls after failed replace: restarts=%d healths=%d, want 0/0", controller.restarts, controller.healths)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rollback: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("config after rollback = %q, want original %q", after, original)
	}
}

func TestConfigApplyLiveRejectsSymlinkBeforeMutationPreservesTopology(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)
	linkPath := filepath.Join(srcDir, "config-link.yaml")
	if err := os.Symlink(cfgPath, linkPath); err != nil {
		t.Fatalf("symlink config: %v", err)
	}

	signed, err := protocol.SignConfigPlan(livePlan("node-1", linkPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: &stubController{}}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected symlink rejection, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "symlink") {
		t.Errorf("validated step = %+v, want symlink failure", s)
	}
	if _, ok := findStep(t, result.Steps, "backup_created"); ok {
		t.Error("backup step reached despite symlink live path rejection")
	}
	if _, ok := findStep(t, result.Steps, "rolled_back"); ok {
		t.Error("rollback should not run because symlink path is rejected before mutation")
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("live rejection changed symlink into a regular file")
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != cfgPath {
		t.Fatalf("symlink target = %q, want %q", target, cfgPath)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read target after rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("target after live rejection = %q, want original %q", after, original)
	}
}

func TestConfigApplyLiveNilControllerFailsBeforeMutation(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected nil controller rejection, got nil")
	}
	if s, ok := findStep(t, result.Steps, "validated"); !ok || s.Status != "failed" || !strings.Contains(s.Detail, "controller") {
		t.Errorf("validated step = %+v, want controller failure", s)
	}
	if _, ok := findStep(t, result.Steps, "backup_created"); ok {
		t.Error("backup step reached despite nil controller rejection")
	}
	if result.BackupPath != "" || result.TempPath != "" {
		t.Fatalf("paths set despite pre-mutation failure: backup=%q temp=%q", result.BackupPath, result.TempPath)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("config after nil controller rejection = %q, want original %q", after, original)
	}
	if n := dirEntryCount(t, workDir); n != 0 {
		t.Errorf("work dir entry count = %d, want 0", n)
	}
}

func TestConfigApplyLiveNoReplaceOnInvalidDesired(t *testing.T) {
	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)

	plan := livePlan("node-1", cfgPath, "openai", "") // empty model fails render/validate
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: t.TempDir(), AllowLiveApply: true, Controller: &stubController{}}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected render/validate failure, got nil")
	}
	if _, ok := findStep(t, result.Steps, "replaced"); ok {
		t.Error("replaced step reached despite invalid desired config")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(original) {
		t.Error("live config modified despite failed validation")
	}
}

func TestAtomicReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := atomicReplaceFile(path, []byte("new-contents"), nil); err != nil {
		t.Fatalf("atomic replace: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new-contents" {
		t.Errorf("contents = %q, want %q", got, "new-contents")
	}
	if n := dirEntryCount(t, dir); n != 1 {
		t.Errorf("dir entry count = %d, want 1 (no temp leftovers)", n)
	}
}

func TestAtomicReplaceFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := atomicReplaceFile(path, []byte("new"), info); err != nil {
		t.Fatalf("atomic replace: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if after.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 (original preserved)", after.Mode().Perm())
	}
}

func TestLiveApplyPreservesOwnerGroupAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify chown-based owner/group preservation")
	}

	targetUID, targetGID := 1, 1
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chown(path, targetUID, targetGID); err != nil {
		t.Skipf("environment cannot change test file ownership to %d:%d: %v", targetUID, targetGID, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after chown: %v", err)
	}
	uid, gid, ok := fileOwner(info)
	if !ok {
		t.Skip("environment does not expose file uid/gid for ownership assertions")
	}
	if uid != targetUID || gid != targetGID {
		t.Skipf("environment did not apply requested ownership: got %d:%d, want %d:%d", uid, gid, targetUID, targetGID)
	}

	if err := atomicReplaceFile(path, []byte("new"), info); err != nil {
		t.Fatalf("atomic replace: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after atomic replace: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("contents after atomic replace = %q, want new", got)
	}
	if uid, gid := mustFileOwner(t, path); uid != targetUID || gid != targetGID {
		t.Fatalf("owner after atomic replace = %d:%d, want %d:%d", uid, gid, targetUID, targetGID)
	}

	pub, priv := newTestSigningKey(t)
	srcDir := t.TempDir()
	workDir := t.TempDir()
	cfgPath, original := writeHermesConfig(t, srcDir)
	if err := os.Chown(cfgPath, targetUID, targetGID); err != nil {
		t.Skipf("environment cannot change config ownership to %d:%d: %v", targetUID, targetGID, err)
	}
	signed, err := protocol.SignConfigPlan(livePlan("node-1", cfgPath, "openai", "gpt-4o"), priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	controller := &stubController{restartErr: errors.New("simulated restart failure")}
	exec := ConfigApplyExecutor{NodeID: "node-1", PublicKey: pub, WorkDir: workDir, AllowLiveApply: true, Controller: controller}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected restart failure rollback, got nil")
	}
	if s, ok := findStep(t, result.Steps, "rolled_back"); !ok || s.Status != "completed" {
		t.Fatalf("rolled_back step = %+v, want completed", s)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after rollback: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("config after rollback = %q, want original %q", after, original)
	}
	if uid, gid := mustFileOwner(t, cfgPath); uid != targetUID || gid != targetGID {
		t.Fatalf("owner after rollback = %d:%d, want %d:%d", uid, gid, targetUID, targetGID)
	}
}

func mustFileOwner(t *testing.T, path string) (int, int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	uid, gid, ok := fileOwner(info)
	if !ok {
		t.Skip("environment does not expose file uid/gid for ownership assertions")
	}
	return uid, gid
}

func TestVerifyWritten(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("abc"), nil }
	if err := verifyWritten(read, "p", []byte("abc")); err != nil {
		t.Errorf("equal bytes rejected: %v", err)
	}
	if err := verifyWritten(read, "p", []byte("different")); err == nil {
		t.Error("hash mismatch accepted")
	}
}

func TestPruneApplyRuns(t *testing.T) {
	workDir := t.TempDir()
	names := []string{
		"plan-20240101T000000Z",
		"plan-20240102T000000Z",
		"plan-20240103T000000Z",
		"plan-20240104T000000Z",
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(workDir, n), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	pruneApplyRuns(workDir, 2)
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("kept %d dirs, want 2", len(entries))
	}
	kept := map[string]bool{}
	for _, e := range entries {
		kept[e.Name()] = true
	}
	if !kept["plan-20240103T000000Z"] || !kept["plan-20240104T000000Z"] {
		t.Errorf("kept = %v, want the newest two", kept)
	}
}
