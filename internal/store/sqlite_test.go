package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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
	assertSQLiteTableExists(t, ctx, first.db, "audit_events")
	assertSQLiteTableExists(t, ctx, first.db, "node_labels")
	assertSQLiteTableExists(t, ctx, first.db, "rollouts")
	assertSQLiteTableExists(t, ctx, first.db, "operator_tokens")
	assertSQLiteTableExists(t, ctx, first.db, "desired_config_history")
	assertSQLiteMigrationApplied(t, ctx, first.db, 1)
	assertSQLiteMigrationApplied(t, ctx, first.db, 2)
	assertSQLiteMigrationApplied(t, ctx, first.db, 3)
	assertSQLiteMigrationApplied(t, ctx, first.db, 4)
	assertSQLiteMigrationApplied(t, ctx, first.db, 5)
	assertSQLiteMigrationApplied(t, ctx, first.db, 6)
	assertSQLiteMigrationApplied(t, ctx, first.db, 7)
	assertSQLiteMigrationApplied(t, ctx, first.db, 8)
	assertSQLiteMigrationApplied(t, ctx, first.db, 9)
	assertSQLiteMigrationApplied(t, ctx, first.db, 10)
	assertSQLiteMigrationApplied(t, ctx, first.db, 11)
	assertSQLiteTableExists(t, ctx, first.db, "alert_webhooks")
	assertSQLiteTableExists(t, ctx, first.db, "server_settings")
	assertSQLiteTableExists(t, ctx, first.db, "rollout_templates")
	assertSQLiteMigrationApplied(t, ctx, first.db, 12)
	assertSQLiteMigrationApplied(t, ctx, first.db, 13)
	assertSQLiteMigrationApplied(t, ctx, first.db, 14)
	assertSQLiteMigrationApplied(t, ctx, first.db, 15)
	assertSQLiteMigrationApplied(t, ctx, first.db, 16)

	observedAt := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	sentAt := observedAt.Add(-time.Second)
	node, err := first.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:         "node-b",
		Hostname:       "worker-b",
		SidecarVersion: "test-version",
		SentAt:         sentAt,
		Runtimes: []protocol.RuntimeStatus{
			{
				Name:           "default",
				Type:           "openclaw",
				DeploymentMode: "container",
				State:          "running",
				Provider:       "openai",
				Model:          "gpt-5",
				ConfigHash:     "sha256:runtime",
				Warnings:       []string{"config path unreadable"},
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

func TestSQLiteNodeStoreListNodesFilteredPaginatesAndCounts(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	for _, nodeID := range []string{"node-c", "node-a", "node-b"} {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
		}, now); err != nil {
			t.Fatalf("record %s heartbeat: %v", nodeID, err)
		}
	}

	list, err := store.ListNodesFiltered(ctx, NodeFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list filtered nodes: %v", err)
	}
	if list.Total != 3 || list.Limit != 1 || list.Offset != 1 {
		t.Fatalf("page metadata = total:%d limit:%d offset:%d, want 3/1/1", list.Total, list.Limit, list.Offset)
	}
	if len(list.Nodes) != 1 || list.Nodes[0].NodeID != "node-b" {
		t.Fatalf("paged nodes = %#v, want node-b", list.Nodes)
	}
	if len(list.Nodes[0].Runtimes) != 1 || list.Nodes[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("paged node runtimes = %#v, want hermes", list.Nodes[0].Runtimes)
	}

	list, err = store.ListNodesFiltered(ctx, NodeFilter{Limit: MaxNodeListLimit + 1, Offset: 10})
	if err != nil {
		t.Fatalf("list capped offset nodes: %v", err)
	}
	if list.Limit != MaxNodeListLimit || list.Offset != 10 || list.Total != 3 {
		t.Fatalf("capped metadata = total:%d limit:%d offset:%d, want 3/%d/10", list.Total, list.Limit, list.Offset, MaxNodeListLimit)
	}
	if len(list.Nodes) != 0 {
		t.Fatalf("paged nodes length = %d, want 0", len(list.Nodes))
	}
}

func TestSQLiteNodeStoreListNodesFilteredByLabels(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	for _, nodeID := range []string{"node-a", "node-b", "node-c"} {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: nodeID}, now); err != nil {
			t.Fatalf("record %s heartbeat: %v", nodeID, err)
		}
	}
	if err := store.SetNodeLabels(ctx, "node-a", map[string]string{"role": "canary", "zone": "lab"}); err != nil {
		t.Fatalf("set node-a labels: %v", err)
	}
	if err := store.SetNodeLabels(ctx, "node-b", map[string]string{"role": "stable", "zone": "lab"}); err != nil {
		t.Fatalf("set node-b labels: %v", err)
	}
	if err := store.SetNodeLabels(ctx, "node-c", map[string]string{"role": "canary", "zone": "vps"}); err != nil {
		t.Fatalf("set node-c labels: %v", err)
	}

	list, err := store.ListNodesFiltered(ctx, NodeFilter{Labels: map[string]string{"role": "canary", "zone": "lab"}})
	if err != nil {
		t.Fatalf("list label-filtered nodes: %v", err)
	}
	if list.Total != 1 || len(list.Nodes) != 1 || list.Nodes[0].NodeID != "node-a" {
		t.Fatalf("label-filtered nodes = total:%d %#v, want node-a only", list.Total, list.Nodes)
	}
}

