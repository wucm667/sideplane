package protocol

import "time"

// AuditEvent records an operator or sidecar-visible control-plane action.
type AuditEvent struct {
	ID         string    `json:"id"`
	Actor      string    `json:"actor"`
	ActorName  string    `json:"actorName,omitempty"`
	Action     string    `json:"action"`
	TargetNode string    `json:"targetNode,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListAuditEventsResponse is the bounded audit event list response.
type ListAuditEventsResponse struct {
	Events []AuditEvent `json:"events"`
}
