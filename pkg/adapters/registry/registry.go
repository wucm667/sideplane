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
			out = append(out, status)
		}
	}
	return out
}
