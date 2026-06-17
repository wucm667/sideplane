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

// JobPollerConfig configures a sidecar job poller.
type JobPollerConfig struct {
	ServerURL      string
	NodeID         string
	NodeCredential string
	HTTPClient     *http.Client
	Collector      adapters.RuntimeCollector
	Logger         *slog.Logger
}

// JobPoller polls for jobs from the server and executes them.
type JobPoller struct {
	serverURL      string
	endpoint       string
	nodeID         string
	nodeCredential string
	httpClient     *http.Client
	collector      adapters.RuntimeCollector
	logger         *slog.Logger
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

	serverURL := strings.TrimRight(cfg.ServerURL, "/")
	endpoint := serverURL + "/api/sidecar/jobs/next"

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &JobPoller{
		serverURL:      serverURL,
		endpoint:       endpoint,
		nodeID:         cfg.NodeID,
		nodeCredential: cfg.NodeCredential,
		httpClient:     httpClient,
		collector:      cfg.Collector,
		logger:         logger,
	}, nil
}

// PollAndExecute polls for the next job and executes it if found.
func (p *JobPoller) PollAndExecute(ctx context.Context) error {
	job, err := p.claimNextJob(ctx)
	if err != nil {
		return fmt.Errorf("claim next job: %w", err)
	}
	if job == nil {
		// No pending jobs
		return nil
	}

	p.logger.Info("claimed job", "job_id", job.ID, "type", job.Type)

	// Execute the job
	result := p.executeJob(ctx, job)

	// Submit the result
	if err := p.submitJobResult(ctx, job.ID, result); err != nil {
		return fmt.Errorf("submit job result: %w", err)
	}

	p.logger.Info("submitted job result", "job_id", job.ID, "status", result.Status)
	return nil
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
	resultJSON, err := json.Marshal(struct {
		Runtimes []protocol.RuntimeStatus `json:"runtimes"`
	}{
		Runtimes: runtimes,
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
