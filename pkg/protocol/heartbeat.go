package protocol

import "time"

// NodeState is the server's freshness view of a node.
type NodeState string

const (
	NodeStateFresh   NodeState = "fresh"
	NodeStateStale   NodeState = "stale"
	NodeStateOffline NodeState = "offline"
)

// RuntimeStatus is the lightweight status summary for a managed runtime.
type RuntimeStatus struct {
	Name       string   `json:"name"`
	Type       string   `json:"type,omitempty"`
	Version    string   `json:"version,omitempty"`
	State      string   `json:"state,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Model      string   `json:"model,omitempty"`
	ConfigHash string   `json:"configHash,omitempty"`
	LastError  string   `json:"lastError,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// NodeStatus is the heartbeat-derived status the server exposes for a node.
type NodeStatus struct {
	NodeID          string          `json:"nodeId"`
	Hostname        string          `json:"hostname,omitempty"`
	State           NodeState       `json:"state"`
	SidecarVersion  string          `json:"sidecarVersion,omitempty"`
	LastHeartbeatAt time.Time       `json:"lastHeartbeatAt"`
	Runtimes        []RuntimeStatus `json:"runtimes,omitempty"`
	ConfigHash      string          `json:"configHash,omitempty"`
	LastError       string          `json:"lastError,omitempty"`
}

// NodeStatusWithDrift is the operator-facing node status view.
type NodeStatusWithDrift struct {
	NodeStatus
	Drift bool `json:"drift"`
}

// ListNodesResponse is a paginated fleet inventory response.
type ListNodesResponse struct {
	Nodes  []NodeStatusWithDrift `json:"nodes"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
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