func TestSQLiteNodeStoreSetGetOverwriteDeleteAndPersistLabels(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	store, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-labels"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if err := store.SetNodeLabels(ctx, "node-labels", map[string]string{
		" role ": " canary ",
		"zone":   "lab",
	}); err != nil {
		t.Fatalf("set labels: %v", err)
	}
	labels, err := store.GetNodeLabels(ctx, "node-labels")
	if err != nil {
		t.Fatalf("get labels: %v", err)
	}
	if labels["role"] != "canary" || labels["zone"] != "lab" {
		t.Fatalf("labels = %#v, want normalized role/zone", labels)
	}

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-labels"}, now.Add(time.Second)); err != nil {
		t.Fatalf("record second heartbeat: %v", err)
	}
	list, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(list) != 1 || list[0].Labels["role"] != "canary" {
		t.Fatalf("listed labels = %#v, want preserved canary label", list)
	}

	if err := store.SetNodeLabels(ctx, "node-labels", map[string]string{"role": "stable"}); err != nil {
		t.Fatalf("overwrite labels: %v", err)
	}
	labels, err = store.GetNodeLabels(ctx, "node-labels")
	if err != nil {
		t.Fatalf("get overwritten labels: %v", err)
	}
	if labels["role"] != "stable" || len(labels) != 1 {
		t.Fatalf("overwritten labels = %#v, want only stable role", labels)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}
	store, err = OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer store.Close()
	labels, err = store.GetNodeLabels(ctx, "node-labels")
	if err != nil {
		t.Fatalf("get persisted labels: %v", err)
	}
	if labels["role"] != "stable" || len(labels) != 1 {
		t.Fatalf("persisted labels = %#v, want stable role", labels)
	}
	if err := store.SetNodeLabels(ctx, "node-labels", map[string]string{}); err != nil {
		t.Fatalf("delete labels: %v", err)
	}
	labels, err = store.GetNodeLabels(ctx, "node-labels")
	if err != nil {
		t.Fatalf("get deleted labels: %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("deleted labels = %#v, want empty", labels)
	}
}

func TestSQLiteNodeStoreSetGetAndPersistMaintenance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	store, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-maint"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if err := store.SetNodeMaintenance(ctx, "node-maint", true); err != nil {
		t.Fatalf("set maintenance: %v", err)
	}
	maintenance, err := store.GetNodeMaintenance(ctx, "node-maint")
	if err != nil {
		t.Fatalf("get maintenance: %v", err)
	}
	if !maintenance {
		t.Fatalf("maintenance = false, want true")
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-maint"}, now.Add(time.Second)); err != nil {
		t.Fatalf("record second heartbeat: %v", err)
	}
	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 || !nodes[0].Maintenance {
		t.Fatalf("listed maintenance = %#v, want true", nodes)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()
	maintenance, err = reopened.GetNodeMaintenance(ctx, "node-maint")
	if err != nil {
		t.Fatalf("get persisted maintenance: %v", err)
	}
	if !maintenance {
		t.Fatalf("persisted maintenance = false, want true")
	}
	if err := reopened.SetNodeMaintenance(ctx, "node-maint", false); err != nil {
		t.Fatalf("clear maintenance: %v", err)
	}
	maintenance, err = reopened.GetNodeMaintenance(ctx, "node-maint")
	if err != nil {
		t.Fatalf("get cleared maintenance: %v", err)
	}
	if maintenance {
		t.Fatalf("cleared maintenance = true, want false")
	}
	if err := reopened.SetNodeMaintenance(ctx, "missing", true); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node set maintenance error = %v, want ErrNodeNotFound", err)
	}
}

func TestSQLiteNodeStoreLabelValidationAndDeleteCascade(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-labels"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	for name, labels := range map[string]map[string]string{
		"empty key":        {"": "value"},
		"long key":         {strings.Repeat("k", MaxNodeLabelKeyLength+1): "value"},
		"long value":       {"key": strings.Repeat("v", MaxNodeLabelValueLength+1)},
		"control in key":   {"bad\nkey": "value"},
		"control in value": {"key": "bad\nvalue"},
	} {
		if err := store.SetNodeLabels(ctx, "node-labels", labels); err == nil {
			t.Fatalf("%s labels unexpectedly accepted", name)
		}
	}
	if err := store.SetNodeLabels(ctx, "missing", map[string]string{"role": "canary"}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node set labels error = %v, want ErrNodeNotFound", err)
	}
	if err := store.SetNodeLabels(ctx, "node-labels", map[string]string{"role": "canary"}); err != nil {
		t.Fatalf("set valid labels: %v", err)
	}
	if err := store.DeleteNode(ctx, "node-labels"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if _, err := store.GetNodeLabels(ctx, "node-labels"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("get labels after delete error = %v, want ErrNodeNotFound", err)
	}
	if got := countSQLiteRowsForValue(t, ctx, store.db, "node_labels", "node_id", "node-labels"); got != 0 {
		t.Fatalf("node label rows after cascade = %d, want 0", got)
	}
}

