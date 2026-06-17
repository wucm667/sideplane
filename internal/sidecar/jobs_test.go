package sidecar

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
)

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
		Runtimes []protocol.RuntimeStatus `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(got.ResultJSON), &result); err != nil {
		t.Fatalf("unmarshal result JSON: %v", err)
	}
	if len(result.Runtimes) != 1 || result.Runtimes[0].Type != "hermes" {
		t.Fatalf("runtimes = %#v, want hermes runtime", result.Runtimes)
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
	runtimes []protocol.RuntimeStatus
}

func (c fakeRuntimeCollector) CollectStatuses(context.Context) []protocol.RuntimeStatus {
	return append([]protocol.RuntimeStatus(nil), c.runtimes...)
}
