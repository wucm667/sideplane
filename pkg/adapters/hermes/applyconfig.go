package hermes

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// modelNameKeys are the keys, in priority order, that hold the model name under
// the top-level Hermes `model:` block. Hermes uses `default`; `model` is
// accepted as a fallback for other layouts.
var modelNameKeys = []string{"default", "model"}

// ModelFields extracts the provider and model name from the top-level `model:`
// block of a Hermes config (model.provider and model.default/model.model).
// found is true only when both are present.
func ModelFields(contents []byte) (provider string, model string, found bool) {
	inModel := false
	gotProvider := false
	gotModel := false
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r")
		if raw == "model:" {
			inModel = true
			continue
		}
		if !inModel {
			continue
		}
		if !isIndented(raw) {
			inModel = false
			continue
		}
		if value, ok := directChildValue(raw, "provider"); ok {
			provider = value
			gotProvider = true
			continue
		}
		for _, key := range modelNameKeys {
			if value, ok := directChildValue(raw, key); ok && !gotModel {
				model = value
				gotModel = true
				break
			}
		}
	}
	return provider, model, gotProvider && gotModel
}

// RenderDesiredModel returns the current Hermes config with the top-level model
// block's provider and model-name fields set to the desired values. Every other
// byte is preserved, so unrelated config (and secrets) are untouched.
func RenderDesiredModel(current []byte, desired protocol.ProviderModelConfig) ([]byte, error) {
	provider := strings.TrimSpace(desired.Provider)
	model := strings.TrimSpace(desired.Model)
	if provider == "" {
		return nil, fmt.Errorf("desired provider is required")
	}
	if model == "" {
		return nil, fmt.Errorf("desired model is required")
	}

	hadTrailingNewline := bytes.HasSuffix(current, []byte("\n"))
	lines := strings.Split(strings.TrimRight(string(current), "\n"), "\n")
	inModel := false
	setProvider := false
	setModel := false
	for i, line := range lines {
		raw := strings.TrimRight(line, "\r")
		if raw == "model:" {
			inModel = true
			continue
		}
		if !inModel {
			continue
		}
		if !isIndented(raw) {
			inModel = false
			continue
		}
		if _, ok := directChildValue(raw, "provider"); ok {
			lines[i] = "  provider: " + provider
			setProvider = true
			continue
		}
		for _, key := range modelNameKeys {
			if _, ok := directChildValue(raw, key); ok && !setModel {
				lines[i] = "  " + key + ": " + model
				setModel = true
				break
			}
		}
	}
	if !setProvider {
		return nil, fmt.Errorf("hermes config: top-level model.provider not found")
	}
	if !setModel {
		return nil, fmt.Errorf("hermes config: top-level model name key (%s) not found", strings.Join(modelNameKeys, "/"))
	}

	out := strings.Join(lines, "\n")
	if hadTrailingNewline {
		out += "\n"
	}
	return []byte(out), nil
}

// ValidateModelConfig confirms the config parses to the desired provider/model.
func ValidateModelConfig(contents []byte, desired protocol.ProviderModelConfig) error {
	provider, model, ok := ModelFields(contents)
	if !ok {
		return fmt.Errorf("hermes config is missing the top-level model provider/name")
	}
	if provider != strings.TrimSpace(desired.Provider) {
		return fmt.Errorf("rendered provider %q does not match desired %q", provider, desired.Provider)
	}
	if model != strings.TrimSpace(desired.Model) {
		return fmt.Errorf("rendered model %q does not match desired %q", model, desired.Model)
	}
	return nil
}

func isIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// directChildValue returns the value of a two-space-indented direct child key
// of the model block, e.g. "  provider: alibaba". Deeper-nested keys do not match.
func directChildValue(line, key string) (string, bool) {
	prefix := "  " + key + ":"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	value := strings.TrimSpace(line[len(prefix):])
	value = strings.Trim(value, "\"'")
	return strings.TrimSpace(value), true
}
