package config

import (
	"fmt"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const maxProviderModelValueLength = 128

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
