package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/sidecar"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
)

func TestResolveRuntimeConfigLoadsStateAndAppliesOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := sidecar.WriteState(path, sidecar.SidecarState{
		ServerURL:      "http://state-server:8080",
		NodeID:         "state-node",
		NodeCredential: "state-credential",
		EnrolledAt:     time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	cfg, err := resolveRuntimeConfig("http://flag-server:8080", "flag-node", "flag-credential", path)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	if cfg.ServerURL != "http://flag-server:8080" {
		t.Fatalf("server URL = %q, want flag override", cfg.ServerURL)
	}
	if cfg.NodeID != "flag-node" {
		t.Fatalf("node ID = %q, want flag override", cfg.NodeID)
	}
	if cfg.NodeCredential != "state-credential" {
		t.Fatalf("node credential = %q, want state credential", cfg.NodeCredential)
	}
	if cfg.StatePath != path {
		t.Fatalf("state path = %q, want %q", cfg.StatePath, path)
	}
}

func TestResolveRuntimeConfigUsesCredentialFlagWhenStateMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-state.json")

	cfg, err := resolveRuntimeConfig("http://localhost:8080", "node-flag", "flag-credential", path)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	if cfg.ServerURL != "http://localhost:8080" {
		t.Fatalf("server URL = %q, want flag server", cfg.ServerURL)
	}
	if cfg.NodeID != "node-flag" {
		t.Fatalf("node ID = %q, want flag node", cfg.NodeID)
	}
	if cfg.NodeCredential != "flag-credential" {
		t.Fatalf("node credential = %q, want flag credential", cfg.NodeCredential)
	}
}

