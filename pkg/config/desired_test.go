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

func TestRuntimeProfileKey(t *testing.T) {
	if got := RuntimeProfileKey(" hermes ", " default "); got != "hermes/default" {
		t.Fatalf("key = %q, want hermes/default", got)
	}
	if got := RuntimeProfileKey("openclaw", ""); got != "openclaw" {
		t.Fatalf("key = %q, want openclaw", got)
	}
}
