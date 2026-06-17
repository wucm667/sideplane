package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

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
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }}
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if present {
		t.Fatalf("Detect = true, want false")
	}
}

func TestAdapterDetectPresent(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/openclaw", nil }}
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	if !present {
		t.Fatalf("Detect = false, want true")
	}
}

func TestAdapterStatusEmptyWhenMissing(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != "" || status.Type != "" {
		t.Fatalf("status = %+v, want empty when not detected", status)
	}
}

func TestAdapterStatusPresentWhenFound(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/openclaw", nil }}
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

func TestAdapterConfigSnapshotsMissingRuntimeNotFatal(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "", errors.New("not found") }}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("snapshots = %#v, want none for missing runtime", snapshots)
	}
}

func TestAdapterConfigSnapshotsPresentWarningAndNoSecrets(t *testing.T) {
	a := &Adapter{lookup: func(string) (string, error) { return "/usr/bin/openclaw", nil }}

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
