// Package adapters defines the runtime adapter interface for discovering and
// reporting status of managed agent runtimes such as Hermes Agent and OpenClaw.
package adapters

import (
	"context"
	"errors"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// ErrLiveApplyDisabled is returned by mutating adapter operations when live
// apply is not enabled for the adapter. Callers should treat it as "skipped".
var ErrLiveApplyDisabled = errors.New("live apply disabled")

// ServiceController is an optional adapter capability for restarting a managed
// runtime and verifying its health after a change. Implementations must use
// allowlisted operations only and must never offer general command execution.
// Mutating operations must be a no-op returning ErrLiveApplyDisabled unless the
// adapter is explicitly permitted to perform live apply.
type ServiceController interface {
	// Restart restarts the managed runtime using an allowlisted operation.
	Restart(ctx context.Context) error
	// HealthCheck reports whether the runtime is healthy after a change.
	// It must be read-only.
	HealthCheck(ctx context.Context) error
}

// HealthChecker is an optional adapter capability for local, read-only runtime
// liveness checks. Implementations must not mutate runtime state, contact
// provider APIs, or reach external networks.
type HealthChecker interface {
	RuntimeHealth(ctx context.Context) (protocol.RuntimeHealth, error)
}

// RuntimeAdapter discovers and reports the status of a managed runtime.
type RuntimeAdapter interface {
	// Name returns the human-readable runtime name.
	Name() string

	// Type returns the runtime type identifier.
	Type() string

	// Detect returns true if the runtime appears to be installed on this node.
	// A missing installation must return (false, nil) rather than an error.
	Detect(ctx context.Context) (bool, error)

	// Status returns the current runtime status.
	// The adapter should not execute dangerous commands or mutate configuration.
	Status(ctx context.Context) (protocol.RuntimeStatus, error)

	// ConfigSnapshots returns read-only, redacted runtime configuration snapshots.
	// The adapter must not mutate runtime configuration.
	ConfigSnapshots(ctx context.Context) ([]protocol.RuntimeConfigSnapshot, error)
}

// StatusFromError builds a RuntimeStatus that surfaces an adapter error without
// breaking the caller. The runtime is marked with state "error" and the
// error text is placed in LastError.
func StatusFromError(name, typ string, err error) protocol.RuntimeStatus {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return protocol.RuntimeStatus{
		Name:      name,
		Type:      typ,
		State:     "error",
		LastError: msg,
	}
}

// ConfigSnapshotFromError builds a read-only snapshot that surfaces adapter errors
// as warnings without exposing secret values.
func ConfigSnapshotFromError(name, typ string, err error) protocol.RuntimeConfigSnapshot {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return protocol.RuntimeConfigSnapshot{
		RuntimeName: name,
		RuntimeType: typ,
		Health:      RuntimeHealthDegraded(msg),
		Warnings:    []string{msg},
	}
}

// RuntimeHealthUnknown returns an explicit unknown health result.
func RuntimeHealthUnknown(reason string) protocol.RuntimeHealth {
	return protocol.RuntimeHealth{State: protocol.RuntimeHealthUnknown, Reason: reason}
}

// RuntimeHealthDegraded returns an explicit degraded health result.
func RuntimeHealthDegraded(reason string) protocol.RuntimeHealth {
	return protocol.RuntimeHealth{State: protocol.RuntimeHealthDegraded, Reason: reason}
}

// RuntimeHealthHealthy returns an explicit healthy health result.
func RuntimeHealthHealthy(reason string) protocol.RuntimeHealth {
	return protocol.RuntimeHealth{State: protocol.RuntimeHealthHealthy, Reason: reason}
}

// RuntimeCollector is the minimal interface needed by the heartbeat client to
// gather runtime statuses for a heartbeat.
type RuntimeCollector interface {
	CollectStatuses(ctx context.Context) []protocol.RuntimeStatus
}

// ConfigSnapshotCollector gathers read-only runtime config snapshots.
type ConfigSnapshotCollector interface {
	CollectConfigSnapshots(ctx context.Context) []protocol.RuntimeConfigSnapshot
}
