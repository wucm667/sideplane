package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// HeartbeatClientConfig configures a sidecar heartbeat client.
type HeartbeatClientConfig struct {
	ServerURL      string
	NodeID         string
	NodeCredential string
	Hostname       string
	SidecarVersion string
	HTTPClient     *http.Client
	Now            func() time.Time
	Collector      adapters.RuntimeCollector
}

// HeartbeatClient sends periodic node status heartbeats to a Sideplane server.
type HeartbeatClient struct {
	endpoint       string
	nodeID         string
	nodeCredential string
	hostname       string
	sidecarVersion string
	httpClient     *http.Client
	now            func() time.Time
	collector      adapters.RuntimeCollector
}

// NewHeartbeatClient builds a heartbeat client for a Sideplane server.
func NewHeartbeatClient(cfg HeartbeatClientConfig) (*HeartbeatClient, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, errors.New("server URL is required")
	}
	if strings.TrimSpace(cfg.NodeCredential) == "" {
		return nil, errors.New("node credential is required")
	}

	serverURL := strings.TrimSpace(cfg.ServerURL)
	if !strings.Contains(serverURL, "://") {
		serverURL = "http://" + serverURL
	}

	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("server URL must be absolute: %q", cfg.ServerURL)
	}

	endpoint, err := url.JoinPath(strings.TrimRight(serverURL, "/"), "/api/heartbeat")
	if err != nil {
		return nil, fmt.Errorf("build heartbeat endpoint: %w", err)
	}

	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	nodeID := strings.TrimSpace(cfg.NodeID)
	if nodeID == "" {
		nodeID = hostname
	}
	if nodeID == "" {
		return nil, errors.New("node ID is required when hostname is unavailable")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &HeartbeatClient{
		endpoint:       endpoint,
		nodeID:         nodeID,
		nodeCredential: strings.TrimSpace(cfg.NodeCredential),
		hostname:       hostname,
		sidecarVersion: cfg.SidecarVersion,
		httpClient:     httpClient,
		now:            now,
		collector:      cfg.Collector,
	}, nil
}

// BuildHeartbeat creates the current heartbeat payload.
func (c *HeartbeatClient) BuildHeartbeat(ctx context.Context) protocol.HeartbeatRequest {
	req := protocol.HeartbeatRequest{
		NodeID:         c.nodeID,
		Hostname:       c.hostname,
		SidecarVersion: c.sidecarVersion,
		SentAt:         c.now().UTC(),
		Runtimes:       []protocol.RuntimeStatus{},
	}
	if c.collector != nil {
		req.Runtimes = c.collector.CollectStatuses(ctx)
	}
	return req
}

// SendHeartbeat POSTs a heartbeat to the configured server.
func (c *HeartbeatClient) SendHeartbeat(ctx context.Context) (*protocol.HeartbeatResponse, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(c.BuildHeartbeat(ctx)); err != nil {
		return nil, fmt.Errorf("encode heartbeat: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.nodeCredential)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("heartbeat rejected: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var heartbeatResp protocol.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&heartbeatResp); err != nil {
		return nil, fmt.Errorf("decode heartbeat response: %w", err)
	}

	return &heartbeatResp, nil
}

// RunHeartbeatLoop sends an immediate heartbeat and then repeats at interval.
func RunHeartbeatLoop(ctx context.Context, client *HeartbeatClient, interval time.Duration, report func(*protocol.HeartbeatResponse, error)) error {
	if client == nil {
		return errors.New("heartbeat client is required")
	}
	if interval <= 0 {
		return errors.New("heartbeat interval must be positive")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		resp, err := client.SendHeartbeat(ctx)
		if report != nil {
			report(resp, err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
