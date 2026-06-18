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

func TestProbeCreatesDeepProbeJob(t *testing.T) {
	job := protocol.Job{
		ID:        "job-1",
		NodeID:    "node-a",
		Type:      protocol.JobTypeDeepProbe,
		Status:    protocol.JobStatusPending,
		CreatedAt: time.Now().UTC(),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/nodes/node-a/jobs" {
			t.Fatalf("path = %s, want /api/nodes/node-a/jobs", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.CreateJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Type != protocol.JobTypeDeepProbe {
			t.Fatalf("job type = %q, want deep_probe", req.Type)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(job); err != nil {
			t.Fatalf("encode job: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"probe", "node-a", "--server", server.URL, "--operator-token", "test-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "job job-1 pending") {
		t.Fatalf("stdout = %q, want job summary", got)
	}
}

func TestProbeWaitPrintsCompletedResultSummary(t *testing.T) {
	resultJSON, err := json.Marshal(protocol.DeepProbeResult{
		Runtimes: []protocol.RuntimeStatus{
			{Name: "hermes", Model: "gpt-4o"},
		},
		ConfigSnapshots: []protocol.RuntimeConfigSnapshot{
			{RuntimeName: "hermes", RuntimeType: "hermes", Provider: "openai", Model: "gpt-4o"},
		},
	})
	if err != nil {
		t.Fatalf("marshal probe result: %v", err)
	}
	created := protocol.Job{
		ID:        "job-2",
		NodeID:    "node-b",
		Type:      protocol.JobTypeDeepProbe,
		Status:    protocol.JobStatusPending,
		CreatedAt: time.Now().UTC(),
	}
	completed := created
	completed.Status = protocol.JobStatusCompleted
	completed.ResultJSON = string(resultJSON)
	completed.FinishedAt = time.Now().UTC()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/node-b/jobs":
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(created); err != nil {
				t.Fatalf("encode created job: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/node-b/jobs":
			if err := json.NewEncoder(w).Encode([]protocol.Job{completed}); err != nil {
				t.Fatalf("encode completed job: %v", err)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"probe", "node-b", "--server", server.URL, "--wait"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{"job job-2 completed", "runtimes: hermes:gpt-4o", "config snapshots: 1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
