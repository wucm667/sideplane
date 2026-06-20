package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	defaultAlertQueueSize   = 256
	defaultAlertWorkers     = 2
	defaultAlertMaxAttempts = 4
	defaultAlertBackoff     = 250 * time.Millisecond
	defaultAlertTimeout     = 5 * time.Second
	// AlertSignatureHeader carries the optional HMAC-SHA256 payload signature.
	AlertSignatureHeader = "X-Sideplane-Signature"
)

// AlertDispatcherConfig configures the outbound alert webhook dispatcher.
type AlertDispatcherConfig struct {
	Store       store.Store
	Events      *EventHub
	Logger      *slog.Logger
	Client      *http.Client
	QueueSize   int
	Workers     int
	MaxAttempts int
	Backoff     time.Duration
	Timeout     time.Duration
	Now         func() time.Time
	Metrics     *Metrics
}

type alertDelivery struct {
	webhookID string
	target    store.AlertWebhookTarget
	payload   protocol.AlertWebhookPayload
}

// alertDispatcher delivers webhook payloads for fleet alert events. It owns a
// bounded queue so that slow or failing receivers never block event producers.
type alertDispatcher struct {
	store       store.Store
	events      *EventHub
	logger      *slog.Logger
	client      *http.Client
	queue       chan alertDelivery
	workers     int
	maxAttempts int
	backoff     time.Duration
	now         func() time.Time
	metrics     *Metrics
}

// StartAlertDispatcher subscribes to the event hub and delivers alert webhooks
// until ctx is done. It is a no-op when no store is configured.
func StartAlertDispatcher(ctx context.Context, cfg AlertDispatcherConfig) {
	if cfg.Store == nil {
		return
	}
	d := newAlertDispatcher(cfg)
	go d.runSubscriber(ctx)
	for i := 0; i < d.workers; i++ {
		go d.runWorker(ctx)
	}
}

func newAlertDispatcher(cfg AlertDispatcherConfig) *alertDispatcher {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultAlertQueueSize
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultAlertWorkers
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultAlertMaxAttempts
	}
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = defaultAlertBackoff
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultAlertTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = discardLogger()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &alertDispatcher{
		store:       cfg.Store,
		events:      eventHubOrDefault(cfg.Events),
		logger:      logger,
		client:      client,
		queue:       make(chan alertDelivery, queueSize),
		workers:     workers,
		maxAttempts: maxAttempts,
		backoff:     backoff,
		now:         now,
		metrics:     cfg.Metrics,
	}
}

func (d *alertDispatcher) runSubscriber(ctx context.Context) {
	events := d.events.subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			d.handleEvent(ctx, event.Name, event.Data)
		}
	}
}

func (d *alertDispatcher) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery := <-d.queue:
			d.deliver(ctx, delivery)
		}
	}
}

// handleEvent maps a server event to an alert event and enqueues a delivery for
// each subscribed webhook. The store lookup is quick; HTTP delivery happens off
// this path in workers, so producers are never blocked.
func (d *alertDispatcher) handleEvent(ctx context.Context, name string, data []byte) {
	event, payload, ok := mapAlertEvent(name, data, d.now().UTC())
	if !ok {
		return
	}
	if d.suppressMaintenanceNodeAlert(ctx, payload) {
		return
	}
	targets, err := d.store.ListAlertWebhookTargets(ctx, event)
	if err != nil {
		d.logger.Warn("list alert webhook targets failed", "event", event, "error", err)
		return
	}
	for _, target := range targets {
		d.enqueue(alertDelivery{webhookID: target.ID, target: target, payload: payload})
	}
}

func (d *alertDispatcher) suppressMaintenanceNodeAlert(ctx context.Context, payload protocol.AlertWebhookPayload) bool {
	switch payload.Event {
	case protocol.AlertEventNodeOffline, protocol.AlertEventNodeDrift:
	default:
		return false
	}
	if payload.NodeID == "" {
		return false
	}
	maintenance, err := d.store.GetNodeMaintenance(ctx, payload.NodeID)
	if err != nil {
		if !errors.Is(err, store.ErrNodeNotFound) {
			d.logger.Warn("read node maintenance for alert suppression failed", "node_id", payload.NodeID, "error", err)
		}
		return false
	}
	return maintenance
}

