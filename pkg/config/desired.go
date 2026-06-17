package config

import (
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// EffectiveConfigTarget identifies the runtime/profile context for a desired config merge.
type EffectiveConfigTarget struct {
	NodeID      string
	RuntimeType string
	Profile     string
}

// RuntimeProfileKey returns the stable map key used for runtime/profile overrides.
func RuntimeProfileKey(runtimeType string, profile string) string {
	runtimeType = strings.TrimSpace(runtimeType)
	profile = strings.TrimSpace(profile)
	if runtimeType == "" {
		return profile
	}
	if profile == "" {
		return runtimeType
	}
	return runtimeType + "/" + profile
}

// EffectiveProviderModelConfig applies MVP desired config precedence:
// global defaults -> node override -> runtime/profile override.
func EffectiveProviderModelConfig(desired protocol.DesiredConfig, target EffectiveConfigTarget) protocol.ProviderModelConfig {
	effective := desired.Global

	if desired.NodeOverrides != nil {
		effective = mergeProviderModel(effective, desired.NodeOverrides[strings.TrimSpace(target.NodeID)])
	}

	if desired.RuntimeProfileOverrides != nil {
		key := RuntimeProfileKey(target.RuntimeType, target.Profile)
		effective = mergeProviderModel(effective, desired.RuntimeProfileOverrides[key])
	}

	return effective
}

func mergeProviderModel(base protocol.ProviderModelConfig, override protocol.ProviderModelConfig) protocol.ProviderModelConfig {
	if strings.TrimSpace(override.Provider) != "" {
		base.Provider = strings.TrimSpace(override.Provider)
	}
	if strings.TrimSpace(override.Model) != "" {
		base.Model = strings.TrimSpace(override.Model)
	}
	return base
}
