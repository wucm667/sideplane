package protocol

import "time"

// RuntimeConfigSnapshot is a read-only allowlisted view of a runtime config.
type RuntimeConfigSnapshot struct {
	RuntimeName string                 `json:"runtimeName"`
	RuntimeType string                 `json:"runtimeType"`
	ConfigPath  string                 `json:"configPath,omitempty"`
	Source      string                 `json:"source,omitempty"`
	Profile     string                 `json:"profile,omitempty"`
	Provider    string                 `json:"provider,omitempty"`
	Model       string                 `json:"model,omitempty"`
	Providers   []ProviderCatalogEntry `json:"providers,omitempty"`
	ConfigHash  string                 `json:"configHash,omitempty"`
	Health      RuntimeHealth          `json:"health,omitzero"`
	Warnings    []string               `json:"warnings,omitempty"`
}

// ProviderCatalogEntry is a read-only, allowlisted view of one provider found in
// a runtime config. It NEVER carries a literal secret: only the env var name
// referenced by an ${ENV} api_key is surfaced in APIKeyEnv.
type ProviderCatalogEntry struct {
	Name      string   `json:"name"`
	BaseURL   string   `json:"baseURL,omitempty"`
	Models    []string `json:"models,omitempty"`
	APIKeyEnv string   `json:"apiKeyEnv,omitempty"`
	Active    bool     `json:"active,omitempty"`
}

// RuntimeHealthState is the read-only liveness state reported by an adapter.
type RuntimeHealthState string

const (
	RuntimeHealthHealthy  RuntimeHealthState = "healthy"
	RuntimeHealthDegraded RuntimeHealthState = "degraded"
	RuntimeHealthUnknown  RuntimeHealthState = "unknown"
)

// RuntimeHealth summarizes local, read-only runtime health checks.
type RuntimeHealth struct {
	State  RuntimeHealthState `json:"state"`
	Reason string             `json:"reason,omitempty"`
}

// ProviderModelConfig is the MVP desired config surface for runtime selection.
type ProviderModelConfig struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// ProviderDefinition is a managed provider entry in the desired catalog.
// APIKey is stored/transmitted in PLAINTEXT by design (operator-owned secret
// security); read paths must redact it before returning desired config.
type ProviderDefinition struct {
	Name    string   `json:"name"`
	BaseURL string   `json:"baseURL,omitempty"`
	Models  []string `json:"models,omitempty"`
	APIKey  string   `json:"apiKey,omitempty"`
}

// DesiredConfig layers desired provider/model settings.
type DesiredConfig struct {
	Global                      ProviderModelConfig            `json:"global,omitempty"`
	NodeOverrides               map[string]ProviderModelConfig `json:"nodeOverrides,omitempty"`
	RuntimeProfileOverrides     map[string]ProviderModelConfig `json:"runtimeProfileOverrides,omitempty"`
	NodeRuntimeProfileOverrides map[string]ProviderModelConfig `json:"nodeRuntimeProfileOverrides,omitempty"`

	GlobalProviders             []ProviderDefinition            `json:"globalProviders,omitempty"`
	NodeProviders               map[string][]ProviderDefinition `json:"nodeProviders,omitempty"`
	RuntimeProfileProviders     map[string][]ProviderDefinition `json:"runtimeProfileProviders,omitempty"`
	NodeRuntimeProfileProviders map[string][]ProviderDefinition `json:"nodeRuntimeProfileProviders,omitempty"`
}

// DesiredConfigHistoryEntry is an immutable desired-config version.
type DesiredConfigHistoryEntry struct {
	ID          string        `json:"id"`
	Config      DesiredConfig `json:"config"`
	DesiredHash string        `json:"desiredHash,omitempty"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	Actor       string        `json:"actor"`
}

// ListDesiredConfigHistoryResponse is a paginated desired-config history page.
type ListDesiredConfigHistoryResponse struct {
	History []DesiredConfigHistoryEntry `json:"history"`
	Total   int                         `json:"total"`
	Limit   int                         `json:"limit"`
	Offset  int                         `json:"offset"`
}

// RevertDesiredConfigRequest selects a desired-config history entry to restore.
type RevertDesiredConfigRequest struct {
	HistoryID string `json:"historyId"`
}

// RevertDesiredConfigResponse returns the new current desired config and
// appended history entry created by the revert.
type RevertDesiredConfigResponse struct {
	Desired DesiredConfig             `json:"desired"`
	History DesiredConfigHistoryEntry `json:"history"`
}

// EffectiveConfigResponse describes server-computed desired config and diff.
type EffectiveConfigResponse struct {
	NodeID      string                 `json:"nodeId"`
	RuntimeType string                 `json:"runtimeType,omitempty"`
	Profile     string                 `json:"profile,omitempty"`
	Effective   ProviderModelConfig    `json:"effective"`
	DesiredHash string                 `json:"desiredHash,omitempty"`
	Actual      *RuntimeConfigSnapshot `json:"actual,omitempty"`
	Diff        []ConfigDiffEntry      `json:"diff"`
}

// EffectiveConfigPreviewRequest asks the server to compute an effective config
// and diff for a proposed target-specific override without persisting it.
type EffectiveConfigPreviewRequest struct {
	NodeID      string              `json:"nodeId"`
	RuntimeType string              `json:"runtimeType,omitempty"`
	Profile     string              `json:"profile,omitempty"`
	Desired     ProviderModelConfig `json:"desired"`
}

// ConfigDiffEntry describes one read-only desired-vs-actual config difference.
type ConfigDiffEntry struct {
	Field   string `json:"field"`
	Actual  string `json:"actual,omitempty"`
	Desired string `json:"desired,omitempty"`
	Change  string `json:"change"`
}

const (
	// ConfigDiffChangeUpdate means actual and desired values differ.
	ConfigDiffChangeUpdate = "update"
	// ConfigDiffChangeMissingActual means no actual config snapshot exists.
	ConfigDiffChangeMissingActual = "missingActual"
)

// ConfigApplyRequest is the operator request to create a signed config apply
// job for a node. DryRun defaults to true when omitted so that the safe path is
// the default; a live apply must be requested explicitly.
type ConfigApplyRequest struct {
	RuntimeType string `json:"runtimeType,omitempty"`
	Profile     string `json:"profile,omitempty"`
	DryRun      *bool  `json:"dryRun,omitempty"`
}
