package openclaw

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
)

func newTestAdapter(paths ...string) *Adapter {
	a := NewAdapter(WithConfigPaths(paths...))
	a.lookup = func(string) (string, error) { return "/usr/bin/openclaw", nil }
	a.getenv = func(string) string { return "" }
	a.defaultConfigPaths = []string{}
	return a
}

func newMissingTestAdapter() *Adapter {
	a := NewAdapter()
	a.lookup = func(string) (string, error) { return "", errors.New("not found") }
	a.getenv = func(string) string { return "" }
	a.defaultConfigPaths = []string{}
	return a
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openclaw.conf")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestAdapterNameAndType(t *testing.T) {
	a := NewAdapter()
	if a.Name() != "openclaw" {
		t.Fatalf("Name = %q, want openclaw", a.Name())
	}
	if a.Type() != "openclaw" {
		t.Fatalf("Type = %q, want openclaw", a.Type())
	}
}

func TestAdapterImplementsInterface(t *testing.T) {
	var _ adapters.RuntimeAdapter = (*Adapter)(nil)
}

func TestAdapterDetectMissing(t *testing.T) {
	a := newMissingTestAdapter()
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if present {
		t.Fatalf("Detect = true, want false")
	}
}

func TestAdapterDetectPresent(t *testing.T) {
	a := newTestAdapter()
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if !present {
		t.Fatalf("Detect = false, want true")
	}
}

func TestAdapterStatusEmptyWhenMissing(t *testing.T) {
	a := newMissingTestAdapter()
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != "" || status.Type != "" {
		t.Fatalf("status = %+v, want empty when not detected", status)
	}
}

func TestAdapterStatusPresentWhenFound(t *testing.T) {
	a := newTestAdapter()
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != AdapterName || status.Type != AdapterType || status.State != "present" {
		t.Fatalf("status = %+v, want present openclaw runtime", status)
	}
}

func TestAdapterConfigSnapshotsMissingRuntimeNotFatal(t *testing.T) {
	a := newMissingTestAdapter()

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("snapshots = %#v, want none for missing runtime", snapshots)
	}
}

func TestAdapterConfigSnapshotFileHashPathProviderModelAndNoSecrets(t *testing.T) {
	contents := `{
  "provider": "openai",
  "model": "gpt-4o",
  "api_key": "sk-test-secret"
}`
	path := writeConfig(t, contents)
	a := newTestAdapter(path)

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].RuntimeName != AdapterName || snapshots[0].RuntimeType != AdapterType {
		t.Fatalf("snapshot runtime = %+v, want openclaw", snapshots[0])
	}
	if snapshots[0].ConfigPath != path {
		t.Fatalf("config path = %q, want %q", snapshots[0].ConfigPath, path)
	}
	sum := sha256.Sum256([]byte(contents))
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if snapshots[0].ConfigHash != wantHash {
		t.Fatalf("config hash = %q, want %q", snapshots[0].ConfigHash, wantHash)
	}
	if snapshots[0].Provider != "openai" || snapshots[0].Model != "gpt-4o" {
		t.Fatalf("provider/model = %q/%q, want openai/gpt-4o", snapshots[0].Provider, snapshots[0].Model)
	}
	if len(snapshots[0].Warnings) != 0 {
		t.Fatalf("snapshot warnings = %#v, want none", snapshots[0].Warnings)
	}
	payload, err := json.Marshal(snapshots)
	if err != nil {
		t.Fatalf("marshal snapshots: %v", err)
	}
	if strings.Contains(string(payload), "sk-test-secret") {
		t.Fatalf("snapshot payload contains secret-like value: %s", payload)
	}
}

func TestAdapterConfigSnapshotMissingProviderModelWarning(t *testing.T) {
	path := writeConfig(t, `{"api_key":"sk-test-secret","notes":"no runtime selection"}`)
	a := newTestAdapter(path)

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].Provider != "" || snapshots[0].Model != "" {
		t.Fatalf("provider/model = %q/%q, want empty", snapshots[0].Provider, snapshots[0].Model)
	}
	if !containsWarning(snapshots[0].Warnings, "provider/model not found in openclaw config") {
		t.Fatalf("warnings = %#v, want provider/model warning", snapshots[0].Warnings)
	}
}

