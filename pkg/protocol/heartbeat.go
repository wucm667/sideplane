package protocol

import "time"

// NodeState is the server's freshness view of a node.
type NodeState string

const (
	NodeStateFresh   NodeState = "fresh"
	NodeStateStale   NodeState = "stale"
	NodeStateOffline NodeState = "offline"
)

// Deployment modes describe how a managed runtime is deployed/managed. They are
// adapter-derived from existing configuration and are best-effort; an empty
// string means the adapter could not determine the deployment mode.
const (
	// DeploymentModeContainer means the runtime is managed as a Docker container.
	DeploymentModeContainer = "container"
	// DeploymentModeSystemd means the runtime is managed as a systemd unit.
	DeploymentModeSystemd = "systemd"
	// DeploymentModeLocal means the runtime is a plain local process / config file.
	DeploymentModeLocal = "local"
)

// RuntimeStatus is the lightweight status summary for a managed runtime.
type RuntimeStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Version string `json:"version,omitempty"`
	// DeploymentMode is how the adapter manages the runtime: one of
	// DeploymentModeContainer, DeploymentModeSystemd, or DeploymentModeLocal.
	// It is empty when the deployment mode is unknown.
	DeploymentMode string        `json:"deploymentMode,omitempty"`
	State          string        `json:"state,omitempty"`
	Provider       string        `json:"provider,omitempty"`
	Model          string        `json:"model,omitempty"`
	ConfigHash     string        `json:"configHash,omitempty"`
	Health         RuntimeHealth `json:"health,omitzero"`
	LastError      string        `json:"lastError,omitempty"`
	Warnings       []string      `json:"warnings,omitempty"`
	// Outdated is true only when both actual and expected runtime versions are
	// known and differ.
	Outdated bool `json:"outdated,omitempty"`
}

// NodeStatus is the heartbeat-derived status the server exposes for a node.
type NodeStatus struct {
	NodeID          string            `json:"nodeId"`
	Hostname        string            `json:"hostname,omitempty"`
	State           NodeState         `json:"state"`
	SidecarVersion  string            `json:"sidecarVersion,omitempty"`
	LastHeartbeatAt time.Time         `json:"lastHeartbeatAt"`
	Runtimes        []RuntimeStatus   `json:"runtimes,omitempty"`
	ConfigHash      string            `json:"configHash,omitempty"`
	LastError       string            `json:"lastError,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Maintenance     bool              `json:"maintenance,omitempty"`
}

// NodeStatusWithDrift is the operator-facing node status view.
type NodeStatusWithDrift struct {
	NodeStatus
	Drift bool `json:"drift"`
	// SidecarOutdated is true when an expected sidecar version is configured and
	// this node's reported sidecarVersion differs from it.
	SidecarOutdated bool `json:"sidecarOutdated,omitempty"`
}

// ServerSettings holds operator-tunable server settings.
type ServerSettings struct {
	// ExpectedSidecarVersion, when set, flags nodes running a different sidecar
	// version as outdated. Empty disables the check.
	ExpectedSidecarVersion string `json:"expectedSidecarVersion"`
	// ExpectedRuntimeVersions maps runtime type to the operator-configured
	// expected runtime version. Empty or missing entries disable checks.
	ExpectedRuntimeVersions map[string]string `json:"expectedRuntimeVersions"`
}

// UpdateServerSettingsRequest updates operator-tunable server settings.
type UpdateServerSettingsRequest struct {
	ExpectedSidecarVersion  string            `json:"expectedSidecarVersion"`
	ExpectedRuntimeVersions map[string]string `json:"expectedRuntimeVersions,omitempty"`
}

// ListNodesResponse is a paginated fleet inventory response.
type ListNodesResponse struct {
	Nodes  []NodeStatusWithDrift `json:"nodes"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

// NodeLabelsRequest replaces operator-managed labels for a node.
type NodeLabelsRequest struct {
	Labels map[string]string `json:"labels"`
}

// NodeLabelsResponse returns operator-managed labels for a node.
type NodeLabelsResponse struct {
	NodeID string            `json:"nodeId"`
	Labels map[string]string `json:"labels"`
}

// NodeMaintenanceRequest updates operator-set node maintenance mode.
type NodeMaintenanceRequest struct {
	Maintenance bool `json:"maintenance"`
}

// NodeMaintenanceResponse returns operator-set node maintenance mode.
type NodeMaintenanceResponse struct {
	NodeID      string `json:"nodeId"`
	Maintenance bool   `json:"maintenance"`
}

// BulkNodeLabelsRequest merges Labels onto every node matched by Selector or
// NodeIDs. Exactly one of Selector or NodeIDs must be set, and Labels must be
// non-empty. Existing labels with other keys are preserved.
type BulkNodeLabelsRequest struct {
	Selector           map[string]string `json:"selector,omitempty"`
	NodeIDs            []string          `json:"nodeIds,omitempty"`
	Labels             map[string]string `json:"labels"`
	IncludeMaintenance bool              `json:"includeMaintenance,omitempty"`
}

// BulkNodeLabelsResponse returns the nodes updated and the applied label keys.
type BulkNodeLabelsResponse struct {
	NodeIDs []string `json:"nodeIds"`
	Updated int      `json:"updated"`
}

// HeartbeatRequest is sent by sidecars to report their current lightweight state.
type HeartbeatRequest struct {
	NodeID         string          `json:"nodeId"`
	Hostname       string          `json:"hostname,omitempty"`
	SidecarVersion string          `json:"sidecarVersion,omitempty"`
	SentAt         time.Time       `json:"sentAt"`
	Runtimes       []RuntimeStatus `json:"runtimes,omitempty"`
	ConfigHash     string          `json:"configHash,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
}

// HeartbeatResponse confirms whether the server accepted a heartbeat.
type HeartbeatResponse struct {
	Accepted   bool       `json:"accepted"`
	ServerTime time.Time  `json:"serverTime"`
	Node       NodeStatus `json:"node"`
}
