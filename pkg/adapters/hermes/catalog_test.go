package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const providerCatalogYAML = `
model:
  provider: openai
  default: gpt-5
  base_url: https://api.openai.test/v1
providers:
  openai:
    name: openai
    api: https://api.openai.test/v1
    api_key: ${OPENAI_API_KEY}
    models:
      gpt-5:
        context_length: 128000
      gpt-4o: {}
  anthropic:
    name: anthropic
    base_url: https://api.anthropic.test/v1
    models:
      claude-sonnet-4.5: {}
custom_providers:
  - name: openai
    base_url: https://api.openai.test/v1
    models:
      gpt-4.1: {}
`

var wantProviderCatalog = []protocol.ProviderCatalogEntry{
	{
		Name:    "anthropic",
		BaseURL: "https://api.anthropic.test/v1",
		Models:  []string{"claude-sonnet-4.5"},
	},
	{
		Name:      "openai",
		BaseURL:   "https://api.openai.test/v1",
		Models:    []string{"gpt-4.1", "gpt-4o", "gpt-5"},
		APIKeyEnv: "OPENAI_API_KEY",
		Active:    true,
	},
}

func TestEnumerateProvidersYAML(t *testing.T) {
	got := EnumerateProviders([]byte(providerCatalogYAML))
	assertProviderCatalog(t, got, wantProviderCatalog)
}

func TestEnumerateProvidersDropsLiteralAPIKey(t *testing.T) {
	const literal = "sk-literal-secret-never-copy"
	got := EnumerateProviders([]byte(`
model:
  provider: local
  default: qwen3
providers:
  local:
    name: local
    base_url: http://localhost:11434/v1
    api_key: sk-literal-secret-never-copy
    models:
      qwen3: {}
`))
	if len(got) != 1 {
		t.Fatalf("len(catalog) = %d, want 1: %#v", len(got), got)
	}
	if got[0].APIKeyEnv != "" {
		t.Fatalf("APIKeyEnv = %q, want empty for literal api_key", got[0].APIKeyEnv)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	if strings.Contains(string(payload), literal) {
		t.Fatalf("catalog payload contains literal api_key %q: %s", literal, payload)
	}
}

func TestEnumerateProvidersJSON(t *testing.T) {
	got := EnumerateProviders([]byte(`{
  "model": {
    "provider": "openai",
    "default": "gpt-5",
    "base_url": "https://api.openai.test/v1"
  },
  "providers": {
    "openai": {
      "name": "openai",
      "api": "https://api.openai.test/v1",
      "api_key": "${OPENAI_API_KEY}",
      "models": {
        "gpt-5": {"context_length": 128000},
        "gpt-4o": {}
      }
    },
    "anthropic": {
      "name": "anthropic",
      "base_url": "https://api.anthropic.test/v1",
      "models": {
        "claude-sonnet-4.5": {}
      }
    }
  },
  "custom_providers": [
    {
      "name": "openai",
      "base_url": "https://api.openai.test/v1",
      "models": {
        "gpt-4.1": {}
      }
    }
  ]
}`))
	assertProviderCatalog(t, got, wantProviderCatalog)
}

func TestEnumerateProvidersActiveOnly(t *testing.T) {
	got := EnumerateProviders([]byte(`
model:
  provider: openai
  default: gpt-5
  base_url: https://api.openai.test/v1
`))
	assertProviderCatalog(t, got, []protocol.ProviderCatalogEntry{{
		Name:    "openai",
		BaseURL: "https://api.openai.test/v1",
		Models:  []string{"gpt-5"},
		Active:  true,
	}})
}

func TestEnumerateProvidersEmptyGarbage(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "nil"},
		{name: "empty", contents: []byte("")},
		{name: "garbage", contents: []byte("this is not a hermes config: [")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("EnumerateProviders panic = %v", recovered)
				}
			}()
			if got := EnumerateProviders(tt.contents); len(got) != 0 {
				t.Fatalf("catalog = %#v, want nil/empty", got)
			}
		})
	}
}

func TestAdapterConfigSnapshotsIncludesProviderCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes.yaml")
	if err := os.WriteFile(path, []byte(providerCatalogYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := &Adapter{
		lookup:      func(string) (string, error) { return "", errors.New("not found") },
		configPaths: []string{path},
		getenv:      func(string) string { return "" },
	}

	snapshots, err := a.ConfigSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshots error = %v, want nil", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	assertProviderCatalog(t, snapshots[0].Providers, wantProviderCatalog)
}

func assertProviderCatalog(t *testing.T, got []protocol.ProviderCatalogEntry, want []protocol.ProviderCatalogEntry) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	t.Fatalf("catalog mismatch\ngot:  %s\nwant: %s", gotJSON, wantJSON)
}
