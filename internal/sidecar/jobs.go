package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	defaultJobResultBufferLimit = 100
	defaultJobResultRetryBase   = time.Second
	defaultJobResultRetryMax    = 30 * time.Second
)

// JobPollerConfig configures a sidecar job poller.
type JobPollerConfig struct {
	ServerURL          string
	NodeID             string
	NodeCredential     string
	PublicKey          string
	ApplyWorkDir       string
	EnvPath            string
	AllowedConfigDirs  []string
	AllowLiveApply     bool
	Controller         adapters.ServiceController
	ControllerResolver ServiceControllerResolver
	HTTPClient         *http.Client
	Collector          adapters.RuntimeCollector
	ConfigCollector    adapters.ConfigSnapshotCollector
	Logger             *slog.Logger
	// JobResultBufferLimit bounds the in-memory retry buffer for job results
	// that could not be delivered. It does not persist across sidecar restarts.
	JobResultBufferLimit int
}

// ServiceControllerResolver selects a runtime-specific service controller.
type ServiceControllerResolver interface {
	ServiceController(runtimeType string) adapters.ServiceController
}

// JobPoller polls for jobs from the server and executes them.
type JobPoller struct {
	serverURL          string
	endpoint           string
	nodeID             string
	nodeCredential     string
	publicKey          string
	applyWorkDir       string
	envPath            string
	allowedConfigDirs  []string
	allowLiveApply     bool
	controller         adapters.ServiceController
	controllerResolver ServiceControllerResolver
	httpClient         *http.Client
	collector          adapters.RuntimeCollector
	configCollector    adapters.ConfigSnapshotCollector
	logger             *slog.Logger
	resultBuffer       []bufferedJobResult
	resultBufferLimit  int
	resultRetryBase    time.Duration
	resultRetryMax     time.Duration
}

type bufferedJobResult struct {
	JobID       string
	Result      protocol.JobResultRequest
	Attempts    int
	NextAttempt time.Time
}

// NewJobPoller creates a new job poller.
func NewJobPoller(cfg JobPollerConfig) (*JobPoller, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if strings.TrimSpace(cfg.NodeID) == "" {
		return nil, fmt.Errorf("node ID is required")
	}
	if strings.TrimSpace(cfg.NodeCredential) == "" {
		return nil, fmt.Errorf("node credential is required")
	}

	serverURL, err := normalizeServerURL(cfg.ServerURL)
	if err != nil {
		return nil, err
	}
	endpoint := serverURL + "/api/sidecar/jobs/next"

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	resultBufferLimit := cfg.JobResultBufferLimit
	if resultBufferLimit <= 0 {
		resultBufferLimit = defaultJobResultBufferLimit
	}
	configCollector := cfg.ConfigCollector
	if configCollector == nil {
		if collector, ok := cfg.Collector.(adapters.ConfigSnapshotCollector); ok {
			configCollector = collector
		}
	}
	controllerResolver := cfg.ControllerResolver
	if controllerResolver == nil {
		if resolver, ok := cfg.Collector.(ServiceControllerResolver); ok {
			controllerResolver = resolver
		}
	}

	return &JobPoller{
		serverURL:          serverURL,
		endpoint:           endpoint,
		nodeID:             cfg.NodeID,
		nodeCredential:     cfg.NodeCredential,
		publicKey:          strings.TrimSpace(cfg.PublicKey),
		applyWorkDir:       strings.TrimSpace(cfg.ApplyWorkDir),
		envPath:            strings.TrimSpace(cfg.EnvPath),
		allowedConfigDirs:  append([]string(nil), cfg.AllowedConfigDirs...),
		allowLiveApply:     cfg.AllowLiveApply,
		controller:         cfg.Controller,
		controllerResolver: controllerResolver,
		httpClient:         httpClient,
		collector:          cfg.Collector,
		configCollector:    configCollector,
		logger:             logger,
		resultBufferLimit:  resultBufferLimit,
		resultRetryBase:    defaultJobResultRetryBase,
		resultRetryMax:     defaultJobResultRetryMax,
	}, nil
}