func TestSQLiteReliabilityPragmas(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	var journalMode string
	if err := store.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := store.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestSQLiteNodeStoreBackupToProducesConsistentCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sideplane.db")
	source, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}
	defer source.Close()

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	if _, err := source.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-a", Hostname: "host-a"}, now); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := source.CreateOperatorToken(ctx, "ops", protocol.OperatorTokenScopeReadonly, now); err != nil {
		t.Fatalf("seed operator token: %v", err)
	}

	if err := source.BackupTo(ctx, "   "); err == nil {
		t.Fatalf("expected error for empty backup destination")
	}

	backupPath := filepath.Join(dir, "backup.db")
	if err := source.BackupTo(ctx, backupPath); err != nil {
		t.Fatalf("backup: %v", err)
	}

	restored, err := OpenSQLiteNodeStore(ctx, backupPath)
	if err != nil {
		t.Fatalf("open backup store: %v", err)
	}
	defer restored.Close()

	srcVer, err := source.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("source schema version: %v", err)
	}
	dstVer, err := restored.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("backup schema version: %v", err)
	}
	if srcVer != dstVer || dstVer != LatestSQLiteSchemaVersion() {
		t.Fatalf("schema versions src=%d backup=%d latest=%d", srcVer, dstVer, LatestSQLiteSchemaVersion())
	}

	nodes, err := restored.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes from backup: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-a" {
		t.Fatalf("backup nodes = %+v, want one node-a", nodes)
	}
	tokens, err := restored.ListOperatorTokens(ctx)
	if err != nil {
		t.Fatalf("list operator tokens from backup: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "ops" || tokens[0].Scope != protocol.OperatorTokenScopeReadonly {
		t.Fatalf("backup operator tokens = %+v, want readonly ops", tokens)
	}
}

func TestSQLiteNodeStoreAlertWebhookPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	created, err := first.CreateAlertWebhook(ctx, protocol.CreateAlertWebhookRequest{
		URL:    "https://hooks.example.com/sp",
		Events: []protocol.AlertEventType{protocol.AlertEventRolloutPaused},
		Secret: "topsecret",
	}, now)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()

	listed, err := second.ListAlertWebhooks(ctx)
	if err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || !listed[0].HasSecret {
		t.Fatalf("listed webhooks = %+v, want persisted metadata", listed)
	}
	if listed[0].Kind != protocol.AlertWebhookKindGeneric {
		t.Fatalf("listed kind = %q, want generic default", listed[0].Kind)
	}
	targets, err := second.ListAlertWebhookTargets(ctx, protocol.AlertEventRolloutPaused)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Kind != protocol.AlertWebhookKindGeneric || targets[0].Secret != "topsecret" {
		t.Fatalf("targets = %+v, want persisted secret for signing", targets)
	}
	if none, err := second.ListAlertWebhookTargets(ctx, protocol.AlertEventNodeDrift); err != nil || len(none) != 0 {
		t.Fatalf("drift targets = %+v err=%v, want none", none, err)
	}
}

func TestSQLiteNodeStoreAlertWebhookKindPersists(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	created, err := first.CreateAlertWebhook(ctx, protocol.CreateAlertWebhookRequest{
		Kind:   protocol.AlertWebhookKindSlack,
		URL:    "https://hooks.example.com/slack",
		Events: []protocol.AlertEventType{protocol.AlertEventNodeDrift},
	}, now)
	if err != nil {
		t.Fatalf("create slack webhook: %v", err)
	}
	if created.Kind != protocol.AlertWebhookKindSlack {
		t.Fatalf("created kind = %q, want slack", created.Kind)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()

	listed, err := second.ListAlertWebhooks(ctx)
	if err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].Kind != protocol.AlertWebhookKindSlack {
		t.Fatalf("listed webhooks = %+v, want slack metadata", listed)
	}
	targets, err := second.ListAlertWebhookTargets(ctx, protocol.AlertEventNodeDrift)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Kind != protocol.AlertWebhookKindSlack || targets[0].Secret != "" {
		t.Fatalf("targets = %+v, want unsigned slack target", targets)
	}
}

func TestSQLiteNodeStoreServerSettingsPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if err := first.SetExpectedSidecarVersion(ctx, "v2.0.0"); err != nil {
		t.Fatalf("set expected version: %v", err)
	}
	if err := first.SetExpectedRuntimeVersions(ctx, map[string]string{"hermes": "v2026.5.1"}); err != nil {
		t.Fatalf("set expected runtime versions: %v", err)
	}
	// Upsert path: a second write replaces the single row.
	if err := first.SetExpectedSidecarVersion(ctx, "v2.1.0"); err != nil {
		t.Fatalf("update expected version: %v", err)
	}
	if err := first.SetExpectedRuntimeVersions(ctx, map[string]string{"hermes": "v2026.5.2", "openclaw": "v2026.5.3"}); err != nil {
		t.Fatalf("update expected runtime versions: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()
	settings, err := second.GetServerSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.ExpectedSidecarVersion != "v2.1.0" {
		t.Fatalf("expected version = %q, want persisted v2.1.0", settings.ExpectedSidecarVersion)
	}
	if settings.ExpectedRuntimeVersions["hermes"] != "v2026.5.2" || settings.ExpectedRuntimeVersions["openclaw"] != "v2026.5.3" {
		t.Fatalf("expected runtime versions = %#v, want persisted map", settings.ExpectedRuntimeVersions)
	}
}

func TestSQLiteSchemaVersionReportsLatestAfterIdempotentMigration(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open first sqlite store: %v", err)
	}
	version, err := first.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("first schema version: %v", err)
	}
	if version != LatestSQLiteSchemaVersion() {
		t.Fatalf("first schema version = %d, want %d", version, LatestSQLiteSchemaVersion())
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open second sqlite store: %v", err)
	}
	defer second.Close()
	version, err = second.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("second schema version: %v", err)
	}
	if version != LatestSQLiteSchemaVersion() {
		t.Fatalf("second schema version = %d, want %d", version, LatestSQLiteSchemaVersion())
	}
	var migrationRows int
	if err := second.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationRows); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if migrationRows != len(sqliteMigrations) {
		t.Fatalf("migration rows = %d, want %d", migrationRows, len(sqliteMigrations))
	}
}

