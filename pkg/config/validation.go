package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const maxProviderModelValueLength = 128
const maxProviderBaseURLLength = 2048
const maxProviderAPIKeyLength = 4096

// ValidateProviderModelSelection validates a complete provider/model pair for
// rendering into runtime configuration.
func ValidateProviderModelSelection(selection protocol.ProviderModelConfig) error {
	if err := validateProviderModelValue("provider", selection.Provider, true); err != nil {
		return err
	}
	if err := validateProviderModelValue("model", selection.Model, true); err != nil {
		return err
	}
	return nil
}

// ValidateDesiredConfigValues validates all non-empty provider/model values in
// a layered desired config without requiring every layer to set both fields.
func ValidateDesiredConfigValues(desired protocol.DesiredConfig) error {
	if err := validateOptionalProviderModel("global", desired.Global); err != nil {
		return err
	}
	for key, value := range desired.NodeOverrides {
		if err := validateOptionalProviderModel("nodeOverrides["+key+"]", value); err != nil {
			return err
		}
	}
	for key, value := range desired.RuntimeProfileOverrides {
		if err := validateOptionalProviderModel("runtimeProfileOverrides["+key+"]", value); err != nil {
			return err
		}
	}
	for key, value := range desired.NodeRuntimeProfileOverrides {
		if err := validateOptionalProviderModel("nodeRuntimeProfileOverrides["+key+"]", value); err != nil {
			return err
		}
	}
	if err := validateProviderDefinitions("globalProviders", desired.GlobalProviders); err != nil {
		return err
	}
	for _, key := range sortedProviderDefinitionMapKeys(desired.NodeProviders) {
		if err := validateProviderDefinitions("nodeProviders["+key+"]", desired.NodeProviders[key]); err != nil {
			return err
		}
	}
	for _, key := range sortedProviderDefinitionMapKeys(desired.RuntimeProfileProviders) {
		if err := validateProviderDefinitions("runtimeProfileProviders["+key+"]", desired.RuntimeProfileProviders[key]); err != nil {
			return err
		}
	}
	for _, key := range sortedProviderDefinitionMapKeys(desired.NodeRuntimeProfileProviders) {
		if err := validateProviderDefinitions("nodeRuntimeProfileProviders["+key+"]", desired.NodeRuntimeProfileProviders[key]); err != nil {
			return err
		}
	}
	return nil
}

func validateOptionalProviderModel(path string, value protocol.ProviderModelConfig) error {
	if err := validateProviderModelValue(path+".provider", value.Provider, false); err != nil {
		return err
	}
	if err := validateProviderModelValue(path+".model", value.Model, false); err != nil {
		return err
	}
	return nil
}

func validateProviderDefinitions(path string, providers []protocol.ProviderDefinition) error {
	names := map[string]struct{}{}
	for i, provider := range providers {
		providerPath := fmt.Sprintf("%s[%d]", path, i)
		if err := validateProviderModelValue(providerPath+".name", provider.Name, true); err != nil {
			return err
		}
		normalizedName := strings.ToLower(strings.TrimSpace(provider.Name))
		if _, ok := names[normalizedName]; ok {
			return fmt.Errorf("%s.name duplicates provider %q in this layer", providerPath, strings.TrimSpace(provider.Name))
		}
		names[normalizedName] = struct{}{}

		for modelIndex, model := range provider.Models {
			if err := validateProviderModelValue(fmt.Sprintf("%s.models[%d]", providerPath, modelIndex), model, true); err != nil {
				return err
			}
		}
		if err := validateProviderBaseURL(providerPath+".baseURL", provider.BaseURL); err != nil {
			return err
		}
		if err := validateProviderAPIKey(providerPath+".apiKey", provider.APIKey); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderBaseURL(field string, raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	if len(value) > maxProviderBaseURLLength {
		return fmt.Errorf("%s is too long", field)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s must be a valid http or https URL", field)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%s must use http or https", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	return nil
}

func validateProviderAPIKey(field string, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxProviderAPIKeyLength {
		return fmt.Errorf("%s is too long", field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains unsupported control character %q", field, r)
		}
	}
	return nil
}

func sortedProviderDefinitionMapKeys(values map[string][]protocol.ProviderDefinition) []string {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validateProviderModelValue(field string, raw string, required bool) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(value) > maxProviderModelValueLength {
		return fmt.Errorf("%s is too long", field)
	}
	for i, r := range value {
		if r > 127 || !isProviderModelChar(r) {
			return fmt.Errorf("%s contains unsupported character %q", field, r)
		}
		if i == 0 && !isASCIIAlnum(r) {
			return fmt.Errorf("%s must start with a letter or digit", field)
		}
	}
	return nil
}

func isProviderModelChar(r rune) bool {
	return isASCIIAlnum(r) || r == '.' || r == '_' || r == '/' || r == '-'
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