func normalizeServerURL(rawURL string) (string, error) {
	serverURL := strings.TrimSpace(rawURL)
	if !strings.Contains(serverURL, "://") {
		serverURL = "http://" + serverURL
	}

	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("server URL must include a host")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (p *JobPoller) jobLogger() *slog.Logger {
	if p == nil || p.logger == nil {
		return slog.Default()
	}
	return p.logger
}

// PollAndExecute polls for the next job and executes it if found.
func (p *JobPoller) PollAndExecute(ctx context.Context) error {
	p.retryBufferedJobResults(ctx)

	job, err := p.claimNextJob(ctx)
	if err != nil {
		return fmt.Errorf("claim next job: %w", err)
	}
	if job == nil {
		// No pending jobs
		return nil
	}

	logger := p.jobLogger()
	logger.Info("claimed job", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "status", job.Status)

	// Execute the job
	logger.Info("executing job", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type)
	result := p.executeJob(ctx, job)
	if result.Status == protocol.JobStatusFailed {
		logger.Warn("job execution failed", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "status", result.Status, "error", result.Error)
	} else {
		logger.Info("job execution completed", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "status", result.Status)
	}

	// Submit the result
	if err := p.submitJobResult(ctx, job.ID, result); err != nil {
		p.bufferJobResult(job.ID, result, err)
		logger.Warn("job result delivery failed; buffered for retry", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "status", result.Status, "error", err)
		return nil
	}

	logger.Info("submitted job result", "job_id", job.ID, "node_id", p.nodeID, "type", job.Type, "status", result.Status)
	return nil
}

func (p *JobPoller) retryBufferedJobResults(ctx context.Context) {
	if p == nil || len(p.resultBuffer) == 0 {
		return
	}
	now := time.Now().UTC()
	remaining := p.resultBuffer[:0]
	logger := p.jobLogger()
	for _, item := range p.resultBuffer {
		if !item.NextAttempt.IsZero() && now.Before(item.NextAttempt) {
			remaining = append(remaining, item)
			continue
		}
		if err := p.submitJobResult(ctx, item.JobID, item.Result); err != nil {
			item.Attempts++
			item.NextAttempt = now.Add(p.jobResultBackoff(item.Attempts))
			remaining = append(remaining, item)
			logger.Warn("buffered job result retry failed", "job_id", item.JobID, "node_id", p.nodeID, "status", item.Result.Status, "attempts", item.Attempts, "error", err)
			continue
		}
		logger.Info("submitted buffered job result", "job_id", item.JobID, "node_id", p.nodeID, "status", item.Result.Status)
	}
	p.resultBuffer = remaining
}

func (p *JobPoller) bufferJobResult(jobID string, result protocol.JobResultRequest, deliveryErr error) {
	if p.resultBufferLimit <= 0 {
		p.resultBufferLimit = defaultJobResultBufferLimit
	}
	if len(p.resultBuffer) >= p.resultBufferLimit {
		dropped := p.resultBuffer[0]
		copy(p.resultBuffer, p.resultBuffer[1:])
		p.resultBuffer = p.resultBuffer[:len(p.resultBuffer)-1]
		p.jobLogger().Warn("job result retry buffer full; dropped oldest result", "job_id", dropped.JobID, "node_id", p.nodeID, "status", dropped.Result.Status)
	}
	now := time.Now().UTC()
	p.resultBuffer = append(p.resultBuffer, bufferedJobResult{
		JobID:       jobID,
		Result:      result,
		Attempts:    1,
		NextAttempt: now.Add(p.jobResultBackoff(1)),
	})
	if deliveryErr != nil {
		p.jobLogger().Warn("queued job result for retry", "job_id", jobID, "node_id", p.nodeID, "status", result.Status, "error", deliveryErr)
	}
}

func (p *JobPoller) jobResultBackoff(attempts int) time.Duration {
	base := p.resultRetryBase
	if base <= 0 || attempts <= 0 {
		return 0
	}
	delay := base
	for i := 1; i < attempts; i++ {
		if p.resultRetryMax > 0 && delay >= p.resultRetryMax/2 {
			return p.resultRetryMax
		}
		delay *= 2
	}
	if p.resultRetryMax > 0 && delay > p.resultRetryMax {
		return p.resultRetryMax
	}
	return delay
}

// claimNextJob polls for the next pending job.
func (p *JobPoller) claimNextJob(ctx context.Context) (*protocol.Job, error) {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	q := u.Query()
	q.Set("nodeId", p.nodeID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.nodeCredential)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// No pending jobs
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var job protocol.Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}

	return &job, nil
}

