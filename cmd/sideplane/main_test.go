package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestFleetStatusPrintsCompactTable(t *testing.T) {
	now := time.Now().UTC()
	nodes := []cliNodeStatus{
		{
			NodeStatus: protocol.NodeStatus{
				NodeID:          "node-a",
				State:           protocol.NodeStateFresh,
				Maintenance:     true,
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
		"MAINT",
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

func TestFleetStatusForwardsOperatorToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]cliNodeStatus{})
	}))
	defer server.Close()

	// Flag form.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"fleet", "status", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer dev-token" {
		t.Fatalf("Authorization = %q, want Bearer dev-token", gotAuth)
	}

	// Environment fallback.
	gotAuth = ""
	t.Setenv("SIDEPLANE_OPERATOR_TOKEN", "env-token")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"fleet", "status", "--server", server.URL}, &stdout, &stderr); code != 0 {
		t.Fatalf("run (env) returned %d, stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer env-token" {
		t.Fatalf("env Authorization = %q, want Bearer env-token", gotAuth)
	}
}

func TestFleetStatusJSONOutput(t *testing.T) {
	response := cliListNodesResponse{Nodes: []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-json",
			State:           protocol.NodeStateFresh,
			LastHeartbeatAt: time.Now().UTC(),
		},
		Drift: true,
	}}, Total: 1, Limit: 100, Offset: 0}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes", response))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"fleet", "status", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got cliListNodesResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Total != 1 || got.Limit != 100 || got.Offset != 0 || len(got.Nodes) != 1 || got.Nodes[0].NodeID != "node-json" || !got.Nodes[0].Drift {
		t.Fatalf("nodes = %#v, want node-json with drift", got)
	}
}