func TestSQLitePruningEmptyTablesNoops(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	cutoff := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if deleted, err := store.PruneHeartbeats(ctx, 1); err != nil || deleted != 0 {
		t.Fatalf("prune empty heartbeats deleted=%d err=%v, want 0 nil", deleted, err)
	}
	if deleted, err := store.PruneTerminalJobs(ctx, cutoff); err != nil || deleted != 0 {
		t.Fatalf("prune empty jobs deleted=%d err=%v, want 0 nil", deleted, err)
	}
	if deleted, err := store.PruneAuditEvents(ctx, cutoff); err != nil || deleted != 0 {
		t.Fatalf("prune empty audit events deleted=%d err=%v, want 0 nil", deleted, err)
	}
	if deleted, err := store.PruneTerminalRollouts(ctx, cutoff); err != nil || deleted != 0 {
		t.Fatalf("prune empty rollouts deleted=%d err=%v, want 0 nil", deleted, err)
	}
}

func TestSQLitePruneHeartbeatsKeepsLatestPerNode(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-a"}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record node-a heartbeat %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-b"}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record node-b heartbeat %d: %v", i, err)
		}
	}

	deleted, err := store.PruneHeartbeats(ctx, 2)
	if err != nil {
		t.Fatalf("prune heartbeats: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}

	assertHeartbeatTimes(t, ctx, store, "node-a", []time.Time{now.Add(4 * time.Minute), now.Add(3 * time.Minute)})
	assertHeartbeatTimes(t, ctx, store, "node-b", []time.Time{now.Add(2 * time.Minute), now.Add(time.Minute)})

	deleted, err = store.PruneHeartbeats(ctx, 2)
	if err != nil {
		t.Fatalf("second prune heartbeats: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second deleted = %d, want 0", deleted)
	}
}

func TestSQLitePruneHeartbeatsKeepsExactBoundary(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-boundary"}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record heartbeat %d: %v", i, err)
		}
	}

	deleted, err := store.PruneHeartbeats(ctx, 3)
	if err != nil {
		t.Fatalf("prune heartbeats: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	assertHeartbeatTimes(t, ctx, store, "node-boundary", []time.Time{now.Add(2 * time.Minute), now.Add(time.Minute), now})
}

func assertHeartbeatTimes(t *testing.T, ctx context.Context, store *SQLiteNodeStore, nodeID string, want []time.Time) {
	t.Helper()
	rows, err := store.db.QueryContext(ctx, `
SELECT observed_at
FROM heartbeats
WHERE node_id = ?
ORDER BY observed_at DESC
`, nodeID)
	if err != nil {
		t.Fatalf("query heartbeats for %s: %v", nodeID, err)
	}
	defer rows.Close()

	var got []time.Time
	for rows.Next() {
		var observedAt string
		if err := rows.Scan(&observedAt); err != nil {
			t.Fatalf("scan heartbeat for %s: %v", nodeID, err)
		}
		parsed, err := parseDBTime(observedAt)
		if err != nil {
			t.Fatalf("parse heartbeat time %q: %v", observedAt, err)
		}
		got = append(got, parsed)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate heartbeats for %s: %v", nodeID, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s heartbeat count = %d, want %d: %v", nodeID, len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("%s heartbeat[%d] = %s, want %s; all=%v", nodeID, i, got[i], want[i], got)
		}
	}
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

func TestSQLiteOperatorTokenFlow(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	created, err := store.CreateOperatorToken(ctx, "ops laptop", "", now)
	if err != nil {
		t.Fatalf("create operator token: %v", err)
	}
	if created.Token == "" {
		t.Fatalf("plaintext token is empty")
	}
	if created.OperatorToken.ID == "" || created.OperatorToken.Name != "ops laptop" {
		t.Fatalf("operator token metadata = %+v, want id/name", created.OperatorToken)
	}
	if created.OperatorToken.Scope != protocol.OperatorTokenScopeAdmin {
		t.Fatalf("operator token scope = %q, want admin default", created.OperatorToken.Scope)
	}
	assertSQLiteDoesNotContainPlaintext(t, ctx, store.db, "operator_tokens", created.Token)

	tokens, err := store.ListOperatorTokens(ctx)
	if err != nil {
		t.Fatalf("list operator tokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != created.OperatorToken.ID || tokens[0].Name != "ops laptop" {
		t.Fatalf("operator token list = %+v, want created metadata", tokens)
	}
	if tokens[0].Scope != protocol.OperatorTokenScopeAdmin {
		t.Fatalf("listed operator token scope = %q, want admin", tokens[0].Scope)
	}

	tokenID, scope, ok, err := store.VerifyOperatorToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("verify operator token: %v", err)
	}
	if !ok || tokenID != created.OperatorToken.ID || scope != protocol.OperatorTokenScopeAdmin {
		t.Fatalf("verify operator token = id:%q scope:%q ok:%t, want created admin token", tokenID, scope, ok)
	}
	_, _, ok, err = store.VerifyOperatorToken(ctx, "wrong token")
	if err != nil {
		t.Fatalf("verify wrong operator token: %v", err)
	}
	if ok {
		t.Fatalf("wrong operator token verified")
	}

	usedAt := now.Add(time.Minute)
	if err := store.UpdateOperatorTokenLastUsed(ctx, tokenID, usedAt); err != nil {
		t.Fatalf("update last used: %v", err)
	}
	tokens, err = store.ListOperatorTokens(ctx)
	if err != nil {
		t.Fatalf("list operator tokens after last used: %v", err)
	}
	if tokens[0].LastUsedAt == nil || !tokens[0].LastUsedAt.Equal(usedAt) {
		t.Fatalf("lastUsedAt = %v, want %s", tokens[0].LastUsedAt, usedAt)
	}

	revoked, err := store.RevokeOperatorToken(ctx, created.OperatorToken.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("revoke operator token: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatalf("revokedAt is nil")
	}
	_, _, ok, err = store.VerifyOperatorToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("verify revoked operator token: %v", err)
	}
	if ok {
		t.Fatalf("revoked operator token verified")
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

func TestSQLiteDeleteNodeRemovesAssociatedData(t *testing.T) {
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
	enrollResp, err := store.EnrollNode(ctx, protocol.EnrollNodeRequest{Token: tokenResp.Token, NodeID: "node-delete"}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("enroll node-delete: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
		NodeID:   "node-delete",
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes"}},
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-keep"}, now); err != nil {
		t.Fatalf("record keep heartbeat: %v", err)
	}
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-delete", now); err != nil {
		t.Fatalf("create delete job: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "job.create", TargetNode: "node-delete", CreatedAt: now}); err != nil {
		t.Fatalf("append delete audit: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "job.create", TargetNode: "node-keep", CreatedAt: now}); err != nil {
		t.Fatalf("append keep audit: %v", err)
	}

	if err := store.DeleteNode(ctx, "node-delete"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	exists, err := store.NodeExists(ctx, "node-delete")
	if err != nil {
		t.Fatalf("node exists: %v", err)
	}
	if exists {
		t.Fatalf("node-delete still exists")
	}
	ok, err := store.VerifyNodeCredential(ctx, "node-delete", enrollResp.NodeCredential)
	if err != nil {
		t.Fatalf("verify deleted credential: %v", err)
	}
	if ok {
		t.Fatalf("deleted node credential still verifies")
	}
	for _, tt := range []struct {
		table  string
		column string
	}{
		{table: "node_credentials", column: "node_id"},
		{table: "node_runtimes", column: "node_id"},
		{table: "node_labels", column: "node_id"},
		{table: "heartbeats", column: "node_id"},
		{table: "jobs", column: "node_id"},
		{table: "audit_events", column: "target_node"},
	} {
		if got := countSQLiteRowsForValue(t, ctx, store.db, tt.table, tt.column, "node-delete"); got != 0 {
			t.Fatalf("%s rows for deleted node = %d, want 0", tt.table, got)
		}
	}
	if got := countSQLiteRowsForValue(t, ctx, store.db, "nodes", "node_id", "node-keep"); got != 1 {
		t.Fatalf("node-keep rows = %d, want 1", got)
	}
	if err := store.DeleteNode(ctx, "node-delete"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("delete missing node error = %v, want ErrNodeNotFound", err)
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

func TestSQLiteFailJobPersistsResultJSON(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-failed"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply}, "node-failed", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-failed", now.Add(time.Second)); err != nil {
		t.Fatalf("claim job: %v", err)
	}
	resultJSON := `{"steps":[{"name":"rolled_back","status":"failed"}]}`
	if err := store.FailJob(ctx, job.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusFailed,
		ResultJSON: resultJSON,
		Error:      "apply failed",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	jobs, err := store.ListNodeJobs(ctx, "node-failed")
	if err != nil {
		t.Fatalf("list node jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if jobs[0].Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", jobs[0].Status)
	}
	if jobs[0].ResultJSON != resultJSON {
		t.Fatalf("result JSON = %q, want %q", jobs[0].ResultJSON, resultJSON)
	}
	if jobs[0].Error != "apply failed" {
		t.Fatalf("job error = %q, want apply failed", jobs[0].Error)
	}
}

func TestSQLiteListNodeJobsFiltered(t *testing.T) {
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

	older, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create older job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-jobs", now.Add(time.Second)); err != nil {
		t.Fatalf("claim older job: %v", err)
	}
	if err := store.CompleteJob(ctx, older.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete older job: %v", err)
	}
	newer, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create newer job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-jobs", now.Add(4*time.Second)); err != nil {
		t.Fatalf("claim newer job: %v", err)
	}
	if err := store.CompleteJob(ctx, newer.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(5*time.Second)); err != nil {
		t.Fatalf("complete newer job: %v", err)
	}
	pending, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	completed, err := store.ListNodeJobsFiltered(ctx, "node-jobs", JobFilter{
		Limit:  1,
		Status: protocol.JobStatusCompleted,
	})
	if err != nil {
		t.Fatalf("list completed jobs: %v", err)
	}
	if len(completed) != 1 || completed[0].ID != newer.ID {
		t.Fatalf("completed jobs = %#v, want newest completed job %s", completed, newer.ID)
	}

	pendingJobs, err := store.ListNodeJobsFiltered(ctx, "node-jobs", JobFilter{Status: protocol.JobStatusPending})
	if err != nil {
		t.Fatalf("list pending jobs: %v", err)
	}
	if len(pendingJobs) != 1 || pendingJobs[0].ID != pending.ID {
		t.Fatalf("pending jobs = %#v, want pending job %s", pendingJobs, pending.ID)
	}
}

func TestSQLiteRolloutLifecycleListUpdateAndPrune(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	first, err := store.CreateRollout(ctx, rolloutForStoreTest("rollout-a", protocol.RolloutStatePending, now))
	if err != nil {
		t.Fatalf("create first rollout: %v", err)
	}
	second, err := store.CreateRollout(ctx, rolloutForStoreTest("rollout-b", protocol.RolloutStateRunning, now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("create second rollout: %v", err)
	}

	got, err := store.GetRollout(ctx, first.ID)
	if err != nil {
		t.Fatalf("get rollout: %v", err)
	}
	if got == nil || got.ID != first.ID || got.Spec.Target.Model != "gpt-5" {
		t.Fatalf("got rollout = %#v, want first rollout", got)
	}

	list, err := store.ListRollouts(ctx, RolloutFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list rollouts: %v", err)
	}
	if list.Total != 2 || list.Limit != 1 || len(list.Rollouts) != 1 || list.Rollouts[0].ID != second.ID {
		t.Fatalf("rollout page = %#v, want newest second rollout", list)
	}

	second.State = protocol.RolloutStateCompleted
	second.UpdatedAt = now.Add(2 * time.Minute)
	second.FinishedAt = now.Add(2 * time.Minute)
	second.Batches[0].State = protocol.RolloutBatchStateCompleted
	if err := store.UpdateRollout(ctx, second); err != nil {
		t.Fatalf("update rollout: %v", err)
	}
	updated, err := store.GetRollout(ctx, second.ID)
	if err != nil {
		t.Fatalf("get updated rollout: %v", err)
	}
	if updated.State != protocol.RolloutStateCompleted || updated.FinishedAt.IsZero() {
		t.Fatalf("updated rollout = %#v, want completed with finishedAt", updated)
	}

	deleted, err := store.PruneTerminalRollouts(ctx, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("prune terminal rollouts: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted rollouts = %d, want 1", deleted)
	}
	missing, err := store.GetRollout(ctx, second.ID)
	if err != nil {
		t.Fatalf("get pruned rollout: %v", err)
	}
	if missing != nil {
		t.Fatalf("pruned rollout still exists: %#v", missing)
	}
}

func TestSQLiteRolloutActiveConflictLookup(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	assertRolloutConflictLookup(t, store)
}

func TestSQLiteRolloutConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	rollout, err := store.CreateRollout(ctx, rolloutForStoreTest("rollout-race", protocol.RolloutStateRunning, now))
	if err != nil {
		t.Fatalf("create rollout: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next, err := cloneRollout(rollout)
			if err != nil {
				t.Errorf("clone rollout %d: %v", i, err)
				return
			}
			next.UpdatedAt = now.Add(time.Duration(i) * time.Second)
			next.Batches[0].Nodes["node-a"] = protocol.RolloutNodeProgress{
				NodeID: "node-a",
				JobID:  "job",
				State:  protocol.RolloutNodeStateDispatched,
			}
			if err := store.UpdateRollout(ctx, next); err != nil {
				t.Errorf("update rollout %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := store.GetRollout(ctx, rollout.ID)
	if err != nil {
		t.Fatalf("get rollout: %v", err)
	}
	if got == nil || got.Batches[0].Nodes["node-a"].State != protocol.RolloutNodeStateDispatched {
		t.Fatalf("concurrent rollout = %#v, want dispatched node", got)
	}
}

func TestSQLitePrunesTerminalJobsAndAuditEvents(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	assertRetentionPruning(t, store)
}

func TestSQLitePruneTerminalJobsPreservesActiveJobs(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for _, nodeID := range []string{"node-claimed", "node-pending"} {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: nodeID}, now); err != nil {
			t.Fatalf("record %s heartbeat: %v", nodeID, err)
		}
	}
	pending, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRollback}, "node-pending", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}
	claimedJob, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRestart}, "node-claimed", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("create claimed job: %v", err)
	}
	claimed, err := store.ClaimNextJob(ctx, "node-claimed", claimedJob.CreatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimed == nil {
		t.Fatal("claimed job is nil")
	}

	deleted, err := store.PruneTerminalJobs(ctx, now)
	if err != nil {
		t.Fatalf("prune terminal jobs: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	gotClaimed, err := store.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get claimed job: %v", err)
	}
	if gotClaimed == nil || gotClaimed.Status != protocol.JobStatusClaimed {
		t.Fatalf("claimed job = %#v, want claimed", gotClaimed)
	}
	gotPending, err := store.GetJob(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get pending job: %v", err)
	}
	if gotPending == nil || gotPending.Status != protocol.JobStatusPending {
		t.Fatalf("pending job = %#v, want pending", gotPending)
	}
}

func TestSQLiteConcurrentHeartbeatInsertAndPrune(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	errCh := make(chan error, 240)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-race"}, now.Add(time.Duration(worker*100+i)*time.Millisecond))
				errCh <- err
			}
		}()
	}
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, err := store.PruneHeartbeats(ctx, 5)
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent prune/insert: %v", err)
		}
	}
	if _, err := store.PruneHeartbeats(ctx, 5); err != nil {
		t.Fatalf("final prune heartbeats: %v", err)
	}
	var heartbeatCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE node_id = ?`, "node-race").Scan(&heartbeatCount); err != nil {
		t.Fatalf("count node-race heartbeats: %v", err)
	}
	if heartbeatCount == 0 || heartbeatCount > 5 {
		t.Fatalf("heartbeat count = %d, want 1..5", heartbeatCount)
	}
}

func TestSQLiteRejectsActiveConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	req := protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now); err != nil {
		t.Fatalf("create first config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now.Add(time.Second)); err != ErrActiveJobExists {
		t.Fatalf("duplicate pending config_apply error = %v, want ErrActiveJobExists", err)
	}
}

func TestSQLiteConfigApplyUsesLongLeaseAndDoesNotRequeueAfterTimeout(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}, "node-apply", now); err != nil {
		t.Fatalf("create config_apply: %v", err)
	}
	claimed, err := store.ClaimNextJob(ctx, "node-apply", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if claimed == nil {
		t.Fatal("claimed job is nil")
	}
	if !claimed.ClaimExpiresAt.Equal(claimed.ClaimedAt.Add(configApplyJobClaimLease)) {
		t.Fatalf("claim expires at = %s, want claimedAt + config apply lease", claimed.ClaimExpiresAt)
	}
	if _, err := store.ClaimNextJob(ctx, "node-apply", claimed.ClaimedAt.Add(defaultJobClaimLease+time.Second)); err != nil {
		t.Fatalf("claim during long config_apply lease: %v", err)
	}
	got, err := store.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get job during lease: %v", err)
	}
	if got.Status != protocol.JobStatusClaimed {
		t.Fatalf("job status after default lease = %q, want claimed", got.Status)
	}
	next, err := store.ClaimNextJob(ctx, "node-apply", claimed.ClaimExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("claim after config_apply timeout: %v", err)
	}
	if next != nil {
		t.Fatalf("next job = %#v, want nil after timeout", next)
	}
	got, err = store.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("get job after timeout: %v", err)
	}
	if got.Status != protocol.JobStatusFailed || !IsJobClaimTimeout(*got) {
		t.Fatalf("job after timeout = %#v, want failed timeout", got)
	}
}

func TestSQLiteRejectsClaimedConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-apply"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	req := protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/config.yaml"),
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now); err != nil {
		t.Fatalf("create first config_apply: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-apply", now.Add(time.Second)); err != nil {
		t.Fatalf("claim config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, req, "node-apply", now.Add(2*time.Second)); err != ErrActiveJobExists {
		t.Fatalf("duplicate claimed config_apply error = %v, want ErrActiveJobExists", err)
	}
}

func TestSQLiteAllowsConfigApplyForDifferentNodeOrPath(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for _, nodeID := range []string{"node-a", "node-b"} {
		if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: nodeID}, now); err != nil {
			t.Fatalf("record heartbeat %s: %v", nodeID, err)
		}
	}
	reqA := protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply, PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/a.yaml")}
	reqB := protocol.CreateJobRequest{Type: protocol.JobTypeConfigApply, PayloadJSON: configApplyPayloadForTest(t, "hermes", "/etc/hermes/b.yaml")}
	if _, err := store.CreateJob(ctx, reqA, "node-a", now); err != nil {
		t.Fatalf("create node-a config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, reqB, "node-a", now.Add(time.Second)); err != nil {
		t.Fatalf("create different path config_apply: %v", err)
	}
	if _, err := store.CreateJob(ctx, reqA, "node-b", now.Add(2*time.Second)); err != nil {
		t.Fatalf("create different node config_apply: %v", err)
	}
}

func TestSQLiteNodeStoreTimesOutExpiredClaimedJob(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-timeout"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimed, err := store.ClaimNextJob(ctx, "node-timeout", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimed == nil {
		t.Fatalf("claimed job is nil")
	}
	if !claimed.ClaimExpiresAt.Equal(claimed.ClaimedAt.Add(defaultJobClaimLease)) {
		t.Fatalf("claim expires at = %s, want claimedAt + lease", claimed.ClaimExpiresAt)
	}

	next, err := store.ClaimNextJob(ctx, "node-timeout", claimed.ClaimExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("claim after timeout: %v", err)
	}
	if next != nil {
		t.Fatalf("next job = %#v, want nil", next)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got == nil {
		t.Fatalf("timed out job not found")
	}
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", got.Status)
	}
	if got.Error != jobClaimTimeoutError {
		t.Fatalf("job error = %q, want %q", got.Error, jobClaimTimeoutError)
	}
	if got.FinishedAt.IsZero() {
		t.Fatalf("finishedAt is zero")
	}
	if !got.ClaimExpiresAt.IsZero() {
		t.Fatalf("claimExpiresAt = %s, want zero after timeout", got.ClaimExpiresAt)
	}

	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", got.FinishedAt.Add(time.Second)); err != nil {
		t.Fatalf("create job after timeout: %v", err)
	}
}

func TestSQLiteAuditEventsInsertAndListNewestFirst(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	older, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{
		Actor:      "operator",
		Action:     "job.create",
		TargetNode: "node-a",
		Detail:     "deep_probe",
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatalf("append older audit: %v", err)
	}
	newer, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{
		Actor:      "sidecar",
		ActorName:  "sidecar-agent",
		Action:     "job.complete",
		TargetNode: "node-a",
		Detail:     "deep_probe",
		CreatedAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("append newer audit: %v", err)
	}

	events, err := store.ListAuditEvents(ctx, 1)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].ID != newer.ID || events[0].ID == older.ID {
		t.Fatalf("events order/limit = %#v, want newest only", events)
	}
	if events[0].ActorName != "sidecar-agent" {
		t.Fatalf("actor name = %q, want sidecar-agent", events[0].ActorName)
	}
	assertSQLiteDoesNotContainPlaintext(t, ctx, store.db, "audit_events", "secret-token-value")
}

func TestSQLiteAuditEventsFilteredByNodeActionAndLimit(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	assertAuditFiltering(t, store)
}

func TestSQLiteDesiredConfigPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Model: "gpt-5-mini"},
		},
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"node-a/hermes/default": {Provider: "anthropic", Model: "claude-sonnet-4"},
		},
	}
	if err := first.SetDesiredConfig(ctx, desired, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()
	got, err := second.GetDesiredConfig(ctx)
	if err != nil {
		t.Fatalf("get desired config: %v", err)
	}
	if got.Global.Provider != "openai" || got.NodeOverrides["node-a"].Model != "gpt-5-mini" || got.NodeRuntimeProfileOverrides["node-a/hermes/default"].Model != "claude-sonnet-4" {
		t.Fatalf("desired config = %#v, want persisted provider/model", got)
	}
}

func TestSQLiteDesiredConfigProviderCatalogRoundTripsPlaintextAPIKey(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sideplane.db")
	first, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	desired := desiredConfigWithProviderCatalogForStoreTest()
	if err := first.SetDesiredConfig(ctx, desired, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set desired config: %v", err)
	}

	var rawConfig string
	if err := first.db.QueryRowContext(ctx, `SELECT config_json FROM desired_config WHERE id = 1`).Scan(&rawConfig); err != nil {
		t.Fatalf("query raw desired config: %v", err)
	}
	for _, plaintext := range []string{"sk-global-plaintext", "node-plaintext-key"} {
		if !strings.Contains(rawConfig, plaintext) {
			t.Fatalf("raw desired config JSON does not contain plaintext apiKey %q: %s", plaintext, rawConfig)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer second.Close()
	got, err := second.GetDesiredConfig(ctx)
	if err != nil {
		t.Fatalf("get desired config: %v", err)
	}
	assertDesiredConfigProviderCatalogRoundTrip(t, got)
}

func TestSQLiteDesiredConfigHistoryAndRevert(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	assertDesiredConfigHistoryAndRevert(t, store)
}

func TestSQLiteConcurrentHeartbeatWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	const workers = 24
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodeID := fmt.Sprintf("node-%02d", i)
			_, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{
				NodeID:         nodeID,
				Hostname:       fmt.Sprintf("worker-%02d", i),
				SidecarVersion: "test",
			}, now.Add(time.Duration(i)*time.Millisecond))
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("record heartbeat: %v", err)
		}
	}

	nodes, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != workers {
		t.Fatalf("nodes length = %d, want %d", len(nodes), workers)
	}
	var heartbeatCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats`).Scan(&heartbeatCount); err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	if heartbeatCount != workers {
		t.Fatalf("heartbeat count = %d, want %d", heartbeatCount, workers)
	}
}

