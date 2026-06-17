package hermes

import (
	"context"
	"os/exec"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// AdapterName is the human-readable name of the Hermes Agent runtime.
const AdapterName = "hermes"

// AdapterType is the runtime type identifier.
const AdapterType = "hermes"

// Adapter is a lightweight runtime adapter for Hermes Agent.
type Adapter struct{}

// NewAdapter returns a Hermes Agent runtime adapter.
func NewAdapter() *Adapter {
	return &Adapter{}
}

// Name returns the adapter name.
func (a *Adapter) Name() string {
	return AdapterName
}

// Type returns the adapter type.
func (a *Adapter) Type() string {
	return AdapterType
}

// Detect reports whether the hermes command is available in PATH.
// If the command is not found, it returns (false, nil) without an error.
func (a *Adapter) Detect(_ context.Context) (bool, error) {
	_, err := exec.LookPath("hermes")
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Status returns a minimal RuntimeStatus for Hermes Agent.
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
