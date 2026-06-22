package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestAdapterNameAndType(t *testing.T) {
	a := NewAdapter()
	if a.Name() != "hermes" {
		t.Fatalf("Name = %q, want hermes", a.Name())
	}
	if a.Type() != "hermes" {
		t.Fatalf("Type = %q, want hermes", a.Type())
	}
}

func TestAdapterImplementsInterface(t *testing.T) {
	var _ adapters.RuntimeAdapter = (*Adapter)(nil)
}

func TestAdapterDetectMissing(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }, getenv: func(string) string { return "" }}
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if present {
		t.Fatalf("Detect = true, want false")
	}
}

func TestAdapterDetectPresent(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/hermes", nil }, getenv: func(string) string { return "" }}
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if !present {
		t.Fatalf("Detect = false, want true")
	}
}

func TestAdapterDetectPresentWithConfigOnly(t *testing.T) {
	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{filepath.Join("testdata", "hermes-config.json")},
		getenv:      func(string) string { return "" },
	}
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if !present {
		t.Fatalf("Detect = false, want true for readable config")
	}
}

func TestAdapterStatusEmptyWhenMissing(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }, getenv: func(string) string { return "" }}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != "" || status.Type != "" {
		t.Fatalf("status = %+v, want empty when not detected", status)
	}
}

func TestAdapterStatusPresentWhenFound(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/hermes", nil }, getenv: func(string) string { return "" }}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != AdapterName || status.Type != AdapterType || status.State != "present" {
		t.Fatalf("status = %+v, want present hermes runtime", status)
	}
}

func TestAdapterStatusIncludesReadOnlyConfigFields(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "hermes-config.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sum := sha256.Sum256(contents)
	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{filepath.Join("testdata", "hermes-config.json")},
		getenv:      func(string) string { return "" },
	}

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Provider != "openai" {
		t.Fatalf("Provider = %q, want openai", status.Provider)
	}
	if status.Model != "gpt-5.2" {
		t.Fatalf("Model = %q, want gpt-5.2", status.Model)
	}
	if status.ConfigHash != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("ConfigHash = %q, want fixture sha256", status.ConfigHash)
	}
}

func TestAdapterConfigSnapshotsMissingRuntimeNotFatal(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }, getenv: func(string) string { return "" }}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("snapshots = %#v, want none for missing runtime", snapshots)
	}
}

func TestAdapterConfigSnapshotsPresentWarningAndNoSecrets(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/hermes", nil }, getenv: func(string) string { return "" }}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].RuntimeName != AdapterName || snapshots[0].RuntimeType != AdapterType {
		t.Fatalf("snapshot runtime = %+v, want hermes", snapshots[0])
	}
	if len(snapshots[0].Warnings) == 0 {
		t.Fatalf("snapshot warnings empty")
	}
	payload, err := json.Marshal(snapshots)
	if err != nil {
		t.Fatalf("marshal snapshots: %v", err)
	}
	if strings.Contains(strings.ToLower(string(payload)), "secret") {
		t.Fatalf("snapshot payload contains secret-like value: %s", payload)
	}
}

func TestAdapterConfigSnapshotsAllowlistUnsafeConfigValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hermes.yaml")
	contents := []byte(`
model:
  provider: openai
  default: gpt-5
key: sk-test-plain-key
openai_key: sk-test-openai-key
anthropic_key: sk-test-anthropic-key
xai_key: xai-test-key
base_url: https://user:pass@example.test/v1
authorization: Bearer sk-test-bearer-token
session: api-token-test-value
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{path},
		getenv:      func(string) string { return "" },
	}
	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].Provider != "openai" || snapshots[0].Model != "gpt-5" {
		t.Fatalf("snapshot provider/model = %q/%q, want openai/gpt-5", snapshots[0].Provider, snapshots[0].Model)
	}

	payload, err := json.Marshal(protocol.DeepProbeResult{ConfigSnapshots: snapshots})
	if err != nil {
		t.Fatalf("marshal probe result: %v", err)
	}
	for _, forbidden := range []string{
		`"key"`,
		"openai_key",
		"anthropic_key",
		"xai_key",
		"base_url",
		"user:pass@",
		"sk-test-plain-key",
		"sk-test-openai-key",
		"sk-test-anthropic-key",
		"xai-test-key",
		"Bearer sk-test-bearer-token",
		"api-token-test-value",
		"redactedValues",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("snapshot result JSON contains unsafe config material %q: %s", forbidden, payload)
		}
	}
}

func TestAdapterConfigSnapshotsRejectMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hermes.json")
	if err := os.WriteFile(path, []byte(`{"model":`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{path},
		getenv:      func(string) string { return "" },
	}
	_, err := a.ConfigSnapshots(context.Background())
	if err == nil {
		t.Fatal("ConfigSnapshots error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "parse hermes JSON config") {
		t.Fatalf("error = %q, want parse hermes JSON config detail", err.Error())
	}
}

func TestAdapterConfigSnapshotsRejectUnsafeProviderModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hermes.yaml")
	contents := []byte(`model:
  provider: openai#bad
  default: gpt-5
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{path},
		getenv:      func(string) string { return "" },
	}
	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil warning", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].Provider != "" || snapshots[0].Model != "" {
		t.Fatalf("provider/model = %q/%q, want blank rejected values", snapshots[0].Provider, snapshots[0].Model)
	}
	if len(snapshots[0].Warnings) == 0 || !strings.Contains(snapshots[0].Warnings[0], "provider/model rejected") {
		t.Fatalf("warnings = %#v, want provider/model rejected warning", snapshots[0].Warnings)
	}
}

