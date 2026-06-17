package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wucm667/sideplane/pkg/protocol"
)

// SQLiteNodeStore persists node status snapshots in SQLite.
type SQLiteNodeStore struct {
	db *sql.DB
}

var _ Store = (*SQLiteNodeStore)(nil)

// OpenSQLiteNodeStore opens a SQLite database and applies pending migrations.
func OpenSQLiteNodeStore(ctx context.Context, path string) (*SQLiteNodeStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite database path is required")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := configureSQLite(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runSQLiteMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteNodeStore{db: db}, nil
}

// Close closes the underlying SQLite database handle.
func (s *SQLiteNodeStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RecordHeartbeat stores the latest heartbeat-derived status for a node.
func (s *SQLiteNodeStore) RecordHeartbeat(ctx context.Context, req protocol.HeartbeatRequest, observedAt time.Time) (protocol.NodeStatus, error) {
	if s == nil || s.db == nil {
		return protocol.NodeStatus{}, errors.New("sqlite node store is closed")
	}
	if strings.TrimSpace(req.NodeID) == "" {
		return protocol.NodeStatus{}, errors.New("node ID is required")
	}

	node := protocol.NodeStatus{
		NodeID:          req.NodeID,
		Hostname:        req.Hostname,
		State:           protocol.NodeStateFresh,
		SidecarVersion:  req.SidecarVersion,
		LastHeartbeatAt: observedAt.UTC(),
		Runtimes:        append([]protocol.RuntimeStatus(nil), req.Runtimes...),
		ConfigHash:      req.ConfigHash,
		LastError:       req.LastError,
	}

	payloadJSON, err := json.Marshal(req)
	if err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("marshal heartbeat payload: %w", err)
	}
	summaryJSON, err := json.Marshal(node)
	if err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("marshal heartbeat summary: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("begin heartbeat transaction: %w", err)
	}
	defer tx.Rollback()

	now := formatDBTime(time.Now().UTC())
	observedAtText := formatDBTime(node.LastHeartbeatAt)
	_, err = tx.ExecContext(ctx, `
INSERT INTO nodes (
	node_id,
	hostname,
	state,
	sidecar_version,
	last_heartbeat_at,
	config_hash,
	last_error,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id) DO UPDATE SET
	hostname = excluded.hostname,
	state = excluded.state,
	sidecar_version = excluded.sidecar_version,
	last_heartbeat_at = excluded.last_heartbeat_at,
	config_hash = excluded.config_hash,
	last_error = excluded.last_error,
	updated_at = excluded.updated_at
`, node.NodeID, node.Hostname, string(node.State), node.SidecarVersion, observedAtText, node.ConfigHash, node.LastError, now)
	if err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("upsert node: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM node_runtimes WHERE node_id = ?`, node.NodeID); err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("delete previous runtimes: %w", err)
	}

	for i, runtime := range node.Runtimes {
		_, err := tx.ExecContext(ctx, `
INSERT INTO node_runtimes (
	node_id,
	runtime_index,
	name,
	type,
	version,
	state,
	provider,
	model,
	config_hash,
	last_error,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, node.NodeID, i, runtime.Name, runtime.Type, runtime.Version, runtime.State, runtime.Provider, runtime.Model, runtime.ConfigHash, runtime.LastError, now)
		if err != nil {
			return protocol.NodeStatus{}, fmt.Errorf("insert runtime %d: %w", i, err)
		}
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO heartbeats (
	node_id,
	observed_at,
	sent_at,
	payload_json,
	summary_json
) VALUES (?, ?, ?, ?, ?)
`, node.NodeID, observedAtText, nullableDBTime(req.SentAt), string(payloadJSON), string(summaryJSON))
	if err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("insert heartbeat: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return protocol.NodeStatus{}, fmt.Errorf("commit heartbeat transaction: %w", err)
	}

	return node, nil
}

// ListNodes returns a stable snapshot of known nodes.
func (s *SQLiteNodeStore) ListNodes(ctx context.Context) ([]protocol.NodeStatus, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, hostname, state, sidecar_version, last_heartbeat_at, config_hash, last_error
FROM nodes
ORDER BY node_id
`)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}

	var nodes []protocol.NodeStatus
	indexByNodeID := make(map[string]int)
	for rows.Next() {
		var node protocol.NodeStatus
		var state string
		var lastHeartbeatAt string
		if err := rows.Scan(
			&node.NodeID,
			&node.Hostname,
			&state,
			&node.SidecarVersion,
			&lastHeartbeatAt,
			&node.ConfigHash,
			&node.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}

		parsed, err := parseDBTime(lastHeartbeatAt)
		if err != nil {
			return nil, fmt.Errorf("parse node %q heartbeat time: %w", node.NodeID, err)
		}
		node.State = protocol.NodeState(state)
		node.LastHeartbeatAt = parsed

		indexByNodeID[node.NodeID] = len(nodes)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close node rows: %w", err)
	}

	runtimeRows, err := s.db.QueryContext(ctx, `
SELECT node_id, name, type, version, state, provider, model, config_hash, last_error
FROM node_runtimes
ORDER BY node_id, runtime_index
`)
	if err != nil {
		return nil, fmt.Errorf("query node runtimes: %w", err)
	}
	defer runtimeRows.Close()

	for runtimeRows.Next() {
		var nodeID string
		var runtime protocol.RuntimeStatus
		if err := runtimeRows.Scan(
			&nodeID,
			&runtime.Name,
			&runtime.Type,
			&runtime.Version,
			&runtime.State,
			&runtime.Provider,
			&runtime.Model,
			&runtime.ConfigHash,
			&runtime.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan node runtime: %w", err)
		}

		i, ok := indexByNodeID[nodeID]
		if !ok {
			continue
		}
		nodes[i].Runtimes = append(nodes[i].Runtimes, runtime)
	}
	if err := runtimeRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node runtimes: %w", err)
	}

	return nodes, nil
}

// CreateEnrollmentToken creates a one-time enrollment token and stores only its hash.
func (s *SQLiteNodeStore) CreateEnrollmentToken(ctx context.Context, expiresAt time.Time, now time.Time) (protocol.CreateEnrollmentTokenResponse, error) {
	if s == nil || s.db == nil {
		return protocol.CreateEnrollmentTokenResponse{}, errors.New("sqlite node store is closed")
	}
	if expiresAt.IsZero() {
		return protocol.CreateEnrollmentTokenResponse{}, errors.New("enrollment token expiry is required")
	}
	if !expiresAt.After(now.UTC()) {
		return protocol.CreateEnrollmentTokenResponse{}, errors.New("enrollment token expiry must be in the future")
	}

	token, err := newSecret()
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}
	tokenHash, err := hashSecret(token)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}
	tokenID, err := newRandomID("enrtok_")
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO enrollment_tokens (
	id,
	token_hash,
	expires_at,
	created_at
) VALUES (?, ?, ?, ?)
`, tokenID, tokenHash, formatDBTime(expiresAt), formatDBTime(now))
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("insert enrollment token: %w", err)
	}

	return protocol.CreateEnrollmentTokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.UTC(),
	}, nil
}

