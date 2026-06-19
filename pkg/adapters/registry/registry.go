package registry

import (
	"context"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// Registry aggregates multiple RuntimeAdapters and collects runtime statuses
// for a heartbeat or status probe.
type Registry struct {
	adapters []adapters.RuntimeAdapter
}

// New builds a Registry from the provided adapters.
func New(adapters ...adapters.RuntimeAdapter) *Registry {
	return &Registry{adapters: adapters}
}

// CollectStatuses runs Detect+Status on each registered adapter and returns
// the slice of RuntimeStatus values to include in a heartbeat. Adapters that
// are not detected are omitted. Adapter errors are surfaced as
// RuntimeStatus with State="error" rather than failing the whole collection.
func (r *Registry) CollectStatuses(ctx context.Context) []protocol.RuntimeStatus {
	var out []protocol.RuntimeStatus
	for _, a := range r.adapters {
		present, err := a.Detect(ctx)
		if err != nil {
			out = append(out, adapters.StatusFromError(a.Name(), a.Type(), err))
			continue
		}
		if !present {
			continue
		}
		status, err := a.Status(ctx)
		if err != nil {
			out = append(out, adapters.StatusFromError(a.Name(), a.Type(), err))
			continue
		}
		if status.Name != "" || status.Type != "" {
			status.Warnings = appendRuntimeWarnings(status.Warnings, r.configSnapshotWarnings(ctx, a, status)...)
			out = append(out, status)
		}
	}
	return out
}

func (r *Registry) configSnapshotWarnings(ctx context.Context, a adapters.RuntimeAdapter, status protocol.RuntimeStatus) []string {
	snapshots, err := a.ConfigSnapshots(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, snapshot := range snapshots {
		if snapshot.RuntimeName != "" && status.Name != "" && snapshot.RuntimeName != status.Name {
			continue
		}
		if snapshot.RuntimeType != "" && status.Type != "" && snapshot.RuntimeType != status.Type {
			continue
		}
		warnings = append(warnings, snapshot.Warnings...)
	}
	return warnings
}

func appendRuntimeWarnings(existing []string, next ...string) []string {
	seen := map[string]struct{}{}
	out := append([]string(nil), existing...)
	for _, warning := range out {
		seen[warning] = struct{}{}
	}
	for _, warning := range next {
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		out = append(out, warning)
	}
	return out
}

// CollectConfigSnapshots runs Detect+ConfigSnapshots on each registered adapter
// and returns read-only, redacted snapshots. Missing runtimes are omitted.
func (r *Registry) CollectConfigSnapshots(ctx context.Context) []protocol.RuntimeConfigSnapshot {
	var out []protocol.RuntimeConfigSnapshot
	for _, a := range r.adapters {
		present, err := a.Detect(ctx)
		if err != nil {
			out = append(out, adapters.ConfigSnapshotFromError(a.Name(), a.Type(), err))
			continue
		}
		if !present {
			continue
		}
		snapshots, err := a.ConfigSnapshots(ctx)
		if err != nil {
			out = append(out, adapters.ConfigSnapshotFromError(a.Name(), a.Type(), err))
			continue
		}
		out = append(out, snapshots...)
	}
	return out
}
