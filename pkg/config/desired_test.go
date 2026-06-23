package config

import (
	"reflect"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestEffectiveProviderModelConfigUsesGlobalDefaults(t *testing.T) {
	got := EffectiveProviderModelConfig(protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
	}, EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "default"})

	want := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"}
	if got != want {
		t.Fatalf("effective config = %+v, want %+v", got, want)
	}
}

func TestEffectiveProviderModelConfigAppliesNodeOverride(t *testing.T) {
	got := EffectiveProviderModelConfig(protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Model: "gpt-5-mini"},
		},
	}, EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "default"})

	want := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5-mini"}
	if got != want {
		t.Fatalf("effective config = %+v, want %+v", got, want)
	}
}

func TestEffectiveProviderModelConfigAppliesRuntimeProfileOverrideLast(t *testing.T) {
	got := EffectiveProviderModelConfig(protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Provider: "anthropic", Model: "claude-sonnet-4"},
		},
		RuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			RuntimeProfileKey("hermes", "default"): {Model: "gpt-5-mini"},
		},
	}, EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "default"})

	want := protocol.ProviderModelConfig{Provider: "anthropic", Model: "gpt-5-mini"}
	if got != want {
		t.Fatalf("effective config = %+v, want %+v", got, want)
	}
}

func TestEffectiveProviderModelConfigAppliesNodeRuntimeProfileOverrideLast(t *testing.T) {
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Provider: "anthropic", Model: "claude-sonnet-4"},
		},
		RuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			RuntimeProfileKey("hermes", "default"): {Model: "gpt-5-mini"},
		},
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {Provider: "local", Model: "qwen3"},
		},
	}

	got := EffectiveProviderModelConfig(desired, EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "default"})
	want := protocol.ProviderModelConfig{Provider: "local", Model: "qwen3"}
	if got != want {
		t.Fatalf("effective config = %+v, want %+v", got, want)
	}

	otherProfile := EffectiveProviderModelConfig(desired, EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "ops"})
	wantOtherProfile := protocol.ProviderModelConfig{Provider: "anthropic", Model: "claude-sonnet-4"}
	if otherProfile != wantOtherProfile {
		t.Fatalf("other profile effective config = %+v, want %+v", otherProfile, wantOtherProfile)
	}
}

func TestDesiredConfigWithTargetOverrideCopiesAndScopesOverride(t *testing.T) {
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Provider: "anthropic"},
		},
	}
	target := EffectiveConfigTarget{NodeID: "node-a", RuntimeType: "hermes", Profile: "default"}

	next := DesiredConfigWithTargetOverride(desired, target, protocol.ProviderModelConfig{Provider: " local ", Model: " qwen3 "})
	key := NodeRuntimeProfileKey("node-a", "hermes", "default")
	if got := next.NodeRuntimeProfileOverrides[key]; got != (protocol.ProviderModelConfig{Provider: "local", Model: "qwen3"}) {
		t.Fatalf("target override = %+v, want trimmed scoped override", got)
	}
	if len(desired.NodeRuntimeProfileOverrides) != 0 {
		t.Fatalf("original desired mutated: %#v", desired.NodeRuntimeProfileOverrides)
	}

	effective := EffectiveProviderModelConfig(next, target)
	if effective != (protocol.ProviderModelConfig{Provider: "local", Model: "qwen3"}) {
		t.Fatalf("effective config = %+v, want scoped override", effective)
	}
}

