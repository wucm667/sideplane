package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

func TestJobPollerLogsJobLifecycleContext(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-log")

	job, err := nodeStore.CreateJob(ctx, protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-log", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	var logs bytes.Buffer
	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-log",
		NodeCredential: credential,
		Collector:      fakeRuntimeCollector{},
		HTTPClient:     api.Client(),
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	body := logs.String()
	for _, want := range []string{
		`"msg":"claimed job"`,
		`"msg":"executing job"`,
		`"msg":"job execution completed"`,
		`"msg":"submitted job result"`,
		`"job_id":"` + job.ID + `"`,
		`"node_id":"node-log"`,
		`"type":"deep_probe"`,
		`"status":"completed"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "resultJson") || strings.Contains(body, "payloadJson") {
		t.Fatalf("sidecar logs exposed payload/result JSON:\n%s", body)
	}
}

func TestJobPollerLogsConfigApplyPayloadRejection(t *testing.T) {
	var logs bytes.Buffer
	poller := &JobPoller{
		nodeID: "node-log",
		logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}

	result := poller.executeConfigApply(context.Background(), &protocol.Job{
		ID:          "job_apply",
		NodeID:      "node-log",
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: `{`,
	})

	if result.Status != protocol.JobStatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	body := logs.String()
	for _, want := range []string{
		`"msg":"config_apply payload rejected"`,
		`"job_id":"job_apply"`,
		`"node_id":"node-log"`,
		`"type":"config_apply"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs missing %q\n%s", want, body)
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

func TestJobPollerRestartDryRunCompletesWithoutController(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-restart")
	job := createRestartJobForTest(t, nodeStore, "node-restart", protocol.RestartJobPayload{
		RuntimeType: "hermes",
		Profile:     "default",
		Reason:      "operator dry-run",
		DryRun:      true,
	})

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-restart",
		NodeCredential: credential,
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got := getJobForTest(t, nodeStore, job.ID)
	if got.Status != protocol.JobStatusCompleted {
		t.Fatalf("restart status = %q, want completed; error=%q", got.Status, got.Error)
	}
	result := decodeRestartResultForTest(t, got.ResultJSON)
	if result.HealthStatus != "skipped" {
		t.Fatalf("health status = %q, want skipped", result.HealthStatus)
	}
	if len(result.Steps) != 3 || result.Steps[1].Status != "skipped" {
		t.Fatalf("restart steps = %#v, want skipped dry-run restart", result.Steps)
	}
}

func TestJobPollerRestartLiveRejectedWithoutAllowLive(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-restart")
	job := createRestartJobForTest(t, nodeStore, "node-restart", protocol.RestartJobPayload{
		RuntimeType: "hermes",
		DryRun:      false,
	})

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	controller := &fakeServiceController{}
	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-restart",
		NodeCredential: credential,
		Controller:     controller,
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got := getJobForTest(t, nodeStore, job.ID)
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("restart status = %q, want failed", got.Status)
	}
	if controller.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0 when live is disabled", controller.restartCalls)
	}
	if !strings.Contains(got.Error, "disabled") {
		t.Fatalf("restart error = %q, want disabled policy", got.Error)
	}
}

func TestJobPollerRestartLiveCallsControllerOnce(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-restart")
	job := createRestartJobForTest(t, nodeStore, "node-restart", protocol.RestartJobPayload{
		RuntimeType: "hermes",
		DryRun:      false,
	})

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	controller := &fakeServiceController{}
	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-restart",
		NodeCredential: credential,
		AllowLiveApply: true,
		Controller:     controller,
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got := getJobForTest(t, nodeStore, job.ID)
	if got.Status != protocol.JobStatusCompleted {
		t.Fatalf("restart status = %q, want completed; error=%q", got.Status, got.Error)
	}
	if controller.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", controller.restartCalls)
	}
	if controller.healthCalls != 1 {
		t.Fatalf("health calls = %d, want 1", controller.healthCalls)
	}
	result := decodeRestartResultForTest(t, got.ResultJSON)
	if result.HealthStatus != "healthy" {
		t.Fatalf("health status = %q, want healthy", result.HealthStatus)
	}
}

func TestJobPollerRestartHealthFailureReturnsFailedResult(t *testing.T) {
	ctx := context.Background()
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-restart")
	job := createRestartJobForTest(t, nodeStore, "node-restart", protocol.RestartJobPayload{
		RuntimeType: "hermes",
		DryRun:      false,
	})

	api := httptest.NewServer(server.NewHandlerWithStore(nodeStore))
	defer api.Close()

	controller := &fakeServiceController{healthErr: errors.New("runtime is not healthy")}
	poller, err := NewJobPoller(JobPollerConfig{
		ServerURL:      api.URL,
		NodeID:         "node-restart",
		NodeCredential: credential,
		AllowLiveApply: true,
		Controller:     controller,
		HTTPClient:     api.Client(),
	})
	if err != nil {
		t.Fatalf("new job poller: %v", err)
	}

	if err := poller.PollAndExecute(ctx); err != nil {
		t.Fatalf("poll and execute: %v", err)
	}

	got := getJobForTest(t, nodeStore, job.ID)
	if got.Status != protocol.JobStatusFailed {
		t.Fatalf("restart status = %q, want failed", got.Status)
	}
	if controller.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", controller.restartCalls)
	}
	result := decodeRestartResultForTest(t, got.ResultJSON)
	if result.HealthStatus != "unhealthy" {
		t.Fatalf("health status = %q, want unhealthy", result.HealthStatus)
	}
	if !strings.Contains(got.Error, "not healthy") {
		t.Fatalf("restart error = %q, want health failure", got.Error)
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

func createRestartJobForTest(t *testing.T, nodeStore store.Store, nodeID string, payload protocol.RestartJobPayload) protocol.Job {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal restart payload: %v", err)
	}
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeRestart,
		PayloadJSON: string(payloadJSON),
	}, nodeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("create restart job: %v", err)
	}
	return job
}

func getJobForTest(t *testing.T, nodeStore store.Store, jobID string) protocol.Job {
	t.Helper()
	job, err := nodeStore.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job == nil {
		t.Fatalf("job %q not found", jobID)
	}
	return *job
}

func decodeRestartResultForTest(t *testing.T, resultJSON string) protocol.RestartJobResult {
	t.Helper()
	var result protocol.RestartJobResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode restart result: %v", err)
	}
	return result
}

type fakeServiceController struct {
	restartCalls int
	healthCalls  int
	restartErr   error
	healthErr    error
}

func (c *fakeServiceController) Restart(context.Context) error {
	c.restartCalls++
	return c.restartErr
}

func (c *fakeServiceController) HealthCheck(context.Context) error {
	c.healthCalls++
	return c.healthErr
}
