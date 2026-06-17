package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	want := protocol.RuntimeStatus{
		Name:  AdapterName,
		Type:  AdapterType,
		State: "present",
	}
	if status != want {
		t.Fatalf("status = %+v, want %+v", status, want)
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
	if len(snapshots[0].RedactedValues) != 0 {
		t.Fatalf("redacted values = %#v, want empty placeholder snapshot", snapshots[0].RedactedValues)
	}
	payload, err := json.Marshal(snapshots)
	if err != nil {
		t.Fatalf("marshal snapshots: %v", err)
	}
	if strings.Contains(strings.ToLower(string(payload)), "secret") {
		t.Fatalf("snapshot payload contains secret-like value: %s", payload)
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
