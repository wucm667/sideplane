package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestDiffProviderModelConfigNoDiff(t *testing.T) {
	actual := &protocol.RuntimeConfigSnapshot{Provider: "openai", Model: "gpt-5"}
	desired := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"}

	diff := DiffProviderModelConfig(actual, desired)
	if len(diff) != 0 {
		t.Fatalf("diff = %#v, want none", diff)
	}
}

func TestDiffProviderModelConfigProviderChange(t *testing.T) {
	actual := &protocol.RuntimeConfigSnapshot{Provider: "openai", Model: "gpt-5"}
	desired := protocol.ProviderModelConfig{Provider: "anthropic", Model: "gpt-5"}

	diff := DiffProviderModelConfig(actual, desired)
	if len(diff) != 1 {
		t.Fatalf("len(diff) = %d, want 1: %#v", len(diff), diff)
	}
	want := protocol.ConfigDiffEntry{
		Field:   "provider",
		Actual:  "openai",
		Desired: "anthropic",
		Change:  protocol.ConfigDiffChangeUpdate,
	}
	if diff[0] != want {
		t.Fatalf("diff[0] = %+v, want %+v", diff[0], want)
	}
}

func TestDiffProviderModelConfigModelChange(t *testing.T) {
	actual := &protocol.RuntimeConfigSnapshot{Provider: "openai", Model: "gpt-5"}
	desired := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5-mini"}

	diff := DiffProviderModelConfig(actual, desired)
	if len(diff) != 1 {
		t.Fatalf("len(diff) = %d, want 1: %#v", len(diff), diff)
	}
	want := protocol.ConfigDiffEntry{
		Field:   "model",
		Actual:  "gpt-5",
		Desired: "gpt-5-mini",
		Change:  protocol.ConfigDiffChangeUpdate,
	}
	if diff[0] != want {
		t.Fatalf("diff[0] = %+v, want %+v", diff[0], want)
	}
}

func TestDiffProviderModelConfigMissingActual(t *testing.T) {
	desired := protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"}

	diff := DiffProviderModelConfig(nil, desired)
	if len(diff) != 2 {
		t.Fatalf("len(diff) = %d, want 2: %#v", len(diff), diff)
	}
	for _, entry := range diff {
		if entry.Change != protocol.ConfigDiffChangeMissingActual {
			t.Fatalf("entry change = %q, want missing actual", entry.Change)
		}
		if entry.Actual != "" {
			t.Fatalf("entry actual = %q, want empty", entry.Actual)
		}
	}
}

func TestDiffProviderModelConfigNeverEmitsSecrets(t *testing.T) {
	actual := &protocol.RuntimeConfigSnapshot{
		Provider: "openai",
		Model:    "gpt-5",
		RedactedValues: map[string]string{
			"apiKey": "sk-secret",
		},
	}
	desired := protocol.ProviderModelConfig{Provider: "anthropic", Model: "claude-sonnet-4"}

	diff := DiffProviderModelConfig(actual, desired)
	payload, err := json.Marshal(diff)
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	if strings.Contains(string(payload), "sk-secret") || strings.Contains(string(payload), "apiKey") {
		t.Fatalf("diff leaked secret material: %s", payload)
	}
}