// executeJob executes a job and returns the result.
func (p *JobPoller) executeJob(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	switch job.Type {
	case protocol.JobTypeDeepProbe:
		return p.executeDeepProbe(ctx, job)
	case protocol.JobTypeConfigApply:
		return p.executeConfigApply(ctx, job)
	case protocol.JobTypeRestart:
		return p.executeRestart(ctx, job)
	case protocol.JobTypeRollback:
		return p.executeRollback(ctx, job)
	default:
		return protocol.JobResultRequest{
			Status: protocol.JobStatusFailed,
			Error:  fmt.Sprintf("unknown job type: %s", job.Type),
		}
	}
}

// executeDeepProbe executes a deep probe job.
func (p *JobPoller) executeDeepProbe(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	if p.collector == nil {
		return protocol.JobResultRequest{
			Status: protocol.JobStatusFailed,
			Error:  "runtime collector not configured",
		}
	}

	runtimes := p.collector.CollectStatuses(ctx)
	if runtimes == nil {
		runtimes = []protocol.RuntimeStatus{}
	}
	configSnapshots := []protocol.RuntimeConfigSnapshot{}
	if p.configCollector != nil {
		configSnapshots = p.configCollector.CollectConfigSnapshots(ctx)
		if configSnapshots == nil {
			configSnapshots = []protocol.RuntimeConfigSnapshot{}
		}
	}
	resultJSON, err := json.Marshal(protocol.DeepProbeResult{
		Runtimes:        runtimes,
		ConfigSnapshots: configSnapshots,
	})
	if err != nil {
		return protocol.JobResultRequest{
			Status: protocol.JobStatusFailed,
			Error:  fmt.Sprintf("marshal runtimes: %v", err),
		}
	}

	return protocol.JobResultRequest{
		Status:     protocol.JobStatusCompleted,
		ResultJSON: string(resultJSON),
	}
}

func (p *JobPoller) executeRestart(ctx context.Context, job *protocol.Job) protocol.JobResultRequest {
	var payload protocol.RestartJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return protocol.JobResultRequest{
			Status: protocol.JobStatusFailed,
			Error:  fmt.Sprintf("invalid restart payload: %v", err),
		}
	}

	runtimeType := restartRuntimeType(payload.RuntimeType)
	controller := p.controllerForRuntime(runtimeType)
	result := protocol.RestartJobResult{
		Controller:   restartControllerLabel(controller),
		HealthStatus: "not_checked",
	}
	addStep := func(name, status, detail string) {
		result.Steps = append(result.Steps, protocol.ConfigApplyStep{Name: name, Status: status, Detail: detail})
	}
	addStep("payload_received", "completed", restartTargetDetail(payload))

	if payload.DryRun {
		addStep("restarted", "skipped", "dry-run")
		addStep("health_checked", "skipped", "dry-run")
		result.HealthStatus = "skipped"
		return marshalRestartResult(protocol.JobStatusCompleted, result, "")
	}

	if !p.allowLiveApply {
		err := "live restart is disabled by sidecar policy (--allow-live-apply off)"
		addStep("restarted", "failed", err)
		return marshalRestartResult(protocol.JobStatusFailed, result, err)
	}
	if controller == nil {
		err := "live restart requires a configured service controller"
		addStep("restarted", "failed", err)
		return marshalRestartResult(protocol.JobStatusFailed, result, err)
	}

	if err := controller.Restart(ctx); err != nil {
		addStep("restarted", "failed", err.Error())
		return marshalRestartResult(protocol.JobStatusFailed, result, err.Error())
	}
	addStep("restarted", "completed", "")

	if err := controller.HealthCheck(ctx); err != nil {
		result.HealthStatus = "unhealthy"
		addStep("health_checked", "failed", err.Error())
		return marshalRestartResult(protocol.JobStatusFailed, result, err.Error())
	}
	result.HealthStatus = "healthy"
	addStep("health_checked", "completed", "")
	return marshalRestartResult(protocol.JobStatusCompleted, result, "")
}