func TestSidecarEnvFallbacksResolveWhenFlagsEmpty(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := sidecar.WriteState(statePath, sidecar.SidecarState{
		ServerURL:      "http://state-server:8080",
		NodeID:         "state-node",
		NodeCredential: "state-credential",
		EnrolledAt:     time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	t.Setenv("SIDEPLANE_SERVER_URL", "http://env-server:8080")
	t.Setenv("SIDEPLANE_NODE_ID", "env-node")
	t.Setenv("SIDEPLANE_SIDECAR_STATE", statePath)
	t.Setenv("SIDEPLANE_HEARTBEAT_INTERVAL", "45s")
	t.Setenv("SIDEPLANE_JOB_POLL_INTERVAL", "15s")
	t.Setenv("SIDEPLANE_HERMES_CONFIG_PATHS", "/etc/hermes/env.json")
	t.Setenv("SIDEPLANE_OPENCLAW_CONFIG_PATHS", "/etc/openclaw/env.json")
	t.Setenv("SIDEPLANE_HERMES_DOCKER_CONTAINER", "hermes-env")
	t.Setenv("SIDEPLANE_HERMES_SERVICE_UNIT", "hermes-env.service")
	t.Setenv("SIDEPLANE_OPENCLAW_DOCKER_CONTAINER", "openclaw-env")
	t.Setenv("SIDEPLANE_OPENCLAW_SERVICE_UNIT", "openclaw-env.service")
	t.Setenv("SIDEPLANE_SERVER_PUBLIC_KEY", "env-public-key")
	t.Setenv("SIDEPLANE_APPLY_WORK_DIR", "/var/lib/sideplane/apply")
	t.Setenv("SIDEPLANE_SERVICE_RESTART_USE_SUDO", "true")

	var serverURL, nodeID, state string
	heartbeatInterval := 30 * time.Second
	jobPollInterval := 30 * time.Second
	var hermesConfigPaths, openclawConfigPaths, hermesDockerContainer, hermesServiceUnit, openclawDockerContainer, openclawServiceUnit, serverPublicKey, applyWorkDir string
	serviceRestartUseSudo := false

	if err := applySidecarEnvFallbacks(map[string]bool{}, sidecarFlagValues{
		serverURL:               &serverURL,
		nodeID:                  &nodeID,
		statePath:               &state,
		heartbeatInterval:       &heartbeatInterval,
		jobPollInterval:         &jobPollInterval,
		hermesConfigPaths:       &hermesConfigPaths,
		openclawConfigPaths:     &openclawConfigPaths,
		hermesDockerContainer:   &hermesDockerContainer,
		hermesServiceUnit:       &hermesServiceUnit,
		openclawDockerContainer: &openclawDockerContainer,
		openclawServiceUnit:     &openclawServiceUnit,
		serverPublicKey:         &serverPublicKey,
		applyWorkDir:            &applyWorkDir,
		serviceRestartUseSudo:   &serviceRestartUseSudo,
	}); err != nil {
		t.Fatalf("apply env fallbacks: %v", err)
	}

	cfg, err := resolveRuntimeConfig(serverURL, nodeID, "", state)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	if cfg.ServerURL != "http://env-server:8080" {
		t.Fatalf("server URL = %q, want env server", cfg.ServerURL)
	}
	if cfg.NodeID != "env-node" {
		t.Fatalf("node ID = %q, want env node", cfg.NodeID)
	}
	if cfg.NodeCredential != "state-credential" {
		t.Fatalf("node credential = %q, want state credential", cfg.NodeCredential)
	}
	if cfg.StatePath != statePath {
		t.Fatalf("state path = %q, want %q", cfg.StatePath, statePath)
	}
	if heartbeatInterval != 45*time.Second {
		t.Fatalf("heartbeat interval = %s, want 45s", heartbeatInterval)
	}
	if jobPollInterval != 15*time.Second {
		t.Fatalf("job poll interval = %s, want 15s", jobPollInterval)
	}
	if hermesConfigPaths != "/etc/hermes/env.json" || openclawConfigPaths != "/etc/openclaw/env.json" {
		t.Fatalf("config paths = %q/%q, want env paths", hermesConfigPaths, openclawConfigPaths)
	}
	if hermesDockerContainer != "hermes-env" || hermesServiceUnit != "hermes-env.service" {
		t.Fatalf("hermes targets = %q/%q, want env targets", hermesDockerContainer, hermesServiceUnit)
	}
	if openclawDockerContainer != "openclaw-env" || openclawServiceUnit != "openclaw-env.service" {
		t.Fatalf("openclaw targets = %q/%q, want env targets", openclawDockerContainer, openclawServiceUnit)
	}
	if serverPublicKey != "env-public-key" || applyWorkDir != "/var/lib/sideplane/apply" {
		t.Fatalf("apply settings = %q/%q, want env values", serverPublicKey, applyWorkDir)
	}
	if !serviceRestartUseSudo {
		t.Fatal("serviceRestartUseSudo = false, want env true")
	}
}

func TestSidecarEnvFallbacksRejectInvalidRestartSudoBool(t *testing.T) {
	t.Setenv("SIDEPLANE_SERVICE_RESTART_USE_SUDO", "sometimes")
	serviceRestartUseSudo := false

	err := applySidecarEnvFallbacks(map[string]bool{}, sidecarFlagValues{
		serviceRestartUseSudo: &serviceRestartUseSudo,
	})
	if err == nil || !strings.Contains(err.Error(), "SIDEPLANE_SERVICE_RESTART_USE_SUDO") {
		t.Fatalf("error = %v, want env name in parse error", err)
	}
}

func TestRunRequiresNodeCredential(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "missing-state.json")

	code := run([]string{
		"--server", "http://localhost:8080",
		"--state", path,
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "run sideplane-sidecar enroll first") {
		t.Fatalf("stderr = %q, want enroll hint", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestDoctorReportsStateAndReadableConfigWithoutCredential(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := sidecar.WriteState(statePath, sidecar.SidecarState{
		ServerURL:      "http://state-server:8080",
		NodeID:         "state-node",
		NodeCredential: "secret-node-credential",
		EnrolledAt:     time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := filepath.Join(dir, "hermes.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"test"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	publicKey := spcrypto.PublicKeyString(keyPair.PublicKey)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"doctor",
		"--state", statePath,
		"--hermes-config-paths", configPath,
		"--apply-work-dir", filepath.Join(dir, "apply"),
		"--server-public-key", publicKey,
		"--allow-live-apply",
		"--service-restart-use-sudo",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Server URL: http://state-server:8080", "State found: yes", "Node ID: state-node", "Live apply: yes", "Service restart sudo: yes", "Public key: valid", configPath + " readable"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-node-credential") {
		t.Fatalf("doctor output leaked node credential:\n%s", output)
	}
}

func TestDoctorJSONDoesNotRequireEnrollment(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "missing-state.json")
	missingConfig := filepath.Join(dir, "missing-hermes.json")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"doctor",
		"--server", "http://localhost:8080",
		"--node-id", "node-doctor",
		"--state", statePath,
		"--hermes-config-paths", missingConfig,
		"--server-public-key", "not-a-public-key",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, stdout.String())
	}
	if report.StateFound {
		t.Fatalf("stateFound = true, want false")
	}
	if report.ServerURL != "http://localhost:8080" || report.NodeID != "node-doctor" {
		t.Fatalf("report = %#v, want server/node overrides", report)
	}
	if report.PublicKeyStatus != "invalid" {
		t.Fatalf("publicKeyStatus = %q, want invalid", report.PublicKeyStatus)
	}
	if len(report.HermesConfigPaths) != 1 || report.HermesConfigPaths[0].Readable {
		t.Fatalf("hermes config path status = %#v, want unreadable missing path", report.HermesConfigPaths)
	}
}

func TestSplitPathListAcceptsPathListCommasAndNewlines(t *testing.T) {
	raw := " /etc/hermes/config.json " + string(os.PathListSeparator) + " /opt/hermes/config.yaml,/tmp/hermes.env\n"
	paths := splitPathList(raw)
	want := []string{"/etc/hermes/config.json", "/opt/hermes/config.yaml", "/tmp/hermes.env"}
	if len(paths) != len(want) {
		t.Fatalf("len(paths) = %d, want %d: %#v", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}
