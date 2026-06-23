package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestRedactStringRedactsMixedCaseNestedJSONSecrets(t *testing.T) {
	raw := `{
		"token":"top-secret-token",
		"nested":{
			"apiKey":"sk-test-secret",
			"Authorization":"Bearer secret-value",
			"items":[{"nodeCredential":"node-secret"}]
		},
		"status":"ok"
	}`

	redacted := RedactString(raw)

	for _, forbidden := range []string{"top-secret-token", "sk-test-secret", "Bearer secret-value", "node-secret"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redacted JSON leaked %q: %s", forbidden, redacted)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(redacted), &decoded); err != nil {
		t.Fatalf("redacted JSON is invalid: %v", err)
	}
	if decoded["status"] != "ok" {
		t.Fatalf("status = %#v, want ok", decoded["status"])
	}
}

func TestRedactStringDoesNotRedactHarmlessKeyWords(t *testing.T) {
	raw := `{"publicKey":"pub-test","monkey":"banana","keynote":"talk","status":"ok"}`

	redacted := RedactString(raw)

	for _, want := range []string{"pub-test", "banana", "talk", "ok"} {
		if !strings.Contains(redacted, want) {
			t.Fatalf("redacted JSON = %s, want harmless value %q preserved", redacted, want)
		}
	}
}

func TestRedactStringRedactsSecretAssignments(t *testing.T) {
	raw := "token=abc credential:node-secret authorization:Bearer api-key=sk-test status=ok"

	redacted := RedactString(raw)

	for _, forbidden := range []string{"abc", "node-secret", "Bearer", "sk-test"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redacted string leaked %q: %s", forbidden, redacted)
		}
	}
	if !strings.Contains(redacted, "status=ok") {
		t.Fatalf("redacted string = %q, want harmless status preserved", redacted)
	}
}

func TestRedactProviderDefinitionBlanksAPIKeyOnly(t *testing.T) {
	provider := protocol.ProviderDefinition{
		Name:    "openai",
		BaseURL: "https://api.example.com/v1",
		Models:  []string{"gpt-5"},
		APIKey:  "plain-key",
	}

	redacted := RedactProviderDefinition(provider)
	if redacted.APIKey != "" {
		t.Fatalf("redacted apiKey = %q, want blank", redacted.APIKey)
	}
	redacted.APIKey = provider.APIKey
	if !reflect.DeepEqual(redacted, provider) {
		t.Fatalf("redacted provider changed non-key fields: %#v, want %#v", redacted, provider)
	}

	empty := RedactProviderDefinition(protocol.ProviderDefinition{Name: "local"})
	if empty.APIKey != "" || empty.Name != "local" {
		t.Fatalf("empty key provider redaction = %#v, want name preserved and apiKey blank", empty)
	}
}

func TestRedactDesiredConfigRedactsProviderCatalogWithoutMutatingInput(t *testing.T) {
	desired := protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{{Name: "global", Models: []string{"gpt-5"}, APIKey: "global-key"}},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {{Name: "node", Models: []string{"node-model"}, APIKey: "node-key"}},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {{Name: "runtime", Models: []string{"runtime-model"}, APIKey: "runtime-key"}},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {{Name: "node-runtime", Models: []string{"node-runtime-model"}, APIKey: "node-runtime-key"}},
		},
	}

	redacted := RedactDesiredConfig(desired)
	for label, provider := range map[string]protocol.ProviderDefinition{
		"global":      redacted.GlobalProviders[0],
		"node":        redacted.NodeProviders["node-a"][0],
		"runtime":     redacted.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")][0],
		"nodeRuntime": redacted.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")][0],
	} {
		if provider.APIKey != "" {
			t.Fatalf("%s apiKey = %q, want blank", label, provider.APIKey)
		}
	}
	if redacted.GlobalProviders[0].Models[0] != "gpt-5" {
		t.Fatalf("redacted provider models = %#v, want preserved", redacted.GlobalProviders[0].Models)
	}

	redacted.GlobalProviders[0].Models[0] = "mutated"
	if desired.GlobalProviders[0].APIKey != "global-key" || desired.NodeProviders["node-a"][0].APIKey != "node-key" {
		t.Fatalf("redaction mutated input apiKeys: %#v", desired)
	}
	if desired.GlobalProviders[0].Models[0] != "gpt-5" {
		t.Fatalf("redaction result shared model slice with input: %#v", desired.GlobalProviders[0].Models)
	}
}
