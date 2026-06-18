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

// NodeRuntimeProfileKey returns the stable map key used for overrides scoped to
// one node's runtime/profile target.
func NodeRuntimeProfileKey(nodeID string, runtimeType string, profile string) string {
	nodeID = strings.TrimSpace(nodeID)
	target := RuntimeProfileKey(runtimeType, profile)
	if nodeID == "" {
		return target
	}
	if target == "" {
		return nodeID
	}
	return nodeID + "/" + target
}

// DesiredConfigWithTargetOverride returns a copy of desired with a
// node/runtime/profile override applied for the target.
func DesiredConfigWithTargetOverride(desired protocol.DesiredConfig, target EffectiveConfigTarget, override protocol.ProviderModelConfig) protocol.DesiredConfig {
	next := cloneDesiredConfig(desired)
	key := NodeRuntimeProfileKey(target.NodeID, target.RuntimeType, target.Profile)
	if key == "" {
		return next
	}
	if next.NodeRuntimeProfileOverrides == nil {
		next.NodeRuntimeProfileOverrides = map[string]protocol.ProviderModelConfig{}
	}
	next.NodeRuntimeProfileOverrides[key] = protocol.ProviderModelConfig{
		Provider: strings.TrimSpace(override.Provider),
		Model:    strings.TrimSpace(override.Model),
	}
	return next
}

// EffectiveProviderModelConfig applies MVP desired config precedence:
// global defaults -> node override -> runtime/profile override ->
// node/runtime/profile override.
func EffectiveProviderModelConfig(desired protocol.DesiredConfig, target EffectiveConfigTarget) protocol.ProviderModelConfig {
	effective := desired.Global

	if desired.NodeOverrides != nil {
		effective = mergeProviderModel(effective, desired.NodeOverrides[strings.TrimSpace(target.NodeID)])
	}

	if desired.RuntimeProfileOverrides != nil {
		key := RuntimeProfileKey(target.RuntimeType, target.Profile)
		effective = mergeProviderModel(effective, desired.RuntimeProfileOverrides[key])
	}

	if desired.NodeRuntimeProfileOverrides != nil {
		key := NodeRuntimeProfileKey(target.NodeID, target.RuntimeType, target.Profile)
		effective = mergeProviderModel(effective, desired.NodeRuntimeProfileOverrides[key])
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

func cloneDesiredConfig(desired protocol.DesiredConfig) protocol.DesiredConfig {
	clone := protocol.DesiredConfig{Global: desired.Global}
	if desired.NodeOverrides != nil {
		clone.NodeOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.NodeOverrides))
		for key, value := range desired.NodeOverrides {
			clone.NodeOverrides[key] = value
		}
	}
	if desired.RuntimeProfileOverrides != nil {
		clone.RuntimeProfileOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.RuntimeProfileOverrides))
		for key, value := range desired.RuntimeProfileOverrides {
			clone.RuntimeProfileOverrides[key] = value
		}
	}
	if desired.NodeRuntimeProfileOverrides != nil {
		clone.NodeRuntimeProfileOverrides = make(map[string]protocol.ProviderModelConfig, len(desired.NodeRuntimeProfileOverrides))
		for key, value := range desired.NodeRuntimeProfileOverrides {
			clone.NodeRuntimeProfileOverrides[key] = value
		}
	}
	return clone
}
