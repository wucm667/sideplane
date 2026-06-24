package hermes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	spconfig "github.com/wucm667/sideplane/pkg/config"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// RenderCustomProviders returns the Hermes config with the top-level
// custom_providers block replaced by Sideplane's managed catalog. YAML is the
// supported writer format for v1; JSON writer support is intentionally out of
// scope so we do not reformat or corrupt operator-owned config bytes.
func RenderCustomProviders(current []byte, providers []protocol.ProviderDefinition) ([]byte, error) {
	normalized, err := normalizeProviderDefinitionsForRender(providers)
	if err != nil {
		return nil, err
	}
	if isJSONObjectConfig(current) {
		return nil, fmt.Errorf("hermes config custom_providers JSON writer is out of scope for v1")
	}

	start, end, found := findCustomProvidersBlock(current)
	if len(normalized) == 0 {
		if !found {
			return append([]byte(nil), current...), nil
		}
		out := make([]byte, 0, len(current)-(end-start))
		out = append(out, current[:start]...)
		out = append(out, current[end:]...)
		return out, nil
	}

	block := renderCustomProvidersBlock(normalized)
	if !found {
		out := make([]byte, 0, len(current)+len(block)+1)
		out = append(out, current...)
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		out = append(out, block...)
		return out, nil
	}

	out := make([]byte, 0, len(current)-(end-start)+len(block))
	out = append(out, current[:start]...)
	out = append(out, block...)
	out = append(out, current[end:]...)
	return out, nil
}

// ValidateCustomProviders confirms that each desired provider is discoverable
// after rendering, with matching name, base URL, and API key environment name.
func ValidateCustomProviders(rendered []byte, providers []protocol.ProviderDefinition) error {
	normalized, err := normalizeProviderDefinitionsForRender(providers)
	if err != nil {
		return err
	}
	entries := EnumerateProviders(rendered)
	for _, want := range normalized {
		if hasMatchingProviderEntry(entries, want) {
			continue
		}
		return fmt.Errorf("rendered custom provider %q with baseURL %q and apiKeyEnv %q not found", want.Name, want.BaseURL, want.APIKeyEnv)
	}
	return nil
}

func normalizeProviderDefinitionsForRender(providers []protocol.ProviderDefinition) ([]protocol.ProviderDefinition, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	normalized := make([]protocol.ProviderDefinition, len(providers))
	for i, provider := range providers {
		normalized[i] = protocol.ProviderDefinition{
			Name:      strings.TrimSpace(provider.Name),
			BaseURL:   strings.TrimSpace(provider.BaseURL),
			APIKeyEnv: strings.TrimSpace(provider.APIKeyEnv),
		}
		if provider.Models != nil {
			normalized[i].Models = make([]string, len(provider.Models))
			for j, model := range provider.Models {
				normalized[i].Models[j] = strings.TrimSpace(model)
			}
		}
	}
	if err := spconfig.ValidateDesiredConfigValues(protocol.DesiredConfig{GlobalProviders: normalized}); err != nil {
		return nil, err
	}
	slices.SortFunc(normalized, func(a, b protocol.ProviderDefinition) int {
		aName := strings.ToLower(a.Name)
		bName := strings.ToLower(b.Name)
		if cmp := strings.Compare(aName, bName); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	return normalized, nil
}

func renderCustomProvidersBlock(providers []protocol.ProviderDefinition) []byte {
	var b strings.Builder
	b.WriteString("custom_providers:\n")
	for _, provider := range providers {
		b.WriteString("  - name: ")
		b.WriteString(provider.Name)
		b.WriteByte('\n')
		if provider.BaseURL == "" {
			b.WriteString("    base_url:\n")
		} else {
			b.WriteString("    base_url: ")
			b.WriteString(provider.BaseURL)
			b.WriteByte('\n')
		}
		if provider.APIKeyEnv != "" {
			b.WriteString("    api_key: ${")
			b.WriteString(provider.APIKeyEnv)
			b.WriteString("}\n")
		}
	}
	return []byte(b.String())
}

func hasMatchingProviderEntry(entries []protocol.ProviderCatalogEntry, want protocol.ProviderDefinition) bool {
	for _, entry := range entries {
		if entry.Name == want.Name && entry.BaseURL == want.BaseURL && entry.APIKeyEnv == want.APIKeyEnv {
			return true
		}
	}
	return false
}

func isJSONObjectConfig(contents []byte) bool {
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var decoded map[string]any
	return json.Unmarshal(trimmed, &decoded) == nil
}

func findCustomProvidersBlock(contents []byte) (start int, end int, found bool) {
	for pos := 0; pos < len(contents); {
		lineStart := pos
		lineEnd, next := nextYAMLLine(contents, pos)
		line := string(bytes.TrimSuffix(contents[lineStart:lineEnd], []byte("\r")))
		key, value, ok := topLevelYAMLKeyValue(line)
		if !ok || key != "custom_providers" {
			pos = next
			continue
		}
		if strings.TrimSpace(value) != "" {
			return lineStart, next, true
		}
		for scan := next; scan < len(contents); {
			scanLineEnd, scanNext := nextYAMLLine(contents, scan)
			scanLine := string(bytes.TrimSuffix(contents[scan:scanLineEnd], []byte("\r")))
			if _, _, ok := topLevelYAMLKeyValue(scanLine); ok {
				return lineStart, scan, true
			}
			scan = scanNext
		}
		return lineStart, len(contents), true
	}
	return 0, 0, false
}

func nextYAMLLine(contents []byte, start int) (lineEnd int, next int) {
	lineEnd = start
	for lineEnd < len(contents) && contents[lineEnd] != '\n' {
		lineEnd++
	}
	next = lineEnd
	if next < len(contents) && contents[next] == '\n' {
		next++
	}
	return lineEnd, next
}

func topLevelYAMLKeyValue(line string) (string, string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return "", "", false
	}
	text := strings.TrimSpace(line)
	if text == "" || strings.HasPrefix(text, "#") {
		return "", "", false
	}
	return yamlKeyValue(text)
}