func TestEffectiveProviderCatalogAppliesPrecedenceAndSorts(t *testing.T) {
	desired := protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{
			{Name: "OpenAI", BaseURL: " https://global.example.com ", Models: []string{" gpt-5 "}, APIKey: "global-key"},
			{Name: "local", Models: []string{"llama2"}},
		},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {
				{Name: " openai ", BaseURL: "https://node.example.com", Models: []string{" gpt-5-mini ", "gpt-5-nano "}, APIKey: "node-key"},
				{Name: "node-only", Models: []string{"qwen3"}},
			},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {
				{Name: "Anthropic", Models: []string{"claude-sonnet-4"}},
				{Name: "LOCAL", Models: []string{" llama3 "}, APIKey: "runtime-key"},
			},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {
				{Name: " anthropic ", BaseURL: " https://anthropic.example.com ", APIKey: "node-runtime-key"},
			},
		},
	}

	got := EffectiveProviderCatalog(desired, EffectiveConfigTarget{NodeID: " node-a ", RuntimeType: " hermes ", Profile: " default "})
	want := []protocol.ProviderDefinition{
		{Name: "anthropic", BaseURL: "https://anthropic.example.com", APIKey: "node-runtime-key"},
		{Name: "LOCAL", Models: []string{"llama3"}, APIKey: "runtime-key"},
		{Name: "node-only", Models: []string{"qwen3"}},
		{Name: "openai", BaseURL: "https://node.example.com", Models: []string{"gpt-5-mini", "gpt-5-nano"}, APIKey: "node-key"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective provider catalog = %#v, want %#v", got, want)
	}
}

func TestCloneDesiredConfigDeepCopiesProviderCatalog(t *testing.T) {
	desired := protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{
			{Name: "openai", Models: []string{"gpt-5"}, APIKey: "global-key"},
		},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {{Name: "node-provider", Models: []string{"node-model"}, APIKey: "node-key"}},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {{Name: "runtime-provider", Models: []string{"runtime-model"}, APIKey: "runtime-key"}},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {{Name: "node-runtime-provider", Models: []string{"node-runtime-model"}, APIKey: "node-runtime-key"}},
		},
	}

	clone := cloneDesiredConfig(desired)
	clone.GlobalProviders[0].Name = "mutated"
	clone.GlobalProviders[0].Models[0] = "mutated"
	nodeProviders := clone.NodeProviders["node-a"]
	nodeProviders[0].APIKey = "mutated"
	nodeProviders[0].Models[0] = "mutated"
	clone.NodeProviders["node-a"] = nodeProviders
	runtimeProviders := clone.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")]
	runtimeProviders[0].Models[0] = "mutated"
	clone.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")] = runtimeProviders
	nodeRuntimeProviders := clone.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")]
	nodeRuntimeProviders[0].Models[0] = "mutated"
	clone.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")] = nodeRuntimeProviders

	if desired.GlobalProviders[0].Name != "openai" || desired.GlobalProviders[0].Models[0] != "gpt-5" {
		t.Fatalf("global provider mutated through clone: %#v", desired.GlobalProviders)
	}
	if desired.NodeProviders["node-a"][0].APIKey != "node-key" || desired.NodeProviders["node-a"][0].Models[0] != "node-model" {
		t.Fatalf("node provider mutated through clone: %#v", desired.NodeProviders)
	}
	if desired.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")][0].Models[0] != "runtime-model" {
		t.Fatalf("runtime provider mutated through clone: %#v", desired.RuntimeProfileProviders)
	}
	if desired.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")][0].Models[0] != "node-runtime-model" {
		t.Fatalf("node runtime provider mutated through clone: %#v", desired.NodeRuntimeProfileProviders)
	}
}

func TestRuntimeProfileKey(t *testing.T) {
	if got := RuntimeProfileKey(" hermes ", " default "); got != "hermes/default" {
		t.Fatalf("key = %q, want hermes/default", got)
	}
	if got := RuntimeProfileKey("openclaw", ""); got != "openclaw" {
		t.Fatalf("key = %q, want openclaw", got)
	}
}

func TestNodeRuntimeProfileKey(t *testing.T) {
	if got := NodeRuntimeProfileKey(" node-a ", " hermes ", " default "); got != "node-a/hermes/default" {
		t.Fatalf("key = %q, want node-a/hermes/default", got)
	}
}
