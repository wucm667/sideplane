package protocol

import "time"

// AlertEventType is one of the fixed events that can trigger an outbound alert
// webhook delivery.
type AlertEventType string

const (
	// AlertEventNodeOffline fires when a node transitions to offline.
	AlertEventNodeOffline AlertEventType = "node.offline"
	// AlertEventNodeDrift fires when a node reports configuration drift.
	AlertEventNodeDrift AlertEventType = "node.drift"
	// AlertEventRolloutPaused fires when a rollout pauses.
	AlertEventRolloutPaused AlertEventType = "rollout.paused"
	// AlertEventRolloutFailed fires when a rollout fails.
	AlertEventRolloutFailed AlertEventType = "rollout.failed"
)

// KnownAlertEventTypes returns the fixed set of supported alert event types.
func KnownAlertEventTypes() []AlertEventType {
	return []AlertEventType{
		AlertEventNodeOffline,
		AlertEventNodeDrift,
		AlertEventRolloutPaused,
		AlertEventRolloutFailed,
	}
}

// ValidAlertEventType reports whether event is a known alert event type.
func ValidAlertEventType(event AlertEventType) bool {
	switch event {
	case AlertEventNodeOffline, AlertEventNodeDrift, AlertEventRolloutPaused, AlertEventRolloutFailed:
		return true
	default:
		return false
	}
}

// AlertWebhook is the operator-facing metadata for a configured outbound alert
// webhook. The signing secret is never returned after creation; HasSecret only
// reports whether one is set.
type AlertWebhook struct {
	ID        string           `json:"id"`
	URL       string           `json:"url"`
	Events    []AlertEventType `json:"events"`
	HasSecret bool             `json:"hasSecret"`
	Disabled  bool             `json:"disabled"`
	CreatedAt time.Time        `json:"createdAt"`
}

// CreateAlertWebhookRequest registers an outbound alert webhook. Secret is
// optional; when set, deliveries carry an HMAC-SHA256 signature header.
type CreateAlertWebhookRequest struct {
	URL    string           `json:"url"`
	Events []AlertEventType `json:"events"`
	Secret string           `json:"secret,omitempty"`
}

// CreateAlertWebhookResponse returns the created webhook metadata.
type CreateAlertWebhookResponse struct {
	Webhook AlertWebhook `json:"webhook"`
}

// ListAlertWebhooksResponse returns alert webhook metadata without secrets.
type ListAlertWebhooksResponse struct {
	Webhooks []AlertWebhook `json:"webhooks"`
}

// AlertWebhookPayload is the small JSON body delivered to alert webhooks. It
// never contains secrets.
type AlertWebhookPayload struct {
	Event     AlertEventType `json:"event"`
	NodeID    string         `json:"nodeId,omitempty"`
	RolloutID string         `json:"rolloutId,omitempty"`
	Detail    string         `json:"detail,omitempty"`
	OccurredAt time.Time     `json:"occurredAt"`
}