func TestAdapterConfigSnapshotsParseConfiguredConfigFiles(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantProvider string
		wantModel    string
	}{
		{
			name:         "json config",
			path:         filepath.Join("testdata", "hermes-config.json"),
			wantProvider: "openai",
			wantModel:    "gpt-5.2",
		},
		{
			name:         "json log lines",
			path:         filepath.Join("testdata", "hermes-runtime.jsonl"),
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			sum := sha256.Sum256(contents)
			a := &Adapter{
				lookup:      func(string) (string, error) { return "", errors.New("not found") },
				configPaths: []string{tt.path},
				getenv:      func(string) string { return "" },
			}

			snapshots, err := a.ConfigSnapshots(context.Background())
			if err != nil {
				t.Fatalf("ConfigSnapshots error = %v, want nil", err)
			}
			if len(snapshots) != 1 {
				t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
			}
			snapshot := snapshots[0]
			if snapshot.Provider != tt.wantProvider {
				t.Fatalf("Provider = %q, want %q", snapshot.Provider, tt.wantProvider)
			}
			if snapshot.Model != tt.wantModel {
				t.Fatalf("Model = %q, want %q", snapshot.Model, tt.wantModel)
			}
			if snapshot.ConfigHash != "sha256:"+hex.EncodeToString(sum[:]) {
				t.Fatalf("ConfigHash = %q, want fixture sha256", snapshot.ConfigHash)
			}
			payload, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatalf("marshal snapshot: %v", err)
			}
			if strings.Contains(string(payload), "example-secret-do-not-use") {
				t.Fatalf("snapshot payload contains fixture secret: %s", payload)
			}
		})
	}
}

func TestAdapterConfigPathEnvironmentSearchList(t *testing.T) {
	configPath := filepath.Join("testdata", "hermes-config.json")
	a := &Adapter{
		lookup: func(string) (string, error) { return "", errors.New("not found") },
		getenv: func(key string) string {
			if key == "SIDEPLANE_HERMES_CONFIG_PATHS" {
				return filepath.Join(t.TempDir(), "missing.json") + string(os.PathListSeparator) + configPath
			}
			return ""
		},
	}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", snapshots[0].ConfigPath, configPath)
	}
}

func TestAdapterConfigSnapshotsParseDockerLogsReadOnly(t *testing.T) {
	a := &Adapter{
		lookup:    func(string) (string, error) { return "", errors.New("not found") },
		container: "hermes-agent",
		getenv:    func(string) string { return "" },
		runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "docker" {
				return nil, fmt.Errorf("unexpected command %q", name)
			}
			got := strings.Join(args, " ")
			switch got {
			case "inspect --type container hermes-agent":
				return []byte("[]"), nil
			case "logs --tail 200 hermes-agent":
				return []byte(`{"level":"info","msg":"initializing chat agent","model":"openai/gpt-5.2","token":"example-secret-do-not-use"}` + "\n"), nil
			case `inspect --format {{index .Config.Labels "com.docker.compose.config-hash"}} hermes-agent`:
				return []byte("abc123\n"), nil
			default:
				return nil, fmt.Errorf("unexpected docker args %q", got)
			}
		},
	}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Source != "docker_logs" {
		t.Fatalf("Source = %q, want docker_logs", snapshot.Source)
	}
	if snapshot.Provider != "openai" {
		t.Fatalf("Provider = %q, want openai", snapshot.Provider)
	}
	if snapshot.Model != "gpt-5.2" {
		t.Fatalf("Model = %q, want gpt-5.2", snapshot.Model)
	}
	if snapshot.ConfigHash != "sha256:abc123" {
		t.Fatalf("ConfigHash = %q, want docker label hash", snapshot.ConfigHash)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(payload), "example-secret-do-not-use") {
		t.Fatalf("snapshot payload contains fixture secret: %s", payload)
	}
}