// EnrollNode exchanges a valid enrollment token for a long-lived node credential.
func (s *SQLiteNodeStore) EnrollNode(ctx context.Context, req protocol.EnrollNodeRequest, now time.Time) (protocol.EnrollNodeResponse, error) {
	if s == nil || s.db == nil {
		return protocol.EnrollNodeResponse{}, errors.New("sqlite node store is closed")
	}

	tokenHash, err := hashSecret(req.Token)
	if err != nil {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenInvalid
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID, err = newRandomID("node_")
		if err != nil {
			return protocol.EnrollNodeResponse{}, err
		}
	}

	nodeCredential, err := newSecret()
	if err != nil {
		return protocol.EnrollNodeResponse{}, err
	}
	credentialHash, err := hashSecret(nodeCredential)
	if err != nil {
		return protocol.EnrollNodeResponse{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("begin enrollment transaction: %w", err)
	}
	defer tx.Rollback()

	var tokenID string
	var expiresAtText string
	var usedAt sql.NullString
	err = tx.QueryRowContext(ctx, `
SELECT id, expires_at, used_at
FROM enrollment_tokens
WHERE token_hash = ?
`, tokenHash).Scan(&tokenID, &expiresAtText, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenInvalid
	}
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("query enrollment token: %w", err)
	}
	if usedAt.Valid && usedAt.String != "" {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenUsed
	}

	expiresAt, err := parseDBTime(expiresAtText)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("parse enrollment token expiry: %w", err)
	}
	if !expiresAt.After(now.UTC()) {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenExpired
	}

	var existingCredentialCount int
	err = tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM node_credentials
WHERE node_id = ?
`, nodeID).Scan(&existingCredentialCount)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("query node credential: %w", err)
	}
	if existingCredentialCount > 0 {
		return protocol.EnrollNodeResponse{}, ErrNodeAlreadyEnrolled
	}

	nowText := formatDBTime(now)
	_, err = tx.ExecContext(ctx, `
INSERT INTO nodes (
	node_id,
	hostname,
	state,
	sidecar_version,
	last_heartbeat_at,
	config_hash,
	last_error,
	updated_at
) VALUES (?, ?, ?, ?, ?, '', '', ?)
ON CONFLICT(node_id) DO NOTHING
`, nodeID, strings.TrimSpace(req.Hostname), string(protocol.NodeStateOffline), strings.TrimSpace(req.SidecarVersion), "", nowText)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("insert enrolled node: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO node_credentials (
	node_id,
	credential_hash,
	created_at
) VALUES (?, ?, ?)
`, nodeID, credentialHash, nowText)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("insert node credential: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
UPDATE enrollment_tokens
SET used_at = ?
WHERE id = ? AND used_at IS NULL
`, nowText, tokenID)
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("mark enrollment token used: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("count enrollment token update: %w", err)
	}
	if rowsAffected != 1 {
		return protocol.EnrollNodeResponse{}, ErrEnrollmentTokenUsed
	}

	if err := tx.Commit(); err != nil {
		return protocol.EnrollNodeResponse{}, fmt.Errorf("commit enrollment transaction: %w", err)
	}

	return protocol.EnrollNodeResponse{
		NodeID:         nodeID,
		NodeCredential: nodeCredential,
	}, nil
}

// VerifyNodeCredential checks whether a node credential matches the stored hash.
func (s *SQLiteNodeStore) VerifyNodeCredential(ctx context.Context, nodeID string, credential string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, nil
	}

	var credentialHash string
	err := s.db.QueryRowContext(ctx, `
SELECT credential_hash
FROM node_credentials
WHERE node_id = ?
`, nodeID).Scan(&credentialHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query node credential: %w", err)
	}

	return secretHashMatches(credential, credentialHash)
}

func configureSQLite(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	return nil
}

func formatDBTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableDBTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatDBTime(t)
}

func parseDBTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