func TestAdapterConfigSnapshotsParseFixtureFiles(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantProvider string
		wantModel    string
	}{
		{
			name:         "valid",
			path:         filepath.Join("testdata", "openclaw-valid.json"),
			wantProvider: "openai",
			wantModel:    "gpt-5",
		},
		{
			name:         "unknown fields",
			path:         filepath.Join("testdata", "openclaw-unknown-fields.json"),
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAdapter(tt.path)
			snapshots, err := a.ConfigSnapshots(context.Background())
			if err != nil {
				t.Fatalf("ConfigSnapshots error = %v, want nil", err)
			}
			if len(snapshots) != 1 {
				t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
			}
			if snapshots[0].Provider != tt.wantProvider || snapshots[0].Model != tt.wantModel {
				t.Fatalf("provider/model = %q/%q, want %s/%s", snapshots[0].Provider, snapshots[0].Model, tt.wantProvider, tt.wantModel)
			}
			if len(snapshots[0].Warnings) != 0 {
				t.Fatalf("warnings = %#v, want none", snapshots[0].Warnings)
			}
		})
	}
}

func TestAdapterConfigSnapshotsMissingModelWarning(t *testing.T) {
	a := newTestAdapter(filepath.Join("testdata", "openclaw-missing-model.json"))

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].Provider != "" || snapshots[0].Model != "" {
		t.Fatalf("provider/model = %q/%q, want empty incomplete values", snapshots[0].Provider, snapshots[0].Model)
	}
	if !containsWarning(snapshots[0].Warnings, "provider/model incomplete in openclaw config") {
		t.Fatalf("warnings = %#v, want incomplete provider/model warning", snapshots[0].Warnings)
	}
}

func TestAdapterConfigSnapshotsRejectMalformedJSON(t *testing.T) {
	a := newTestAdapter(filepath.Join("testdata", "openclaw-malformed.json"))

	_, err := a.ConfigSnapshots(context.Background())
	if err == nil {
		t.Fatal("ConfigSnapshots error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "parse openclaw JSON config") {
		t.Fatalf("error = %q, want parse openclaw JSON config detail", err.Error())
	}
}

func TestAdapterConfigSnapshotsRejectUnsafeProviderModel(t *testing.T) {
	path := writeConfig(t, `{"provider":"openai#bad","model":"gpt-5"}`)
	a := newTestAdapter(path)

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
	if !containsWarningFragment(snapshots[0].Warnings, "provider/model rejected in openclaw config") {
		t.Fatalf("warnings = %#v, want provider/model rejected warning", snapshots[0].Warnings)
	}
}

func TestAdapterConfigSnapshotsPresentWarningWhenNoConfig(t *testing.T) {
	a := newTestAdapter(filepath.Join(t.TempDir(), "missing.conf"))

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if len(snapshots[0].Warnings) == 0 {
		t.Fatalf("snapshot warnings empty")
	}
	warningText := strings.Join(snapshots[0].Warnings, " ")
	if strings.Contains(warningText, "not implemented") {
		t.Fatalf("warning still uses old stub text: %#v", snapshots[0].Warnings)
	}
	if !strings.Contains(warningText, "config file not found") {
		t.Fatalf("warnings = %#v, want config file not found", snapshots[0].Warnings)
	}
}

func TestAdapterStatusUsesConfigSnapshot(t *testing.T) {
	contents := `provider = anthropic
model = claude-3-7-sonnet`
	path := writeConfig(t, contents)
	a := newTestAdapter(path)

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	sum := sha256.Sum256([]byte(contents))
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if status.Provider != "anthropic" || status.Model != "claude-3-7-sonnet" || status.ConfigHash != wantHash {
		t.Fatalf("status = %+v, want provider/model/hash from snapshot", status)
	}
}

func TestAdapterConfigPathFromEnvironment(t *testing.T) {
	path := writeConfig(t, `{"provider":"openai","model":"gpt-4o"}`)
	a := newTestAdapter()
	a.configPaths = nil
	a.getenv = func(key string) string {
		if key == "SIDEPLANE_OPENCLAW_CONFIG_PATHS" {
			return path
		}
		return ""
	}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 || snapshots[0].ConfigPath != path {
		t.Fatalf("snapshots = %#v, want env config path %q", snapshots, path)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}

func containsWarningFragment(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