func (p *JobPoller) controllerForRuntime(runtimeType string) adapters.ServiceController {
	if p == nil {
		return nil
	}
	runtimeType = restartRuntimeType(runtimeType)
	if p.controllerResolver != nil {
		if controller := p.controllerResolver.ServiceController(runtimeType); controller != nil {
			return controller
		}
		if runtimeType != "hermes" {
			return nil
		}
	}
	return p.controller
}

func restartRuntimeType(runtimeType string) string {
	runtimeType = strings.TrimSpace(runtimeType)
	if runtimeType == "" {
		return "hermes"
	}
	return runtimeType
}

func marshalRestartResult(status protocol.JobStatus, result protocol.RestartJobResult, errText string) protocol.JobResultRequest {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return protocol.JobResultRequest{
			Status: protocol.JobStatusFailed,
			Error:  fmt.Sprintf("marshal restart result: %v", err),
		}
	}
	return protocol.JobResultRequest{
		Status:     status,
		ResultJSON: string(resultJSON),
		Error:      errText,
	}
}

func (p *JobPoller) fetchConfigApplySecrets(ctx context.Context, signedPlan protocol.SignedConfigPlan) (map[string]string, error) {
	endpoint, err := url.JoinPath(p.serverURL, "/api/sidecar/config-apply/secrets")
	if err != nil {
		return nil, fmt.Errorf("build config apply secrets endpoint: %w", err)
	}
	body, err := json.Marshal(signedPlan)
	if err != nil {
		return nil, fmt.Errorf("marshal signed config plan: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create config apply secrets request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.nodeCredential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send config apply secrets request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	var decoded protocol.ConfigApplySecretsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode config apply secrets response: %w", err)
	}
	if decoded.Secrets == nil {
		return map[string]string{}, nil
	}
	return decoded.Secrets, nil
}

func restartControllerLabel(controller adapters.ServiceController) string {
	if controller == nil {
		return "none"
	}
	return fmt.Sprintf("%T", controller)
}

func restartTargetDetail(payload protocol.RestartJobPayload) string {
	parts := []string{}
	if payload.RuntimeType != "" {
		parts = append(parts, "type="+payload.RuntimeType)
	}
	if payload.RuntimeName != "" {
		parts = append(parts, "name="+payload.RuntimeName)
	}
	if payload.Profile != "" {
		parts = append(parts, "profile="+payload.Profile)
	}
	if payload.Reason != "" {
		parts = append(parts, "reason="+payload.Reason)
	}
	if len(parts) == 0 {
		return "default target"
	}
	return strings.Join(parts, " ")
}

// submitJobResult submits a job result to the server.
func (p *JobPoller) submitJobResult(ctx context.Context, jobID string, result protocol.JobResultRequest) error {
	endpoint, err := url.JoinPath(p.serverURL, "/api/sidecar/jobs", jobID, "result")
	if err != nil {
		return fmt.Errorf("build result endpoint: %w", err)
	}

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.nodeCredential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// RunJobPoller runs a job polling loop at the specified interval.
func RunJobPoller(ctx context.Context, poller *JobPoller, interval time.Duration) error {
	if poller == nil {
		return fmt.Errorf("poller is nil")
	}
	if interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := poller.PollAndExecute(ctx); err != nil {
		poller.logger.Error("job poll failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := poller.PollAndExecute(ctx); err != nil {
				poller.logger.Error("job poll failed", "error", err)
			}
		}
	}
}
