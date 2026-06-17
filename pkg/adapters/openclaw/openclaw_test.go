package openclaw

import (
	"context"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
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

func TestAdapterDetectDoesNotErrorWhenMissing(t *testing.T) {
	a := NewAdapter()
	present, err := a.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect error = %v, want nil", err)
	}
	// openclaw is unlikely to be on PATH in test environments.
	if present {
		t.Logf("openclaw was unexpectedly found on PATH; test environment may have it installed")
	}
}

func TestAdapterStatusReturnsEmptyWhenMissing(t *testing.T) {
	a := NewAdapter()
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status error = %v, want nil", err)
	}
	if status.Name != "" || status.Type != "" {
		t.Fatalf("status = %+v, want empty when not detected", status)
	}
}
