package registry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

type fakeAdapter struct {
	name        string
	typ         string
	detected    bool
	detectErr   error
	status      protocol.RuntimeStatus
	statusErr   error
	snapshots   []protocol.RuntimeConfigSnapshot
	snapshotErr error
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Type() string { return f.typ }
func (f *fakeAdapter) Detect(_ context.Context) (bool, error) {
	return f.detected, f.detectErr
}
func (f *fakeAdapter) Status(_ context.Context) (protocol.RuntimeStatus, error) {
	return f.status, f.statusErr
}
func (f *fakeAdapter) ConfigSnapshots(_ context.Context) ([]protocol.RuntimeConfigSnapshot, error) {
	return append([]protocol.RuntimeConfigSnapshot(nil), f.snapshots...), f.snapshotErr
}

type fakeControllerAdapter struct {
	fakeAdapter
}

func (f *fakeControllerAdapter) Restart(context.Context) error {
	return nil
}

func (f *fakeControllerAdapter) HealthCheck(context.Context) error {
	return nil
}

var _ adapters.ServiceController = (*fakeControllerAdapter)(nil)

func TestRegistryReturnsServiceControllerByRuntimeType(t *testing.T) {
	hermesController := &fakeControllerAdapter{fakeAdapter: fakeAdapter{name: "hermes", typ: "hermes"}}
	openclawController := &fakeControllerAdapter{fakeAdapter: fakeAdapter{name: "openclaw", typ: "openclaw"}}
	reg := New(hermesController, openclawController)

	if got := reg.ServiceController("openclaw"); got != openclawController {
		t.Fatalf("openclaw controller = %#v, want openclaw controller", got)
	}
	if got := reg.ServiceController("hermes"); got != hermesController {
		t.Fatalf("hermes controller = %#v, want hermes controller", got)
	}
	if got := reg.ServiceController("missing"); got != nil {
		t.Fatalf("missing controller = %#v, want nil", got)
	}
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

func TestRegistryMergesConfigSnapshotWarningsIntoStatus(t *testing.T) {
	reg := New(
		&fakeAdapter{
			name:     "hermes",
			typ:      "hermes",
			detected: true,
			status: protocol.RuntimeStatus{
				Name:     "hermes",
				Type:     "hermes",
				State:    "present",
				Warnings: []string{"existing warning"},
			},
			snapshots: []protocol.RuntimeConfigSnapshot{{
				RuntimeName: "hermes",
				RuntimeType: "hermes",
				Warnings:    []string{"existing warning", "config path unreadable"},
			}},
		},
	)

	statuses := reg.CollectStatuses(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	want := []string{"existing warning", "config path unreadable"}
	if strings.Join(statuses[0].Warnings, "|") != strings.Join(want, "|") {
		t.Fatalf("warnings = %#v, want %#v", statuses[0].Warnings, want)
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

func TestRegistryCollectsConfigSnapshots(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detected: true, snapshots: []protocol.RuntimeConfigSnapshot{{RuntimeName: "hermes", RuntimeType: "hermes"}}},
		&fakeAdapter{name: "openclaw", typ: "openclaw", detected: false, snapshots: []protocol.RuntimeConfigSnapshot{{RuntimeName: "openclaw", RuntimeType: "openclaw"}}},
	)

	snapshots := reg.CollectConfigSnapshots(context.Background())
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].RuntimeName != "hermes" {
		t.Fatalf("snapshots[0].RuntimeName = %q, want hermes", snapshots[0].RuntimeName)
	}
}

func TestRegistrySurfacesConfigSnapshotError(t *testing.T) {
	reg := New(
		&fakeAdapter{name: "hermes", typ: "hermes", detected: true, snapshotErr: errors.New("snapshot failed")},
	)

	snapshots := reg.CollectConfigSnapshots(context.Background())
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].RuntimeName != "hermes" {
		t.Fatalf("snapshot name = %q, want hermes", snapshots[0].RuntimeName)
	}
	if len(snapshots[0].Warnings) != 1 || snapshots[0].Warnings[0] != "snapshot failed" {
		t.Fatalf("warnings = %#v, want snapshot failed", snapshots[0].Warnings)
	}
}

func TestRegistryImplementsConfigSnapshotCollector(t *testing.T) {
	var _ adapters.ConfigSnapshotCollector = (*Registry)(nil)
}
