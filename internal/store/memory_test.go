package store

import (
	"context"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestMemoryNodeStoreRecordsAndListsNodes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-b",
		Hostname: "worker-b",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "openclaw"}},
	}, now); err != nil {
		t.Fatalf("record node-b heartbeat: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-a",
		Hostname: "worker-a",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("record node-a heartbeat: %v", err)
	}

	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes length = %d, want 2", len(nodes))
	}
	if nodes[0].NodeID != "node-a" || nodes[1].NodeID != "node-b" {
		t.Fatalf("nodes are not sorted by node ID: %#v", nodes)
	}
	if nodes[0].State != protocol.NodeStateFresh {
		t.Fatalf("node state = %q, want fresh", nodes[0].State)
	}

	nodes[0].Runtimes[0].Type = "mutated"
	again, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes again: %v", err)
	}
	if again[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("store snapshot was mutated: %#v", again[0].Runtimes)
	}
}
