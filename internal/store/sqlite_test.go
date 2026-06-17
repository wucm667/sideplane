package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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
	assertSQLiteTableExists(t, ctx, first.db, "enrollment_tokens")
	assertSQLiteTableExists(t, ctx, first.db, "node_credentials")
	assertSQLiteMigrationApplied(t, ctx, first.db, 1)
	assertSQLiteMigrationApplied(t, ctx, first.db, 2)

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

func TestSQLiteEnrollmentTokenFlow(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	tokenResp, err := store.CreateEnrollmentToken(ctx, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatalf("token is empty")
	}
	if !tokenResp.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expiresAt = %s, want %s", tokenResp.ExpiresAt, now.Add(time.Hour))
	}
	assertSQLiteDoesNotContainPlaintext(t, ctx, store.db, "enrollment_tokens", tokenResp.Token)

	enrollResp, err := store.EnrollNode(ctx, protocol.EnrollNodeRequest{
		Token:    tokenResp.Token,
		NodeID:   "node-enroll",
		Hostname: "worker-enroll",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("enroll node: %v", err)
	}
	if enrollResp.NodeID != "node-enroll" {
		t.Fatalf("nodeId = %q, want node-enroll", enrollResp.NodeID)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("nodeCredential is empty")
	}
	assertSQLiteDoesNotContainPlaintext(t, ctx, store.db, "node_credentials", enrollResp.NodeCredential)

	ok, err := store.VerifyNodeCredential(ctx, enrollResp.NodeID, enrollResp.NodeCredential)
	if err != nil {
		t.Fatalf("verify node credential: %v", err)
	}
	if !ok {
		t.Fatalf("credential did not verify")
	}
	ok, err = store.VerifyNodeCredential(ctx, enrollResp.NodeID, "wrong credential")
	if err != nil {
		t.Fatalf("verify wrong node credential: %v", err)
	}
	if ok {
		t.Fatalf("wrong credential verified")
	}

	_, err = store.EnrollNode(ctx, protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: "node-reuse",
	}, now.Add(2*time.Minute))
	if !errors.Is(err, ErrEnrollmentTokenUsed) {
		t.Fatalf("reuse token error = %v, want ErrEnrollmentTokenUsed", err)
	}
}

func TestSQLiteExpiredEnrollmentTokenCannotBeUsed(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	tokenResp, err := store.CreateEnrollmentToken(ctx, now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}

	_, err = store.EnrollNode(ctx, protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: "node-expired",
	}, now.Add(2*time.Minute))
	if !errors.Is(err, ErrEnrollmentTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrEnrollmentTokenExpired", err)
	}
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

func TestSQLiteJobLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-jobs"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{
		Type:        protocol.JobTypeDeepProbe,
		PayloadJSON: `{"probe":"full"}`,
	}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.ID == "" {
		t.Fatalf("job ID is empty")
	}
	if job.Status != protocol.JobStatusPending {
		t.Fatalf("job status = %q, want pending", job.Status)
	}

	claimed, err := store.ClaimNextJob(ctx, "node-jobs", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim next job: %v", err)
	}
	if claimed == nil {
		t.Fatalf("claimed job is nil")
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	if claimed.Status != protocol.JobStatusClaimed {
		t.Fatalf("claimed status = %q, want claimed", claimed.Status)
	}

	if err := store.CompleteJob(ctx, job.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: `{"runtimes":[]}`,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	jobs, err := store.ListNodeJobs(ctx, "node-jobs")
	if err != nil {
		t.Fatalf("list node jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if jobs[0].Status != protocol.JobStatusCompleted {
		t.Fatalf("listed job status = %q, want completed", jobs[0].Status)
	}
	if jobs[0].ResultJSON != `{"runtimes":[]}` {
		t.Fatalf("result JSON = %q, want runtimes result", jobs[0].ResultJSON)
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

func assertSQLiteDoesNotContainPlaintext(t *testing.T, ctx context.Context, db *sql.DB, table string, plaintext string) {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT * FROM `+table)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns for %s: %v", table, err)
	}
	values := make([]sql.NullString, len(columns))
	scanTargets := make([]any, len(columns))
	for i := range values {
		scanTargets[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanTargets...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		for i, value := range values {
			if !value.Valid {
				continue
			}
			if value.String == plaintext || strings.Contains(value.String, plaintext) {
				t.Fatalf("%s.%s contains plaintext secret", table, columns[i])
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", table, err)
	}
}
