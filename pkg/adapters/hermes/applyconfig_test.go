package hermes

import (
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// sampleConfig mirrors the real Hermes config shape (top-level model block plus
// nested toolset provider/model entries) without any real endpoints or secrets.
const sampleConfig = `model:
  default: qwen3.6-plus
  provider: alibaba
  base_url: https://example.invalid/v1
providers: {}
toolsets:
  shell:
    provider: auto
    model: ''
  web:
    provider: auto
    model: ''
display:
  provider: edge
`

func TestModelFields(t *testing.T) {
	provider, model, ok := ModelFields([]byte(sampleConfig))
	if !ok {
		t.Fatal("ModelFields found=false, want true")
	}
	if provider != "alibaba" {
		t.Errorf("provider = %q, want alibaba", provider)
	}
	if model != "qwen3.6-plus" {
		t.Errorf("model = %q, want qwen3.6-plus", model)
	}
}

func TestModelFieldsMissing(t *testing.T) {
	if _, _, ok := ModelFields([]byte("providers: {}\ntoolsets:\n  shell:\n    provider: auto\n")); ok {
		t.Error("ModelFields found=true for config without a model block")
	}
}

func TestRenderDesiredModelSurgical(t *testing.T) {
	rendered, err := RenderDesiredModel([]byte(sampleConfig), protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := string(rendered)

	provider, model, ok := ModelFields(rendered)
	if !ok || provider != "openai" || model != "gpt-4o" {
		t.Errorf("rendered model fields = (%q, %q, %t), want (openai, gpt-4o, true)", provider, model, ok)
	}
	// Unrelated content must be preserved verbatim.
	if !strings.Contains(out, "base_url: https://example.invalid/v1") {
		t.Error("base_url not preserved")
	}
	if !strings.Contains(out, "display:\n  provider: edge") {
		t.Error("unrelated display.provider was modified")
	}
	// Nested toolset provider/model must remain untouched.
	if strings.Count(out, "provider: auto") != 2 {
		t.Errorf("nested toolset providers changed; got:\n%s", out)
	}
	// Exactly the two model-block lines change; line count is stable.
	if got, want := strings.Count(out, "\n"), strings.Count(sampleConfig, "\n"); got != want {
		t.Errorf("line count changed: got %d, want %d", got, want)
	}
}

func TestRenderDesiredModelRequiresValues(t *testing.T) {
	if _, err := RenderDesiredModel([]byte(sampleConfig), protocol.ProviderModelConfig{Provider: "openai"}); err == nil {
		t.Error("expected error for empty model")
	}
	if _, err := RenderDesiredModel([]byte(sampleConfig), protocol.ProviderModelConfig{Model: "gpt-4o"}); err == nil {
		t.Error("expected error for empty provider")
	}
}

func TestRenderDesiredModelRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "newline", value: "gpt-4o\nprovider: bad"},
		{name: "comment", value: "gpt-4o#bad"},
		{name: "colon", value: "gpt-4o:bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RenderDesiredModel([]byte(sampleConfig), protocol.ProviderModelConfig{Provider: "openai", Model: tt.value}); err == nil {
				t.Fatalf("expected unsafe value %q to fail", tt.value)
			}
		})
	}
}

func TestRenderDesiredModelMissingBlock(t *testing.T) {
	if _, err := RenderDesiredModel([]byte("providers: {}\n"), protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"}); err == nil {
		t.Error("expected error when no model block is present")
	}
}

func TestValidateModelConfig(t *testing.T) {
	desired := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"}
	rendered, err := RenderDesiredModel([]byte(sampleConfig), desired)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := ValidateModelConfig(rendered, desired); err != nil {
		t.Errorf("validate rendered: %v", err)
	}
	if err := ValidateModelConfig([]byte(sampleConfig), desired); err == nil {
		t.Error("validate should fail when config does not match desired")
	}
}
