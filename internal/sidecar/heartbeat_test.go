package sidecar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestHeartbeatClientPostsHeartbeat(t *testing.T) {
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	var got protocol.HeartbeatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/heartbeat" {
			t.Fatalf("path = %q, want /api/heartbeat", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-credential" {
			t.Fatalf("Authorization = %q, want Bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode heartbeat request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{
			Accepted:   true,
			ServerTime: now.Add(time.Second),
			Node: protocol.NodeStatus{
				NodeID:          got.NodeID,
				Hostname:        got.Hostname,
				State:           protocol.NodeStateFresh,
				SidecarVersion:  got.SidecarVersion,
				LastHeartbeatAt: now.Add(time.Second),
			},
		})
	}))
	defer server.Close()

	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      server.URL,
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		Hostname:       "worker-a",
		SidecarVersion: "test-version",
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	resp, err := client.SendHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	if got.NodeID != "node-1" {
		t.Fatalf("nodeId = %q, want node-1", got.NodeID)
	}
	if got.Hostname != "worker-a" {
		t.Fatalf("hostname = %q, want worker-a", got.Hostname)
	}
	if got.SidecarVersion != "test-version" {
		t.Fatalf("sidecarVersion = %q, want test-version", got.SidecarVersion)
	}
	if !got.SentAt.Equal(now) {
		t.Fatalf("sentAt = %s, want %s", got.SentAt, now)
	}
	if !resp.Accepted {
		t.Fatalf("accepted = false, want true")
	}
}

func TestHeartbeatClientDefaultsNodeIDToHostname(t *testing.T) {
	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      "http://example.test",
		NodeCredential: "test-credential",
		Hostname:       "worker-a",
		Now:            func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	heartbeat := client.BuildHeartbeat(context.Background())
	if heartbeat.NodeID != "worker-a" {
		t.Fatalf("nodeId = %q, want worker-a", heartbeat.NodeID)
	}
}

func TestRunHeartbeatLoopSendsPeriodically(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{
			Accepted:   true,
			ServerTime: time.Now().UTC(),
			Node: protocol.NodeStatus{
				NodeID: "node-1",
				State:  protocol.NodeStateFresh,
			},
		})
	}))
	defer server.Close()

	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      server.URL,
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		Hostname:       "worker-a",
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = RunHeartbeatLoop(ctx, client, time.Millisecond, func(resp *protocol.HeartbeatResponse, err error) {
		if err != nil {
			t.Fatalf("heartbeat failed: %v", err)
		}
		if resp == nil || !resp.Accepted {
			t.Fatalf("heartbeat response = %#v, want accepted response", resp)
		}
		if count.Load() >= 2 {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("run heartbeat loop: %v", err)
	}
	if count.Load() < 2 {
		t.Fatalf("heartbeats sent = %d, want at least 2", count.Load())
	}
}

func TestHeartbeatClientIncludesRuntimesFromCollector(t *testing.T) {
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	var got protocol.HeartbeatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode heartbeat request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{Accepted: true})
	}))
	defer server.Close()

	collector := &fakeCollector{
		statuses: []protocol.RuntimeStatus{
			{Name: "hermes", Type: "hermes", State: "present"},
			{Name: "openclaw", Type: "openclaw", State: "present"},
		},
	}

	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      server.URL,
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		Now:            func() time.Time { return now },
		Collector:      collector,
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	_, err = client.SendHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	if len(got.Runtimes) != 2 {
		t.Fatalf("len(runtimes) = %d, want 2", len(got.Runtimes))
	}
	if got.Runtimes[0].Name != "hermes" {
		t.Fatalf("runtimes[0].Name = %q, want hermes", got.Runtimes[0].Name)
	}
	if got.Runtimes[1].Name != "openclaw" {
		t.Fatalf("runtimes[1].Name = %q, want openclaw", got.Runtimes[1].Name)
	}
}

func TestHeartbeatClientOmitsRuntimesWhenCollectorReturnsEmpty(t *testing.T) {
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	var got protocol.HeartbeatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode heartbeat request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{Accepted: true})
	}))
	defer server.Close()

	collector := &fakeCollector{statuses: []protocol.RuntimeStatus{}}

	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      server.URL,
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		Now:            func() time.Time { return now },
		Collector:      collector,
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	_, err = client.SendHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	if len(got.Runtimes) != 0 {
		t.Fatalf("len(runtimes) = %d, want 0", len(got.Runtimes))
	}
}

func TestHeartbeatClientSurfacesAdapterErrorWithoutFailingHeartbeat(t *testing.T) {
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	var got protocol.HeartbeatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode heartbeat request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{Accepted: true})
	}))
	defer server.Close()

	collector := &fakeCollector{
		statuses: []protocol.RuntimeStatus{
			{Name: "hermes", Type: "hermes", State: "error", LastError: "probe failed"},
		},
	}

	client, err := NewHeartbeatClient(HeartbeatClientConfig{
		ServerURL:      server.URL,
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		Now:            func() time.Time { return now },
		Collector:      collector,
	})
	if err != nil {
		t.Fatalf("new heartbeat client: %v", err)
	}

	_, err = client.SendHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	if len(got.Runtimes) != 1 {
		t.Fatalf("len(runtimes) = %d, want 1", len(got.Runtimes))
	}
	if got.Runtimes[0].State != "error" {
		t.Fatalf("runtimes[0].State = %q, want error", got.Runtimes[0].State)
	}
	if got.Runtimes[0].LastError != "probe failed" {
		t.Fatalf("runtimes[0].LastError = %q, want 'probe failed'", got.Runtimes[0].LastError)
	}
}

type fakeCollector struct {
	statuses []protocol.RuntimeStatus
}

func (f *fakeCollector) CollectStatuses(_ context.Context) []protocol.RuntimeStatus {
	return f.statuses
}
