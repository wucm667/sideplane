package hermes

import (
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func testProviderCatalog() []protocol.ProviderDefinition {
	return []protocol.ProviderDefinition{
		{Name: "z-local", BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen3"}},
		{Name: "anthropic", BaseURL: "https://api.anthropic.example/v1", APIKeyEnv: "ANTHROPIC_API_KEY"},
		{Name: "openai", BaseURL: "https://api.openai.example/v1", APIKeyEnv: "OPENAI_API_KEY"},
	}
}

func TestRenderCustomProvidersAppendsWhenAbsent(t *testing.T) {
	rendered, err := RenderCustomProviders([]byte(sampleConfig), testProviderCatalog())
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	out := string(rendered)
	if !strings.HasPrefix(out, sampleConfig) {
		t.Fatalf("rendered config did not preserve original prefix:\n%s", out)
	}
	if !strings.Contains(out, "custom_providers:\n") {
		t.Fatalf("rendered config missing custom_providers:\n%s", out)
	}
	if strings.Index(out, "  - name: anthropic") > strings.Index(out, "  - name: openai") {
		t.Fatalf("providers are not sorted by name:\n%s", out)
	}
	if err := ValidateCustomProviders(rendered, testProviderCatalog()); err != nil {
		t.Fatalf("validate rendered custom providers: %v", err)
	}
	assertNoPlaintextProviderKey(t, rendered)
}

func TestRenderCustomProvidersReplacesExistingBlockAndPreservesOtherBytes(t *testing.T) {
	prefix := "model:\n  default: claude-3.7-sonnet\n  provider: anthropic\nproviders: {}\n"
	owned := "custom_providers:\n  - name: old\n    base_url: https://old.example/v1\n    api_key: sk-literal-never-keep\n"
	suffix := "toolsets:\n  shell:\n    provider: auto\n"
	current := []byte(prefix + owned + suffix)

	rendered, err := RenderCustomProviders(current, testProviderCatalog())
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	out := string(rendered)
	if !strings.HasPrefix(out, prefix) || !strings.HasSuffix(out, suffix) {
		t.Fatalf("surrounding bytes changed:\n%s", out)
	}
	if strings.Contains(out, "old") || strings.Contains(out, "sk-literal-never-keep") {
		t.Fatalf("old custom_providers block was not fully replaced:\n%s", out)
	}
	if err := ValidateCustomProviders(rendered, testProviderCatalog()); err != nil {
		t.Fatalf("validate rendered custom providers: %v", err)
	}
	assertNoPlaintextProviderKey(t, rendered)
}

func TestRenderCustomProvidersReplacesInlineEmptyBlock(t *testing.T) {
	current := []byte("model:\n  default: qwen3\n  provider: local\ncustom_providers: []\nproviders: {}\n")

	rendered, err := RenderCustomProviders(current, []protocol.ProviderDefinition{
		{Name: "local", BaseURL: "http://127.0.0.1:11434/v1"},
	})
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	out := string(rendered)
	if strings.Contains(out, "custom_providers: []") {
		t.Fatalf("inline custom_providers was not replaced:\n%s", out)
	}
	if !strings.Contains(out, "custom_providers:\n  - name: local\n    base_url: http://127.0.0.1:11434/v1\nproviders: {}\n") {
		t.Fatalf("rendered inline replacement not found:\n%s", out)
	}
	if err := ValidateCustomProviders(rendered, []protocol.ProviderDefinition{{Name: "local", BaseURL: "http://127.0.0.1:11434/v1"}}); err != nil {
		t.Fatalf("validate rendered custom providers: %v", err)
	}
}

func TestRenderCustomProvidersOmitsAPIKeyWhenAPIKeyEnvEmpty(t *testing.T) {
	rendered, err := RenderCustomProviders([]byte("providers: {}\n"), []protocol.ProviderDefinition{
		{Name: "local", BaseURL: "http://127.0.0.1:11434/v1"},
	})
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	out := string(rendered)
	if strings.Contains(out, "api_key:") {
		t.Fatalf("api_key line emitted for empty APIKeyEnv:\n%s", out)
	}
	if err := ValidateCustomProviders(rendered, []protocol.ProviderDefinition{{Name: "local", BaseURL: "http://127.0.0.1:11434/v1"}}); err != nil {
		t.Fatalf("validate rendered custom providers: %v", err)
	}
}

func TestRenderCustomProvidersEmptyProvidersRemovesBlock(t *testing.T) {
	current := []byte("model:\n  default: qwen3\n  provider: local\ncustom_providers:\n  - name: old\n    base_url: https://old.example/v1\nproviders: {}\n")

	rendered, err := RenderCustomProviders(current, nil)
	if err != nil {
		t.Fatalf("render empty custom providers: %v", err)
	}
	out := string(rendered)
	if strings.Contains(out, "custom_providers") || strings.Contains(out, "old") {
		t.Fatalf("custom_providers block was not removed:\n%s", out)
	}
	if !strings.Contains(out, "model:\n  default: qwen3\n  provider: local\nproviders: {}\n") {
		t.Fatalf("surrounding config not preserved after removal:\n%s", out)
	}
}

func TestRenderCustomProvidersRoundTripsThroughEnumerateProviders(t *testing.T) {
	rendered, err := RenderCustomProviders([]byte("providers: {}\n"), testProviderCatalog())
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	got := EnumerateProviders(rendered)
	for _, want := range testProviderCatalog() {
		if !hasMatchingProviderEntry(got, want) {
			t.Fatalf("enumerated catalog missing %#v from %#v", want, got)
		}
	}
}

func TestValidateCustomProvidersFailsOnMismatchedCatalog(t *testing.T) {
	rendered, err := RenderCustomProviders([]byte("providers: {}\n"), []protocol.ProviderDefinition{
		{Name: "openai", BaseURL: "https://api.openai.example/v1", APIKeyEnv: "OPENAI_API_KEY"},
	})
	if err != nil {
		t.Fatalf("render custom providers: %v", err)
	}
	err = ValidateCustomProviders(rendered, []protocol.ProviderDefinition{
		{Name: "openai", BaseURL: "https://api.openai.example/v1", APIKeyEnv: "OTHER_API_KEY"},
	})
	if err == nil {
		t.Fatal("ValidateCustomProviders accepted mismatched apiKeyEnv")
	}
}

func TestRenderCustomProvidersRejectsLiteralAPIKeyEnv(t *testing.T) {
	_, err := RenderCustomProviders([]byte("providers: {}\n"), []protocol.ProviderDefinition{
		{Name: "openai", BaseURL: "https://api.openai.example/v1", APIKeyEnv: "sk-literal-secret"},
	})
	if err == nil {
		t.Fatal("RenderCustomProviders accepted plaintext-shaped APIKeyEnv")
	}
	if !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("error = %q, want apiKeyEnv validation", err.Error())
	}
}

func TestRenderCustomProvidersRejectsJSONConfigForV1(t *testing.T) {
	_, err := RenderCustomProviders([]byte(`{"model":{"provider":"openai","default":"gpt-5"}}`), testProviderCatalog())
	if err == nil {
		t.Fatal("RenderCustomProviders accepted JSON config")
	}
	if !strings.Contains(err.Error(), "JSON writer is out of scope") {
		t.Fatalf("error = %q, want JSON writer out of scope", err.Error())
	}
}

func assertNoPlaintextProviderKey(t *testing.T, rendered []byte) {
	t.Helper()
	out := string(rendered)
	for _, forbidden := range []string{"sk-literal", "sk-test", "api_key: OPENAI_API_KEY", "api_key: ANTHROPIC_API_KEY"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("rendered config leaked plaintext api_key material %q:\n%s", forbidden, out)
		}
	}
	for _, want := range []string{"api_key: ${OPENAI_API_KEY}", "api_key: ${ANTHROPIC_API_KEY}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config missing env api_key reference %q:\n%s", want, out)
		}
	}
}
