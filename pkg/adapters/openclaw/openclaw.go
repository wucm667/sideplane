package openclaw

import (
	"context"
	"os/exec"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// AdapterName is the human-readable name of the OpenClaw runtime.
const AdapterName = "openclaw"

// AdapterType is the runtime type identifier.
const AdapterType = "openclaw"

// Adapter is a lightweight runtime adapter for OpenClaw.
type Adapter struct {
	lookup func(string) (string, error)
}

// NewAdapter returns an OpenClaw runtime adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		lookup: exec.LookPath,
	}
}

// Name returns the adapter name.
func (a *Adapter) Name() string {
	return AdapterName
}

// Type returns the adapter type.
func (a *Adapter) Type() string {
	return AdapterType
}

// Detect reports whether the openclaw command is available in PATH.
// If the command is not found, it returns (false, nil) without an error.
func (a *Adapter) Detect(_ context.Context) (bool, error) {
	_, err := a.lookup("openclaw")
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Status returns a minimal RuntimeStatus for OpenClaw.
// It does not execute dangerous commands or read configuration files.
func (a *Adapter) Status(ctx context.Context) (protocol.RuntimeStatus, error) {
	present, err := a.Detect(ctx)
	if err != nil {
		return adapters.StatusFromError(AdapterName, AdapterType, err), nil
	}
	if !present {
		return protocol.RuntimeStatus{}, nil
	}
	return protocol.RuntimeStatus{
		Name:  AdapterName,
		Type:  AdapterType,
		State: "present",
	}, nil
}

// ConfigSnapshots returns read-only OpenClaw config snapshots.
// Full config discovery is intentionally deferred until real install paths are verified.
func (a *Adapter) ConfigSnapshots(ctx context.Context) ([]protocol.RuntimeConfigSnapshot, error) {
	present, err := a.Detect(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return []protocol.RuntimeConfigSnapshot{
		{
			RuntimeName: AdapterName,
			RuntimeType: AdapterType,
			Source:      "adapter",
			Warnings:    []string{"config snapshot discovery not implemented"},
		},
	}, nil
}
