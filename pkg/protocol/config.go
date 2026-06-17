package protocol

// RuntimeConfigSnapshot is a read-only, redacted view of a runtime config.
type RuntimeConfigSnapshot struct {
	RuntimeName    string            `json:"runtimeName"`
	RuntimeType    string            `json:"runtimeType"`
	ConfigPath     string            `json:"configPath,omitempty"`
	Source         string            `json:"source,omitempty"`
	Profile        string            `json:"profile,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Model          string            `json:"model,omitempty"`
	ConfigHash     string            `json:"configHash,omitempty"`
	Warnings       []string          `json:"warnings,omitempty"`
	RedactedValues map[string]string `json:"redactedValues,omitempty"`
}
