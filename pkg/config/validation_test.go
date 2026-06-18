package config

import (
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestValidateProviderModelSelectionAllowsConservativeValues(t *testing.T) {
	err := ValidateProviderModelSelection(protocol.ProviderModelConfig{
		Provider: "openai",
		Model:    "openai/gpt-5.1-mini",
	})
	if err != nil {
		t.Fatalf("validate provider/model: %v", err)
	}
}

func TestValidateProviderModelSelectionRejectsYAMLBreakingValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "newline", value: "gpt-5\nmodel: hacked"},
		{name: "comment", value: "gpt-5#comment"},
		{name: "colon", value: "provider:model"},
		{name: "space", value: "gpt 5"},
		{name: "leading dash", value: "-gpt-5"},
		{name: "control", value: "gpt-5\x00"},
		{name: "non ascii", value: "模型"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProviderModelSelection(protocol.ProviderModelConfig{Provider: "openai", Model: tt.value})
			if err == nil {
				t.Fatalf("ValidateProviderModelSelection accepted %q", tt.value)
			}
		})
	}
}

func TestValidateDesiredConfigValuesAllowsPartialLayers(t *testing.T) {
	err := ValidateDesiredConfigValues(protocol.DesiredConfig{
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Model: "gpt-5"},
		},
		RuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"hermes/default": {Provider: "anthropic"},
		},
	})
	if err != nil {
		t.Fatalf("validate partial desired config: %v", err)
	}
}

func TestValidateDesiredConfigValuesRejectsNestedInvalidValue(t *testing.T) {
	err := ValidateDesiredConfigValues(protocol.DesiredConfig{
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"node-a/hermes/default": {Model: "gpt-5:bad"},
		},
	})
	if err == nil {
		t.Fatal("ValidateDesiredConfigValues accepted colon in desired config")
	}
	if !strings.Contains(err.Error(), "nodeRuntimeProfileOverrides[node-a/hermes/default].model") {
		t.Fatalf("error = %q, want nested field path", err.Error())
	}
}
