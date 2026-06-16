package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// NewHandler returns the Sideplane server HTTP handler.
func NewHandler() http.Handler {
	return NewHandlerWithStore(store.NewMemoryNodeStore())
}

// NewHandlerWithStore returns a Sideplane server HTTP handler backed by store.
func NewHandlerWithStore(nodeStore store.NodeStore) http.Handler {
	handler := &handler{store: nodeStore}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", jsonStatusHandler("ok"))
	mux.HandleFunc("/readyz", jsonStatusHandler("ready"))
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/api/heartbeat", handler.heartbeat)
	mux.HandleFunc("/api/nodes", handler.nodes)
	return mux
}

type handler struct {
	store store.NodeStore
}

func jsonStatusHandler(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": status}); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# Sideplane metrics placeholder\n"))
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()

	var req protocol.HeartbeatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid heartbeat JSON", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.NodeID) == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	node, err := h.store.RecordHeartbeat(r.Context(), req, now)
	if err != nil {
		http.Error(w, "record heartbeat", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{
		Accepted:   true,
		ServerTime: now,
		Node:       node,
	})
}

func (h *handler) nodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	nodes, err := h.store.ListNodes(r.Context())
	if err != nil {
		http.Error(w, "list nodes", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, nodes)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
