package hermes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

var apiKeyEnvPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

type activeModelSelection struct {
	provider string
	model    string
	baseURL  string
}

type providerCatalogBuilder struct {
	active activeModelSelection
	seen   map[string]*protocol.ProviderCatalogEntry
}

// EnumerateProviders returns a read-only provider/model catalog from a Hermes
// config. Literal api_key values are never copied into the output.
func EnumerateProviders(contents []byte) []protocol.ProviderCatalogEntry {
	if len(bytes.TrimSpace(contents)) == 0 {
		return nil
	}

	builder := providerCatalogBuilder{
		active: activeModelFromConfig(contents),
		seen:   map[string]*protocol.ProviderCatalogEntry{},
	}

	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err == nil {
		builder.addJSONProviders(decoded)
	} else {
		builder.addYAMLProviders(contents)
	}

	if builder.active.provider != "" && !builder.hasProviderNamed(builder.active.provider) {
		builder.add(protocol.ProviderCatalogEntry{
			Name:    builder.active.provider,
			BaseURL: builder.active.baseURL,
			Models:  []string{builder.active.model},
			Active:  true,
		})
	}

	return builder.entries()
}

func activeModelFromConfig(contents []byte) activeModelSelection {
	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err == nil {
		if model, ok := decoded["model"].(map[string]any); ok {
			return activeModelSelection{
				provider: jsonString(model, "provider"),
				model:    firstNonEmpty(jsonString(model, "default"), jsonString(model, "model")),
				baseURL:  firstNonEmpty(jsonString(model, "base_url"), jsonString(model, "api")),
			}
		}
	}

	provider, model, ok := ModelFields(contents)
	if !ok {
		return activeModelSelection{}
	}
	return activeModelSelection{
		provider: provider,
		model:    model,
		baseURL:  yamlModelBaseURL(contents),
	}
}

func (b *providerCatalogBuilder) addJSONProviders(decoded map[string]any) {
	if providers, ok := decoded["providers"].(map[string]any); ok {
		for _, key := range sortedMapKeys(providers) {
			if entry, ok := providerEntryFromJSON(key, providers[key]); ok {
				b.add(entry)
			}
		}
	}

	customProviders, ok := decoded["custom_providers"].([]any)
	if !ok {
		return
	}
	for _, raw := range customProviders {
		if entry, ok := customProviderEntryFromJSON(raw); ok {
			b.add(entry)
		}
	}
}

func providerEntryFromJSON(providerKey string, raw any) (protocol.ProviderCatalogEntry, bool) {
	entry := protocol.ProviderCatalogEntry{Name: strings.TrimSpace(providerKey)}
	fields, ok := raw.(map[string]any)
	if !ok {
		return entry, entry.Name != ""
	}

	if name := jsonString(fields, "name"); name != "" {
		entry.Name = name
	}
	entry.BaseURL = firstNonEmpty(jsonString(fields, "base_url"), jsonString(fields, "api"))
	entry.APIKeyEnv = apiKeyEnv(jsonString(fields, "api_key"))
	entry.Models = jsonModelKeys(fields["models"])
	return entry, entry.Name != ""
}

func customProviderEntryFromJSON(raw any) (protocol.ProviderCatalogEntry, bool) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return protocol.ProviderCatalogEntry{}, false
	}
	entry := protocol.ProviderCatalogEntry{
		Name:      jsonString(fields, "name"),
		BaseURL:   firstNonEmpty(jsonString(fields, "base_url"), jsonString(fields, "api")),
		APIKeyEnv: apiKeyEnv(jsonString(fields, "api_key")),
		Models:    jsonModelKeys(fields["models"]),
	}
	return entry, entry.Name != ""
}