func TestSQLiteConcurrentClaimNextJobOnlyClaimsOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-race"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	job, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-race", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	const workers = 20
	type claimResult struct {
		job *protocol.Job
		err error
	}
	resultCh := make(chan claimResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := store.ClaimNextJob(ctx, "node-race", now.Add(time.Duration(i)*time.Millisecond))
			resultCh <- claimResult{job: claimed, err: err}
		}()
	}
	wg.Wait()
	close(resultCh)

	claimedCount := 0
	for result := range resultCh {
		if result.err != nil {
			t.Fatalf("claim job: %v", result.err)
		}
		if result.job != nil {
			claimedCount++
			if result.job.ID != job.ID {
				t.Fatalf("claimed job ID = %q, want %q", result.job.ID, job.ID)
			}
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want 1", claimedCount)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got == nil || got.Status != protocol.JobStatusClaimed {
		t.Fatalf("job after race = %#v, want claimed", got)
	}
}

func TestSQLiteConcurrentAuditAppends(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	const workers = 40
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{
				Actor:      "operator",
				Action:     "job.create",
				TargetNode: fmt.Sprintf("node-%02d", i%5),
				Detail:     fmt.Sprintf("event-%02d", i),
				CreatedAt:  now.Add(time.Duration(i) * time.Second),
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("append audit event: %v", err)
		}
	}

	events, err := store.ListAuditEvents(ctx, workers)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != workers {
		t.Fatalf("events length = %d, want %d", len(events), workers)
	}
	ids := map[string]bool{}
	for _, event := range events {
		if ids[event.ID] {
			t.Fatalf("duplicate audit event ID %q", event.ID)
		}
		ids[event.ID] = true
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
	if nodes[0].Runtimes[0].DeploymentMode != "container" {
		t.Fatalf("runtime deployment mode = %q, want container", nodes[0].Runtimes[0].DeploymentMode)
	}
	if len(nodes[0].Runtimes[0].Warnings) != 1 || nodes[0].Runtimes[0].Warnings[0] != "config path unreadable" {
		t.Fatalf("runtime warnings = %#v, want config path unreadable", nodes[0].Runtimes[0].Warnings)
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

func countSQLiteRowsForValue(t *testing.T, ctx context.Context, db *sql.DB, table string, column string, value string) int {
	t.Helper()
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, value).Scan(&count)
	if err != nil {
		t.Fatalf("count %s.%s: %v", table, column, err)
	}
	return count
}