func TestAdapterConfiguredDockerContainerIsOptionalAndAllowlisted(t *testing.T) {
	a := &Adapter{
		lookup: func(string) (string, error) { return "", errors.New("not found") },
		getenv: func(key string) string {
			if key == "SIDEPLANE_HERMES_DOCKER_CONTAINER" {
				return "hermes-agent"
			}
			return ""
		},
		runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "docker" || strings.Join(args, " ") != "inspect --type container hermes-agent" {
				return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
			}
			return []byte("[]"), nil
		},
	}

	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if !present {
		t.Fatalf("Detect = false, want true for configured container")
	}
}

func TestAdapterStatusCapturesDockerImageVersionTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes.json")
	if err := os.WriteFile(path, []byte(`{"model":{"provider":"openai","default":"gpt-5"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{path},
		container:   "hermes-agent",
		getenv:      func(string) string { return "" },
		runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "docker" || strings.Join(args, " ") != "inspect --format {{.Config.Image}} hermes-agent" {
				return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
			}
			return []byte("nousresearch/hermes-agent:v2026.4.30\n"), nil
		},
	}

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Version != "v2026.4.30" {
		t.Fatalf("Version = %q, want docker tag", status.Version)
	}
}

func TestAdapterStatusCapturesVersionCommandOutput(t *testing.T) {
	a := &Adapter{
		lookup:         func(string) (string, error) { return "/usr/bin/hermes", nil },
		versionCommand: "hermes --version",
		getenv:         func(string) string { return "" },
		runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "hermes" || strings.Join(args, " ") != "--version" {
				return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
			}
			return []byte(" v2026.5.1 \n"), nil
		},
	}

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Version != "v2026.5.1" {
		t.Fatalf("Version = %q, want trimmed command output", status.Version)
	}
	if len(status.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", status.Warnings)
	}
}

func TestAdapterStatusVersionCommandFailureWarns(t *testing.T) {
	a := &Adapter{
		lookup:         func(string) (string, error) { return "/usr/bin/hermes", nil },
		versionCommand: "hermes --version",
		getenv:         func(string) string { return "" },
		runCommand: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("exit status 1")
		},
	}

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Version != "" {
		t.Fatalf("Version = %q, want empty on failure", status.Version)
	}
	if !containsWarningFragment(status.Warnings, "hermes version command failed") {
		t.Fatalf("Warnings = %#v, want version command failure warning", status.Warnings)
	}
}

func TestAdapterStatusVersionCommandUnsetLeavesVersionEmpty(t *testing.T) {
	a := &Adapter{
		lookup:     func(string) (string, error) { return "/usr/bin/hermes", nil },
		getenv:     func(string) string { return "" },
		runCommand: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("must not run") },
	}

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Version != "" {
		t.Fatalf("Version = %q, want empty when command unset", status.Version)
	}
	if len(status.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", status.Warnings)
	}
}

func TestAdapterStatusDeploymentModeLocal(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/hermes", nil }, getenv: func(string) string { return "" }}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.DeploymentMode != protocol.DeploymentModeLocal {
		t.Fatalf("DeploymentMode = %q, want local", status.DeploymentMode)
	}
}

func TestAdapterStatusDeploymentModeSystemd(t *testing.T) {
	a := &Adapter{
		lookup:          func(string) (string, error) { return "/usr/bin/hermes", nil },
		serviceUnitName: "hermes.service",
		getenv:          func(string) string { return "" },
	}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.DeploymentMode != protocol.DeploymentModeSystemd {
		t.Fatalf("DeploymentMode = %q, want systemd", status.DeploymentMode)
	}
}

func TestAdapterStatusDeploymentModeContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes.json")
	if err := os.WriteFile(path, []byte(`{"model":{"provider":"openai","default":"gpt-5"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := &Adapter{
		lookup:          func(string) (string, error) { return "", errors.New("not found") },
		configPaths:     []string{path},
		container:       "hermes-agent",
		serviceUnitName: "hermes.service",
		getenv:          func(string) string { return "" },
		runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "docker" {
				return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
			}
			return []byte("nousresearch/hermes-agent:v2026.4.30\n"), nil
		},
	}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	// A configured container takes precedence over a configured service unit.
	if status.DeploymentMode != protocol.DeploymentModeContainer {
		t.Fatalf("DeploymentMode = %q, want container", status.DeploymentMode)
	}
}

func containsWarningFragment(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
