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
			{Name: "OpenAI", BaseURL: " https://global.example.com ", Models: []string{" gpt-5 "}, APIKeyEnv: "GLOBAL_KEY"},
			{Name: "local", Models: []string{"llama2"}},
		},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {
				{Name: " openai ", BaseURL: "https://node.example.com", Models: []string{" gpt-5-mini ", "gpt-5-nano "}, APIKeyEnv: "NODE_KEY"},
				{Name: "node-only", Models: []string{"qwen3"}},
			},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {
				{Name: "Anthropic", Models: []string{"claude-sonnet-4"}},
				{Name: "LOCAL", Models: []string{" llama3 "}, APIKeyEnv: "RUNTIME_KEY"},
			},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {
				{Name: " anthropic ", BaseURL: " https://anthropic.example.com ", APIKeyEnv: "NODE_RUNTIME_KEY"},
			},
		},
	}

	got := EffectiveProviderCatalog(desired, EffectiveConfigTarget{NodeID: " node-a ", RuntimeType: " hermes ", Profile: " default "})
	want := []protocol.ProviderDefinition{
		{Name: "anthropic", BaseURL: "https://anthropic.example.com", APIKeyEnv: "NODE_RUNTIME_KEY"},
		{Name: "LOCAL", Models: []string{"llama3"}, APIKeyEnv: "RUNTIME_KEY"},
		{Name: "node-only", Models: []string{"qwen3"}},
		{Name: "openai", BaseURL: "https://node.example.com", Models: []string{"gpt-5-mini", "gpt-5-nano"}, APIKeyEnv: "NODE_KEY"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective provider catalog = %#v, want %#v", got, want)
	}
}

