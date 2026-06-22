package protocol

import (
	"encoding/json"
	"testing"
)

func TestRuntimeConfigSnapshotJSONShape(t *testing.T) {
	snapshot := RuntimeConfigSnapshot{
		RuntimeName: "default",
		RuntimeType: "hermes",
		ConfigPath:  "/etc/hermes/config.toml",
		Source:      "config file",
		Profile:     "worker",
		Provider:    "openai",
		Model:       "gpt-5",
		ConfigHash:  "sha256:abc",
		Health:      RuntimeHealth{State: RuntimeHealthHealthy, Reason: "container running"},
		Warnings:    []string{"provider key redacted"},
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	for _, key := range []string{
		"runtimeName",
		"runtimeType",
		"configPath",
		"source",
		"profile",
		"provider",
		"model",
		"configHash",
		"health",
		"warnings",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("snapshot JSON omits %q: %s", key, payload)
		}
	}
	if _, ok := got["redactedValues"]; ok {
		t.Fatalf("snapshot JSON includes broad config values: %s", payload)
	}
	health, ok := got["health"].(map[string]any)
	if !ok || health["state"] != string(RuntimeHealthHealthy) || health["reason"] != "container running" {
		t.Fatalf("snapshot health JSON = %#v, want healthy container running", got["health"])
	}
}

func TestRuntimeStatusJSONShape(t *testing.T) {
	status := RuntimeStatus{
		Name:       "hermes",
		Type:       "hermes",
		Version:    "v2026.5.1",
		State:      "present",
		Provider:   "openai",
		Model:      "gpt-5",
		ConfigHash: "sha256:abc",
		Health:     RuntimeHealth{State: RuntimeHealthDegraded, Reason: "service inactive"},
		Warnings:   []string{"config path unreadable"},
		Outdated:   true,
	}

	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal runtime status: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal runtime status: %v", err)
	}
	for _, key := range []string{"name", "type", "version", "state", "provider", "model", "configHash", "health", "warnings", "outdated"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("runtime status JSON omits %q: %s", key, payload)
		}
	}
	health, ok := got["health"].(map[string]any)
	if !ok || health["state"] != string(RuntimeHealthDegraded) || health["reason"] != "service inactive" {
		t.Fatalf("runtime status health JSON = %#v, want degraded service inactive", got["health"])
	}
}

func TestNodeStatusJSONIncludesOperatorLabels(t *testing.T) {
	status := NodeStatus{
		NodeID:      "node-a",
		State:       NodeStateFresh,
		Maintenance: true,
		Labels: map[string]string{
			"role":   "canary",
			"region": "local",
		},
	}

	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal node status: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal node status: %v", err)
	}
	if _, ok := got["labels"]; !ok {
		t.Fatalf("node status JSON omits labels: %s", payload)
	}
	if got["maintenance"] != true {
		t.Fatalf("node status JSON maintenance = %#v, want true", got["maintenance"])
	}
}

func TestDesiredConfigJSONShape(t *testing.T) {
	desired := DesiredConfig{
		Global: ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]ProviderModelConfig{
			"node-a": {Model: "gpt-5-mini"},
		},
		RuntimeProfileOverrides: map[string]ProviderModelConfig{
			"hermes/default": {Provider: "anthropic"},
		},
		NodeRuntimeProfileOverrides: map[string]ProviderModelConfig{
			"node-a/hermes/default": {Model: "claude-sonnet-4"},
		},
	}

	payload, err := json.Marshal(desired)
	if err != nil {
		t.Fatalf("marshal desired config: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal desired config: %v", err)
	}
	for _, key := range []string{"global", "nodeOverrides", "runtimeProfileOverrides", "nodeRuntimeProfileOverrides"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("desired config JSON omits %q: %s", key, payload)
		}
	}
}

func TestConfigDiffEntryJSONShape(t *testing.T) {
	entry := ConfigDiffEntry{
		Field:   "provider",
		Actual:  "openai",
		Desired: "anthropic",
		Change:  ConfigDiffChangeUpdate,
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal config diff entry: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal config diff entry: %v", err)
	}
	for _, key := range []string{"field", "actual", "desired", "change"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("diff entry JSON omits %q: %s", key, payload)
		}
	}
}

func TestAuditEventJSONShape(t *testing.T) {
	event := AuditEvent{
		ID:         "audit_123",
		Actor:      "operator",
		Action:     "job.create",
		TargetNode: "node-a",
		Detail:     "deep_probe",
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal audit event: %v", err)
	}
	for _, key := range []string{"id", "actor", "action", "targetNode", "detail", "createdAt"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("audit event JSON omits %q: %s", key, payload)
		}
	}
}
