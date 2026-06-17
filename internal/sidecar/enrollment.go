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
	"path/filepath"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// EnrollmentClientConfig configures a sidecar enrollment client.
type EnrollmentClientConfig struct {
	ServerURL      string
	NodeID         string
	Hostname       string
	SidecarVersion string
	Token          string
	HTTPClient     *http.Client
}

// EnrollmentClient exchanges a one-time enrollment token for node credentials.
type EnrollmentClient struct {
	endpoint       string
	nodeID         string
	hostname       string
	sidecarVersion string
	token          string
	httpClient     *http.Client
}

// SidecarState is persisted locally after enrollment.
type SidecarState struct {
	ServerURL      string    `json:"serverUrl"`
	NodeID         string    `json:"nodeId"`
	NodeCredential string    `json:"nodeCredential"`
	EnrolledAt     time.Time `json:"enrolledAt"`
}

// NewEnrollmentClient builds an enrollment client for a Sideplane server.
func NewEnrollmentClient(cfg EnrollmentClientConfig) (*EnrollmentClient, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, errors.New("server URL is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("enrollment token is required")
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

	endpoint, err := url.JoinPath(strings.TrimRight(serverURL, "/"), "/api/enroll")
	if err != nil {
		return nil, fmt.Errorf("build enroll endpoint: %w", err)
	}

	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return &EnrollmentClient{
		endpoint:       endpoint,
		nodeID:         strings.TrimSpace(cfg.NodeID),
		hostname:       hostname,
		sidecarVersion: cfg.SidecarVersion,
		token:          strings.TrimSpace(cfg.Token),
		httpClient:     httpClient,
	}, nil
}

// Enroll posts the enrollment request and returns the credential response.
func (c *EnrollmentClient) Enroll(ctx context.Context) (*protocol.EnrollNodeResponse, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(protocol.EnrollNodeRequest{
		Token:          c.token,
		NodeID:         c.nodeID,
		Hostname:       c.hostname,
		SidecarVersion: c.sidecarVersion,
	}); err != nil {
		return nil, fmt.Errorf("encode enroll request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create enroll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post enroll request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("enroll rejected: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var enrollResp protocol.EnrollNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&enrollResp); err != nil {
		return nil, fmt.Errorf("decode enroll response: %w", err)
	}
	if strings.TrimSpace(enrollResp.NodeID) == "" || strings.TrimSpace(enrollResp.NodeCredential) == "" {
		return nil, errors.New("enroll response missing node credential")
	}

	return &enrollResp, nil
}

// DefaultStatePath returns the default sidecar state file path.
func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".sideplane", "sidecar.json"), nil
}

// ReadState loads sidecar state from disk.
func ReadState(path string) (SidecarState, error) {
	if strings.TrimSpace(path) == "" {
		return SidecarState{}, errors.New("state path is required")
	}

	f, err := os.Open(path)
	if err != nil {
		return SidecarState{}, fmt.Errorf("open sidecar state: %w", err)
	}
	defer f.Close()

	var state SidecarState
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return SidecarState{}, fmt.Errorf("decode sidecar state JSON: %w", err)
	}
	return state, nil
}

// WriteState persists sidecar state with owner-only permissions.
func WriteState(path string, state SidecarState) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("state path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sidecar state directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".sidecar-*.json")
	if err != nil {
		return fmt.Errorf("create temporary sidecar state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write sidecar state JSON: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod sidecar state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close sidecar state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace sidecar state: %w", err)
	}
	return nil
}
