package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewRuntimeConfigSnapshotOnlyIncludesAllowlistedFields(t *testing.T) {
	snapshot := NewRuntimeConfigSnapshot(SnapshotInput{
		RuntimeName: " default ",
		RuntimeType: "hermes",
		ConfigPath:  " /etc/hermes/config.yaml ",
		Source:      " file ",
		Profile:     " default ",
		Provider:    "openai",
		Model:       "gpt-5",
		ConfigHash:  " sha256:abc ",
		Warnings:    []string{"provider/model discovered"},
	})

	if snapshot.RuntimeName != "default" {
		t.Fatalf("runtime name = %q, want trimmed default", snapshot.RuntimeName)
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, forbidden := range []string{"redactedValues", "apiKey", "token", "password", "secret"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("snapshot JSON contains non-allowlisted field/value %q: %s", forbidden, payload)
		}
	}
}
