package config

import (
	"encoding/json"
	"strings"
	"testing"
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