func (b *providerCatalogBuilder) addYAMLProviders(contents []byte) {
	lines := scanCatalogYAMLLines(contents)
	for i := 0; i < len(lines); {
		line := lines[i]
		if line.indent != 0 {
			i++
			continue
		}
		key, value, ok := yamlKeyValue(line.text)
		if !ok {
			i++
			continue
		}
		switch key {
		case "providers":
			if value == "" {
				i = b.addYAMLProviderMap(lines, i+1)
				continue
			}
		case "custom_providers":
			if value == "" {
				i = b.addYAMLCustomProviders(lines, i+1)
				continue
			}
		}
		i++
	}
}

func (b *providerCatalogBuilder) addYAMLProviderMap(lines []catalogYAMLLine, start int) int {
	for i := start; i < len(lines); {
		line := lines[i]
		if line.indent == 0 {
			return i
		}
		if line.indent != 2 || strings.HasPrefix(line.text, "- ") {
			i++
			continue
		}

		providerKey, _, ok := yamlKeyValue(line.text)
		if !ok {
			i++
			continue
		}
		entry := protocol.ProviderCatalogEntry{Name: providerKey}
		baseURL := ""
		apiURL := ""

		i++
		for i < len(lines) && lines[i].indent > line.indent {
			child := lines[i]
			if child.indent != line.indent+2 {
				i++
				continue
			}
			key, value, ok := yamlKeyValue(child.text)
			if !ok {
				i++
				continue
			}
			switch key {
			case "name":
				if value != "" {
					entry.Name = value
				}
			case "base_url":
				baseURL = value
			case "api":
				apiURL = value
			case "api_key":
				entry.APIKeyEnv = apiKeyEnv(value)
			case "models":
				models, next := yamlNestedMapKeys(lines, i+1, child.indent)
				entry.Models = append(entry.Models, models...)
				i = next
				continue
			}
			i++
		}
		entry.BaseURL = firstNonEmpty(baseURL, apiURL)
		b.add(entry)
	}
	return len(lines)
}

func (b *providerCatalogBuilder) addYAMLCustomProviders(lines []catalogYAMLLine, start int) int {
	for i := start; i < len(lines); {
		line := lines[i]
		if line.indent == 0 {
			return i
		}
		item, ok := yamlListItem(line.text)
		if !ok {
			i++
			continue
		}

		entry := protocol.ProviderCatalogEntry{}
		baseURL := ""
		apiURL := ""
		if key, value, ok := yamlKeyValue(item); ok {
			applyYAMLProviderField(&entry, &baseURL, &apiURL, key, value)
		}

		i++
		for i < len(lines) && lines[i].indent > line.indent {
			child := lines[i]
			if child.indent != line.indent+2 {
				i++
				continue
			}
			key, value, ok := yamlKeyValue(child.text)
			if !ok {
				i++
				continue
			}
			if key == "models" {
				models, next := yamlNestedMapKeys(lines, i+1, child.indent)
				entry.Models = append(entry.Models, models...)
				i = next
				continue
			}
			applyYAMLProviderField(&entry, &baseURL, &apiURL, key, value)
			i++
		}
		entry.BaseURL = firstNonEmpty(baseURL, apiURL)
		b.add(entry)
	}
	return len(lines)
}

func applyYAMLProviderField(entry *protocol.ProviderCatalogEntry, baseURL *string, apiURL *string, key string, value string) {
	switch key {
	case "name":
		entry.Name = value
	case "base_url":
		*baseURL = value
	case "api":
		*apiURL = value
	case "api_key":
		entry.APIKeyEnv = apiKeyEnv(value)
	}
}

