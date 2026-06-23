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

func TestValidateDesiredConfigValuesAllowsProviderCatalogWithPlaintextAPIKey(t *testing.T) {
	err := ValidateDesiredConfigValues(protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{
			{Name: "openai", BaseURL: "https://api.openai.example/v1", Models: []string{"gpt-5", "gpt-5-mini"}, APIKey: "sk-plain:text/operator owned"},
		},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {{Name: "local", BaseURL: "http://127.0.0.1:11434", Models: []string{"qwen3"}}},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {{Name: "anthropic", Models: []string{"claude-sonnet-4"}, APIKey: "plain-runtime-key"}},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {{Name: "node-local", Models: []string{"llama3"}, APIKey: "node profile key"}},
		},
	})
	if err != nil {
		t.Fatalf("validate provider catalog with plaintext apiKey: %v", err)
	}
}

func TestValidateDesiredConfigValuesRejectsInvalidProviderCatalog(t *testing.T) {
	tests := []struct {
		name    string
		desired protocol.DesiredConfig
		want    string
	}{
		{
			name: "missing name",
			desired: protocol.DesiredConfig{
				GlobalProviders: []protocol.ProviderDefinition{{Models: []string{"gpt-5"}}},
			},
			want: "globalProviders[0].name is required",
		},
		{
			name: "bad name char",
			desired: protocol.DesiredConfig{
				GlobalProviders: []protocol.ProviderDefinition{{Name: ":openai"}},
			},
			want: "globalProviders[0].name contains unsupported character",
		},
		{
			name: "duplicate name",
			desired: protocol.DesiredConfig{
				GlobalProviders: []protocol.ProviderDefinition{{Name: "openai"}, {Name: " OpenAI "}},
			},
			want: "globalProviders[1].name duplicates provider",
		},
		{
			name: "bad model char",
			desired: protocol.DesiredConfig{
				NodeProviders: map[string][]protocol.ProviderDefinition{
					"node-a": {{Name: "openai", Models: []string{"gpt-5:bad"}}},
				},
			},
			want: "nodeProviders[node-a][0].models[0] contains unsupported character",
		},
		{
			name: "bad base url scheme",
			desired: protocol.DesiredConfig{
				RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
					RuntimeProfileKey("hermes", "default"): {{Name: "openai", BaseURL: "ftp://api.example.com"}},
				},
			},
			want: "runtimeProfileProviders[hermes/default][0].baseURL must use http or https",
		},
		{
			name: "bad base url host",
			desired: protocol.DesiredConfig{
				NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
					NodeRuntimeProfileKey("node-a", "hermes", "default"): {{Name: "openai", BaseURL: "https:///v1"}},
				},
			},
			want: "nodeRuntimeProfileProviders[node-a/hermes/default][0].baseURL must include a host",
		},
		{
			name: "oversized api key",
			desired: protocol.DesiredConfig{
				GlobalProviders: []protocol.ProviderDefinition{{Name: "openai", APIKey: strings.Repeat("x", maxProviderAPIKeyLength+1)}},
			},
			want: "globalProviders[0].apiKey is too long",
		},
		{
			name: "control char api key",
			desired: protocol.DesiredConfig{
				GlobalProviders: []protocol.ProviderDefinition{{Name: "openai", APIKey: "sk-test\nsecret"}},
			},
			want: "globalProviders[0].apiKey contains unsupported control character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDesiredConfigValues(tt.desired)
			if err == nil {
				t.Fatalf("ValidateDesiredConfigValues accepted invalid provider catalog")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}
