package config

import (
	"regexp"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// RedactedValue is the placeholder used for secret-like config values.
const RedactedValue = "[redacted]"

var secretLikeKey = regexp.MustCompile(`(?i)(secret|token|password|api[_-]?key|credential|authorization)`)

// SnapshotInput contains read-only runtime config fields before redaction.
type SnapshotInput struct {
	RuntimeName string
	RuntimeType string
	ConfigPath  string
	Source      string
	Profile     string
	Provider    string
	Model       string
	ConfigHash  string
	Warnings    []string
	Values      map[string]string
}

// NewRuntimeConfigSnapshot builds a protocol snapshot with secret-like values redacted.
func NewRuntimeConfigSnapshot(input SnapshotInput) protocol.RuntimeConfigSnapshot {
	return protocol.RuntimeConfigSnapshot{
		RuntimeName:    strings.TrimSpace(input.RuntimeName),
		RuntimeType:    strings.TrimSpace(input.RuntimeType),
		ConfigPath:     strings.TrimSpace(input.ConfigPath),
		Source:         strings.TrimSpace(input.Source),
		Profile:        strings.TrimSpace(input.Profile),
		Provider:       strings.TrimSpace(input.Provider),
		Model:          strings.TrimSpace(input.Model),
		ConfigHash:     strings.TrimSpace(input.ConfigHash),
		Warnings:       append([]string(nil), input.Warnings...),
		RedactedValues: RedactValues(input.Values),
	}
}

// RedactValues returns a copy of values with obvious secret-like keys redacted.
func RedactValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if secretLikeKey.MatchString(key) {
			redacted[key] = RedactedValue
			continue
		}
		redacted[key] = value
	}
	return redacted
}
