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

func TestFleetStatusJSONOutput(t *testing.T) {
	nodes := []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-json",
			State:           protocol.NodeStateFresh,
			LastHeartbeatAt: time.Now().UTC(),
		},
		Drift: true,
	}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes", nodes))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"fleet", "status", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got []cliNodeStatus
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if len(got) != 1 || got[0].NodeID != "node-json" || !got[0].Drift {
		t.Fatalf("nodes = %#v, want node-json with drift", got)
	}
}

func TestNodeInspectPrintsNodeDetail(t *testing.T) {
	now := time.Now().UTC()
	nodes := []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-a",
			Hostname:        "host-a",
			State:           protocol.NodeStateFresh,
			LastHeartbeatAt: now.Add(-time.Minute),
			SidecarVersion:  "dev",
			ConfigHash:      "sha256:abc",
			Runtimes: []protocol.RuntimeStatus{{
				Name:       "hermes",
				Type:       "hermes",
				State:      "running",
				Version:    "1.2.3",
				Provider:   "openai",
				Model:      "gpt-4o",
				ConfigHash: "sha256:def",
				Warnings:   []string{"config path unreadable"},
			}},
		},
		Drift: true,
	}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes", nodes))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "inspect", "node-a", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Node: node-a", "Hostname: host-a", "Drift: yes", "hermes", "openai", "gpt-4o", "config path unreadable"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestNodeInspectJSONAndMissingNode(t *testing.T) {
	nodes := []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-a",
			State:           protocol.NodeStateFresh,
			LastHeartbeatAt: time.Now().UTC(),
		},
	}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes", nodes))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "inspect", "node-a", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got cliNodeStatus
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.NodeID != "node-a" {
		t.Fatalf("nodeId = %q, want node-a", got.NodeID)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"node", "inspect", "missing", "--server", server.URL}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `node "missing" not found`) {
		t.Fatalf("stderr = %q, want missing node message", stderr.String())
	}
}

func TestHelpListsCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"Usage: sideplane <command>",
		"fleet status",
		"probe <nodeId>",
		"restart <nodeId>",
		"rollback <nodeId>",
		"jobs list <nodeId>",
		"audit list",
		"config apply <id>",
		"config preview <id>",
		"config get",
		"config set",
		"node inspect <id>",
		"node remove <id>",
		"enrollment create",
		"version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help missing %q:\n%s", want, output)
		}
	}
}

func TestPerCommandHelpPrintsFlags(t *testing.T) {
	tests := [][]string{
		{"config", "apply", "--help"},
		{"restart", "--help"},
		{"rollback", "--help"},
		{"jobs", "list", "--help"},
		{"enrollment", "create", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}
			output := stdout.String()
			if !strings.Contains(output, "usage: sideplane") || !strings.Contains(output, "--server") {
				t.Fatalf("help output missing usage/server flag:\n%s", output)
			}
		})
	}
}

func TestEnvFallbackAndFlagPrecedence(t *testing.T) {
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer env-token" {
			t.Fatalf("env Authorization = %q, want Bearer env-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(protocol.Job{
			ID:        "job-env",
			NodeID:    "node-a",
			Type:      protocol.JobTypeDeepProbe,
			Status:    protocol.JobStatusPending,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("encode env job: %v", err)
		}
	}))
	defer envServer.Close()

	flagServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer flag-token" {
			t.Fatalf("flag Authorization = %q, want Bearer flag-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(protocol.Job{
			ID:        "job-flag",
			NodeID:    "node-a",
			Type:      protocol.JobTypeDeepProbe,
			Status:    protocol.JobStatusPending,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("encode flag job: %v", err)
		}
	}))
	defer flagServer.Close()

	t.Setenv(serverURLEnv, envServer.URL)
	t.Setenv("SIDEPLANE_OPERATOR_TOKEN", "env-token")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"probe", "node-a"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("env fallback run returned %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "job job-env pending") {
		t.Fatalf("stdout = %q, want env server job", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"probe", "node-a", "--server", flagServer.URL, "--operator-token", "flag-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("flag precedence run returned %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "job job-flag pending") {
		t.Fatalf("stdout = %q, want flag server job", stdout.String())
	}
}

func TestUnknownCommandPrintsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"fleet", "unknown"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	output := stderr.String()
	for _, want := range []string{"unknown command: fleet unknown", "Usage: sideplane <command>"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr missing %q:\n%s", want, output)
		}
	}
}

func TestUnknownNestedCommandsPrintHelp(t *testing.T) {
	tests := [][]string{
		{"config", "unknown"},
		{"jobs", "unknown"},
		{"node", "unknown"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("run returned %d, want 1", code)
			}
			output := stderr.String()
			if !strings.Contains(output, "unknown command: "+strings.Join(args, " ")) || !strings.Contains(output, "Usage: sideplane <command>") {
				t.Fatalf("stderr missing unknown command help:\n%s", output)
			}
		})
	}
}

func TestCommandsRejectMissingRequiredArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "probe node", args: []string{"probe"}, want: "usage: sideplane probe"},
		{name: "restart node", args: []string{"restart"}, want: "usage: sideplane restart"},
		{name: "jobs node", args: []string{"jobs", "list"}, want: "usage: sideplane jobs list"},
		{name: "config apply node", args: []string{"config", "apply"}, want: "usage: sideplane config apply"},
		{name: "config preview node", args: []string{"config", "preview"}, want: "usage: sideplane config preview"},
		{name: "node inspect id", args: []string{"node", "inspect"}, want: "usage: sideplane node inspect"},
		{name: "node remove id", args: []string{"node", "remove"}, want: "usage: sideplane node remove"},
		{name: "rollback backup", args: []string{"rollback", "node-a"}, want: "--backup-ref is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("run returned 0, want failure")
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestAPIErrorMessageRedactsJSONAndPlainTextSecrets(t *testing.T) {
	jsonMessage := apiErrorMessage([]byte(`{"code":"bad_request","message":"token=secret-token status=bad"}`))
	if strings.Contains(jsonMessage, "secret-token") || !strings.Contains(jsonMessage, "token=[REDACTED]") {
		t.Fatalf("JSON API error message = %q, want redacted token", jsonMessage)
	}

	textMessage := apiErrorMessage([]byte(`authorization:Bearer-secret status=bad`))
	if strings.Contains(textMessage, "Bearer-secret") || !strings.Contains(textMessage, "authorization:[REDACTED]") {
		t.Fatalf("plain API error message = %q, want redacted authorization", textMessage)
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "sideplane dev") {
		t.Fatalf("stdout = %q, want version", got)
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

func TestProbeJSONOutput(t *testing.T) {
	job := protocol.Job{ID: "job-probe-json", NodeID: "node-json", Type: protocol.JobTypeDeepProbe, Status: protocol.JobStatusPending}
	server := httptest.NewServer(jsonHandlerWithStatus(t, http.MethodPost, "/api/nodes/node-json/jobs", http.StatusCreated, job))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"probe", "node-json", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.Job
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.ID != "job-probe-json" || got.Type != protocol.JobTypeDeepProbe {
		t.Fatalf("job = %#v, want probe JSON job", got)
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

func TestJobsListPrintsTableAndUsesFilters(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	jobs := []protocol.Job{{
		ID:         "job-1",
		NodeID:     "node-a",
		Type:       protocol.JobTypeDeepProbe,
		Status:     protocol.JobStatusCompleted,
		CreatedAt:  now,
		FinishedAt: now.Add(time.Minute),
	}}
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/nodes/node-a/jobs" {
			t.Fatalf("path = %s, want /api/nodes/node-a/jobs", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jobs); err != nil {
			t.Fatalf("encode jobs: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"jobs", "list", "node-a", "--server", server.URL, "--limit", "25", "--status", "completed"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if gotQuery != "limit=25&status=completed" && gotQuery != "status=completed&limit=25" {
		t.Fatalf("query = %q, want limit/status", gotQuery)
	}
	output := stdout.String()
	for _, want := range []string{"JOB ID", "TYPE", "STATUS", "CREATED", "FINISHED/ERROR", "job-1", "deep_probe", "completed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestJobsListJSONAndInvalidStatus(t *testing.T) {
	jobs := []protocol.Job{{
		ID:        "job-2",
		NodeID:    "node-a",
		Type:      protocol.JobTypeConfigApply,
		Status:    protocol.JobStatusPending,
		CreatedAt: time.Now().UTC(),
	}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes/node-a/jobs", jobs))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"jobs", "list", "node-a", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got []protocol.Job
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(got) != 1 || got[0].ID != "job-2" {
		t.Fatalf("jobs = %#v, want job-2", got)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"jobs", "list", "node-a", "--server", server.URL, "--status", "unknown"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `unsupported status "unknown"`) {
		t.Fatalf("stderr = %q, want invalid status", stderr.String())
	}
}

func TestAuditListPrintsTableAndUsesFilters(t *testing.T) {
	events := []protocol.AuditEvent{{
		ID:         "audit-1",
		Actor:      "operator",
		Action:     "job.create",
		TargetNode: "node-a",
		Detail:     "deep_probe token=secret-token",
		CreatedAt:  time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	}}
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/audit" {
			t.Fatalf("path = %s, want /api/audit", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(protocol.ListAuditEventsResponse{Events: events}); err != nil {
			t.Fatalf("encode audit response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"audit", "list", "--server", server.URL, "--node-id", "node-a", "--action", "job.create", "--limit", "25"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"nodeId=node-a", "action=job.create", "limit=25"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query = %q, missing %s", gotQuery, want)
		}
	}
	output := stdout.String()
	for _, want := range []string{"CREATED", "ACTOR", "ACTION", "NODE", "DETAIL", "operator", "job.create", "node-a", "deep_probe"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("audit output leaked secret: %s", output)
	}
}

func TestAuditListJSONOutput(t *testing.T) {
	resp := protocol.ListAuditEventsResponse{Events: []protocol.AuditEvent{{
		ID:        "audit-2",
		Actor:     "sidecar",
		Action:    "job.complete",
		CreatedAt: time.Now().UTC(),
	}}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/audit", resp))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"audit", "list", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.ListAuditEventsResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "audit-2" {
		t.Fatalf("events = %#v, want audit-2", got.Events)
	}
}

func TestConfigPreviewPrintsSummary(t *testing.T) {
	effective := protocol.EffectiveConfigResponse{
		NodeID:      "node-a",
		RuntimeType: "hermes",
		Profile:     "default",
		Effective: protocol.ProviderModelConfig{
			Provider: "openai",
			Model:    "gpt-4o",
		},
		DesiredHash: "sha256:desired",
		Actual:      &protocol.RuntimeConfigSnapshot{ConfigHash: "sha256:actual"},
		Diff: []protocol.ConfigDiffEntry{{
			Field:   "model",
			Actual:  "gpt-3.5",
			Desired: "gpt-4o",
			Change:  protocol.ConfigDiffChangeUpdate,
		}},
	}
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/config/effective" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(effective); err != nil {
			t.Fatalf("encode effective config: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "preview", "node-a", "--server", server.URL, "--runtime-type", "hermes", "--profile", "default", "--actual-hash", "sha256:provided"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"nodeId=node-a", "runtimeType=hermes", "profile=default", "actualHash=sha256%3Aprovided"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query = %q, missing %s", gotQuery, want)
		}
	}
	output := stdout.String()
	for _, want := range []string{"Node: node-a", "Desired provider: openai", "Desired model: gpt-4o", "Desired hash: sha256:desired", "Actual hash: sha256:provided", "model", "gpt-3.5", "gpt-4o"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigPreviewJSONOutput(t *testing.T) {
	effective := protocol.EffectiveConfigResponse{
		NodeID:    "node-a",
		Effective: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/config/effective", effective))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "preview", "node-a", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.EffectiveConfigResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if got.NodeID != "node-a" || got.Effective.Provider != "openai" {
		t.Fatalf("effective = %#v, want node-a/openai", got)
	}
}

func TestConfigApplyDryRunDefaultAndOperatorToken(t *testing.T) {
	job := protocol.Job{
		ID:          "job-apply",
		NodeID:      "node-a",
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusPending,
		PayloadJSON: signedPlanPayload(t, "plan-dry-run", protocol.ConfigPlanModeDryRun),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/nodes/node-a/config-apply" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.ConfigApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.DryRun == nil || !*req.DryRun {
			t.Fatalf("dryRun = %#v, want true", req.DryRun)
		}
		if req.RuntimeType != "hermes" || req.Profile != "default" {
			t.Fatalf("request = %#v, want hermes/default", req)
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
	code := run([]string{"config", "apply", "node-a", "--server", server.URL, "--operator-token", "test-token", "--runtime-type", "hermes", "--profile", "default", "--config-path", "fake-config.json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Plan: plan-dry-run", "Job: job-apply", "Mode: dry_run", "Status: pending", "Requested config path: fake-config.json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestConfigApplyLiveRequiresYes(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "apply", "node-a", "--server", server.URL, "--live"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if called {
		t.Fatal("server was called despite missing --yes")
	}
	if !strings.Contains(stderr.String(), "--live requires --yes") {
		t.Fatalf("stderr = %q, want live confirmation error", stderr.String())
	}
}

func TestConfigApplyWaitPrintsResultSteps(t *testing.T) {
	created := protocol.Job{
		ID:          "job-apply",
		NodeID:      "node-a",
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusPending,
		PayloadJSON: signedPlanPayload(t, "plan-live", protocol.ConfigPlanModeLive),
		CreatedAt:   time.Now().UTC(),
	}
	resultJSON, err := json.Marshal(protocol.ConfigApplyResult{
		PlanID: "plan-live",
		DryRun: false,
		Steps: []protocol.ConfigApplyStep{
			{Name: "validated", Status: "completed"},
			{Name: "restarted", Status: "completed", Detail: "fake controller"},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	completed := created
	completed.Status = protocol.JobStatusCompleted
	completed.ResultJSON = string(resultJSON)
	completed.FinishedAt = time.Now().UTC()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/node-a/config-apply":
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(created); err != nil {
				t.Fatalf("encode created job: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/node-a/jobs":
			if err := json.NewEncoder(w).Encode([]protocol.Job{completed}); err != nil {
				t.Fatalf("encode jobs: %v", err)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "apply", "node-a", "--server", server.URL, "--live", "--yes", "--wait"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Plan: plan-live", "Status: completed", "Result mode: live", "Steps:", "validated", "restarted", "fake controller"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestConfigApplyJSONOutput(t *testing.T) {
	job := protocol.Job{
		ID:          "job-json",
		NodeID:      "node-a",
		Type:        protocol.JobTypeConfigApply,
		Status:      protocol.JobStatusPending,
		PayloadJSON: signedPlanPayload(t, "plan-json", protocol.ConfigPlanModeDryRun),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(jsonHandlerWithStatus(t, http.MethodPost, "/api/nodes/node-a/config-apply", http.StatusCreated, job))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "apply", "node-a", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.Job
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if got.ID != "job-json" {
		t.Fatalf("job ID = %q, want job-json", got.ID)
	}
}

func TestRestartDryRunDefaultAndOperatorToken(t *testing.T) {
	job := protocol.Job{
		ID:          "job-restart",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRestart,
		Status:      protocol.JobStatusPending,
		PayloadJSON: restartPayload(t, "hermes", "default", true),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/nodes/node-a/restart" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.RestartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Live {
			t.Fatalf("live = true, want dry-run request")
		}
		if req.RuntimeType != "hermes" || req.Profile != "default" {
			t.Fatalf("request = %#v, want hermes/default", req)
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
	code := run([]string{"restart", "node-a", "--server", server.URL, "--operator-token", "test-token", "--runtime-type", "hermes", "--profile", "default"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Job: job-restart", "Mode: dry-run", "Runtime: hermes", "Profile: default", "Status: pending"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRestartLiveRequiresYes(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"restart", "node-a", "--server", server.URL, "--live"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if called {
		t.Fatal("server was called despite missing --yes")
	}
	if !strings.Contains(stderr.String(), "--live requires --yes") {
		t.Fatalf("stderr = %q, want live confirmation error", stderr.String())
	}
}

func TestRestartWaitPrintsResultSteps(t *testing.T) {
	created := protocol.Job{
		ID:          "job-restart",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRestart,
		Status:      protocol.JobStatusPending,
		PayloadJSON: restartPayload(t, "hermes", "default", false),
		CreatedAt:   time.Now().UTC(),
	}
	resultJSON, err := json.Marshal(protocol.RestartJobResult{
		Controller:   "fake-controller",
		HealthStatus: "healthy",
		Steps: []protocol.ConfigApplyStep{
			{Name: "restarted", Status: "completed", Detail: "fake restart"},
			{Name: "health_checked", Status: "completed"},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	completed := created
	completed.Status = protocol.JobStatusCompleted
	completed.ResultJSON = string(resultJSON)
	completed.FinishedAt = time.Now().UTC()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/node-a/restart":
			var req protocol.RestartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode restart request: %v", err)
			}
			if !req.Live {
				t.Fatalf("live = false, want live restart request")
			}
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(created); err != nil {
				t.Fatalf("encode created job: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/node-a/jobs":
			if err := json.NewEncoder(w).Encode([]protocol.Job{completed}); err != nil {
				t.Fatalf("encode jobs: %v", err)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"restart", "node-a", "--server", server.URL, "--live", "--yes", "--wait"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Job: job-restart", "Mode: live", "Status: completed", "Controller: fake-controller", "Health: healthy", "Steps:", "restarted", "fake restart"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRestartJSONOutput(t *testing.T) {
	job := protocol.Job{
		ID:          "job-restart-json",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRestart,
		Status:      protocol.JobStatusPending,
		PayloadJSON: restartPayload(t, "hermes", "default", true),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(jsonHandlerWithStatus(t, http.MethodPost, "/api/nodes/node-a/restart", http.StatusCreated, job))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"restart", "node-a", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.Job
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if got.ID != "job-restart-json" {
		t.Fatalf("job ID = %q, want job-restart-json", got.ID)
	}
}

func TestRollbackDryRunDefaultAndOperatorToken(t *testing.T) {
	job := protocol.Job{
		ID:          "job-rollback",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRollback,
		Status:      protocol.JobStatusPending,
		PayloadJSON: rollbackPayload(t, "hermes", "default", "config_apply:job_apply:plan_1", true),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/nodes/node-a/rollback" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.RollbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Live {
			t.Fatalf("live = true, want dry-run request")
		}
		if req.BackupRef != "config_apply:job_apply:plan_1" || req.RuntimeType != "hermes" || req.Profile != "default" {
			t.Fatalf("request = %#v, want backup/hermes/default", req)
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
	code := run([]string{"rollback", "node-a", "--server", server.URL, "--operator-token", "test-token", "--backup-ref", "config_apply:job_apply:plan_1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Job: job-rollback", "Mode: dry-run", "Runtime: hermes", "Profile: default", "Backup: config_apply:job_apply:plan_1", "Status: pending"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRollbackRequiresBackupRef(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollback", "node-a", "--server", "http://127.0.0.1:9"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--backup-ref is required") {
		t.Fatalf("stderr = %q, want backup ref error", stderr.String())
	}
}

func TestRollbackLiveRequiresYes(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollback", "node-a", "--server", server.URL, "--backup-ref", "config_apply:job_apply:plan_1", "--live"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if called {
		t.Fatal("server was called despite missing --yes")
	}
	if !strings.Contains(stderr.String(), "--live requires --yes") {
		t.Fatalf("stderr = %q, want live confirmation error", stderr.String())
	}
}

func TestRollbackWaitPrintsResultSteps(t *testing.T) {
	created := protocol.Job{
		ID:          "job-rollback",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRollback,
		Status:      protocol.JobStatusPending,
		PayloadJSON: rollbackPayload(t, "hermes", "default", "config_apply:job_apply:plan_1", false),
		CreatedAt:   time.Now().UTC(),
	}
	resultJSON, err := json.Marshal(protocol.RollbackJobResult{
		BackupRef:    "config_apply:job_apply:plan_1",
		HealthStatus: "healthy",
		Steps: []protocol.ConfigApplyStep{
			{Name: "restored", Status: "completed", Detail: "backup restored"},
			{Name: "health_checked", Status: "completed"},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	completed := created
	completed.Status = protocol.JobStatusCompleted
	completed.ResultJSON = string(resultJSON)
	completed.FinishedAt = time.Now().UTC()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/node-a/rollback":
			var req protocol.RollbackRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode rollback request: %v", err)
			}
			if !req.Live {
				t.Fatalf("live = false, want live rollback request")
			}
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(created); err != nil {
				t.Fatalf("encode created job: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/node-a/jobs":
			if err := json.NewEncoder(w).Encode([]protocol.Job{completed}); err != nil {
				t.Fatalf("encode jobs: %v", err)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollback", "node-a", "--server", server.URL, "--backup-ref", "config_apply:job_apply:plan_1", "--live", "--yes", "--wait"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Job: job-rollback", "Mode: live", "Status: completed", "Result backup: config_apply:job_apply:plan_1", "Health: healthy", "Steps:", "restored", "backup restored"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRollbackJSONOutput(t *testing.T) {
	job := protocol.Job{
		ID:          "job-rollback-json",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRollback,
		Status:      protocol.JobStatusPending,
		PayloadJSON: rollbackPayload(t, "hermes", "default", "config_apply:job_apply:plan_1", true),
		CreatedAt:   time.Now().UTC(),
	}
	server := httptest.NewServer(jsonHandlerWithStatus(t, http.MethodPost, "/api/nodes/node-a/rollback", http.StatusCreated, job))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollback", "node-a", "--server", server.URL, "--backup-ref", "config_apply:job_apply:plan_1", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.Job
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if got.ID != "job-rollback-json" {
		t.Fatalf("job ID = %q, want job-rollback-json", got.ID)
	}
}

func TestConfigGetPrintsDesiredConfigSummary(t *testing.T) {
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Provider: "anthropic", Model: "claude-3-5-sonnet"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/config/desired" {
			t.Fatalf("path = %s, want /api/config/desired", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(desired); err != nil {
			t.Fatalf("encode desired config: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "get", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{"Global: openai / gpt-4o", "Node overrides:", "node-a: anthropic / claude-3-5-sonnet"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigGetJSONOutput(t *testing.T) {
	desired := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/config/desired", desired))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "get", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.DesiredConfig
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Global.Provider != "openai" || got.Global.Model != "gpt-4o" {
		t.Fatalf("desired = %#v, want openai/gpt-4o", got)
	}
}

func TestConfigSetUpdatesGlobalDesiredConfig(t *testing.T) {
	existing := protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "anthropic", Model: "claude-3-5-sonnet"},
		NodeOverrides: map[string]protocol.ProviderModelConfig{
			"node-a": {Provider: "local", Model: "qwen3"},
		},
	}
	var sawPut bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/api/config/desired" {
				t.Fatalf("GET path = %s, want /api/config/desired", r.URL.Path)
			}
			if err := json.NewEncoder(w).Encode(existing); err != nil {
				t.Fatalf("encode existing config: %v", err)
			}
		case http.MethodPut:
			sawPut = true
			if r.URL.Path != "/api/config/desired" {
				t.Fatalf("PUT path = %s, want /api/config/desired", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("Authorization = %q, want bearer token", got)
			}
			var req protocol.DesiredConfig
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode desired config: %v", err)
			}
			if req.Global.Provider != "openai" || req.Global.Model != "gpt-4o" {
				t.Fatalf("global = %+v, want openai/gpt-4o", req.Global)
			}
			if got := req.NodeOverrides["node-a"]; got.Provider != "local" || got.Model != "qwen3" {
				t.Fatalf("node override = %+v, want preserved local/qwen3", got)
			}
			if err := json.NewEncoder(w).Encode(req); err != nil {
				t.Fatalf("encode updated config: %v", err)
			}
		default:
			t.Fatalf("method = %s, want GET or PUT", r.Method)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"config", "set",
		"--server", server.URL,
		"--operator-token", "test-token",
		"--provider", "openai",
		"--model", "gpt-4o",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawPut {
		t.Fatal("server did not receive PUT /api/config/desired")
	}
	if got := stdout.String(); !strings.Contains(got, "Global: openai / gpt-4o") {
		t.Fatalf("stdout = %q, want updated global config", got)
	}
}

func TestNodeRemoveWithYesDeletesNode(t *testing.T) {
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/nodes/node-a" {
			t.Fatalf("path = %s, want /api/nodes/node-a", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		sawDelete = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "remove", "node-a", "--server", server.URL, "--operator-token", "test-token", "--yes"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawDelete {
		t.Fatal("server did not receive DELETE /api/nodes/node-a")
	}
	if got := stdout.String(); !strings.Contains(got, "Node node-a removed.") {
		t.Fatalf("stdout = %q, want removal message", got)
	}
}

func TestNodeRemovePromptsForConfirmation(t *testing.T) {
	oldStdin := cliStdin
	cliStdin = strings.NewReader("y\n")
	defer func() {
		cliStdin = oldStdin
	}()

	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/nodes/node-b" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawDelete = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "remove", "node-b", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawDelete {
		t.Fatal("server did not receive DELETE after confirmation")
	}
	output := stdout.String()
	for _, want := range []string{`Remove node "node-b"? [y/N]`, "Node node-b removed."} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func jsonHandler(t *testing.T, method string, path string, response any) http.Handler {
	return jsonHandlerWithStatus(t, method, path, http.StatusOK, response)
}

func jsonHandlerWithStatus(t *testing.T, method string, path string, status int, response any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			t.Fatalf("method = %s, want %s", r.Method, method)
		}
		if r.URL.Path != path {
			t.Fatalf("path = %s, want %s", r.URL.Path, path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	})
}

func signedPlanPayload(t *testing.T, planID string, mode string) string {
	t.Helper()
	payload, err := json.Marshal(protocol.SignedConfigPlan{
		Plan: protocol.ConfigPlan{
			ID:   planID,
			Mode: mode,
		},
		Signature: "test-signature",
	})
	if err != nil {
		t.Fatalf("marshal signed plan: %v", err)
	}
	return string(payload)
}

func restartPayload(t *testing.T, runtimeType string, profile string, dryRun bool) string {
	t.Helper()
	payload, err := json.Marshal(protocol.RestartJobPayload{
		RuntimeType: runtimeType,
		Profile:     profile,
		DryRun:      dryRun,
	})
	if err != nil {
		t.Fatalf("marshal restart payload: %v", err)
	}
	return string(payload)
}

func rollbackPayload(t *testing.T, runtimeType string, profile string, backupRef string, dryRun bool) string {
	t.Helper()
	payload, err := json.Marshal(protocol.RollbackJobPayload{
		RuntimeType: runtimeType,
		Profile:     profile,
		BackupRef:   backupRef,
		ConfigPath:  "/tmp/sideplane-test/config.json",
		BackupPath:  "/tmp/sideplane-test/current.backup",
		DryRun:      dryRun,
	})
	if err != nil {
		t.Fatalf("marshal rollback payload: %v", err)
	}
	return string(payload)
}
