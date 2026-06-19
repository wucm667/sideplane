package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const eventClientBuffer = 16

var defaultEventHub = NewEventHub()

type serverEvent struct {
	Name string
	Data []byte
}

// EventHub is an in-process, bounded fan-out hub for small server-sent events.
type EventHub struct {
	mu      sync.Mutex
	nextID  int
	clients map[int]chan serverEvent
}

// NewEventHub creates an empty event hub.
func NewEventHub() *EventHub {
	return &EventHub{clients: map[int]chan serverEvent{}}
}

func eventHubOrDefault(hub *EventHub) *EventHub {
	if hub != nil {
		return hub
	}
	return defaultEventHub
}

func (h *EventHub) subscribe(ctx context.Context) <-chan serverEvent {
	if h == nil {
		ch := make(chan serverEvent)
		close(ch)
		return ch
	}
	ch := make(chan serverEvent, eventClientBuffer)
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.clients[id] = ch
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.remove(id)
	}()
	return ch
}

func (h *EventHub) publish(name string, payload any) {
	if h == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	event := serverEvent{Name: name, Data: data}

	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.clients {
		select {
		case ch <- event:
		default:
			close(ch)
			delete(h.clients, id)
		}
	}
}

func (h *EventHub) remove(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.clients[id]; ok {
		close(ch)
		delete(h.clients, id)
	}
}

func (h *EventHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *handler) eventsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if _, err := fmt.Fprint(w, ": sideplane\n\n"); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}

	events := h.events.subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, event.Data); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

func publishNodeEvent(hub *EventHub, node protocol.NodeStatus) {
	hub.publish("node", map[string]string{
		"nodeId": node.NodeID,
		"state":  string(node.State),
	})
}

func publishJobEvent(hub *EventHub, job protocol.Job) {
	hub.publish("job", map[string]string{
		"jobId":  job.ID,
		"nodeId": job.NodeID,
		"status": string(job.Status),
	})
}

func publishRolloutEvent(hub *EventHub, rollout protocol.Rollout) {
	hub.publish("rollout", map[string]string{
		"rolloutId": rollout.ID,
		"state":     string(rollout.State),
	})
}
