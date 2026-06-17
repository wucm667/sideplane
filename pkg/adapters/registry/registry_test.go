package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

type fakeAdapter struct {
	name      string
	typ       string
	detected  bool
	detectErr error
	status    protocol.RuntimeStatus
	statusErr error
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Type() string { return f.typ }
func (f *fakeAdapter) Detect(_ context.Context) (bool, error) {
	return f.detected, f.detectErr
}
func (f *fakeAdapter) Status(_ context.Context) (protocol.RuntimeStatus, error) {
	return f.status, f.statusErr
}

func TestRegistryCollectsMultipleRuntimes(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detected: true, status: protocol.RuntimeStatus{Name: "hermes", Type: "hermes", State: "present"}},
		&fakeAdapter{name: "openclaw", typ: "openclaw", detected: true, status: protocol.RuntimeStatus{Name: "openclaw", Type: "openclaw", State: "present"}},
	)

	statuses := reg.CollectStatuses(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}
	if statuses[0].Name != "hermes" {
		t.Fatalf("statuses[0].Name = %q, want hermes", statuses[0].Name)
	}
	if statuses[1].Name != "openclaw" {
		t.Fatalf("statuses[1].Name = %q, want openclaw", statuses[1].Name)
	}
}

func TestRegistryOmitsUndetectedRuntimes(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detected: false},
		&fakeAdapter{name: "openclaw", typ: "openclaw", detected: true, status: protocol.RuntimeStatus{Name: "openclaw", Type: "openclaw", State: "present"}},
	)

	statuses := reg.CollectStatuses(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Name != "openclaw" {
		t.Fatalf("statuses[0].Name = %q, want openclaw", statuses[0].Name)
	}
}

func TestRegistrySurfacesAdapterErrorWithoutFailing(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detected: true, statusErr: errors.New("status probe failed")},
		&fakeAdapter{name: "openclaw", typ: "openclaw", detected: true, status: protocol.RuntimeStatus{Name: "openclaw", Type: "openclaw", State: "present"}},
	)

	statuses := reg.CollectStatuses(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}
	if statuses[0].State != "error" {
		t.Fatalf("statuses[0].State = %q, want error", statuses[0].State)
	}
	if statuses[0].LastError != "status probe failed" {
		t.Fatalf("statuses[0].LastError = %q, want 'status probe failed'", statuses[0].LastError)
	}
	if statuses[1].State != "present" {
		t.Fatalf("statuses[1].State = %q, want present", statuses[1].State)
	}
}

func TestRegistrySurfacesDetectError(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detectErr: errors.New("detect failed")},
	)

	statuses := reg.CollectStatuses(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].State != "error" {
		t.Fatalf("statuses[0].State = %q, want error", statuses[0].State)
	}
	if statuses[0].LastError != "detect failed" {
		t.Fatalf("statuses[0].LastError = %q, want 'detect failed'", statuses[0].LastError)
	}
}

func TestRegistryImplementsRuntimeCollector(t *testing.T) {
	// Compile-time check that Registry can be used as a RuntimeCollector.
	var _ adapters.RuntimeCollector = (*Registry)(nil)
}
