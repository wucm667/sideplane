package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestFleetStatusPrintsCompactTable(t *testing.T) {
	now := time.Now().UTC()
	nodes := []cliNodeStatus{
		{
			NodeStatus: protocol.NodeStatus{
				NodeID:          "node-a",
				State:           protocol.NodeStateFresh,
				LastHeartbeatAt: now.Add(-2 * time.Minute),
				Runtimes: []protocol.RuntimeStatus{
					{Name: "hermes", Model: "gpt-4o"},
				},
			},
		},
		{
			NodeStatus: protocol.NodeStatus{
				NodeID:          "node-b",
				State:           protocol.NodeStateStale,
				LastHeartbeatAt: now.Add(-8 * time.Minute),
				Runtimes: []protocol.RuntimeStatus{
					{Name: "openclaw"},
				},
			},
			Drift: true,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/nodes" {
			t.Fatalf("path = %s, want /api/nodes", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(nodes); err != nil {
			t.Fatalf("encode nodes: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"fleet", "status", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"NODE ID",
		"STATE",
		"RUNTIMES",
		"DRIFT",
		"HEARTBEAT",
		"node-a",
		"fresh",
		"hermes:gpt-4o",
		"no",
		"node-b",
		"stale",
		"openclaw",
		"yes",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
