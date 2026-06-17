// Package adapters defines the runtime adapter interface for discovering and
// reporting status of managed agent runtimes such as Hermes Agent and OpenClaw.
package adapters

import (
	"context"

	"github.com/wucm667/sideplane/pkg/protocol"
)

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
		Warnings:    []string{msg},
	}
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
