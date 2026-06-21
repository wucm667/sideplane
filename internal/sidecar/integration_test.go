package sidecar_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/sidecar"
	"github.com/wucm667/sideplane/internal/store"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// fakeAdapter is an in-memory runtime collector that reports a single Hermes
// runtime and a config snapshot pointing at a temp config file. It backs the
// sidecar's heartbeat, deep-probe, and config-apply paths without touching a
// real machine.
type fakeAdapter struct {
	snapshot protocol.RuntimeConfigSnapshot
}

func (f fakeAdapter) CollectStatuses(context.Context) []protocol.RuntimeStatus {
	return []protocol.RuntimeStatus{{
		Name:       f.snapshot.RuntimeName,
		Type:       f.snapshot.RuntimeType,
		State:      "running",
		Provider:   f.snapshot.Provider,
		Model:      f.snapshot.Model,
		ConfigHash: f.snapshot.ConfigHash,
	}}
}

func (f fakeAdapter) CollectConfigSnapshots(context.Context) []protocol.RuntimeConfigSnapshot {
	return []protocol.RuntimeConfigSnapshot{f.snapshot}
}

// TestSidecarToServerIntegrationOverHTTP drives the real sidecar client code
// (enrollment client, heartbeat client, job poller) against a real server
// handler over httptest, backed by a temp SQLite store and fake adapters. It
// exercises the sidecar↔server HTTP contract end to end: enroll, heartbeat,
// deep probe, and a dry-run config apply, asserting server-visible state at
// each step. No real machine, live config write, or outbound network is used.
func TestSidecarToServerIntegrationOverHTTP(t *testing.T) {
	ctx := context.Background()

	st, err := store.OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	const operatorToken = "operator-secret"
	handler, err := server.NewHandlerWithConfig(server.HandlerConfig{
		Store:          st,
		Freshness:      server.DefaultFreshnessPolicy(),
		OperatorToken:  operatorToken,
		SigningKeyPair: keyPair,
	})
	if err != nil {
		t.Fatalf("build server handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := srv.Client()

	configPath := writeIntegrationHermesConfig(t)
	adapter := fakeAdapter{snapshot: protocol.RuntimeConfigSnapshot{
		RuntimeName: "Hermes Agent",
		RuntimeType: "hermes",
		ConfigPath:  configPath,
		Source:      "fixture",
		Provider:    "anthropic",
		Model:       "claude-3.7-sonnet",
		ConfigHash:  "sha256:fixture-actual",
	}}

	// Step 1: operator mints a one-time enrollment token.
	var enrollToken protocol.CreateEnrollmentTokenResponse
	operatorRequest(t, client, http.MethodPost, srv.URL+"/api/enrollment-tokens", operatorToken, protocol.CreateEnrollmentTokenRequest{}, &enrollToken)
	if enrollToken.Token == "" {
		t.Fatal("enrollment token is empty")
	}

	// Step 2: the sidecar exchanges it for node credentials via the real client.
	enrollClient, err := sidecar.NewEnrollmentClient(sidecar.EnrollmentClientConfig{
		ServerURL:      srv.URL,
		NodeID:         "node-integration",
		Hostname:       "fixture-host",
		SidecarVersion: "test",
		Token:          enrollToken.Token,
		HTTPClient:     client,
	})
	if err != nil {
		t.Fatalf("build enrollment client: %v", err)
	}
	enrolled, err := enrollClient.Enroll(ctx)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	nodeID := enrolled.NodeID
	if nodeID == "" || enrolled.NodeCredential == "" {
		t.Fatalf("enroll response missing credentials: %+v", enrolled)
	}

	// Step 3: a heartbeat makes the node visible to the server.
	heartbeatClient, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      srv.URL,
		NodeID:         nodeID,
		NodeCredential: enrolled.NodeCredential,
		Hostname:       "fixture-host",
		SidecarVersion: "test",
		HTTPClient:     client,
		Collector:      adapter,
	})
	if err != nil {
		t.Fatalf("build heartbeat client: %v", err)
	}
	heartbeatResp, err := heartbeatClient.SendHeartbeat(ctx)
	if err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	if !heartbeatResp.Accepted {
		t.Fatal("heartbeat was not accepted")
	}

	var nodes protocol.ListNodesResponse
	operatorRequest(t, client, http.MethodGet, srv.URL+"/api/nodes", operatorToken, nil, &nodes)
	if !containsNode(nodes.Nodes, nodeID) {
		t.Fatalf("node %q not visible after heartbeat: %+v", nodeID, nodes.Nodes)
	}

	// Step 4: the operator creates a deep-probe job.
	var probeJob protocol.Job
	operatorRequest(t, client, http.MethodPost, srv.URL+"/api/nodes/"+nodeID+"/jobs", operatorToken, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, &probeJob)
	if probeJob.Status != protocol.JobStatusPending {
		t.Fatalf("deep probe job status = %q, want pending", probeJob.Status)
	}

	// Step 5: the sidecar poller claims the job and submits its result.
	poller, err := sidecar.NewJobPoller(sidecar.JobPollerConfig{
		ServerURL:      srv.URL,
		NodeID:         nodeID,
		NodeCredential: enrolled.NodeCredential,
		PublicKey:      spcrypto.PublicKeyString(keyPair.PublicKey),
		Collector:      adapter,
		HTTPClient:     client,
	})
	if err != nil {
		t.Fatalf("build job poller: %v", err)
	}
	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll deep probe: %v", err)
	}

	completedProbe := findNodeJob(t, client, srv.URL, operatorToken, nodeID, probeJob.ID)
	if completedProbe.Status != protocol.JobStatusCompleted {
		t.Fatalf("deep probe job status = %q, want completed (error=%q)", completedProbe.Status, completedProbe.Error)
	}
	if !strings.Contains(completedProbe.ResultJSON, configPath) {
		t.Fatalf("deep probe result missing config path %q: %s", configPath, completedProbe.ResultJSON)
	}

	// Step 6: the operator sets a desired provider/model.
	operatorRequest(t, client, http.MethodPut, srv.URL+"/api/config/desired", operatorToken, protocol.DesiredConfig{
		Global: protocol.ProviderModelConfig{Provider: "openai", Model: "gpt-4o"},
	}, nil)

	// Step 7: the operator requests a dry-run config apply.
	dryRun := true
	var applyJob protocol.Job
	operatorRequest(t, client, http.MethodPost, srv.URL+"/api/nodes/"+nodeID+"/config-apply", operatorToken, protocol.ConfigApplyRequest{
		RuntimeType: "hermes",
		DryRun:      &dryRun,
	}, &applyJob)
	if applyJob.Type != protocol.JobTypeConfigApply || applyJob.Status != protocol.JobStatusPending {
		t.Fatalf("config apply job = %+v, want pending config_apply", applyJob)
	}

	// Step 8: the sidecar applies it in dry-run mode and reports success.
	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll config apply: %v", err)
	}

	completedApply := findNodeJob(t, client, srv.URL, operatorToken, nodeID, applyJob.ID)
	if completedApply.Status != protocol.JobStatusCompleted {
		t.Fatalf("config apply job status = %q, want completed (error=%q)", completedApply.Status, completedApply.Error)
	}
	var applyResult protocol.ConfigApplyResult
	if err := json.Unmarshal([]byte(completedApply.ResultJSON), &applyResult); err != nil {
		t.Fatalf("decode config apply result: %v", err)
	}
	if !applyResult.DryRun {
		t.Fatal("config apply result is not marked dry-run")
	}
	if !stepHasStatus(applyResult, "signature_verified", "completed") {
		t.Fatalf("dry-run apply did not verify the signed plan: %+v", applyResult.Steps)
	}
	if !stepHasStatus(applyResult, "replaced", "skipped") {
		t.Fatalf("dry-run apply replaced the live config: %+v", applyResult.Steps)
	}
}

func writeIntegrationHermesConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := []byte("model:\n  default: claude-3.7-sonnet\n  provider: anthropic\n  base_url: https://example.invalid/v1\nproviders: {}\ntoolsets:\n  shell:\n    provider: auto\n    model: ''\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write hermes config: %v", err)
	}
	return path
}

// operatorRequest performs an operator-authenticated JSON request and, when out
// is non-nil, decodes a 2xx JSON response into it.
func operatorRequest(t *testing.T, client *http.Client, method, url, token string, body, out any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		t.Fatalf("%s %s status = %d: %s", method, url, resp.StatusCode, payload)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s response: %v", method, url, err)
		}
	}
}

func findNodeJob(t *testing.T, client *http.Client, baseURL, token, nodeID, jobID string) protocol.Job {
	t.Helper()
	var jobs []protocol.Job
	operatorRequest(t, client, http.MethodGet, baseURL+"/api/nodes/"+nodeID+"/jobs", token, nil, &jobs)
	for _, job := range jobs {
		if job.ID == jobID {
			return job
		}
	}
	t.Fatalf("job %q not found for node %q", jobID, nodeID)
	return protocol.Job{}
}

func containsNode(nodes []protocol.NodeStatusWithDrift, nodeID string) bool {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

func stepHasStatus(result protocol.ConfigApplyResult, name, status string) bool {
	for _, step := range result.Steps {
		if step.Name == name && step.Status == status {
			return true
		}
	}
	return false
}
