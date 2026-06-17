package sidecar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestJobPollerAcceptsServerURLWithoutScheme(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sidecar/jobs/next" {
			t.Fatalf("path = %q, want jobs next endpoint", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()

	serverURL := strings.TrimPrefix(api.URL, "http://")
	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      serverURL,
		NodeID:         "node-no-scheme",
		NodeCredential: "credential",
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(context.Background()); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}
}

func TestRunJobPollerPollsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-immediate")
	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-immediate", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-immediate",
		NodeCredential: credential,
		Collector:      fakeRuntimeCollector{},
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- RunJobPoller(ctx, poller, time.Hour)
	}()

	deadline := time.After(time.Second)
	for {
		got, err := nodeStore.GetJob(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if got.Status == protocol.JobStatusCompleted {
			cancel()
			<-done
			return
		}

		select {
		case <-deadline:
			t.Fatalf("job status = %q, want completed before first interval", got.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestJobPollerCompletesDeepProbe(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-deep-probe")

	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-deep-probe", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-deep-probe",
		NodeCredential: credential,
		Collector: fakeRuntimeCollector{runtimes: []protocol.RuntimeStatus{
			{Name: "default", Type: "hermes", State: "running", Provider: "openai", Model: "gpt-5"},
		}},
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got, err := nodeStore.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != protocol.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed; error=%q", got.Status, got.Error)
	}

	var result struct {
		Runtimes        []protocol.RuntimeStatus         `json:"runtimes"`
		ConfigSnapshots []protocol.RuntimeConfigSnapshot `json:"configSnapshots"`
	}
	if err := json.Unmarshal([]byte(got.ResultJSON), &result); err != nil {
		t.Fatalf("unmarshal result JSON: %v", err)
	}
	if len(result.Runtimes) != 1 || result.Runtimes[0].Type != "hermes" {
		t.Fatalf("runtimes = %#v, want hermes runtime", result.Runtimes)
	}
	if len(result.ConfigSnapshots) != 0 {
		t.Fatalf("config snapshots = %#v, want none", result.ConfigSnapshots)
	}
}

func TestJobPollerDeepProbeEmptyCollectionsAsArrays(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-empty-probe")

	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-empty-probe", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-empty-probe",
		NodeCredential: credential,
		Collector:      fakeRuntimeCollector{},
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got, err := nodeStore.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != protocol.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed; error=%q", got.Status, got.Error)
	}

	var result struct {
		Runtimes        json.RawMessage `json:"runtimes"`
		ConfigSnapshots json.RawMessage `json:"configSnapshots"`
	}
	if err := json.Unmarshal([]byte(got.ResultJSON), &result); err != nil {
		t.Fatalf("unmarshal result JSON: %v", err)
	}
	if string(result.Runtimes) != "[]" {
		t.Fatalf("runtimes JSON = %s, want [] in %s", result.Runtimes, got.ResultJSON)
	}
	if string(result.ConfigSnapshots) != "[]" {
		t.Fatalf("configSnapshots JSON = %s, want [] in %s", result.ConfigSnapshots, got.ResultJSON)
	}
}

func TestJobPollerDeepProbeIncludesConfigSnapshots(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-config-probe")

	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-config-probe", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-config-probe",
		NodeCredential: credential,
		Collector: fakeRuntimeCollector{
			runtimes: []protocol.RuntimeStatus{
				{Name: "default", Type: "hermes", State: "present"},
			},
			configSnapshots: []protocol.RuntimeConfigSnapshot{
				{
					RuntimeName: "default",
					RuntimeType: "hermes",
					Source:      "adapter",
					Provider:    "openai",
					Model:       "gpt-5",
					ConfigHash:  "sha256:config",
				},
			},
		},
		HTTPClient: api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got, err := nodeStore.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != protocol.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed; error=%q", got.Status, got.Error)
	}

	var result protocol.DeepProbeResult
	if err := json.Unmarshal([]byte(got.ResultJSON), &result); err != nil {
		t.Fatalf("unmarshal result JSON: %v", err)
	}
	if len(result.ConfigSnapshots) != 1 {
		t.Fatalf("len(configSnapshots) = %d, want 1", len(result.ConfigSnapshots))
	}
	if result.ConfigSnapshots[0].Provider != "openai" || result.ConfigSnapshots[0].Model != "gpt-5" {
		t.Fatalf("config snapshot = %#v, want provider/model", result.ConfigSnapshots[0])
	}
}

func TestJobPollerFailsUnknownJobType(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-unknown-job")

	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobType("unknown")}, "node-unknown-job", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-unknown-job",
		NodeCredential: credential,
		Collector:      fakeRuntimeCollector{},
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got, err := nodeStore.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", got.Status)
	}
	if got.Error == "" {
		t.Fatalf("job error is empty")
	}
}

func TestJobPollerUsesBearerCredential(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-auth")
	_, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-auth", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-auth",
		NodeCredential: "wrong-credential",
		Collector:      fakeRuntimeCollector{},
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err == nil {
		t.Fatalf("poll and execute error = nil, want unauthorized error")
	}
}

func enrollTestNode(t *testing.T, nodeStore store.Store, nodeID string) string {
	t.Helper()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	tokenResp, err := nodeStore.CreateEnrollmentToken(context.Background(), now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	enrollResp, err := nodeStore.EnrollNode(context.Background(), protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: nodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("enroll test node %q: %v", nodeID, err)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("node credential is empty")
	}
	return enrollResp.NodeCredential
}

type fakeRuntimeCollector struct {
	runtimes        []protocol.RuntimeStatus
	configSnapshots []protocol.RuntimeConfigSnapshot
}

func (c fakeRuntimeCollector) CollectStatuses(context.Context) []protocol.RuntimeStatus {
	return append([]protocol.RuntimeStatus(nil), c.runtimes...)
}

func (c fakeRuntimeCollector) CollectConfigSnapshots(context.Context) []protocol.RuntimeConfigSnapshot {
	return append([]protocol.RuntimeConfigSnapshot(nil), c.configSnapshots...)
}
