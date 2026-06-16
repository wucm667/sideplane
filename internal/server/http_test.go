package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ok")
}

func TestReadyz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ready")
}

func TestMetricsPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := rec.Body.String(); !strings.Contains(got, "Sideplane metrics placeholder") {
		t.Fatalf("metrics body = %q, want placeholder text", got)
	}
}

func TestHeartbeatRecordsNode(t *testing.T) {
	body := protocol.HeartbeatRequest{
		NodeID:         "node-1",
		Hostname:       "worker-a",
		SidecarVersion: "dev",
		SentAt:         time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC),
		Runtimes: []protocol.RuntimeStatus{
			{
				Name:       "default",
				Type:       "hermes",
				State:      "running",
				Provider:   "openai",
				Model:      "gpt-5",
				ConfigHash: "sha256:abc",
			},
		},
		ConfigHash: "sha256:node",
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode heartbeat: %v", err)
	}

	handler := NewHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", &buf)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var heartbeatResp protocol.HeartbeatResponse
	if err := json.NewDecoder(rec.Body).Decode(&heartbeatResp); err != nil {
		t.Fatalf("decode heartbeat response: %v", err)
	}
	if !heartbeatResp.Accepted {
		t.Fatalf("accepted = false, want true")
	}
	if heartbeatResp.Node.NodeID != "node-1" {
		t.Fatalf("nodeId = %q, want node-1", heartbeatResp.Node.NodeID)
	}
	if heartbeatResp.Node.State != protocol.NodeStateFresh {
		t.Fatalf("node state = %q, want fresh", heartbeatResp.Node.State)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].NodeID != "node-1" {
		t.Fatalf("nodes[0].nodeId = %q, want node-1", nodes[0].NodeID)
	}
	if nodes[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("runtime type = %q, want hermes", nodes[0].Runtimes[0].Type)
	}
}

func TestHeartbeatRequiresNodeID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"hostname":"worker-a"}`))

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestNodesApplyFreshnessPolicy(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	heartbeats := map[string]time.Time{
		"node-fresh":   now.Add(-time.Minute),
		"node-stale":   now.Add(-3 * time.Minute),
		"node-offline": now.Add(-11 * time.Minute),
	}
	for nodeID, observedAt := range heartbeats {
		_, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Hostname: nodeID,
		}, observedAt)
		if err != nil {
			t.Fatalf("record heartbeat for %s: %v", nodeID, err)
		}
	}

	handler, err := NewHandlerWithStoreAndFreshnessPolicy(nodeStore, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}

	got := make(map[string]protocol.NodeState)
	for _, node := range nodes {
		got[node.NodeID] = node.State
	}
	want := map[string]protocol.NodeState{
		"node-fresh":   protocol.NodeStateFresh,
		"node-stale":   protocol.NodeStateStale,
		"node-offline": protocol.NodeStateOffline,
	}
	for nodeID, wantState := range want {
		if got[nodeID] != wantState {
			t.Fatalf("node %s state = %q, want %q", nodeID, got[nodeID], wantState)
		}
	}
}

func TestNodesTreatZeroHeartbeatAsOffline(t *testing.T) {
	handler, err := NewHandlerWithStoreAndFreshnessPolicy(staticNodeStore{
		nodes: []protocol.NodeStatus{
			{
				NodeID:          "node-zero",
				State:           protocol.NodeStateFresh,
				LastHeartbeatAt: time.Time{},
			},
		},
	}, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].State != protocol.NodeStateOffline {
		t.Fatalf("node state = %q, want offline", nodes[0].State)
	}
}

func TestStatusEndpointsRejectNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestHeartbeatRejectsNonPOST(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/heartbeat", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", got, http.MethodPost)
	}
}

func TestNodesRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func assertJSONStatus(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != want {
		t.Fatalf("status body = %q, want %q", body.Status, want)
	}
}

type staticNodeStore struct {
	nodes []protocol.NodeStatus
}

func (s staticNodeStore) RecordHeartbeat(context.Context, protocol.HeartbeatRequest, time.Time) (protocol.NodeStatus, error) {
	return protocol.NodeStatus{}, nil
}

func (s staticNodeStore) ListNodes(context.Context) ([]protocol.NodeStatus, error) {
	nodes := append([]protocol.NodeStatus(nil), s.nodes...)
	return nodes, nil
}
