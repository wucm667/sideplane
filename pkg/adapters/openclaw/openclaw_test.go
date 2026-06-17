package openclaw

import (
	"context"
	"errors"
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