func TestFleetStatusSelectorQuery(t *testing.T) {
	response := cliListNodesResponse{Nodes: []cliNodeStatus{}, Total: 0, Limit: 100, Offset: 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/nodes" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("selector"); got != "role=canary,zone=lab" {
			t.Fatalf("selector query = %q, want role=canary,zone=lab", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode nodes: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"fleet", "status", "--server", server.URL, "--selector", "role=canary,zone=lab"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
}

func TestWhoamiCommandPrintsIdentityAndJSON(t *testing.T) {
	response := protocol.WhoamiResponse{Scope: protocol.OperatorTokenScopeAdmin, TokenName: "ops laptop"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/whoami" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dev-token" {
			t.Fatalf("Authorization = %q, want Bearer dev-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode whoami: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"whoami", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Scope: admin", "Token name: ops laptop"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("whoami output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"whoami", "--server", server.URL, "--operator-token", "dev-token", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.WhoamiResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode whoami JSON: %v\n%s", err, stdout.String())
	}
	if got.TokenName != "ops laptop" || got.Scope != protocol.OperatorTokenScopeAdmin {
		t.Fatalf("whoami JSON = %+v", got)
	}
}

func TestStatusCommandPrintsSummaryAndJSON(t *testing.T) {
	response := protocol.ServerStatusResponse{
		Version:       "dev",
		Commit:        "abc123",
		UptimeSeconds: 125,
		SchemaVersion: 12,
		NodeCount:     3,
		RolloutCount:  2,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/status" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dev-token" {
			t.Fatalf("Authorization = %q, want Bearer dev-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode status: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"status", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Version: dev", "Commit: abc123", "Uptime: 2m5s", "Schema version: 12", "Nodes: 3", "Rollouts: 2"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "--server", server.URL, "--operator-token", "dev-token", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.ServerStatusResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, stdout.String())
	}
	if got.NodeCount != 3 || got.RolloutCount != 2 || got.UptimeSeconds != 125 {
		t.Fatalf("status JSON = %+v", got)
	}
}

func TestNodeInspectPrintsNodeDetail(t *testing.T) {
	now := time.Now().UTC()
	nodes := []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-a",
			Hostname:        "host-a",
			State:           protocol.NodeStateFresh,
			Maintenance:     true,
			LastHeartbeatAt: now.Add(-time.Minute),
			SidecarVersion:  "dev",
			ConfigHash:      "sha256:abc",
			Labels:          map[string]string{"role": "canary", "zone": "lab"},
			Runtimes: []protocol.RuntimeStatus{{
				Name:       "hermes",
				Type:       "hermes",
				State:      "running",
				Version:    "1.2.3",
				Provider:   "openai",
				Model:      "gpt-4o",
				ConfigHash: "sha256:def",
				Health:     protocol.RuntimeHealth{State: protocol.RuntimeHealthDegraded, Reason: "service inactive"},
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
	for _, want := range []string{"Node: node-a", "Hostname: host-a", "Maintenance: yes", "Drift: yes", "Labels: role=canary,zone=lab", "hermes", "openai", "gpt-4o", "degraded: service inactive", "config path unreadable"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestNodeLabelSetsRemovesAndPrintsLabels(t *testing.T) {
	var sawGet bool
	var sawPut bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/node-a/labels" {
			t.Fatalf("path = %s, want /api/nodes/node-a/labels", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dev-token" {
			t.Fatalf("Authorization = %q, want Bearer dev-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			sawGet = true
			if err := json.NewEncoder(w).Encode(protocol.NodeLabelsResponse{
				NodeID: "node-a",
				Labels: map[string]string{"role": "old", "zone": "lab"},
			}); err != nil {
				t.Fatalf("encode labels: %v", err)
			}
		case http.MethodPut:
			sawPut = true
			var req protocol.NodeLabelsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode labels request: %v", err)
			}
			if req.Labels["role"] != "canary" || req.Labels["env"] != "dev" {
				t.Fatalf("labels request = %#v, want role canary and env dev", req.Labels)
			}
			if _, ok := req.Labels["zone"]; ok {
				t.Fatalf("labels request kept removed zone: %#v", req.Labels)
			}
			if err := json.NewEncoder(w).Encode(protocol.NodeLabelsResponse{
				NodeID: "node-a",
				Labels: req.Labels,
			}); err != nil {
				t.Fatalf("encode labels response: %v", err)
			}
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "label", "node-a", "role=canary", "env=dev", "--remove", "zone", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawGet || !sawPut {
		t.Fatalf("sawGet=%t sawPut=%t, want both", sawGet, sawPut)
	}
	output := stdout.String()
	for _, want := range []string{"Node: node-a", "env=dev", "role=canary"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestNodeMaintenanceSetsMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/nodes/node-a/maintenance" {
			t.Fatalf("path = %s, want /api/nodes/node-a/maintenance", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dev-token" {
			t.Fatalf("Authorization = %q, want Bearer dev-token", got)
		}
		var req protocol.NodeMaintenanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode maintenance request: %v", err)
		}
		if !req.Maintenance {
			t.Fatalf("maintenance request = false, want true")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(protocol.NodeMaintenanceResponse{
			NodeID:      "node-a",
			Maintenance: true,
		}); err != nil {
			t.Fatalf("encode maintenance response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"node", "maintenance", "node-a", "--on", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Node: node-a", "Maintenance: yes"} {
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

func TestNodeInspectReadsPaginatedNodeListResponse(t *testing.T) {
	response := cliListNodesResponse{Nodes: []cliNodeStatus{{
		NodeStatus: protocol.NodeStatus{
			NodeID:          "node-a",
			State:           protocol.NodeStateFresh,
			LastHeartbeatAt: time.Now().UTC(),
		},
		Drift: true,
	}}, Total: 1, Limit: 100, Offset: 0}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/nodes", response))
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
	if got.NodeID != "node-a" || !got.Drift {
		t.Fatalf("node = %#v, want drifted node-a", got)
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
		"whoami",
		"status",
		"probe <nodeId>",
		"restart <nodeId>",
		"rollback <nodeId>",
		"backups list <id>",
		"rollout create",
		"rollout list",
		"rollout status <id>",
		"jobs list <nodeId>",
		"audit list",
		"token create",
		"token list",
		"token revoke <id>",
		"config apply <id>",
		"config preview <id>",
		"config get",
		"config set",
		"config history",
		"config revert <id>",
		"node inspect <id>",
		"node label <id>",
		"node maintenance <id>",
		"node remove <id>",
		"enrollment create",
		"config-file path",
		"completion <shell>",
		"version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help missing %q:\n%s", want, output)
		}
	}
}

func TestPerCommandHelpPrintsFlags(t *testing.T) {
	tests := []struct {
		args       []string
		wantServer bool
	}{
		{args: []string{"config", "apply", "--help"}, wantServer: true},
		{args: []string{"config", "history", "--help"}, wantServer: true},
		{args: []string{"config", "revert", "--help"}, wantServer: true},
		{args: []string{"restart", "--help"}, wantServer: true},
		{args: []string{"rollback", "--help"}, wantServer: true},
		{args: []string{"backups", "list", "--help"}, wantServer: true},
		{args: []string{"rollout", "create", "--help"}, wantServer: true},
		{args: []string{"rollout", "list", "--help"}, wantServer: true},
		{args: []string{"rollout", "status", "--help"}, wantServer: true},
		{args: []string{"rollout", "pause", "--help"}, wantServer: true},
		{args: []string{"whoami", "--help"}, wantServer: true},
		{args: []string{"status", "--help"}, wantServer: true},
		{args: []string{"jobs", "list", "--help"}, wantServer: true},
		{args: []string{"token", "create", "--help"}, wantServer: true},
		{args: []string{"token", "list", "--help"}, wantServer: true},
		{args: []string{"token", "revoke", "--help"}, wantServer: true},
		{args: []string{"node", "label", "--help"}, wantServer: true},
		{args: []string{"enrollment", "create", "--help"}, wantServer: true},
		{args: []string{"config-file", "path", "--help"}},
		{args: []string{"completion", "--help"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}
			output := stdout.String()
			if !strings.Contains(output, "usage: sideplane") {
				t.Fatalf("help output missing usage:\n%s", output)
			}
			if tt.wantServer && !strings.Contains(output, "--server") {
				t.Fatalf("help output missing server flag:\n%s", output)
			}
		})
	}
}

func TestCompletionScriptsContainTopLevelCommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"completion", shell}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}
			output := stdout.String()
			for _, want := range []string{
				"fleet",
				"whoami",
				"status",
				"probe",
				"restart",
				"rollback",
				"backups",
				"rollout",
				"jobs",
				"audit",
				"token",
				"config",
				"node",
				"enrollment",
				"config-file",
				"completion",
				"version",
			} {
				if !strings.Contains(output, want) {
					t.Fatalf("%s completion missing %q:\n%s", shell, want, output)
				}
			}
			if shell == "bash" && !strings.Contains(output, "complete -F _sideplane_completion sideplane") {
				t.Fatalf("bash completion missing registration:\n%s", output)
			}
			if shell == "zsh" && !strings.Contains(output, "compdef _sideplane sideplane") {
				t.Fatalf("zsh completion missing registration:\n%s", output)
			}
		})
	}
}

func TestCLIConfigFilePathUsesOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv(cliConfigEnv, configPath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config-file", "path"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != configPath {
		t.Fatalf("config path = %q, want %q", strings.TrimSpace(stdout.String()), configPath)
	}
}

func TestCLIConfigFilePrecedence(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
server: http://config-server
operatorToken: config-token
runtimeType: openclaw
profile: lab
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(cliConfigEnv, configPath)
	t.Setenv(serverURLEnv, "")
	t.Setenv(auth.OperatorTokenEnv, "")
	t.Setenv(runtimeTypeEnv, "")
	t.Setenv(profileEnv, "")

	cfg := loadCLIConfig()
	if got := serverURLValueWithConfig("", cfg); got != "http://config-server" {
		t.Fatalf("server from config = %q, want config server", got)
	}
	if got := operatorTokenValueWithConfig("", cfg); got != "config-token" {
		t.Fatalf("operator token from config = %q, want config-token", got)
	}
	if got := runtimeTypeValueWithConfig("", cfg); got != "openclaw" {
		t.Fatalf("runtime type from config = %q, want openclaw", got)
	}
	if got := profileValueWithConfig("", cfg); got != "lab" {
		t.Fatalf("profile from config = %q, want lab", got)
	}

	t.Setenv(serverURLEnv, "http://env-server")
	t.Setenv(auth.OperatorTokenEnv, "env-token")
	t.Setenv(runtimeTypeEnv, "hermes")
	t.Setenv(profileEnv, "env-profile")
	if got := serverURLValueWithConfig("", cfg); got != "http://env-server" {
		t.Fatalf("server from env = %q, want env server", got)
	}
	if got := operatorTokenValueWithConfig("", cfg); got != "env-token" {
		t.Fatalf("operator token from env = %q, want env-token", got)
	}
	if got := runtimeTypeValueWithConfig("", cfg); got != "hermes" {
		t.Fatalf("runtime type from env = %q, want hermes", got)
	}
	if got := profileValueWithConfig("", cfg); got != "env-profile" {
		t.Fatalf("profile from env = %q, want env-profile", got)
	}

	if got := serverURLValueWithConfig("http://flag-server", cfg); got != "http://flag-server" {
		t.Fatalf("server from flag = %q, want flag server", got)
	}
	if got := operatorTokenValueWithConfig("flag-token", cfg); got != "flag-token" {
		t.Fatalf("operator token from flag = %q, want flag-token", got)
	}
	if got := runtimeTypeValueWithConfig("flag-runtime", cfg); got != "flag-runtime" {
		t.Fatalf("runtime type from flag = %q, want flag runtime", got)
	}
	if got := profileValueWithConfig("flag-profile", cfg); got != "flag-profile" {
		t.Fatalf("profile from flag = %q, want flag profile", got)
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

func TestRolloutCreateDryRunDefault(t *testing.T) {
	created := testRollout("rollout-1", protocol.RolloutStatePending)
	var sawCreate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rollouts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.CreateRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rollout create: %v", err)
		}
		if req.Spec.Live {
			t.Fatalf("live = true, want dry-run default")
		}
		if req.Spec.AllowOverlap {
			t.Fatalf("allowOverlap = true, want false by default")
		}
		if len(req.Spec.NodeIDs) != 1 || req.Spec.NodeIDs[0] != "node-a" {
			t.Fatalf("node IDs = %#v, want node-a", req.Spec.NodeIDs)
		}
		if req.Spec.Target.Provider != "openai" || req.Spec.Target.Model != "gpt-4o" {
			t.Fatalf("target = %+v, want openai/gpt-4o", req.Spec.Target)
		}
		if req.Spec.RuntimeType != "hermes" || req.Spec.Profile != "default" || req.Spec.BatchSize != 1 {
			t.Fatalf("spec runtime/profile/batch = %+v, want hermes/default/1", req.Spec)
		}
		sawCreate = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(protocol.CreateRolloutResponse{Rollout: created}); err != nil {
			t.Fatalf("encode rollout response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"rollout", "create",
		"--server", server.URL,
		"--operator-token", "test-token",
		"--node", "node-a",
		"--provider", "openai",
		"--model", "gpt-4o",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawCreate {
		t.Fatal("server did not receive rollout create")
	}
	output := stdout.String()
	for _, want := range []string{"Rollout: rollout-1", "State: pending", "Mode: dry-run", "Target: openai / gpt-4o"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRolloutCreateLiveRequiresYes(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"rollout", "create",
		"--node", "node-a",
		"--provider", "openai",
		"--model", "gpt-4o",
		"--live",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run returned 0, want failure")
	}
	if !strings.Contains(stderr.String(), "rollout create: --live requires --yes") {
		t.Fatalf("stderr = %q, want live confirmation error", stderr.String())
	}
}

func TestRolloutCreateRejectsInvalidStartAt(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"rollout", "create",
		"--node", "node-a",
		"--provider", "openai",
		"--model", "gpt-4o",
		"--start-at", "tomorrow",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run returned 0, want failure")
	}
	if !strings.Contains(stderr.String(), "--start-at must be an RFC3339 timestamp") {
		t.Fatalf("stderr = %q, want start-at validation error", stderr.String())
	}
}

func TestRolloutCreateJSONUsesSelectorAndLiveOptions(t *testing.T) {
	created := testRollout("rollout-json", protocol.RolloutStateRunning)
	startAt := time.Date(2026, 6, 20, 14, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rollouts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req protocol.CreateRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rollout create: %v", err)
		}
		if req.Spec.Selector["role"] != "canary" || req.Spec.Selector["zone"] != "lab" {
			t.Fatalf("selector = %#v, want role/zone selector", req.Spec.Selector)
		}
		if len(req.Spec.NodeIDs) != 0 {
			t.Fatalf("node IDs = %#v, want selector-only request", req.Spec.NodeIDs)
		}
		if !req.Spec.Live || req.Spec.RuntimeType != "openclaw" || req.Spec.Profile != "worker" {
			t.Fatalf("spec = %+v, want live openclaw/worker", req.Spec)
		}
		if !req.Spec.AllowOverlap {
			t.Fatalf("allowOverlap = false, want true")
		}
		if req.Spec.BatchSize != 2 || req.Spec.HealthTimeout != 30*time.Second {
			t.Fatalf("batch/timeout = %d/%s, want 2/30s", req.Spec.BatchSize, req.Spec.HealthTimeout)
		}
		if !req.Spec.StartAt.Equal(startAt) {
			t.Fatalf("startAt = %s, want %s", req.Spec.StartAt, startAt)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(protocol.CreateRolloutResponse{Rollout: created}); err != nil {
			t.Fatalf("encode rollout response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"rollout", "create",
		"--server", server.URL,
		"--selector", "role=canary,zone=lab",
		"--provider", "anthropic",
		"--model", "claude-3-7-sonnet",
		"--runtime-type", "openclaw",
		"--profile", "worker",
		"--batch-size", "2",
		"--health-timeout", "30s",
		"--start-at", startAt.Format(time.RFC3339),
		"--live",
		"--yes",
		"--allow-overlap",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.CreateRolloutResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Rollout.ID != "rollout-json" {
		t.Fatalf("rollout ID = %q, want rollout-json", got.Rollout.ID)
	}
}

func TestRolloutCreatePrintsConflictMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rollouts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		if err := json.NewEncoder(w).Encode(protocol.APIError{
			Code:    "conflict",
			Message: "rollout overlaps active target(s): node-a in rollout rollout-active (running); set allowOverlap=true to override",
		}); err != nil {
			t.Fatalf("encode conflict response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"rollout", "create",
		"--server", server.URL,
		"--node", "node-a",
		"--provider", "openai",
		"--model", "gpt-4o",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run returned 0, want conflict failure")
	}
	for _, want := range []string{"server returned status 409", "node-a", "rollout-active", "allowOverlap=true"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRolloutListTableAndJSON(t *testing.T) {
	startAt := time.Date(2026, 6, 20, 14, 30, 0, 0, time.UTC)
	scheduled := testRollout("rollout-b", protocol.RolloutStateScheduled)
	scheduled.Spec.StartAt = startAt
	resp := protocol.ListRolloutsResponse{
		Rollouts: []protocol.Rollout{
			testRollout("rollout-a", protocol.RolloutStateRunning),
			scheduled,
		},
		Total: 2,
		Limit: 50,
	}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/rollouts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode rollouts: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollout", "list", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("table run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"ROLLOUT", "START", "rollout-a", "running", "rollout-b", "scheduled", startAt.Format(time.RFC3339), "dry-run", "openai / gpt-4o"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"rollout", "list", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.ListRolloutsResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Total != 2 || requestCount.Load() != 2 {
		t.Fatalf("json rollouts total=%d requestCount=%d, want total 2 and two requests", got.Total, requestCount.Load())
	}
}

func TestRolloutStatusTableJSONAndWatch(t *testing.T) {
	running := testRollout("rollout-watch", protocol.RolloutStateRunning)
	startAt := time.Date(2026, 6, 20, 14, 30, 0, 0, time.UTC)
	running.Spec.StartAt = startAt
	completed := testRollout("rollout-watch", protocol.RolloutStateCompleted)
	completed.Batches[0].State = protocol.RolloutBatchStateCompleted
	completed.Batches[0].Nodes["node-a"] = protocol.RolloutNodeProgress{NodeID: "node-a", JobID: "job-a", State: protocol.RolloutNodeStateSucceeded}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/rollouts/rollout-watch" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		count := requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			if err := json.NewEncoder(w).Encode(protocol.GetRolloutResponse{Rollout: running}); err != nil {
				t.Fatalf("encode running rollout: %v", err)
			}
			return
		}
		if err := json.NewEncoder(w).Encode(protocol.GetRolloutResponse{Rollout: completed}); err != nil {
			t.Fatalf("encode completed rollout: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollout", "status", "rollout-watch", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Rollout: rollout-watch", "Start at: " + startAt.Format(time.RFC3339), "Batches:", "Nodes:", "node-a", "job-a"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"rollout", "status", "rollout-watch", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json status returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.GetRolloutResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Rollout.State != protocol.RolloutStateCompleted {
		t.Fatalf("json rollout state = %q, want completed", got.Rollout.State)
	}

	stdout.Reset()
	stderr.Reset()
	requestCount.Store(0)
	code = run([]string{"rollout", "status", "rollout-watch", "--server", server.URL, "--watch"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("watch status returned %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "State: completed") || requestCount.Load() < 2 {
		t.Fatalf("watch output/count = %q/%d, want completed after polling", stdout.String(), requestCount.Load())
	}
}

func TestRolloutActions(t *testing.T) {
	actions := []protocol.RolloutAction{protocol.RolloutActionPause, protocol.RolloutActionResume, protocol.RolloutActionAbort}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			var sawAction bool
			updated := testRollout("rollout-action", protocol.RolloutStatePaused)
			if action == protocol.RolloutActionAbort {
				updated.State = protocol.RolloutStateAborted
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/rollouts/rollout-action/actions" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Fatalf("Authorization = %q, want bearer token", got)
				}
				var req protocol.RolloutActionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode action request: %v", err)
				}
				if req.Action != action {
					t.Fatalf("action = %q, want %q", req.Action, action)
				}
				sawAction = true
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(protocol.RolloutActionResponse{Rollout: updated}); err != nil {
					t.Fatalf("encode action response: %v", err)
				}
			}))
			defer server.Close()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"rollout", string(action), "rollout-action", "--server", server.URL, "--operator-token", "test-token"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}
			if !sawAction {
				t.Fatal("server did not receive rollout action")
			}
			if !strings.Contains(stdout.String(), "Rollout: rollout-action") {
				t.Fatalf("stdout = %q, want rollout summary", stdout.String())
			}
		})
	}
}

func TestRolloutActionJSONOutput(t *testing.T) {
	updated := testRollout("rollout-json-action", protocol.RolloutStatePaused)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rollouts/rollout-json-action/actions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req protocol.RolloutActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode action request: %v", err)
		}
		if req.Action != protocol.RolloutActionPause {
			t.Fatalf("action = %q, want pause", req.Action)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(protocol.RolloutActionResponse{Rollout: updated}); err != nil {
			t.Fatalf("encode action response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollout", "pause", "rollout-json-action", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.RolloutActionResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got.Rollout.ID != "rollout-json-action" {
		t.Fatalf("rollout ID = %q, want rollout-json-action", got.Rollout.ID)
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
		{name: "backups node", args: []string{"backups", "list"}, want: "usage: sideplane backups list"},
		{name: "rollout status id", args: []string{"rollout", "status"}, want: "usage: sideplane rollout status"},
		{name: "rollout action id", args: []string{"rollout", "pause"}, want: "usage: sideplane rollout pause"},
		{name: "jobs node", args: []string{"jobs", "list"}, want: "usage: sideplane jobs list"},
		{name: "config apply node", args: []string{"config", "apply"}, want: "usage: sideplane config apply"},
		{name: "config preview node", args: []string{"config", "preview"}, want: "usage: sideplane config preview"},
		{name: "node inspect id", args: []string{"node", "inspect"}, want: "usage: sideplane node inspect"},
		{name: "node label id", args: []string{"node", "label"}, want: "usage: sideplane node label"},
		{name: "node maintenance id", args: []string{"node", "maintenance"}, want: "usage: sideplane node maintenance"},
		{name: "node remove id", args: []string{"node", "remove"}, want: "usage: sideplane node remove"},
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

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"version", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json run returned %d, stderr=%q", code, stderr.String())
	}
	var got struct {
		Binary  string `json:"binary"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON version: %v", err)
	}
	if got.Binary != "sideplane" || got.Version != "dev" {
		t.Fatalf("json version = %+v, want sideplane/dev", got)
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

func TestTokenCreatePrintsPlaintextOnce(t *testing.T) {
	createdAt := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	resp := protocol.CreateOperatorTokenResponse{
		OperatorToken: protocol.OperatorToken{ID: "optok_123", Name: "ops laptop", CreatedAt: createdAt},
		Token:         "plain-token-value",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/operator-tokens" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer bootstrap-token" {
			t.Fatalf("Authorization = %q, want bootstrap token", got)
		}
		var req protocol.CreateOperatorTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Name != "ops laptop" {
			t.Fatalf("request name = %q, want ops laptop", req.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"token", "create", "--name", "ops laptop", "--server", server.URL, "--operator-token", "bootstrap-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"operator token: plain-token-value", "id: optok_123", "name: ops laptop", "shown once: yes"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestTokenListPrintsMetadataOnly(t *testing.T) {
	createdAt := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	lastUsedAt := createdAt.Add(time.Minute)
	resp := protocol.ListOperatorTokensResponse{Tokens: []protocol.OperatorToken{{
		ID:         "optok_123",
		Name:       "ops laptop",
		CreatedAt:  createdAt,
		LastUsedAt: &lastUsedAt,
	}}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/operator-tokens", resp))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"token", "list", "--server", server.URL, "--operator-token", "bootstrap-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"ID", "NAME", "CREATED", "LAST USED", "REVOKED", "optok_123", "ops laptop"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "plain-token-value") {
		t.Fatalf("token list output leaked plaintext token")
	}
}

func TestTokenRevokeDeletesByID(t *testing.T) {
	var sawDelete bool
	resp := protocol.RevokeOperatorTokenResponse{OperatorToken: protocol.OperatorToken{ID: "optok_123", Name: "ops laptop", CreatedAt: time.Now().UTC()}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/operator-tokens/optok_123" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer bootstrap-token" {
			t.Fatalf("Authorization = %q, want bootstrap token", got)
		}
		sawDelete = true
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"token", "revoke", "optok_123", "--server", server.URL, "--operator-token", "bootstrap-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawDelete {
		t.Fatal("server did not receive DELETE")
	}
	if !strings.Contains(stdout.String(), "Operator token optok_123 revoked.") {
		t.Fatalf("stdout = %q, want revoke confirmation", stdout.String())
	}
}

func TestWebhookCreatePostsKindAndPrintsSummary(t *testing.T) {
	created := protocol.AlertWebhook{
		ID:        "whk_slack",
		Kind:      protocol.AlertWebhookKindSlack,
		URL:       "https://hooks.example.com/slack",
		Events:    []protocol.AlertEventType{protocol.AlertEventNodeOffline},
		CreatedAt: time.Now().UTC(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/webhooks" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req protocol.CreateAlertWebhookRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Kind != protocol.AlertWebhookKindSlack {
			t.Fatalf("kind = %q, want slack", req.Kind)
		}
		if req.Sign {
			t.Fatalf("sign = true, want unsigned slack request")
		}
		if len(req.Events) != 1 || req.Events[0] != protocol.AlertEventNodeOffline {
			t.Fatalf("events = %+v, want node.offline", req.Events)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(protocol.CreateAlertWebhookResponse{Webhook: created}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"webhook", "create",
		"--server", server.URL,
		"--url", "https://hooks.example.com/slack",
		"--event", "node.offline",
		"--kind", "slack",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"webhook: whk_slack", "kind: slack", "url: https://hooks.example.com/slack", "events: node.offline"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestWebhookCreateRejectsSignedSlack(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"webhook", "create",
		"--url", "https://hooks.example.com/slack",
		"--event", "node.offline",
		"--kind", "slack",
		"--sign",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run returned 0, want signed slack validation failure")
	}
	if !strings.Contains(stderr.String(), "--sign is only supported for --kind generic") {
		t.Fatalf("stderr = %q, want signed slack validation", stderr.String())
	}
}

func TestWebhookListShowsKind(t *testing.T) {
	resp := protocol.ListAlertWebhooksResponse{Webhooks: []protocol.AlertWebhook{
		{
			ID:        "whk_generic",
			Kind:      protocol.AlertWebhookKindGeneric,
			URL:       "https://hooks.example.com/generic",
			Events:    []protocol.AlertEventType{protocol.AlertEventRolloutPaused},
			HasSecret: true,
			CreatedAt: time.Now().UTC(),
		},
		{
			ID:        "whk_slack",
			Kind:      protocol.AlertWebhookKindSlack,
			URL:       "https://hooks.example.com/slack",
			Events:    []protocol.AlertEventType{protocol.AlertEventNodeOffline},
			CreatedAt: time.Now().UTC(),
		},
	}}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/webhooks", resp))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"webhook", "list", "--server", server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"ID", "KIND", "whk_generic", "generic", "whk_slack", "slack"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
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

func TestRollbackWithoutBackupRefUsesLatestBackup(t *testing.T) {
	job := protocol.Job{
		ID:          "job-rollback-latest",
		NodeID:      "node-a",
		Type:        protocol.JobTypeRollback,
		Status:      protocol.JobStatusPending,
		PayloadJSON: rollbackPayload(t, "hermes", "default", "config_apply:job_latest:plan_latest", true),
		CreatedAt:   time.Now().UTC(),
	}
	var sawBackups bool
	var sawRollback bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/node-a/backups":
			sawBackups = true
			if got := r.URL.Query().Get("limit"); got != "1" {
				t.Fatalf("backup limit = %q, want 1", got)
			}
			if err := json.NewEncoder(w).Encode(protocol.ListRollbackBackupsResponse{
				Backups: []protocol.RollbackBackupInventoryItem{{
					Ref:         "config_apply:job_latest:plan_latest",
					SourceJobID: "job_latest",
					RuntimeType: "hermes",
					Profile:     "default",
					CreatedAt:   time.Now().UTC(),
				}},
				Total: 1,
				Limit: 1,
			}); err != nil {
				t.Fatalf("encode backups: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/node-a/rollback":
			sawRollback = true
			var req protocol.RollbackRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode rollback request: %v", err)
			}
			if req.BackupRef != "config_apply:job_latest:plan_latest" {
				t.Fatalf("backupRef = %q, want latest backup", req.BackupRef)
			}
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(job); err != nil {
				t.Fatalf("encode job: %v", err)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"rollback", "node-a", "--server", server.URL, "--operator-token", "test-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawBackups || !sawRollback {
		t.Fatalf("sawBackups=%t sawRollback=%t, want both", sawBackups, sawRollback)
	}
	if !strings.Contains(stdout.String(), "Backup: config_apply:job_latest:plan_latest") {
		t.Fatalf("stdout = %q, want latest backup summary", stdout.String())
	}
}

func TestBackupsListPrintsTableAndJSON(t *testing.T) {
	response := protocol.ListRollbackBackupsResponse{
		Backups: []protocol.RollbackBackupInventoryItem{{
			Ref:         "config_apply:job_apply:plan_1",
			SourceJobID: "job_apply",
			RuntimeType: "hermes",
			Profile:     "default",
			ConfigHash:  "sha256:before",
			CreatedAt:   time.Now().UTC(),
		}},
		Total: 1,
		Limit: 50,
	}
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodGet || r.URL.Path != "/api/nodes/node-a/backups" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode backups: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"backups", "list", "node-a", "--server", server.URL, "--operator-token", "test-token", "--limit", "50"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"REF", "RUNTIME", "config_apply:job_apply:plan_1", "hermes", "sha256:before", "job_apply"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"backups", "list", "node-a", "--server", server.URL, "--operator-token", "test-token", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.ListRollbackBackupsResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(got.Backups) != 1 || got.Backups[0].Ref != response.Backups[0].Ref || requestCount != 2 {
		t.Fatalf("json backups = %#v requestCount=%d, want one backup and two requests", got, requestCount)
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

func TestConfigHistoryPrintsTableAndUsesPagination(t *testing.T) {
	updatedAt := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	resp := protocol.ListDesiredConfigHistoryResponse{
		History: []protocol.DesiredConfigHistoryEntry{{
			ID:          "deshist_123",
			Config:      protocol.DesiredConfig{Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"}},
			DesiredHash: "sha256:abc",
			UpdatedAt:   updatedAt,
			Actor:       "operator",
		}},
		Total:  1,
		Limit:  25,
		Offset: 5,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/config/desired/history" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("offset") != "5" {
			t.Fatalf("query = %s, want limit 25 offset 5", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "history", "--server", server.URL, "--operator-token", "test-token", "--limit", "25", "--offset", "5"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"ID", "UPDATED", "ACTOR", "HASH", "GLOBAL", "deshist_123", "sha256:abc", "openai / gpt-4o"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigHistoryJSONOutput(t *testing.T) {
	resp := protocol.ListDesiredConfigHistoryResponse{
		History: []protocol.DesiredConfigHistoryEntry{{ID: "deshist_json"}},
		Total:   1,
		Limit:   50,
	}
	server := httptest.NewServer(jsonHandler(t, http.MethodGet, "/api/config/desired/history", resp))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "history", "--server", server.URL, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var got protocol.ListDesiredConfigHistoryResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(got.History) != 1 || got.History[0].ID != "deshist_json" {
		t.Fatalf("history = %+v, want deshist_json", got.History)
	}
}

func TestConfigRevertRequiresYesAndPostsHistoryID(t *testing.T) {
	var sawPost bool
	resp := protocol.RevertDesiredConfigResponse{
		Desired: protocol.DesiredConfig{Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"}},
		History: protocol.DesiredConfigHistoryEntry{
			ID:     "deshist_new",
			Config: protocol.DesiredConfig{Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"}},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/config/desired/revert" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var req protocol.RevertDesiredConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.HistoryID != "deshist_old" {
			t.Fatalf("historyId = %q, want deshist_old", req.HistoryID)
		}
		sawPost = true
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "revert", "deshist_old", "--server", server.URL, "--operator-token", "test-token"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run without --yes returned %d, want 1", code)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"config", "revert", "deshist_old", "--server", server.URL, "--operator-token", "test-token", "--yes"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !sawPost {
		t.Fatal("server did not receive revert POST")
	}
	if !strings.Contains(stdout.String(), "Reverted desired config to history deshist_old.") || !strings.Contains(stdout.String(), "Global: openai / gpt-4o") {
		t.Fatalf("stdout = %q, want revert summary", stdout.String())
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

func testRollout(id string, state protocol.RolloutState) protocol.Rollout {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	return protocol.Rollout{
		ID: id,
		Spec: protocol.RolloutSpec{
			NodeIDs:       []string{"node-a"},
			RuntimeType:   "hermes",
			Profile:       "default",
			Target:        protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
			BatchSize:     1,
			HealthTimeout: 5 * time.Minute,
		},
		State: state,
		Batches: []protocol.RolloutBatch{{
			Index:   0,
			NodeIDs: []string{"node-a"},
			State:   protocol.RolloutBatchStateRunning,
			Nodes: map[string]protocol.RolloutNodeProgress{
				"node-a": {NodeID: "node-a", JobID: "job-a", State: protocol.RolloutNodeStateDispatched},
			},
		}},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
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

func TestRunAuditExportWritesFileWithFilters(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/audit/export" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer dev-token" {
			t.Fatalf("missing operator auth header")
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"id\":\"audit_1\",\"action\":\"job.create\"}\n"))
	}))
	defer server.Close()

	outPath := filepath.Join(t.TempDir(), "audit.ndjson")
	var stdout, stderr bytes.Buffer
	code := run([]string{"audit", "export", "--format", "ndjson", "--out", outPath, "--node-id", "node-a", "--action", "job.create", "--server", server.URL, "--operator-token", "dev-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(gotQuery, "format=ndjson") || !strings.Contains(gotQuery, "nodeId=node-a") || !strings.Contains(gotQuery, "action=job.create") {
		t.Fatalf("query = %q, want format/nodeId/action filters", gotQuery)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	if !strings.Contains(string(data), "audit_1") {
		t.Fatalf("export file = %q, want audit row", string(data))
	}
}

func TestRunAuditExportRejectsInvalidFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"audit", "export", "--format", "xml"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--format must be ndjson or csv") {
		t.Fatalf("stderr = %q, want format error", stderr.String())
	}
}
