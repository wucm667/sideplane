package store

import (
	"context"
	"errors"
	"strings"
	"sync"
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
		Runtimes: []protocol.RuntimeStatus{{Name: "default", Type: "hermes", Warnings: []string{"config path unreadable"}}},
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
	nodes[0].Runtimes[0].Warnings[0] = "mutated"
	again, err := store.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes again: %v", err)
	}
	if again[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("store snapshot was mutated: %#v", again[0].Runtimes)
	}
	if len(again[0].Runtimes[0].Warnings) != 1 || again[0].Runtimes[0].Warnings[0] != "config path unreadable" {
		t.Fatalf("runtime warnings = %#v, want preserved warning", again[0].Runtimes[0].Warnings)
	}
}

func TestMemoryNodeStoreListNodesFilteredPaginatesAndCounts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStoreListNodesFilteredByLabels(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStoreSetGetOverwriteAndDeleteLabels(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

	labels["role"] = "mutated"
	again, err := store.GetNodeLabels(ctx, "node-labels")
	if err != nil {
		t.Fatalf("get labels again: %v", err)
	}
	if again["role"] != "canary" {
		t.Fatalf("returned labels mutated store: %#v", again)
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

func TestMemoryNodeStoreSetGetMaintenance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
	if err := store.SetNodeMaintenance(ctx, "node-maint", false); err != nil {
		t.Fatalf("clear maintenance: %v", err)
	}
	maintenance, err = store.GetNodeMaintenance(ctx, "node-maint")
	if err != nil {
		t.Fatalf("get cleared maintenance: %v", err)
	}
	if maintenance {
		t.Fatalf("cleared maintenance = true, want false")
	}
	if err := store.SetNodeMaintenance(ctx, "missing", true); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node set maintenance error = %v, want ErrNodeNotFound", err)
	}
}

func TestMemoryNodeStoreLabelValidationAndDeleteCascade(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
}

func TestMemoryNodeStorePruneHeartbeatsKeepsLatestPerNode(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

	gotA := store.heartbeats["node-a"]
	if len(gotA) != 2 || !gotA[0].Equal(now.Add(4*time.Minute)) || !gotA[1].Equal(now.Add(3*time.Minute)) {
		t.Fatalf("node-a heartbeats = %v, want latest two", gotA)
	}
	gotB := store.heartbeats["node-b"]
	if len(gotB) != 2 || !gotB[0].Equal(now.Add(2*time.Minute)) || !gotB[1].Equal(now.Add(time.Minute)) {
		t.Fatalf("node-b heartbeats = %v, want latest two", gotB)
	}

	deleted, err = store.PruneHeartbeats(ctx, 2)
	if err != nil {
		t.Fatalf("second prune heartbeats: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second deleted = %d, want 0", deleted)
	}
}

func TestMemoryOperatorTokenFlow(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	created, err := store.CreateOperatorToken(ctx, "ops laptop", protocol.OperatorTokenScopeReadonly, now)
	if err != nil {
		t.Fatalf("create operator token: %v", err)
	}
	if created.Token == "" {
		t.Fatalf("plaintext token is empty")
	}
	if created.OperatorToken.ID == "" || created.OperatorToken.Name != "ops laptop" {
		t.Fatalf("operator token metadata = %+v, want id/name", created.OperatorToken)
	}
	if created.OperatorToken.Scope != protocol.OperatorTokenScopeReadonly {
		t.Fatalf("operator token scope = %q, want readonly", created.OperatorToken.Scope)
	}

	tokens, err := store.ListOperatorTokens(ctx)
	if err != nil {
		t.Fatalf("list operator tokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != created.OperatorToken.ID || tokens[0].Name != "ops laptop" {
		t.Fatalf("operator token list = %+v, want created metadata", tokens)
	}
	if tokens[0].Scope != protocol.OperatorTokenScopeReadonly {
		t.Fatalf("listed operator token scope = %q, want readonly", tokens[0].Scope)
	}
	if strings.Contains(tokens[0].ID+tokens[0].Name, created.Token) {
		t.Fatalf("operator token metadata exposed plaintext token")
	}

	tokenID, scope, ok, err := store.VerifyOperatorToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("verify operator token: %v", err)
	}
	if !ok || tokenID != created.OperatorToken.ID || scope != protocol.OperatorTokenScopeReadonly {
		t.Fatalf("verify operator token = id:%q scope:%q ok:%t, want created readonly token", tokenID, scope, ok)
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
	*tokens[0].LastUsedAt = tokens[0].LastUsedAt.Add(time.Hour)
	tokens, err = store.ListOperatorTokens(ctx)
	if err != nil {
		t.Fatalf("list operator tokens after mutation: %v", err)
	}
	if tokens[0].LastUsedAt == nil || !tokens[0].LastUsedAt.Equal(usedAt) {
		t.Fatalf("mutated lastUsedAt leaked into store: %v", tokens[0].LastUsedAt)
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

func TestMemoryNodeStoreAlertWebhookLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	created, err := store.CreateAlertWebhook(ctx, protocol.CreateAlertWebhookRequest{
		URL:    "https://hooks.example.com/sideplane",
		Events: []protocol.AlertEventType{protocol.AlertEventNodeOffline, protocol.AlertEventNodeOffline, protocol.AlertEventRolloutFailed},
		Secret: "shhh",
	}, now)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if created.ID == "" || !created.HasSecret || created.Disabled {
		t.Fatalf("created webhook = %+v, want id, hasSecret, enabled", created)
	}
	if len(created.Events) != 2 {
		t.Fatalf("created events = %+v, want deduplicated to 2", created.Events)
	}

	// Metadata listing never exposes the secret value.
	listed, err := store.ListAlertWebhooks(ctx)
	if err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || !listed[0].HasSecret {
		t.Fatalf("listed webhooks = %+v, want created metadata", listed)
	}

	// Delivery targets include the secret and filter by subscribed event.
	targets, err := store.ListAlertWebhookTargets(ctx, protocol.AlertEventNodeOffline)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Secret != "shhh" || targets[0].URL != "https://hooks.example.com/sideplane" {
		t.Fatalf("offline targets = %+v, want webhook with secret", targets)
	}
	driftTargets, err := store.ListAlertWebhookTargets(ctx, protocol.AlertEventNodeDrift)
	if err != nil {
		t.Fatalf("list drift targets: %v", err)
	}
	if len(driftTargets) != 0 {
		t.Fatalf("drift targets = %+v, want none (not subscribed)", driftTargets)
	}

	if err := store.DeleteAlertWebhook(ctx, created.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if err := store.DeleteAlertWebhook(ctx, created.ID); !errors.Is(err, ErrAlertWebhookNotFound) {
		t.Fatalf("double delete err = %v, want ErrAlertWebhookNotFound", err)
	}
}

func TestMemoryNodeStoreRolloutTemplateLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	spec := protocol.RolloutSpec{
		Selector:    map[string]string{"role": "canary"},
		RuntimeType: "hermes",
		Target:      protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		BatchSize:   2,
		Live:        true,
	}

	created, err := store.CreateRolloutTemplate(ctx, "canary", spec, now)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.ID == "" || created.Name != "canary" || created.Spec.BatchSize != 2 {
		t.Fatalf("created template = %+v, want name and spec", created)
	}

	// Stored spec is a copy; mutating the input must not affect the store.
	spec.Selector["role"] = "mutated"
	got, err := store.GetRolloutTemplate(ctx, created.ID)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if got.Spec.Selector["role"] != "canary" {
		t.Fatalf("template selector = %q, want isolated copy", got.Spec.Selector["role"])
	}

	listed, err := store.ListRolloutTemplates(ctx)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed templates = %+v, want created template", listed)
	}

	if err := store.DeleteRolloutTemplate(ctx, created.ID); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	if _, err := store.GetRolloutTemplate(ctx, created.ID); !errors.Is(err, ErrRolloutTemplateNotFound) {
		t.Fatalf("get after delete err = %v, want ErrRolloutTemplateNotFound", err)
	}
	if _, err := store.CreateRolloutTemplate(ctx, "  ", spec, now); err == nil {
		t.Fatalf("expected error for blank template name")
	}
}

func TestMemoryNodeStoreServerSettingsExpectedSidecarVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()

	settings, err := store.GetServerSettings(ctx)
	if err != nil {
		t.Fatalf("get default settings: %v", err)
	}
	if settings.ExpectedSidecarVersion != "" {
		t.Fatalf("default expected version = %q, want empty", settings.ExpectedSidecarVersion)
	}

	if err := store.SetExpectedSidecarVersion(ctx, "  v1.2.3  "); err != nil {
		t.Fatalf("set expected version: %v", err)
	}
	settings, err = store.GetServerSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.ExpectedSidecarVersion != "v1.2.3" {
		t.Fatalf("expected version = %q, want trimmed v1.2.3", settings.ExpectedSidecarVersion)
	}

	if err := store.SetExpectedSidecarVersion(ctx, ""); err != nil {
		t.Fatalf("clear expected version: %v", err)
	}
	settings, _ = store.GetServerSettings(ctx)
	if settings.ExpectedSidecarVersion != "" {
		t.Fatalf("expected version after clear = %q, want empty", settings.ExpectedSidecarVersion)
	}
}

func TestMemoryNodeStoreAlertWebhookValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		req  protocol.CreateAlertWebhookRequest
	}{
		{name: "empty url", req: protocol.CreateAlertWebhookRequest{Events: []protocol.AlertEventType{protocol.AlertEventNodeOffline}}},
		{name: "bad scheme", req: protocol.CreateAlertWebhookRequest{URL: "ftp://x.example.com", Events: []protocol.AlertEventType{protocol.AlertEventNodeOffline}}},
		{name: "no events", req: protocol.CreateAlertWebhookRequest{URL: "https://x.example.com"}},
		{name: "unknown event", req: protocol.CreateAlertWebhookRequest{URL: "https://x.example.com", Events: []protocol.AlertEventType{"node.exploded"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.CreateAlertWebhook(ctx, tc.req, now); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestMemoryNodeStoreDeleteNodeRemovesAssociatedData(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
	if _, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-keep", now); err != nil {
		t.Fatalf("create keep job: %v", err)
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
	jobs, err := store.ListNodeJobs(ctx, "node-delete")
	if err != nil {
		t.Fatalf("list deleted jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("deleted jobs length = %d, want 0", len(jobs))
	}
	events, err := store.ListAuditEvents(ctx, 100)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	for _, event := range events {
		if event.TargetNode == "node-delete" {
			t.Fatalf("audit event for deleted node remains: %#v", event)
		}
	}
	if err := store.DeleteNode(ctx, "node-delete"); err != ErrNodeNotFound {
		t.Fatalf("delete missing node error = %v, want ErrNodeNotFound", err)
	}
}

func TestMemoryNodeStoreTimesOutExpiredClaimedJob(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStoreFailJobPersistsResultJSON(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
	resultJSON := `{"steps":[{"name":"rolled_back","status":"completed"}]}`
	if err := store.FailJob(ctx, job.ID, protocol.JobResultRequest{
		Status:     protocol.JobStatusFailed,
		ResultJSON: resultJSON,
		Error:      "apply failed",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got == nil {
		t.Fatalf("failed job not found")
	}
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", got.Status)
	}
	if got.ResultJSON != resultJSON {
		t.Fatalf("result JSON = %q, want %q", got.ResultJSON, resultJSON)
	}
	if got.Error != "apply failed" {
		t.Fatalf("job error = %q, want apply failed", got.Error)
	}
}

func TestMemoryNodeStoreListNodeJobsFiltered(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryRolloutLifecycleListUpdateAndPrune(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
	if got == nil || got.ID != first.ID || got.Batches[0].Nodes["node-a"].State != protocol.RolloutNodeStatePending {
		t.Fatalf("got rollout = %#v, want first rollout", got)
	}
	got.Batches[0].Nodes["node-a"] = protocol.RolloutNodeProgress{NodeID: "node-a", State: protocol.RolloutNodeStateFailed}
	again, err := store.GetRollout(ctx, first.ID)
	if err != nil {
		t.Fatalf("get rollout again: %v", err)
	}
	if again.Batches[0].Nodes["node-a"].State != protocol.RolloutNodeStatePending {
		t.Fatalf("stored rollout mutated through returned pointer: %#v", again)
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

func TestMemoryRolloutConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStorePrunesTerminalJobsAndAuditEvents(t *testing.T) {
	assertRetentionPruning(t, NewMemoryNodeStore())
}

func TestMemoryNodeStoreRejectsActiveConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryConfigApplyUsesLongLeaseAndDoesNotRequeueAfterTimeout(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStoreRejectsClaimedConfigApplyForSamePath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryNodeStoreAllowsConfigApplyForDifferentNodeOrPath(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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

func TestMemoryAuditEventsInsertAndListNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
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
}

func TestMemoryAuditEventsFilteredByNodeActionAndLimit(t *testing.T) {
	assertAuditFiltering(t, NewMemoryNodeStore())
}

func TestMemoryDesiredConfigPersistsCopy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryNodeStore()
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Model: "gpt-5-mini"},
		},
		NodeRuntimeProfileOverrides: map[string]protocol.ProviderModelConfig{
			"node-a/hermes/default": {Provider: "anthropic", Model: "claude-sonnet-4"},
		},
	}
	if err := store.SetDesiredConfig(ctx, desired, time.Now().UTC()); err != nil {
		t.Fatalf("set desired config: %v", err)
	}
	desired.NodeOverrides["node-a"] = protocol.ProviderModelConfig{Model: "mutated"}
	desired.NodeRuntimeProfileOverrides["node-a/hermes/default"] = protocol.ProviderModelConfig{Model: "mutated"}

	got, err := store.GetDesiredConfig(ctx)
	if err != nil {
		t.Fatalf("get desired config: %v", err)
	}
	if got.NodeOverrides["node-a"].Model != "gpt-5-mini" {
		t.Fatalf("stored desired config mutated: %#v", got)
	}
	if got.NodeRuntimeProfileOverrides["node-a/hermes/default"].Model != "claude-sonnet-4" {
		t.Fatalf("stored node runtime profile desired config mutated: %#v", got)
	}
}

func TestMemoryDesiredConfigHistoryAndRevert(t *testing.T) {
	assertDesiredConfigHistoryAndRevert(t, NewMemoryNodeStore())
}

func assertDesiredConfigHistoryAndRevert(t *testing.T, nodeStore Store) {
	t.Helper()
	ctx := context.Background()
	first := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}
	second := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "anthropic", Model: "claude-sonnet-4"},
	}
	if err := nodeStore.SetDesiredConfig(ctx, first, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set first desired config: %v", err)
	}
	if err := nodeStore.SetDesiredConfig(ctx, second, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set second desired config: %v", err)
	}

	page, err := nodeStore.ListDesiredConfigHistory(ctx, DesiredConfigHistoryFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list desired config history: %v", err)
	}
	if page.Total != 2 || page.Limit != 1 || len(page.History) != 1 {
		t.Fatalf("history page = %+v, want total 2 limit 1 length 1", page)
	}
	if page.History[0].Config.Global.Model != "claude-sonnet-4" || page.History[0].Actor != desiredConfigHistoryActorOperator || !strings.HasPrefix(page.History[0].DesiredHash, "sha256:") {
		t.Fatalf("newest history entry = %+v, want second config/operator/hash", page.History[0])
	}

	full, err := nodeStore.ListDesiredConfigHistory(ctx, DesiredConfigHistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list full desired config history: %v", err)
	}
	if len(full.History) != 2 {
		t.Fatalf("full history length = %d, want 2", len(full.History))
	}
	firstHistoryID := full.History[1].ID
	reverted, err := nodeStore.RevertDesiredConfig(ctx, firstHistoryID)
	if err != nil {
		t.Fatalf("revert desired config: %v", err)
	}
	if reverted.ID == firstHistoryID || reverted.Config.Global.Model != "gpt-4o" {
		t.Fatalf("reverted history = %+v, want new entry for first config", reverted)
	}
	current, err := nodeStore.GetDesiredConfig(ctx)
	if err != nil {
		t.Fatalf("get reverted desired config: %v", err)
	}
	if current.Global.Provider != "openai" || current.Global.Model != "gpt-4o" {
		t.Fatalf("current desired config = %+v, want first config", current)
	}
	after, err := nodeStore.ListDesiredConfigHistory(ctx, DesiredConfigHistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list history after revert: %v", err)
	}
	if after.Total != 3 || len(after.History) != 3 || after.History[0].ID != reverted.ID {
		t.Fatalf("history after revert = %+v, want appended revert entry first", after)
	}
}

func rolloutForStoreTest(id string, state protocol.RolloutState, createdAt time.Time) protocol.Rollout {
	return protocol.Rollout{
		ID:    id,
		State: state,
		Spec: protocol.RolloutSpec{
			NodeIDs:       []string{"node-a", "node-b"},
			RuntimeType:   "hermes",
			Profile:       "default",
			Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			BatchSize:     1,
			HealthTimeout: 5 * time.Minute,
		},
		Batches: []protocol.RolloutBatch{{
			Index:   0,
			NodeIDs: []string{"node-a"},
			State:   protocol.RolloutBatchStatePending,
			Nodes: map[string]protocol.RolloutNodeProgress{
				"node-a": {NodeID: "node-a", State: protocol.RolloutNodeStatePending},
			},
		}},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func assertAuditFiltering(t *testing.T, auditStore AuditStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	events := []protocol.AuditEvent{
		{Actor: "operator", Action: "job.create", TargetNode: "node-a", CreatedAt: now},
		{Actor: "operator", Action: "job.create", TargetNode: "node-b", CreatedAt: now.Add(time.Minute)},
		{Actor: "sidecar", Action: "job.fail", TargetNode: "node-a", CreatedAt: now.Add(2 * time.Minute)},
	}
	for _, event := range events {
		if _, err := auditStore.AppendAuditEvent(ctx, event); err != nil {
			t.Fatalf("append audit event: %v", err)
		}
	}

	got, err := auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a"})
	if err != nil {
		t.Fatalf("filter by node: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.fail", "node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{Action: "job.create"})
	if err != nil {
		t.Fatalf("filter by action: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-b/job.create", "node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a", Action: "job.create"})
	if err != nil {
		t.Fatalf("filter by node and action: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.create"})

	got, err = auditStore.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-a", Limit: 1})
	if err != nil {
		t.Fatalf("filter with limit: %v", err)
	}
	assertAuditEventKeys(t, got, []string{"node-a/job.fail"})
}

func assertRetentionPruning(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)

	if _, err := store.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-retention"}, now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	oldCompleted, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("create old completed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", oldCompleted.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim old completed job: %v", err)
	}
	if err := store.CompleteJob(ctx, oldCompleted.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, oldCompleted.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("complete old job: %v", err)
	}

	oldFailed, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-71*time.Hour))
	if err != nil {
		t.Fatalf("create old failed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", oldFailed.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim old failed job: %v", err)
	}
	if err := store.FailJob(ctx, oldFailed.ID, protocol.JobResultRequest{Status: protocol.JobStatusFailed, Error: "probe failed"}, oldFailed.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("fail old job: %v", err)
	}

	recentCompleted, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-retention", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("create recent completed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", recentCompleted.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim recent completed job: %v", err)
	}
	if err := store.CompleteJob(ctx, recentCompleted.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, recentCompleted.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("complete recent job: %v", err)
	}

	claimed, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRestart}, "node-retention", now.Add(-70*time.Hour))
	if err != nil {
		t.Fatalf("create claimed job: %v", err)
	}
	if _, err := store.ClaimNextJob(ctx, "node-retention", claimed.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("claim active job: %v", err)
	}
	pending, err := store.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeRollback}, "node-retention", claimed.CreatedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	deletedJobs, err := store.PruneTerminalJobs(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune terminal jobs: %v", err)
	}
	if deletedJobs != 2 {
		t.Fatalf("deleted jobs = %d, want 2", deletedJobs)
	}
	for _, jobID := range []string{oldCompleted.ID, oldFailed.ID} {
		got, err := store.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get pruned job %s: %v", jobID, err)
		}
		if got != nil {
			t.Fatalf("job %s remains after pruning: %#v", jobID, got)
		}
	}
	for _, jobID := range []string{recentCompleted.ID, claimed.ID, pending.ID} {
		got, err := store.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get retained job %s: %v", jobID, err)
		}
		if got == nil {
			t.Fatalf("job %s was pruned unexpectedly", jobID)
		}
	}

	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "old", TargetNode: "node-retention", CreatedAt: now.Add(-72 * time.Hour)}); err != nil {
		t.Fatalf("append old audit event: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, protocol.AuditEvent{Actor: "operator", Action: "recent", TargetNode: "node-retention", CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("append recent audit event: %v", err)
	}
	deletedEvents, err := store.PruneAuditEvents(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune audit events: %v", err)
	}
	if deletedEvents != 1 {
		t.Fatalf("deleted audit events = %d, want 1", deletedEvents)
	}
	events, err := store.ListAuditEventsFiltered(ctx, AuditFilter{NodeID: "node-retention"})
	if err != nil {
		t.Fatalf("list audit events after pruning: %v", err)
	}
	assertAuditEventKeys(t, events, []string{"node-retention/recent"})
}

func assertAuditEventKeys(t *testing.T, events []protocol.AuditEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event length = %d, want %d: %#v", len(events), len(want), events)
	}
	for i, event := range events {
		got := event.TargetNode + "/" + event.Action
		if got != want[i] {
			t.Fatalf("event[%d] = %q, want %q; events=%#v", i, got, want[i], events)
		}
	}
}
