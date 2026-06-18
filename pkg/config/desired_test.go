package config

import (
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
