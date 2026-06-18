package protocol

// RuntimeConfigSnapshot is a read-only allowlisted view of a runtime config.
type RuntimeConfigSnapshot struct {
	RuntimeName string   `json:"runtimeName"`
	RuntimeType string   `json:"runtimeType"`
	ConfigPath  string   `json:"configPath,omitempty"`
	Source      string   `json:"source,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model,omitempty"`
	ConfigHash  string   `json:"configHash,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// ProviderModelConfig is the MVP desired config surface for runtime selection.
type ProviderModelConfig struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// DesiredConfig layers desired provider/model settings.
type DesiredConfig struct {
	Global                  ProviderModelConfig            `json:"global,omitempty"`
	NodeOverrides           map[string]ProviderModelConfig `json:"nodeOverrides,omitempty"`
	RuntimeProfileOverrides map[string]ProviderModelConfig `json:"runtimeProfileOverrides,omitempty"`
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
