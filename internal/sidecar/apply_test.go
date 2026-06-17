package sidecar

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

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
		Version:      1,
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
	path = filepath.Join(dir, "hermes.json")
	contents = []byte(`{"runtime":{"provider":"anthropic","model":"claude-3.7-sonnet"}}`)
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: workDir}
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: workDir}
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

	exec := ConfigApplyExecutor{PublicKey: otherPub, WorkDir: t.TempDir()}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected verification error with wrong key, got nil")
	}
	if s, ok := findStep(t, result.Steps, "signature_verified"); !ok || s.Status != "failed" {
		t.Errorf("signature_verified step = %+v, want failed", s)
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: workDir}
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: t.TempDir()}
	if _, err := exec.Execute(context.Background(), signed); err == nil {
		t.Fatal("expected unsupported runtime error, got nil")
	}
}

func TestConfigApplyMissingConfigPath(t *testing.T) {
	pub, priv := newTestSigningKey(t)

	plan := dryRunPlan("node-1", "", "openai", "gpt-4o")
	signed, err := protocol.SignConfigPlan(plan, priv)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: t.TempDir()}
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: t.TempDir()}
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

	exec := ConfigApplyExecutor{PublicKey: pub, WorkDir: t.TempDir()}
	result, err := exec.Execute(context.Background(), signed)
	if err == nil {
		t.Fatal("expected error for empty desired model, got nil")
	}
	if s, ok := findStep(t, result.Steps, "temp_written"); !ok || s.Status != "failed" {
		t.Errorf("temp_written step = %+v, want failed", s)
	}
}

func TestValidateHermesConfigBytes(t *testing.T) {
	if err := ValidateHermesConfigBytes([]byte(`{"runtime":{"provider":"openai","model":"gpt-4o"}}`)); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	if err := ValidateHermesConfigBytes([]byte(`{"runtime":{"provider":"","model":"gpt-4o"}}`)); err == nil {
		t.Error("empty provider accepted")
	}
	if err := ValidateHermesConfigBytes([]byte(`{"runtime":{"provider":"openai","model":""}}`)); err == nil {
		t.Error("empty model accepted")
	}
	if err := ValidateHermesConfigBytes([]byte(`not json`)); err == nil {
		t.Error("invalid json accepted")
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

	p := &JobPoller{publicKey: pub, applyWorkDir: t.TempDir()}
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