func TestProviderCatalogUpsertRemoveAcrossScopes(t *testing.T) {
	type scopeCase struct {
		name       string
		scope      ProviderScope
		key        string
		withLayer  func([]protocol.ProviderDefinition) protocol.DesiredConfig
		layer      func(protocol.DesiredConfig) []protocol.ProviderDefinition
		keyDropped func(protocol.DesiredConfig) bool
	}
	cases := []scopeCase{
		{
			name:  "global",
			scope: ProviderScope{},
			withLayer: func(providers []protocol.ProviderDefinition) protocol.DesiredConfig {
				return protocol.DesiredConfig{GlobalProviders: providers}
			},
			layer: func(desired protocol.DesiredConfig) []protocol.ProviderDefinition {
				return desired.GlobalProviders
			},
			keyDropped: func(desired protocol.DesiredConfig) bool {
				return len(desired.GlobalProviders) == 0
			},
		},
		{
			name:  "node",
			scope: ProviderScope{NodeID: " node-a "},
			key:   "node-a",
			withLayer: func(providers []protocol.ProviderDefinition) protocol.DesiredConfig {
				return protocol.DesiredConfig{NodeProviders: map[string][]protocol.ProviderDefinition{"node-a": providers}}
			},
			layer: func(desired protocol.DesiredConfig) []protocol.ProviderDefinition {
				return desired.NodeProviders["node-a"]
			},
			keyDropped: func(desired protocol.DesiredConfig) bool {
				_, ok := desired.NodeProviders["node-a"]
				return !ok
			},
		},
		{
			name:  "runtime profile",
			scope: ProviderScope{RuntimeType: " hermes ", Profile: " default "},
			key:   RuntimeProfileKey("hermes", "default"),
			withLayer: func(providers []protocol.ProviderDefinition) protocol.DesiredConfig {
				return protocol.DesiredConfig{RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{RuntimeProfileKey("hermes", "default"): providers}}
			},
			layer: func(desired protocol.DesiredConfig) []protocol.ProviderDefinition {
				return desired.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")]
			},
			keyDropped: func(desired protocol.DesiredConfig) bool {
				_, ok := desired.RuntimeProfileProviders[RuntimeProfileKey("hermes", "default")]
				return !ok
			},
		},
		{
			name:  "node runtime profile",
			scope: ProviderScope{NodeID: " node-a ", RuntimeType: " hermes ", Profile: " default "},
			key:   NodeRuntimeProfileKey("node-a", "hermes", "default"),
			withLayer: func(providers []protocol.ProviderDefinition) protocol.DesiredConfig {
				return protocol.DesiredConfig{NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{NodeRuntimeProfileKey("node-a", "hermes", "default"): providers}}
			},
			layer: func(desired protocol.DesiredConfig) []protocol.ProviderDefinition {
				return desired.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")]
			},
			keyDropped: func(desired protocol.DesiredConfig) bool {
				_, ok := desired.NodeRuntimeProfileProviders[NodeRuntimeProfileKey("node-a", "hermes", "default")]
				return !ok
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.withLayer([]protocol.ProviderDefinition{
				{Name: "existing", Models: []string{"old-model"}, APIKeyEnv: "EXISTING_KEY"},
			})
			originalSnapshot := cloneDesiredConfig(original)

			inserted := UpsertProviderDefinition(original, tt.scope, protocol.ProviderDefinition{
				Name:      " NewProvider ",
				BaseURL:   " https://providers.example.com/v1 ",
				Models:    []string{" gpt-5 ", " gpt-5-mini "},
				APIKeyEnv: " NEW_PROVIDER_KEY ",
			})
			if !reflect.DeepEqual(original, originalSnapshot) {
				t.Fatalf("upsert mutated input: got %#v want %#v", original, originalSnapshot)
			}
			wantInserted := []protocol.ProviderDefinition{
				{Name: "existing", Models: []string{"old-model"}, APIKeyEnv: "EXISTING_KEY"},
				{Name: "NewProvider", BaseURL: "https://providers.example.com/v1", Models: []string{"gpt-5", "gpt-5-mini"}, APIKeyEnv: "NEW_PROVIDER_KEY"},
			}
			if got := tt.layer(inserted); !reflect.DeepEqual(got, wantInserted) {
				t.Fatalf("inserted layer = %#v, want %#v", got, wantInserted)
			}

			replaced := UpsertProviderDefinition(inserted, tt.scope, protocol.ProviderDefinition{
				Name:      "newprovider",
				Models:    []string{"replacement-model"},
				APIKeyEnv: "REPLACEMENT_KEY",
			})
			wantReplaced := []protocol.ProviderDefinition{
				{Name: "existing", Models: []string{"old-model"}, APIKeyEnv: "EXISTING_KEY"},
				{Name: "newprovider", Models: []string{"replacement-model"}, APIKeyEnv: "REPLACEMENT_KEY"},
			}
			if got := tt.layer(replaced); !reflect.DeepEqual(got, wantReplaced) {
				t.Fatalf("replaced layer = %#v, want %#v", got, wantReplaced)
			}

			appended := UpsertProviderDefinition(replaced, tt.scope, protocol.ProviderDefinition{Name: "another", Models: []string{"append-model"}})
			if got := tt.layer(appended); len(got) != 3 || got[2].Name != "another" {
				t.Fatalf("appended layer = %#v, want third provider", got)
			}

			removed, ok := RemoveProviderDefinition(appended, tt.scope, " NEWPROVIDER ")
			if !ok {
				t.Fatal("remove returned false, want true")
			}
			wantRemoved := []protocol.ProviderDefinition{
				{Name: "existing", Models: []string{"old-model"}, APIKeyEnv: "EXISTING_KEY"},
				{Name: "another", Models: []string{"append-model"}},
			}
			if got := tt.layer(removed); !reflect.DeepEqual(got, wantRemoved) {
				t.Fatalf("removed layer = %#v, want %#v", got, wantRemoved)
			}
			removedSnapshot := cloneDesiredConfig(removed)
			missing, ok := RemoveProviderDefinition(removed, tt.scope, "missing")
			if ok {
				t.Fatal("remove missing returned true, want false")
			}
			if !reflect.DeepEqual(missing, removedSnapshot) {
				t.Fatalf("remove missing changed desired: got %#v want %#v", missing, removedSnapshot)
			}

			single := tt.withLayer([]protocol.ProviderDefinition{{Name: "only", Models: []string{"model"}}})
			emptied, ok := RemoveProviderDefinition(single, tt.scope, "ONLY")
			if !ok {
				t.Fatal("remove only returned false, want true")
			}
			if !tt.keyDropped(emptied) {
				t.Fatalf("empty layer key not dropped for key %q: %#v", tt.key, emptied)
			}
			if got := tt.layer(single); len(got) != 1 || got[0].Name != "only" {
				t.Fatalf("remove mutated single-provider input: %#v", single)
			}
		})
	}
}

func TestCloneDesiredConfigDeepCopiesProviderCatalog(t *testing.T) {
	desired := protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{
			{Name: "openai", Models: []string{"gpt-5"}, APIKeyEnv: "GLOBAL_KEY"},
		},
		NodeProviders: map[string][]protocol.ProviderDefinition{
			"node-a": {{Name: "node-provider", Models: []string{"node-model"}, APIKeyEnv: "NODE_KEY"}},
		},
		RuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			RuntimeProfileKey("hermes", "default"): {{Name: "runtime-provider", Models: []string{"runtime-model"}, APIKeyEnv: "RUNTIME_KEY"}},
		},
		NodeRuntimeProfileProviders: map[string][]protocol.ProviderDefinition{
			NodeRuntimeProfileKey("node-a", "hermes", "default"): {{Name: "node-runtime-provider", Models: []string{"node-runtime-model"}, APIKeyEnv: "NODE_RUNTIME_KEY"}},
		},
	}

	clone := cloneDesiredConfig(desired)
	clone.GlobalProviders[0].Name = "mutated"
	clone.GlobalProviders[0].Models[0] = "mutated"
	nodeProviders := clone.NodeProviders["node-a"]
	nodeProviders[0].APIKeyEnv = "MUTATED"
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
	if desired.NodeProviders["node-a"][0].APIKeyEnv != "NODE_KEY" || desired.NodeProviders["node-a"][0].Models[0] != "node-model" {
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
