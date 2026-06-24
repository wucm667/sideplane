package config

import (
	"slices"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// EffectiveConfigTarget identifies the runtime/profile context for a desired config merge.
type EffectiveConfigTarget struct {
	NodeID      string
	RuntimeType string
	Profile     string
}

// ProviderScope identifies the desired provider-catalog layer to mutate.
type ProviderScope struct {
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

// UpsertProviderDefinition returns a copy of desired with provider inserted or
// replaced by case-insensitive name in the layer derived from scope.
func UpsertProviderDefinition(desired protocol.DesiredConfig, scope ProviderScope, provider protocol.ProviderDefinition) protocol.DesiredConfig {
	next := cloneDesiredConfig(desired)
	normalizedProvider := normalizeProviderDefinition(provider)
	scope = normalizeProviderScope(scope)
	switch providerScopeLayer(scope) {
	case providerLayerGlobal:
		next.GlobalProviders = upsertProviderDefinition(next.GlobalProviders, normalizedProvider)
	case providerLayerNode:
		if next.NodeProviders == nil {
			next.NodeProviders = map[string][]protocol.ProviderDefinition{}
		}
		next.NodeProviders[scope.NodeID] = upsertProviderDefinition(next.NodeProviders[scope.NodeID], normalizedProvider)
	case providerLayerRuntimeProfile:
		key := RuntimeProfileKey(scope.RuntimeType, scope.Profile)
		if next.RuntimeProfileProviders == nil {
			next.RuntimeProfileProviders = map[string][]protocol.ProviderDefinition{}
		}
		next.RuntimeProfileProviders[key] = upsertProviderDefinition(next.RuntimeProfileProviders[key], normalizedProvider)
	case providerLayerNodeRuntimeProfile:
		key := NodeRuntimeProfileKey(scope.NodeID, scope.RuntimeType, scope.Profile)
		if next.NodeRuntimeProfileProviders == nil {
			next.NodeRuntimeProfileProviders = map[string][]protocol.ProviderDefinition{}
		}
		next.NodeRuntimeProfileProviders[key] = upsertProviderDefinition(next.NodeRuntimeProfileProviders[key], normalizedProvider)
	}
	return next
}

// RemoveProviderDefinition returns a copy of desired with the named provider
// removed from the layer derived from scope. The bool reports whether an entry
// was removed.
func RemoveProviderDefinition(desired protocol.DesiredConfig, scope ProviderScope, name string) (protocol.DesiredConfig, bool) {
	next := cloneDesiredConfig(desired)
	scope = normalizeProviderScope(scope)
	switch providerScopeLayer(scope) {
	case providerLayerGlobal:
		var removed bool
		next.GlobalProviders, removed = removeProviderDefinition(next.GlobalProviders, name)
		return next, removed
	case providerLayerNode:
		return removeProviderDefinitionFromMap(next, providerLayerNode, scope.NodeID, name)
	case providerLayerRuntimeProfile:
		return removeProviderDefinitionFromMap(next, providerLayerRuntimeProfile, RuntimeProfileKey(scope.RuntimeType, scope.Profile), name)
	case providerLayerNodeRuntimeProfile:
		return removeProviderDefinitionFromMap(next, providerLayerNodeRuntimeProfile, NodeRuntimeProfileKey(scope.NodeID, scope.RuntimeType, scope.Profile), name)
	default:
		return next, false
	}
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

// EffectiveProviderCatalog applies provider-catalog precedence:
// global defaults -> node catalog -> runtime/profile catalog ->
// node/runtime/profile catalog.
func EffectiveProviderCatalog(desired protocol.DesiredConfig, target EffectiveConfigTarget) []protocol.ProviderDefinition {
	merged := []protocol.ProviderDefinition{}
	positions := map[string]int{}

	mergeProviderDefinitions(&merged, positions, desired.GlobalProviders)

	if desired.NodeProviders != nil {
		mergeProviderDefinitions(&merged, positions, desired.NodeProviders[strings.TrimSpace(target.NodeID)])
	}

	if desired.RuntimeProfileProviders != nil {
		key := RuntimeProfileKey(target.RuntimeType, target.Profile)
		mergeProviderDefinitions(&merged, positions, desired.RuntimeProfileProviders[key])
	}

	if desired.NodeRuntimeProfileProviders != nil {
		key := NodeRuntimeProfileKey(target.NodeID, target.RuntimeType, target.Profile)
		mergeProviderDefinitions(&merged, positions, desired.NodeRuntimeProfileProviders[key])
	}

	slices.SortFunc(merged, func(a, b protocol.ProviderDefinition) int {
		aName := strings.ToLower(a.Name)
		bName := strings.ToLower(b.Name)
		if cmp := strings.Compare(aName, bName); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	return merged
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

func mergeProviderDefinitions(merged *[]protocol.ProviderDefinition, positions map[string]int, providers []protocol.ProviderDefinition) {
	for _, provider := range providers {
		normalized := normalizeProviderDefinition(provider)
		key := strings.ToLower(normalized.Name)
		if index, ok := positions[key]; ok {
			(*merged)[index] = normalized
			continue
		}
		positions[key] = len(*merged)
		*merged = append(*merged, normalized)
	}
}

type providerLayer int

const (
	providerLayerGlobal providerLayer = iota
	providerLayerNode
	providerLayerRuntimeProfile
	providerLayerNodeRuntimeProfile
)

func normalizeProviderScope(scope ProviderScope) ProviderScope {
	return ProviderScope{
		NodeID:      strings.TrimSpace(scope.NodeID),
		RuntimeType: strings.TrimSpace(scope.RuntimeType),
		Profile:     strings.TrimSpace(scope.Profile),
	}
}

func providerScopeLayer(scope ProviderScope) providerLayer {
	if scope.NodeID == "" && scope.RuntimeType == "" {
		return providerLayerGlobal
	}
	if scope.RuntimeType == "" {
		return providerLayerNode
	}
	if scope.NodeID == "" {
		return providerLayerRuntimeProfile
	}
	return providerLayerNodeRuntimeProfile
}

func upsertProviderDefinition(providers []protocol.ProviderDefinition, provider protocol.ProviderDefinition) []protocol.ProviderDefinition {
	next := cloneProviderDefinitions(providers)
	name := strings.ToLower(strings.TrimSpace(provider.Name))
	for i := range next {
		if strings.ToLower(strings.TrimSpace(next[i].Name)) == name {
			next[i] = provider
			return next
		}
	}
	return append(next, provider)
}

func removeProviderDefinitionFromMap(desired protocol.DesiredConfig, layer providerLayer, key string, name string) (protocol.DesiredConfig, bool) {
	var providers []protocol.ProviderDefinition
	switch layer {
	case providerLayerNode:
		if desired.NodeProviders == nil {
			return desired, false
		}
		providers = desired.NodeProviders[key]
	case providerLayerRuntimeProfile:
		if desired.RuntimeProfileProviders == nil {
			return desired, false
		}
		providers = desired.RuntimeProfileProviders[key]
	case providerLayerNodeRuntimeProfile:
		if desired.NodeRuntimeProfileProviders == nil {
			return desired, false
		}
		providers = desired.NodeRuntimeProfileProviders[key]
	default:
		return desired, false
	}

	nextProviders, removed := removeProviderDefinition(providers, name)
	if !removed {
		return desired, false
	}
	switch layer {
	case providerLayerNode:
		if len(nextProviders) == 0 {
			delete(desired.NodeProviders, key)
		} else {
			desired.NodeProviders[key] = nextProviders
		}
	case providerLayerRuntimeProfile:
		if len(nextProviders) == 0 {
			delete(desired.RuntimeProfileProviders, key)
		} else {
			desired.RuntimeProfileProviders[key] = nextProviders
		}
	case providerLayerNodeRuntimeProfile:
		if len(nextProviders) == 0 {
			delete(desired.NodeRuntimeProfileProviders, key)
		} else {
			desired.NodeRuntimeProfileProviders[key] = nextProviders
		}
	}
	return desired, true
}

func removeProviderDefinition(providers []protocol.ProviderDefinition, name string) ([]protocol.ProviderDefinition, bool) {
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	next := make([]protocol.ProviderDefinition, 0, len(providers))
	removed := false
	for _, provider := range providers {
		if strings.ToLower(strings.TrimSpace(provider.Name)) == normalizedName {
			removed = true
			continue
		}
		next = append(next, normalizeProviderDefinition(provider))
	}
	if !removed {
		return cloneProviderDefinitions(providers), false
	}
	return next, true
}

func normalizeProviderDefinition(provider protocol.ProviderDefinition) protocol.ProviderDefinition {
	normalized := protocol.ProviderDefinition{
		Name:      strings.TrimSpace(provider.Name),
		BaseURL:   strings.TrimSpace(provider.BaseURL),
		APIKeyEnv: strings.TrimSpace(provider.APIKeyEnv),
	}
	if provider.Models != nil {
		normalized.Models = make([]string, len(provider.Models))
		for i, model := range provider.Models {
			normalized.Models[i] = strings.TrimSpace(model)
		}
	}
	return normalized
}

func cloneDesiredConfig(desired protocol.DesiredConfig) protocol.DesiredConfig {
	clone := protocol.DesiredConfig{
		Global:          desired.Global,
		GlobalProviders: cloneProviderDefinitions(desired.GlobalProviders),
	}
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
	if desired.NodeProviders != nil {
		clone.NodeProviders = cloneProviderDefinitionMap(desired.NodeProviders)
	}
	if desired.RuntimeProfileProviders != nil {
		clone.RuntimeProfileProviders = cloneProviderDefinitionMap(desired.RuntimeProfileProviders)
	}
	if desired.NodeRuntimeProfileProviders != nil {
		clone.NodeRuntimeProfileProviders = cloneProviderDefinitionMap(desired.NodeRuntimeProfileProviders)
	}
	return clone
}

func cloneProviderDefinitionMap(values map[string][]protocol.ProviderDefinition) map[string][]protocol.ProviderDefinition {
	clone := make(map[string][]protocol.ProviderDefinition, len(values))
	for key, providers := range values {
		clone[key] = cloneProviderDefinitions(providers)
	}
	return clone
}

func cloneProviderDefinitions(providers []protocol.ProviderDefinition) []protocol.ProviderDefinition {
	if providers == nil {
		return nil
	}
	clone := make([]protocol.ProviderDefinition, len(providers))
	for i, provider := range providers {
		clone[i] = provider
		if provider.Models != nil {
			clone[i].Models = append([]string(nil), provider.Models...)
		}
	}
	return clone
}
