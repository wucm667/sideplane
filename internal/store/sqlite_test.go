package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestSQLiteNodeStoreMigratesAndPersistsHeartbeat(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")

	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	firstClosed := false
	defer func() {
		if !firstClosed {
			_ = first.Close()
		}
	}()

	assertSQLiteTableExists(t, ctx, first.db, "schema_migrations")
	assertSQLiteTableExists(t, ctx, first.db, "nodes")
	assertSQLiteTableExists(t, ctx, first.db, "node_runtimes")
	assertSQLiteTableExists(t, ctx, first.db, "heartbeats")
	assertSQLiteMigrationApplied(t, ctx, first.db, 1)

	observedAt := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	sentAt := observedAt.Add(-time.Second)
	node, err := first.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:         "node-b",
		Hostname:       "worker-b",
		SidecarVersion: "test-version",
		SentAt:         sentAt,
		Runtimes: []protocol.RuntimeStatus{
			{
				Name:       "default",
				Type:       "openclaw",
				State:      "running",
				Provider:   "openai",
				Model:      "gpt-5",
				ConfigHash: "sha256:runtime",
			},
		},
		ConfigHash: "sha256:node",
	}, observedAt)
	if err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if node.NodeID != "node-b" {
		t.Fatalf("nodeId = %q, want node-b", node.NodeID)
	}

	nodes, err := first.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	assertSQLiteNodeSnapshot(t, nodes, observedAt)

	var heartbeatCount int
	err = first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE node_id = ?`, "node-b").Scan(&heartbeatCount)
	if err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	if heartbeatCount != 1 {
		t.Fatalf("heartbeat count = %d, want 1", heartbeatCount)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first sqlite store: %v", err)
	}
	firstClosed = true

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()

	nodes, err = second.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes after reopen: %v", err)
	}
	assertSQLiteNodeSnapshot(t, nodes, observedAt)
}

func TestSQLiteNodeStoreUpdatesLatestNodeSnapshot(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	store, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	firstAt := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-a",
		Hostname: "worker-a",
		Runtimes: []protocol.RuntimeStatus{
			{Name: "default", Type: "hermes"},
			{Name: "stale", Type: "openclaw"},
		},
	}, firstAt); err != nil {
		t.Fatalf("record first heartbeat: %v", err)
	}

	secondAt := firstAt.Add(time.Minute)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:    "node-a",
		Hostname:  "worker-a-renamed",
		Runtimes:  []protocol.RuntimeStatus{{Name: "default", Type: "openclaw", Model: "gpt-5"}},
		LastError: "runtime restart pending",
	}, secondAt); err != nil {
		t.Fatalf("record second heartbeat: %v", err)
	}

	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].Hostname != "worker-a-renamed" {
		t.Fatalf("hostname = %q, want worker-a-renamed", nodes[0].Hostname)
	}
	if !nodes[0].LastHeartbeatAt.Equal(secondAt) {
		t.Fatalf("last heartbeat = %s, want %s", nodes[0].LastHeartbeatAt, secondAt)
	}
	if nodes[0].LastError != "runtime restart pending" {
		t.Fatalf("last error = %q, want runtime restart pending", nodes[0].LastError)
	}
	if len(nodes[0].Runtimes) != 1 {
		t.Fatalf("runtime length = %d, want 1", len(nodes[0].Runtimes))
	}
	if nodes[0].Runtimes[0].Type != "openclaw" {
		t.Fatalf("runtime type = %q, want openclaw", nodes[0].Runtimes[0].Type)
	}

	var heartbeatCount int
	err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE node_id = ?`, "node-a").Scan(&heartbeatCount)
	if err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	if heartbeatCount != 2 {
		t.Fatalf("heartbeat count = %d, want 2", heartbeatCount)
	}
}

func assertSQLiteNodeSnapshot(t *testing.T, nodes []protocol.NodeStatus, observedAt time.Time) {
	t.Helper()

	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].NodeID != "node-b" {
		t.Fatalf("nodeId = %q, want node-b", nodes[0].NodeID)
	}
	if nodes[0].State != protocol.NodeStateFresh {
		t.Fatalf("state = %q, want fresh", nodes[0].State)
	}
	if !nodes[0].LastHeartbeatAt.Equal(observedAt) {
		t.Fatalf("last heartbeat = %s, want %s", nodes[0].LastHeartbeatAt, observedAt)
	}
	if len(nodes[0].Runtimes) != 1 {
		t.Fatalf("runtime length = %d, want 1", len(nodes[0].Runtimes))
	}
	if nodes[0].Runtimes[0].Type != "openclaw" {
		t.Fatalf("runtime type = %q, want openclaw", nodes[0].Runtimes[0].Type)
	}
}

func assertSQLiteTableExists(t *testing.T, ctx context.Context, db *sql.DB, table string) {
	t.Helper()

	var count int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM sqlite_master
WHERE type = 'table' AND name = ?
`, table).Scan(&count)
	if err != nil {
		t.Fatalf("query table %q: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("table %q exists count = %d, want 1", table, count)
	}
}

func assertSQLiteMigrationApplied(t *testing.T, ctx context.Context, db *sql.DB, version int) {
	t.Helper()

	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE version = ?`, version).Scan(&name)
	if err != nil {
		t.Fatalf("query schema migration %d: %v", version, err)
	}
	if name == "" {
		t.Fatalf("schema migration %d has empty name", version)
	}
}