func (b *providerCatalogBuilder) add(entry protocol.ProviderCatalogEntry) {
	entry.Name = strings.TrimSpace(entry.Name)
	entry.BaseURL = strings.TrimSpace(entry.BaseURL)
	entry.APIKeyEnv = strings.TrimSpace(entry.APIKeyEnv)
	entry.Models = normalizeCatalogModels(entry.Models)
	if entry.Name == "" {
		return
	}
	if b.active.provider != "" && strings.EqualFold(entry.Name, b.active.provider) {
		entry.Active = true
		if entry.BaseURL == "" {
			entry.BaseURL = b.active.baseURL
		}
		entry.Models = normalizeCatalogModels(append(entry.Models, b.active.model))
	}

	key := catalogDedupeKey(entry)
	existing, ok := b.seen[key]
	if !ok {
		copied := entry
		b.seen[key] = &copied
		return
	}
	if existing.APIKeyEnv == "" {
		existing.APIKeyEnv = entry.APIKeyEnv
	}
	existing.Models = normalizeCatalogModels(append(existing.Models, entry.Models...))
	existing.Active = existing.Active || entry.Active
}

func (b *providerCatalogBuilder) entries() []protocol.ProviderCatalogEntry {
	if len(b.seen) == 0 {
		return nil
	}
	out := make([]protocol.ProviderCatalogEntry, 0, len(b.seen))
	for _, entry := range b.seen {
		copied := *entry
		copied.Models = normalizeCatalogModels(copied.Models)
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(out[i].Name)
		right := strings.ToLower(out[j].Name)
		if left == right {
			return out[i].BaseURL < out[j].BaseURL
		}
		return left < right
	})
	return out
}

func (b *providerCatalogBuilder) hasProviderNamed(name string) bool {
	for _, entry := range b.seen {
		if strings.EqualFold(entry.Name, name) {
			return true
		}
	}
	return false
}

func catalogDedupeKey(entry protocol.ProviderCatalogEntry) string {
	return strings.ToLower(strings.TrimSpace(entry.Name)) + "\x00" + strings.TrimSpace(entry.BaseURL)
}

func apiKeyEnv(value string) string {
	matches := apiKeyEnvPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func jsonString(fields map[string]any, key string) string {
	value, ok := fields[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func jsonModelKeys(raw any) []string {
	models, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(models))
	for key := range models {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func normalizeCatalogModels(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type catalogYAMLLine struct {
	indent int
	text   string
}

func scanCatalogYAMLLines(contents []byte) []catalogYAMLLine {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines []catalogYAMLLine
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r")
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		lines = append(lines, catalogYAMLLine{
			indent: yamlIndent(raw),
			text:   text,
		})
	}
	return lines
}

func yamlIndent(line string) int {
	indent := 0
	for _, r := range line {
		switch r {
		case ' ':
			indent++
		case '\t':
			indent += 2
		default:
			return indent
		}
	}
	return indent
}

func yamlKeyValue(text string) (string, string, bool) {
	idx := strings.Index(text, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(text[:idx])
	key = strings.Trim(key, "\"'")
	value := yamlValue(text[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func yamlListItem(text string) (string, bool) {
	if text == "-" {
		return "", true
	}
	if !strings.HasPrefix(text, "- ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, "- ")), true
}

func yamlValue(value string) string {
	value = strings.TrimSpace(trimYAMLInlineComment(value))
	value = strings.Trim(value, " ,")
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func trimYAMLInlineComment(value string) string {
	inSingle := false
	inDouble := false
	for i, r := range value {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
				return strings.TrimSpace(value[:i])
			}
		}
	}
	return value
}

func yamlNestedMapKeys(lines []catalogYAMLLine, start int, parentIndent int) ([]string, int) {
	var keys []string
	directIndent := -1
	i := start
	for i < len(lines) && lines[i].indent > parentIndent {
		if directIndent == -1 {
			directIndent = lines[i].indent
		}
		if lines[i].indent == directIndent && !strings.HasPrefix(lines[i].text, "- ") {
			key, _, ok := yamlKeyValue(lines[i].text)
			if ok && key != "" {
				keys = append(keys, key)
			}
		}
		i++
	}
	return normalizeCatalogModels(keys), i
}

func yamlModelBaseURL(contents []byte) string {
	inModel := false
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
		if value, ok := directChildValue(raw, "base_url"); ok {
			return value
		}
		if value, ok := directChildValue(raw, "api"); ok {
			return value
		}
	}
	return ""
}