// enqueue adds a delivery without blocking; a full queue drops the delivery so
// that a backlog never stalls event producers.
func (d *alertDispatcher) enqueue(delivery alertDelivery) {
	select {
	case d.queue <- delivery:
	default:
		d.metrics.IncWebhookDelivery("dropped")
		d.logger.Warn("alert webhook queue full; dropping delivery", "webhook", delivery.webhookID, "event", delivery.payload.Event)
	}
}

// deliver POSTs the payload with bounded retries on transient failures.
func (d *alertDispatcher) deliver(ctx context.Context, delivery alertDelivery) {
	body, err := json.Marshal(delivery.payload)
	if err != nil {
		d.metrics.IncWebhookDelivery("failed")
		return
	}
	signature := ""
	if delivery.target.Secret != "" {
		mac := hmac.New(sha256.New, []byte(delivery.target.Secret))
		mac.Write(body)
		signature = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		ok, retryable := d.attempt(ctx, delivery.target.URL, body, signature)
		if ok {
			d.metrics.IncWebhookDelivery("succeeded")
			return
		}
		if !retryable {
			d.metrics.IncWebhookDelivery("failed")
			d.logger.Warn("alert webhook permanent failure", "webhook", delivery.webhookID, "event", delivery.payload.Event)
			return
		}
		if attempt < d.maxAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d.backoff * time.Duration(attempt)):
			}
		}
	}
	d.metrics.IncWebhookDelivery("dropped")
	d.logger.Warn("alert webhook dropped after retries", "webhook", delivery.webhookID, "event", delivery.payload.Event, "attempts", d.maxAttempts)
}

// attempt performs a single delivery, reporting success and whether a failure is
// retryable. Network errors, 5xx, and 429 are retryable; other 4xx are not.
func (d *alertDispatcher) attempt(ctx context.Context, url string, body []byte, signature string) (bool, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sideplane-alert/1")
	if signature != "" {
		req.Header.Set(AlertSignatureHeader, signature)
	}
	res, err := d.client.Do(req)
	if err != nil {
		return false, true
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return true, false
	case res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests:
		return false, true
	default:
		return false, false
	}
}

// mapAlertEvent translates a hub event into an alert event and payload. Only
// node-offline/drift and rollout-paused/failed transitions map to alerts.
func mapAlertEvent(name string, data []byte, now time.Time) (protocol.AlertEventType, protocol.AlertWebhookPayload, bool) {
	switch name {
	case "node":
		var ev struct {
			NodeID string `json:"nodeId"`
			State  string `json:"state"`
			Drift  bool   `json:"drift"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return "", protocol.AlertWebhookPayload{}, false
		}
		switch {
		case ev.State == string(protocol.NodeStateOffline):
			return protocol.AlertEventNodeOffline, protocol.AlertWebhookPayload{Event: protocol.AlertEventNodeOffline, NodeID: ev.NodeID, Detail: "node offline", OccurredAt: now}, true
		case ev.Drift:
			return protocol.AlertEventNodeDrift, protocol.AlertWebhookPayload{Event: protocol.AlertEventNodeDrift, NodeID: ev.NodeID, Detail: "node configuration drift", OccurredAt: now}, true
		}
	case "rollout":
		var ev struct {
			RolloutID string `json:"rolloutId"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return "", protocol.AlertWebhookPayload{}, false
		}
		switch ev.State {
		case string(protocol.RolloutStatePaused):
			return protocol.AlertEventRolloutPaused, protocol.AlertWebhookPayload{Event: protocol.AlertEventRolloutPaused, RolloutID: ev.RolloutID, Detail: "rollout paused", OccurredAt: now}, true
		case string(protocol.RolloutStateFailed):
			return protocol.AlertEventRolloutFailed, protocol.AlertWebhookPayload{Event: protocol.AlertEventRolloutFailed, RolloutID: ev.RolloutID, Detail: "rollout failed", OccurredAt: now}, true
		}
	}
	return "", protocol.AlertWebhookPayload{}, false
}

// AlertSignature computes the hex HMAC-SHA256 signature header value for body.
func AlertSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
