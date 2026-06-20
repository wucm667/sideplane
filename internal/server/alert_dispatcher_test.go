package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestAlertDispatcherSignsAndDeliversPayload(t *testing.T) {
	type received struct {
		signature string
		body      []byte
	}
	got := make(chan received, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{signature: r.Header.Get(AlertSignatureHeader), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	nodeStore := store.NewMemoryNodeStore()
	if _, err := nodeStore.CreateAlertWebhook(context.Background(), protocol.CreateAlertWebhookRequest{
		URL:    receiver.URL,
		Events: []protocol.AlertEventType{protocol.AlertEventRolloutPaused},
		Secret: "sign-me",
	}, time.Now()); err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	d := newAlertDispatcher(AlertDispatcherConfig{Store: nodeStore, Client: receiver.Client(), Backoff: time.Millisecond})
	d.handleEvent(context.Background(), "rollout", mustJSON(t, map[string]string{"rolloutId": "rollout_1", "state": "paused"}))
	// Drain the queue synchronously.
	select {
	case delivery := <-d.queue:
		d.deliver(context.Background(), delivery)
	case <-time.After(time.Second):
		t.Fatal("no delivery enqueued for rollout paused event")
	}

	select {
	case r := <-got:
		want := AlertSignature("sign-me", r.body)
		if r.signature != want {
			t.Fatalf("signature = %q, want %q", r.signature, want)
		}
		var payload protocol.AlertWebhookPayload
		if err := json.Unmarshal(r.body, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Event != protocol.AlertEventRolloutPaused || payload.RolloutID != "rollout_1" {
			t.Fatalf("payload = %+v, want rollout paused", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not get delivery")
	}
}

func TestAlertDispatcherRetriesOn5xxThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	d := newAlertDispatcher(AlertDispatcherConfig{Store: store.NewMemoryNodeStore(), Client: receiver.Client(), Backoff: time.Millisecond, MaxAttempts: 5})
	d.deliver(context.Background(), alertDelivery{
		webhookID: "whk_1",
		target:    store.AlertWebhookTarget{URL: receiver.URL},
		payload:   protocol.AlertWebhookPayload{Event: protocol.AlertEventRolloutFailed},
	})
	if got := calls.Load(); got != 3 {
		t.Fatalf("receiver calls = %d, want 3 (two 5xx then success)", got)
	}
}

func TestAlertDispatcherDropsAfterPersistentFailure(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer receiver.Close()

	d := newAlertDispatcher(AlertDispatcherConfig{Store: store.NewMemoryNodeStore(), Client: receiver.Client(), Backoff: time.Millisecond, MaxAttempts: 3})
	d.deliver(context.Background(), alertDelivery{
		webhookID: "whk_1",
		target:    store.AlertWebhookTarget{URL: receiver.URL},
		payload:   protocol.AlertWebhookPayload{Event: protocol.AlertEventNodeOffline},
	})
	if got := calls.Load(); got != 3 {
		t.Fatalf("receiver calls = %d, want exactly MaxAttempts=3", got)
	}
}

func TestAlertDispatcherDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer receiver.Close()

	d := newAlertDispatcher(AlertDispatcherConfig{Store: store.NewMemoryNodeStore(), Client: receiver.Client(), Backoff: time.Millisecond, MaxAttempts: 4})
	d.deliver(context.Background(), alertDelivery{
		webhookID: "whk_1",
		target:    store.AlertWebhookTarget{URL: receiver.URL},
		payload:   protocol.AlertWebhookPayload{Event: protocol.AlertEventNodeOffline},
	})
	if got := calls.Load(); got != 1 {
		t.Fatalf("receiver calls = %d, want 1 (4xx is permanent)", got)
	}
}

func TestAlertDispatcherEnqueueNeverBlocksProducers(t *testing.T) {
	d := newAlertDispatcher(AlertDispatcherConfig{Store: store.NewMemoryNodeStore(), QueueSize: 1})
	done := make(chan struct{})
	go func() {
		// More deliveries than the queue can hold; excess is dropped, not blocked.
		for i := 0; i < 100; i++ {
			d.enqueue(alertDelivery{webhookID: "whk_1", payload: protocol.AlertWebhookPayload{Event: protocol.AlertEventNodeOffline}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked when queue was full")
	}
}

func TestAlertDispatcherSlowReceiverDoesNotBlockHub(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	defer close(release)

	nodeStore := store.NewMemoryNodeStore()
	if _, err := nodeStore.CreateAlertWebhook(context.Background(), protocol.CreateAlertWebhookRequest{
		URL:    receiver.URL,
		Events: []protocol.AlertEventType{protocol.AlertEventRolloutFailed},
	}, time.Now()); err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := NewEventHub()
	StartAlertDispatcher(ctx, AlertDispatcherConfig{Store: nodeStore, Events: hub, Client: receiver.Client(), Workers: 1, Backoff: time.Millisecond})
	// Give the subscriber a moment to register.
	waitFor(t, func() bool { return hub.clientCount() == 1 })

	// Publishing many rollout-failed events must return promptly even though
	// the single worker is stuck on the slow receiver.
	publishDone := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			publishRolloutEvent(hub, protocol.Rollout{ID: "rollout_x", State: protocol.RolloutStateFailed})
		}
		close(publishDone)
	}()
	select {
	case <-publishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked behind slow webhook receiver")
	}
	// At least the first delivery should have reached the receiver.
	waitFor(t, func() bool { return calls.Load() >= 1 })
}

func TestAlertDispatcherShutdownStopsWorkers(t *testing.T) {
	d := newAlertDispatcher(AlertDispatcherConfig{Store: store.NewMemoryNodeStore()})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.runWorker(ctx)
	}()
	cancel()
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on context cancel")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
