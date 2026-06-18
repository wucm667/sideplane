package config

import (
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// SnapshotInput contains the allowlisted runtime config fields that may cross
// from a sidecar to the server.
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
}

// NewRuntimeConfigSnapshot builds a protocol snapshot from allowlisted fields only.
func NewRuntimeConfigSnapshot(input SnapshotInput) protocol.RuntimeConfigSnapshot {
	return protocol.RuntimeConfigSnapshot{
		RuntimeName: strings.TrimSpace(input.RuntimeName),
		RuntimeType: strings.TrimSpace(input.RuntimeType),
		ConfigPath:  strings.TrimSpace(input.ConfigPath),
		Source:      strings.TrimSpace(input.Source),
		Profile:     strings.TrimSpace(input.Profile),
		Provider:    strings.TrimSpace(input.Provider),
		Model:       strings.TrimSpace(input.Model),
		ConfigHash:  strings.TrimSpace(input.ConfigHash),
		Warnings:    append([]string(nil), input.Warnings...),
	}
}
