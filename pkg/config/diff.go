package config

import "github.com/wucm667/sideplane/pkg/protocol"

// DiffProviderModelConfig compares actual read-only config to desired config.
func DiffProviderModelConfig(actual *protocol.RuntimeConfigSnapshot, desired protocol.ProviderModelConfig) []protocol.ConfigDiffEntry {
	if actual == nil {
		return missingActualDiff(desired)
	}

	var entries []protocol.ConfigDiffEntry
	if actual.Provider != desired.Provider {
		entries = append(entries, protocol.ConfigDiffEntry{
			Field:   "provider",
			Actual:  actual.Provider,
			Desired: desired.Provider,
			Change:  protocol.ConfigDiffChangeUpdate,
		})
	}
	if actual.Model != desired.Model {
		entries = append(entries, protocol.ConfigDiffEntry{
			Field:   "model",
			Actual:  actual.Model,
			Desired: desired.Model,
			Change:  protocol.ConfigDiffChangeUpdate,
		})
	}
	return entries
}

func missingActualDiff(desired protocol.ProviderModelConfig) []protocol.ConfigDiffEntry {
	var entries []protocol.ConfigDiffEntry
	if desired.Provider != "" {
		entries = append(entries, protocol.ConfigDiffEntry{
			Field:   "provider",
			Desired: desired.Provider,
			Change:  protocol.ConfigDiffChangeMissingActual,
		})
	}
	if desired.Model != "" {
		entries = append(entries, protocol.ConfigDiffEntry{
			Field:   "model",
			Desired: desired.Model,
			Change:  protocol.ConfigDiffChangeMissingActual,
		})
	}
	return entries
}
