package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewRuntimeConfigSnapshotRedactsSecretLikeValues(t *testing.T) {
	snapshot := NewRuntimeConfigSnapshot(SnapshotInput{
		RuntimeName: " default ",
		RuntimeType: "hermes",
		Provider:    "openai",
		Model:       "gpt-5",
		Values: map[string]string{
			"baseURL":        "https://api.example.test",
			"OPENAI_API_KEY": "sk-secret",
			"providerToken":  "tok-secret",
			"password":       "pw-secret",
		},
	})

	if snapshot.RuntimeName != "default" {
		t.Fatalf("runtime name = %q, want trimmed default", snapshot.RuntimeName)
	}
	if snapshot.RedactedValues["baseURL"] != "https://api.example.test" {
		t.Fatalf("baseURL was redacted: %#v", snapshot.RedactedValues)
	}
	for _, key := range []string{"OPENAI_API_KEY", "providerToken", "password"} {
		if snapshot.RedactedValues[key] != RedactedValue {
			t.Fatalf("%s = %q, want redacted", key, snapshot.RedactedValues[key])
		}
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, secret := range []string{"sk-secret", "tok-secret", "pw-secret"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("snapshot JSON contains secret %q: %s", secret, payload)
		}
	}
}
