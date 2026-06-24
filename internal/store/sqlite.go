package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// Check verifies that the SQLite store can execute a lightweight query.
func (s *SQLiteNodeStore) Check(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	var ok int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&ok); err != nil {
		return fmt.Errorf("check sqlite database: %w", err)
	}
	if ok != 1 {
		return fmt.Errorf("check sqlite database returned %d", ok)
	}
	return nil
}

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

// SchemaVersion reports the newest applied SQLite migration version.
func (s *SQLiteNodeStore) SchemaVersion(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite node store is closed")
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("query sqlite schema version: %w", err)
	}
	return version, nil
}

// BackupTo writes a transactionally consistent copy of the database to destPath
// using SQLite's online `VACUUM INTO`. It does not block writers for long and
// requires destPath to not already exist.
func (s *SQLiteNodeStore) BackupTo(ctx context.Context, destPath string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		return errors.New("backup destination path is required")
	}
	// VACUUM INTO does not accept bind parameters, so the path is embedded as a
	// quoted SQL string literal with single quotes escaped.
	escaped := strings.ReplaceAll(destPath, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("sqlite vacuum into backup: %w", err)
	}
	return nil
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
		warningsJSON := ""
		if len(runtime.Warnings) > 0 {
			payload, err := json.Marshal(runtime.Warnings)
			if err != nil {
				return protocol.NodeStatus{}, fmt.Errorf("marshal runtime %d warnings: %w", i, err)
			}
			warningsJSON = string(payload)
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO node_runtimes (
	node_id,
	runtime_index,
	name,
	type,
	version,
	deployment_mode,
	state,
	provider,
	model,
	config_hash,
	last_error,
	warnings_json,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, node.NodeID, i, runtime.Name, runtime.Type, runtime.Version, runtime.DeploymentMode, runtime.State, runtime.Provider, runtime.Model, runtime.ConfigHash, runtime.LastError, warningsJSON, now)
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
	node.Labels, err = s.GetNodeLabels(ctx, node.NodeID)
	if err != nil {
		return protocol.NodeStatus{}, err
	}
	node.Maintenance, err = s.GetNodeMaintenance(ctx, node.NodeID)
	if err != nil {
		return protocol.NodeStatus{}, err
	}

	return node, nil
}

// ListNodes returns a stable snapshot of known nodes.
func (s *SQLiteNodeStore) ListNodes(ctx context.Context) ([]protocol.NodeStatus, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, hostname, state, sidecar_version, last_heartbeat_at, config_hash, last_error, maintenance
FROM nodes
ORDER BY node_id
`)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	nodes, indexByNodeID, err := scanSQLiteNodes(rows)
	if err != nil {
		return nil, err
	}

	runtimeRows, err := s.db.QueryContext(ctx, `
SELECT node_id, name, type, version, deployment_mode, state, provider, model, config_hash, last_error, warnings_json
FROM node_runtimes
ORDER BY node_id, runtime_index
`)
	if err != nil {
		return nil, fmt.Errorf("query node runtimes: %w", err)
	}
	defer runtimeRows.Close()

	if err := scanSQLiteNodeRuntimes(runtimeRows, nodes, indexByNodeID); err != nil {
		return nil, err
	}
	if err := s.scanSQLiteNodeLabels(ctx, nodes, indexByNodeID); err != nil {
		return nil, err
	}

	return nodes, nil
}

// ListNodesFiltered returns a bounded, stable snapshot of known nodes.
func (s *SQLiteNodeStore) ListNodesFiltered(ctx context.Context, filter NodeFilter) (NodeList, error) {
	if s == nil || s.db == nil {
		return NodeList{}, errors.New("sqlite node store is closed")
	}
	filter = NormalizeNodeFilter(filter)
	if len(filter.Labels) > 0 {
		nodes, err := s.ListNodes(ctx)
		if err != nil {
			return NodeList{}, err
		}
		nodes = filterNodesByLabels(nodes, filter.Labels)
		total := len(nodes)
		start := filter.Offset
		if start > total {
			start = total
		}
		end := start + filter.Limit
		if end > total {
			end = total
		}
		return NodeList{
			Nodes:  nodes[start:end],
			Total:  total,
			Limit:  filter.Limit,
			Offset: filter.Offset,
		}, nil
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&total); err != nil {
		return NodeList{}, fmt.Errorf("count nodes: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, hostname, state, sidecar_version, last_heartbeat_at, config_hash, last_error, maintenance
FROM nodes
ORDER BY node_id
LIMIT ? OFFSET ?
`, filter.Limit, filter.Offset)
	if err != nil {
		return NodeList{}, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	nodes, indexByNodeID, err := scanSQLiteNodes(rows)
	if err != nil {
		return NodeList{}, err
	}
	if len(nodes) > 0 {
		runtimeRows, err := s.db.QueryContext(ctx, `
WITH page AS (
	SELECT node_id
	FROM nodes
	ORDER BY node_id
	LIMIT ? OFFSET ?
)
SELECT nr.node_id, nr.name, nr.type, nr.version, nr.deployment_mode, nr.state, nr.provider, nr.model, nr.config_hash, nr.last_error, nr.warnings_json
FROM node_runtimes nr
JOIN page ON page.node_id = nr.node_id
ORDER BY nr.node_id, nr.runtime_index
`, filter.Limit, filter.Offset)
		if err != nil {
			return NodeList{}, fmt.Errorf("query node runtimes: %w", err)
		}
		defer runtimeRows.Close()
		if err := scanSQLiteNodeRuntimes(runtimeRows, nodes, indexByNodeID); err != nil {
			return NodeList{}, err
		}
		if err := s.scanSQLiteNodeLabels(ctx, nodes, indexByNodeID); err != nil {
			return NodeList{}, err
		}
	}

	return NodeList{
		Nodes:  nodes,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}, nil
}

func scanSQLiteNodes(rows *sql.Rows) ([]protocol.NodeStatus, map[string]int, error) {
	var nodes []protocol.NodeStatus
	indexByNodeID := make(map[string]int)
	for rows.Next() {
		var node protocol.NodeStatus
		var state string
		var lastHeartbeatAt string
		var maintenance int
		if err := rows.Scan(
			&node.NodeID,
			&node.Hostname,
			&state,
			&node.SidecarVersion,
			&lastHeartbeatAt,
			&node.ConfigHash,
			&node.LastError,
			&maintenance,
		); err != nil {
			return nil, nil, fmt.Errorf("scan node: %w", err)
		}

		parsed, err := parseDBTime(lastHeartbeatAt)
		if err != nil {
			return nil, nil, fmt.Errorf("parse node %q heartbeat time: %w", node.NodeID, err)
		}
		node.State = protocol.NodeState(state)
		node.LastHeartbeatAt = parsed
		node.Maintenance = maintenance != 0

		indexByNodeID[node.NodeID] = len(nodes)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return nodes, indexByNodeID, nil
}

func scanSQLiteNodeRuntimes(rows *sql.Rows, nodes []protocol.NodeStatus, indexByNodeID map[string]int) error {
	for rows.Next() {
		var nodeID string
		var runtime protocol.RuntimeStatus
		var warningsJSON string
		if err := rows.Scan(
			&nodeID,
			&runtime.Name,
			&runtime.Type,
			&runtime.Version,
			&runtime.DeploymentMode,
			&runtime.State,
			&runtime.Provider,
			&runtime.Model,
			&runtime.ConfigHash,
			&runtime.LastError,
			&warningsJSON,
		); err != nil {
			return fmt.Errorf("scan node runtime: %w", err)
		}
		if strings.TrimSpace(warningsJSON) != "" {
			if err := json.Unmarshal([]byte(warningsJSON), &runtime.Warnings); err != nil {
				return fmt.Errorf("parse node runtime warnings: %w", err)
			}
		}

		i, ok := indexByNodeID[nodeID]
		if !ok {
			continue
		}
		nodes[i].Runtimes = append(nodes[i].Runtimes, runtime)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate node runtimes: %w", err)
	}
	return nil
}

func (s *SQLiteNodeStore) scanSQLiteNodeLabels(ctx context.Context, nodes []protocol.NodeStatus, indexByNodeID map[string]int) error {
	if len(nodes) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, key, value
FROM node_labels
ORDER BY node_id, key
`)
	if err != nil {
		return fmt.Errorf("query node labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var nodeID, key, value string
		if err := rows.Scan(&nodeID, &key, &value); err != nil {
			return fmt.Errorf("scan node label: %w", err)
		}
		i, ok := indexByNodeID[nodeID]
		if !ok {
			continue
		}
		if nodes[i].Labels == nil {
			nodes[i].Labels = map[string]string{}
		}
		nodes[i].Labels[key] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate node labels: %w", err)
	}
	return nil
}

// NodeExists reports whether a node is known to the store.
func (s *SQLiteNodeStore) NodeExists(ctx context.Context, nodeID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, nil
	}

	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE node_id = ?`, nodeID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query node existence: %w", err)
	}
	return true, nil
}

// SetNodeLabels replaces operator-managed labels for a node.
func (s *SQLiteNodeStore) SetNodeLabels(ctx context.Context, nodeID string, labels map[string]string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node ID is required")
	}
	normalized, err := ValidateNodeLabels(labels)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set node labels transaction: %w", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE node_id = ?`, nodeID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNodeNotFound
	}
	if err != nil {
		return fmt.Errorf("query node before setting labels: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_labels WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("delete previous node labels: %w", err)
	}
	for key, value := range normalized {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO node_labels (node_id, key, value)
VALUES (?, ?, ?)
`, nodeID, key, value); err != nil {
			return fmt.Errorf("insert node label %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set node labels transaction: %w", err)
	}
	return nil
}

// GetNodeLabels returns a copy of operator-managed labels for a node.
func (s *SQLiteNodeStore) GetNodeLabels(ctx context.Context, nodeID string) (map[string]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}
	exists, err := s.NodeExists(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT key, value
FROM node_labels
WHERE node_id = ?
ORDER BY key
`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query node labels: %w", err)
	}
	defer rows.Close()

	labels := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan node label: %w", err)
		}
		labels[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node labels: %w", err)
	}
	if len(labels) == 0 {
		return nil, nil
	}
	return labels, nil
}

// SetNodeMaintenance updates operator-set maintenance mode for a node.
func (s *SQLiteNodeStore) SetNodeMaintenance(ctx context.Context, nodeID string, maintenance bool) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node ID is required")
	}
	value := 0
	if maintenance {
		value = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE nodes SET maintenance = ?, updated_at = ? WHERE node_id = ?`, value, formatDBTime(time.Now().UTC()), nodeID)
	if err != nil {
		return fmt.Errorf("set node maintenance: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count node maintenance update: %w", err)
	}
	if affected == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// GetNodeMaintenance returns operator-set maintenance mode for a node.
func (s *SQLiteNodeStore) GetNodeMaintenance(ctx context.Context, nodeID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, errors.New("node ID is required")
	}
	var maintenance int
	err := s.db.QueryRowContext(ctx, `SELECT maintenance FROM nodes WHERE node_id = ?`, nodeID).Scan(&maintenance)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNodeNotFound
	}
	if err != nil {
		return false, fmt.Errorf("query node maintenance: %w", err)
	}
	return maintenance != 0, nil
}

// DeleteNode removes a node and all node-scoped associated data.
func (s *SQLiteNodeStore) DeleteNode(ctx context.Context, nodeID string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node ID is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete node transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM node_credentials WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("delete node credentials: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE target_node = ?`, nodeID); err != nil {
		return fmt.Errorf("delete node audit events: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE node_id = ?`, nodeID)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted nodes: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNodeNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete node transaction: %w", err)
	}
	return nil
}

// PruneHeartbeats keeps the latest keep heartbeat rows per node.
func (s *SQLiteNodeStore) PruneHeartbeats(ctx context.Context, keep int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite node store is closed")
	}
	if keep <= 0 {
		return 0, errors.New("heartbeat keep count must be positive")
	}

	result, err := s.db.ExecContext(ctx, `
DELETE FROM heartbeats
WHERE id IN (
	SELECT id
	FROM (
		SELECT
			id,
			ROW_NUMBER() OVER (
				PARTITION BY node_id
				ORDER BY observed_at DESC, id DESC
			) AS row_number
		FROM heartbeats
	)
	WHERE row_number > ?
)
`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune heartbeats: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned heartbeats: %w", err)
	}
	return deleted, nil
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

// CreateOperatorToken creates a named operator token and stores only its hash.
func (s *SQLiteNodeStore) CreateOperatorToken(ctx context.Context, name string, scope protocol.OperatorTokenScope, now time.Time) (protocol.CreateOperatorTokenResponse, error) {
	if s == nil || s.db == nil {
		return protocol.CreateOperatorTokenResponse{}, errors.New("sqlite node store is closed")
	}
	name, err := ValidateOperatorTokenName(name)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	scope, err = ValidateOperatorTokenScope(scope)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	token, err := newSecret()
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	tokenHash, err := hashSecret(token)
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}
	tokenID, err := newRandomID("optok_")
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, err
	}

	createdAt := now.UTC()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO operator_tokens (
	id,
	name,
	scope,
	token_hash,
	created_at
) VALUES (?, ?, ?, ?, ?)
`, tokenID, name, string(scope), tokenHash, formatDBTime(createdAt))
	if err != nil {
		return protocol.CreateOperatorTokenResponse{}, fmt.Errorf("insert operator token: %w", err)
	}

	return protocol.CreateOperatorTokenResponse{
		OperatorToken: protocol.OperatorToken{
			ID:        tokenID,
			Name:      name,
			Scope:     scope,
			CreatedAt: createdAt,
		},
		Token: token,
	}, nil
}

// ListOperatorTokens returns operator token metadata without plaintext secrets.
func (s *SQLiteNodeStore) ListOperatorTokens(ctx context.Context) ([]protocol.OperatorToken, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, scope, created_at, last_used_at, revoked_at
FROM operator_tokens
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query operator tokens: %w", err)
	}
	defer rows.Close()

	tokens := []protocol.OperatorToken{}
	for rows.Next() {
		token, err := scanSQLiteOperatorToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator tokens: %w", err)
	}
	return tokens, nil
}

// RevokeOperatorToken marks a named operator token as revoked.
func (s *SQLiteNodeStore) RevokeOperatorToken(ctx context.Context, tokenID string, now time.Time) (protocol.OperatorToken, error) {
	if s == nil || s.db == nil {
		return protocol.OperatorToken{}, errors.New("sqlite node store is closed")
	}
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return protocol.OperatorToken{}, ErrOperatorTokenNotFound
	}

	token, err := s.loadOperatorToken(ctx, tokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.OperatorToken{}, ErrOperatorTokenNotFound
	}
	if err != nil {
		return protocol.OperatorToken{}, err
	}
	if token.RevokedAt != nil {
		return token, nil
	}

	revokedAt := now.UTC()
	_, err = s.db.ExecContext(ctx, `
UPDATE operator_tokens
SET revoked_at = ?
WHERE id = ?
`, formatDBTime(revokedAt), tokenID)
	if err != nil {
		return protocol.OperatorToken{}, fmt.Errorf("revoke operator token: %w", err)
	}
	token.RevokedAt = &revokedAt
	return token, nil
}

// VerifyOperatorToken verifies an active named operator token and returns its
// ID and scope.
func (s *SQLiteNodeStore) VerifyOperatorToken(ctx context.Context, token string) (string, protocol.OperatorTokenScope, bool, error) {
	if s == nil || s.db == nil {
		return "", "", false, errors.New("sqlite node store is closed")
	}
	tokenHash, err := hashSecret(token)
	if err != nil {
		return "", "", false, nil
	}

	var tokenID, scopeText string
	err = s.db.QueryRowContext(ctx, `
SELECT id, scope
FROM operator_tokens
WHERE token_hash = ? AND (revoked_at IS NULL OR revoked_at = '')
`, tokenHash).Scan(&tokenID, &scopeText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("query operator token: %w", err)
	}
	scope, _ := protocol.NormalizeOperatorTokenScope(protocol.OperatorTokenScope(scopeText))
	return tokenID, scope, true, nil
}

// UpdateOperatorTokenLastUsed records a best-effort named token use timestamp.
func (s *SQLiteNodeStore) UpdateOperatorTokenLastUsed(ctx context.Context, tokenID string, usedAt time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return ErrOperatorTokenNotFound
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE operator_tokens
SET last_used_at = ?
WHERE id = ? AND (revoked_at IS NULL OR revoked_at = '')
`, formatDBTime(usedAt.UTC()), tokenID)
	if err != nil {
		return fmt.Errorf("update operator token last used: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count operator token last-used update: %w", err)
	}
	if rowsAffected == 0 {
		return ErrOperatorTokenNotFound
	}
	return nil
}

// CreateAlertWebhook stores an outbound alert webhook configuration.
func (s *SQLiteNodeStore) CreateAlertWebhook(ctx context.Context, req protocol.CreateAlertWebhookRequest, now time.Time) (protocol.AlertWebhook, error) {
	if s == nil || s.db == nil {
		return protocol.AlertWebhook{}, errors.New("sqlite node store is closed")
	}
	req, err := ValidateAlertWebhookRequest(req)
	if err != nil {
		return protocol.AlertWebhook{}, err
	}
	id, err := newRandomID("whk_")
	if err != nil {
		return protocol.AlertWebhook{}, err
	}
	eventsJSON, err := json.Marshal(req.Events)
	if err != nil {
		return protocol.AlertWebhook{}, fmt.Errorf("encode webhook events: %w", err)
	}
	createdAt := now.UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO alert_webhooks (id, kind, url, events_json, secret, disabled, created_at)
VALUES (?, ?, ?, ?, ?, 0, ?)
`, id, string(req.Kind), req.URL, string(eventsJSON), req.Secret, formatDBTime(createdAt)); err != nil {
		return protocol.AlertWebhook{}, fmt.Errorf("insert alert webhook: %w", err)
	}
	return protocol.AlertWebhook{
		ID:        id,
		Kind:      req.Kind,
		URL:       req.URL,
		Events:    append([]protocol.AlertEventType(nil), req.Events...),
		HasSecret: req.Secret != "",
		Disabled:  false,
		CreatedAt: createdAt,
	}, nil
}

// ListAlertWebhooks returns alert webhook metadata newest-first without secrets.
func (s *SQLiteNodeStore) ListAlertWebhooks(ctx context.Context) ([]protocol.AlertWebhook, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, url, events_json, secret, disabled, created_at
FROM alert_webhooks
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query alert webhooks: %w", err)
	}
	defer rows.Close()
	webhooks := []protocol.AlertWebhook{}
	for rows.Next() {
		webhook, _, err := scanSQLiteAlertWebhook(rows)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, webhook)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alert webhooks: %w", err)
	}
	return webhooks, nil
}

// DeleteAlertWebhook removes an alert webhook by ID.
func (s *SQLiteNodeStore) DeleteAlertWebhook(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrAlertWebhookNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM alert_webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete alert webhook: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count alert webhook delete: %w", err)
	}
	if affected == 0 {
		return ErrAlertWebhookNotFound
	}
	return nil
}

// ListAlertWebhookTargets returns enabled webhooks subscribed to event with secrets.
func (s *SQLiteNodeStore) ListAlertWebhookTargets(ctx context.Context, event protocol.AlertEventType) ([]AlertWebhookTarget, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, url, events_json, secret, disabled, created_at
FROM alert_webhooks
WHERE disabled = 0
ORDER BY id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("query alert webhook targets: %w", err)
	}
	defer rows.Close()
	targets := make([]AlertWebhookTarget, 0)
	for rows.Next() {
		webhook, secret, err := scanSQLiteAlertWebhook(rows)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(webhook.Events, event) {
			continue
		}
		targets = append(targets, AlertWebhookTarget{ID: webhook.ID, Kind: webhook.Kind, URL: webhook.URL, Secret: secret})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alert webhook targets: %w", err)
	}
	return targets, nil
}

func scanSQLiteAlertWebhook(scanner sqliteScanner) (protocol.AlertWebhook, string, error) {
	var webhook protocol.AlertWebhook
	var kindText, eventsJSON, secret, createdAtText string
	var disabled int
	if err := scanner.Scan(&webhook.ID, &kindText, &webhook.URL, &eventsJSON, &secret, &disabled, &createdAtText); err != nil {
		return protocol.AlertWebhook{}, "", err
	}
	kind, ok := protocol.NormalizeAlertWebhookKind(protocol.AlertWebhookKind(kindText))
	if !ok {
		return protocol.AlertWebhook{}, "", fmt.Errorf("decode webhook kind %q", kindText)
	}
	if err := json.Unmarshal([]byte(eventsJSON), &webhook.Events); err != nil {
		return protocol.AlertWebhook{}, "", fmt.Errorf("decode webhook events: %w", err)
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return protocol.AlertWebhook{}, "", fmt.Errorf("parse webhook created_at: %w", err)
	}
	webhook.Kind = kind
	webhook.CreatedAt = createdAt
	webhook.Disabled = disabled != 0
	webhook.HasSecret = secret != ""
	return webhook, secret, nil
}

// CreateRolloutTemplate stores a reusable rollout spec under a name.
func (s *SQLiteNodeStore) CreateRolloutTemplate(ctx context.Context, name string, spec protocol.RolloutSpec, now time.Time) (protocol.RolloutTemplate, error) {
	if s == nil || s.db == nil {
		return protocol.RolloutTemplate{}, errors.New("sqlite node store is closed")
	}
	name, err := ValidateRolloutTemplateName(name)
	if err != nil {
		return protocol.RolloutTemplate{}, err
	}
	id, err := newRandomID("rtpl_")
	if err != nil {
		return protocol.RolloutTemplate{}, err
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return protocol.RolloutTemplate{}, fmt.Errorf("encode rollout template spec: %w", err)
	}
	createdAt := now.UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO rollout_templates (id, name, spec_json, created_at)
VALUES (?, ?, ?, ?)
`, id, name, string(specJSON), formatDBTime(createdAt)); err != nil {
		return protocol.RolloutTemplate{}, fmt.Errorf("insert rollout template: %w", err)
	}
	return protocol.RolloutTemplate{ID: id, Name: name, Spec: spec, CreatedAt: createdAt}, nil
}

// ListRolloutTemplates returns saved rollout templates newest-first.
func (s *SQLiteNodeStore) ListRolloutTemplates(ctx context.Context) ([]protocol.RolloutTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, spec_json, created_at
FROM rollout_templates
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query rollout templates: %w", err)
	}
	defer rows.Close()
	templates := []protocol.RolloutTemplate{}
	for rows.Next() {
		template, err := scanSQLiteRolloutTemplate(rows)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rollout templates: %w", err)
	}
	return templates, nil
}

// GetRolloutTemplate returns one rollout template by ID.
func (s *SQLiteNodeStore) GetRolloutTemplate(ctx context.Context, id string) (*protocol.RolloutTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrRolloutTemplateNotFound
	}
	template, err := scanSQLiteRolloutTemplate(s.db.QueryRowContext(ctx, `
SELECT id, name, spec_json, created_at
FROM rollout_templates
WHERE id = ?
`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRolloutTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	return &template, nil
}

// DeleteRolloutTemplate removes a rollout template by ID.
func (s *SQLiteNodeStore) DeleteRolloutTemplate(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrRolloutTemplateNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM rollout_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete rollout template: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count rollout template delete: %w", err)
	}
	if affected == 0 {
		return ErrRolloutTemplateNotFound
	}
	return nil
}

func scanSQLiteRolloutTemplate(scanner sqliteScanner) (protocol.RolloutTemplate, error) {
	var template protocol.RolloutTemplate
	var specJSON, createdAtText string
	if err := scanner.Scan(&template.ID, &template.Name, &specJSON, &createdAtText); err != nil {
		return protocol.RolloutTemplate{}, err
	}
	if err := json.Unmarshal([]byte(specJSON), &template.Spec); err != nil {
		return protocol.RolloutTemplate{}, fmt.Errorf("decode rollout template spec: %w", err)
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return protocol.RolloutTemplate{}, fmt.Errorf("parse rollout template created_at: %w", err)
	}
	template.CreatedAt = createdAt
	return template, nil
}

// GetServerSettings returns the operator-tunable server settings.
func (s *SQLiteNodeStore) GetServerSettings(ctx context.Context) (protocol.ServerSettings, error) {
	if s == nil || s.db == nil {
		return protocol.ServerSettings{}, errors.New("sqlite node store is closed")
	}
	var version string
	var runtimeVersionsJSON string
	err := s.db.QueryRowContext(ctx, `SELECT expected_sidecar_version, expected_runtime_versions_json FROM server_settings WHERE id = 1`).Scan(&version, &runtimeVersionsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.ServerSettings{ExpectedRuntimeVersions: map[string]string{}}, nil
	}
	if err != nil {
		return protocol.ServerSettings{}, fmt.Errorf("query server settings: %w", err)
	}
	runtimeVersions, err := decodeExpectedRuntimeVersions(runtimeVersionsJSON)
	if err != nil {
		return protocol.ServerSettings{}, err
	}
	return protocol.ServerSettings{
		ExpectedSidecarVersion:  version,
		ExpectedRuntimeVersions: runtimeVersions,
	}, nil
}

// SetExpectedSidecarVersion records the operator-configured expected sidecar version.
func (s *SQLiteNodeStore) SetExpectedSidecarVersion(ctx context.Context, version string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	version = strings.TrimSpace(version)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO server_settings (id, expected_sidecar_version)
VALUES (1, ?)
ON CONFLICT(id) DO UPDATE SET expected_sidecar_version = excluded.expected_sidecar_version
`, version)
	if err != nil {
		return fmt.Errorf("set expected sidecar version: %w", err)
	}
	return nil
}

// SetExpectedRuntimeVersions records operator-configured expected runtime versions.
func (s *SQLiteNodeStore) SetExpectedRuntimeVersions(ctx context.Context, versions map[string]string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	encoded, err := encodeExpectedRuntimeVersions(versions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO server_settings (id, expected_runtime_versions_json)
VALUES (1, ?)
ON CONFLICT(id) DO UPDATE SET expected_runtime_versions_json = excluded.expected_runtime_versions_json
`, encoded)
	if err != nil {
		return fmt.Errorf("set expected runtime versions: %w", err)
	}
	return nil
}

// GetJob retrieves a job by ID.
func (s *SQLiteNodeStore) GetJob(ctx context.Context, jobID string) (*protocol.Job, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job ID is required")
	}

	job, err := s.loadJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

// CreateJob creates a new job for a node.
func (s *SQLiteNodeStore) CreateJob(ctx context.Context, req protocol.CreateJobRequest, nodeID string, now time.Time) (protocol.Job, error) {
	if s == nil || s.db == nil {
		return protocol.Job{}, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return protocol.Job{}, errors.New("node ID is required")
	}

	jobID, err := newRandomID("job_")
	if err != nil {
		return protocol.Job{}, err
	}

	job := protocol.Job{
		ID:          jobID,
		NodeID:      nodeID,
		Type:        req.Type,
		Status:      protocol.JobStatusPending,
		PayloadJSON: req.PayloadJSON,
		CreatedAt:   now.UTC(),
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Job{}, fmt.Errorf("begin create job transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.expireClaimedJobsTx(ctx, tx, now.UTC()); err != nil {
		return protocol.Job{}, err
	}

	rows, err := tx.QueryContext(ctx, `
SELECT payload_json, status
FROM jobs
WHERE node_id = ? AND type = ? AND status IN (?, ?)
`, nodeID, string(req.Type), string(protocol.JobStatusPending), string(protocol.JobStatusClaimed))
	if err != nil {
		return protocol.Job{}, fmt.Errorf("query active jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payloadJSON string
		var status string
		if err := rows.Scan(&payloadJSON, &status); err != nil {
			return protocol.Job{}, fmt.Errorf("scan active job: %w", err)
		}
		existing := protocol.Job{
			NodeID:      nodeID,
			Type:        req.Type,
			Status:      protocol.JobStatus(status),
			PayloadJSON: payloadJSON,
		}
		if activeJobConflict(job, existing) {
			return protocol.Job{}, ErrActiveJobExists
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.Job{}, fmt.Errorf("iterate active jobs: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO jobs (
	id,
	node_id,
	type,
	status,
	payload_json,
	result_json,
	error,
	created_at,
	claimed_at,
	finished_at
) VALUES (?, ?, ?, ?, ?, '', '', ?, NULL, NULL)
`, job.ID, job.NodeID, string(job.Type), string(job.Status), job.PayloadJSON, formatDBTime(job.CreatedAt))
	if err != nil {
		return protocol.Job{}, fmt.Errorf("insert job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Job{}, fmt.Errorf("commit create job transaction: %w", err)
	}

	return job, nil
}

// ClaimNextJob claims the next pending job for a node.
func (s *SQLiteNodeStore) ClaimNextJob(ctx context.Context, nodeID string, now time.Time) (*protocol.Job, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim job transaction: %w", err)
	}
	defer tx.Rollback()

	now = now.UTC()
	if err := s.expireClaimedJobsTx(ctx, tx, now); err != nil {
		return nil, err
	}

	var jobID string
	var jobType string
	err = tx.QueryRowContext(ctx, `
SELECT id, type
FROM jobs
WHERE node_id = ? AND status = ?
ORDER BY created_at ASC
LIMIT 1
`, nodeID, string(protocol.JobStatusPending)).Scan(&jobID, &jobType)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit expired job updates: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query next pending job: %w", err)
	}

	claimedAt := now
	claimExpiresAt := claimedAt.Add(jobClaimLease(protocol.JobType(jobType)))
	result, err := tx.ExecContext(ctx, `
UPDATE jobs
SET status = ?, claimed_at = ?, claim_expires_at = ?
WHERE id = ? AND status = ?
`, string(protocol.JobStatusClaimed), formatDBTime(claimedAt), formatDBTime(claimExpiresAt), jobID, string(protocol.JobStatusPending))
	if err != nil {
		return nil, fmt.Errorf("update job status to claimed: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count job claim update: %w", err)
	}
	if rowsAffected != 1 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit expired job updates: %w", err)
		}
		return nil, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit job claim transaction: %w", err)
	}

	job, err := s.loadJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// CompleteJob marks a job as completed with a result.
func (s *SQLiteNodeStore) CompleteJob(ctx context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job ID is required")
	}

	finishedAt := now.UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = ?, result_json = ?, finished_at = ?, claim_expires_at = NULL
WHERE id = ? AND status = ?
`, string(protocol.JobStatusCompleted), result.ResultJSON, formatDBTime(finishedAt), jobID, string(protocol.JobStatusClaimed))
	if err != nil {
		return fmt.Errorf("update job to completed: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count job completion update: %w", err)
	}
	if rowsAffected == 0 {
		if err := s.recordLateJobResult(ctx, jobID, result, now); err != nil {
			return err
		}
		return ErrLateJobResultRecorded
	}
	return nil
}

// FailJob marks a job as failed with an error message and optional result JSON.
func (s *SQLiteNodeStore) FailJob(ctx context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job ID is required")
	}

	finishedAt := now.UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = ?, result_json = ?, error = ?, finished_at = ?, claim_expires_at = NULL
WHERE id = ? AND status = ?
`, string(protocol.JobStatusFailed), result.ResultJSON, result.Error, formatDBTime(finishedAt), jobID, string(protocol.JobStatusClaimed))
	if err != nil {
		return fmt.Errorf("update job to failed: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count job failure update: %w", err)
	}
	if rowsAffected == 0 {
		if err := s.recordLateJobResult(ctx, jobID, result, now); err != nil {
			return err
		}
		return ErrLateJobResultRecorded
	}
	return nil
}

func (s *SQLiteNodeStore) recordLateJobResult(ctx context.Context, jobID string, result protocol.JobResultRequest, now time.Time) error {
	job, err := s.loadJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("job not found or not in claimed state")
		}
		return err
	}
	if !IsJobClaimTimeout(job) {
		return errors.New("job not found or not in claimed state")
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE jobs
SET result_json = ?, error = ?, finished_at = ?, claim_expires_at = NULL
WHERE id = ? AND status = ?
`, result.ResultJSON, lateJobResultError(result), formatDBTime(now.UTC()), jobID, string(protocol.JobStatusFailed))
	if err != nil {
		return fmt.Errorf("record late job result: %w", err)
	}
	return nil
}

// ListNodeJobs returns the default page of jobs for a node.
func (s *SQLiteNodeStore) ListNodeJobs(ctx context.Context, nodeID string) ([]protocol.Job, error) {
	return s.ListNodeJobsFiltered(ctx, nodeID, JobFilter{})
}

// ListNodeJobsFiltered returns bounded jobs for a node, optionally filtered by status.
func (s *SQLiteNodeStore) ListNodeJobsFiltered(ctx context.Context, nodeID string, filter JobFilter) ([]protocol.Job, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, errors.New("node ID is required")
	}
	filter = normalizeJobFilter(filter)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list jobs transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.expireClaimedJobsTx(ctx, tx, time.Now().UTC()); err != nil {
		return nil, err
	}

	query := `
SELECT id
FROM jobs
WHERE node_id = ?
`
	args := []any{nodeID}
	if filter.Status != "" {
		query += `AND status = ?
`
		args = append(args, string(filter.Status))
	}
	query += `ORDER BY created_at DESC
LIMIT ?
`
	args = append(args, filter.Limit)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query node jobs: %w", err)
	}
	defer rows.Close()

	var jobIDs []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, fmt.Errorf("scan job ID: %w", err)
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job IDs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit list jobs transaction: %w", err)
	}

	jobs := make([]protocol.Job, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		job, err := s.loadJob(ctx, jobID)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// PruneTerminalJobs removes completed and failed jobs finished before before.
func (s *SQLiteNodeStore) PruneTerminalJobs(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite node store is closed")
	}
	if before.IsZero() {
		return 0, errors.New("job retention cutoff is required")
	}

	result, err := s.db.ExecContext(ctx, `
DELETE FROM jobs
WHERE status IN (?, ?)
AND finished_at IS NOT NULL
AND finished_at != ''
AND julianday(finished_at) < julianday(?)
`, string(protocol.JobStatusCompleted), string(protocol.JobStatusFailed), formatDBTime(before.UTC()))
	if err != nil {
		return 0, fmt.Errorf("prune terminal jobs: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned terminal jobs: %w", err)
	}
	return deleted, nil
}

// CreateRollout stores a new rollout snapshot and assigns an ID when needed.
func (s *SQLiteNodeStore) CreateRollout(ctx context.Context, rollout protocol.Rollout) (protocol.Rollout, error) {
	if s == nil || s.db == nil {
		return protocol.Rollout{}, errors.New("sqlite node store is closed")
	}
	if rollout.ID == "" {
		id, err := newRandomID("rollout_")
		if err != nil {
			return protocol.Rollout{}, err
		}
		rollout.ID = id
	}
	if rollout.State == "" {
		rollout.State = protocol.RolloutStatePending
	}
	if rollout.CreatedAt.IsZero() {
		rollout.CreatedAt = time.Now().UTC()
	} else {
		rollout.CreatedAt = rollout.CreatedAt.UTC()
	}
	if rollout.UpdatedAt.IsZero() {
		rollout.UpdatedAt = rollout.CreatedAt
	} else {
		rollout.UpdatedAt = rollout.UpdatedAt.UTC()
	}
	if !rollout.FinishedAt.IsZero() {
		rollout.FinishedAt = rollout.FinishedAt.UTC()
	}
	clone, err := cloneRollout(rollout)
	if err != nil {
		return protocol.Rollout{}, err
	}
	payload, err := json.Marshal(clone)
	if err != nil {
		return protocol.Rollout{}, fmt.Errorf("marshal rollout: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO rollouts (
	id,
	state,
	rollout_json,
	created_at,
	updated_at,
	finished_at
) VALUES (?, ?, ?, ?, ?, ?)
`, clone.ID, string(clone.State), string(payload), formatDBTime(clone.CreatedAt), formatDBTime(clone.UpdatedAt), nullableDBTime(clone.FinishedAt))
	if err != nil {
		return protocol.Rollout{}, fmt.Errorf("insert rollout: %w", err)
	}
	return cloneRollout(clone)
}

// GetRollout returns one rollout snapshot by ID.
func (s *SQLiteNodeStore) GetRollout(ctx context.Context, rolloutID string) (*protocol.Rollout, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return nil, errors.New("rollout ID is required")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT rollout_json FROM rollouts WHERE id = ?`, rolloutID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query rollout: %w", err)
	}
	rollout, err := parseSQLiteRollout(payload)
	if err != nil {
		return nil, err
	}
	return &rollout, nil
}

// ListRollouts returns a paginated newest-first rollout list.
func (s *SQLiteNodeStore) ListRollouts(ctx context.Context, filter RolloutFilter) (RolloutList, error) {
	if s == nil || s.db == nil {
		return RolloutList{}, errors.New("sqlite node store is closed")
	}
	filter = NormalizeRolloutFilter(filter)

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollouts`).Scan(&total); err != nil {
		return RolloutList{}, fmt.Errorf("count rollouts: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT rollout_json
FROM rollouts
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?
`, filter.Limit, filter.Offset)
	if err != nil {
		return RolloutList{}, fmt.Errorf("query rollouts: %w", err)
	}
	defer rows.Close()

	rollouts := []protocol.Rollout{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return RolloutList{}, fmt.Errorf("scan rollout: %w", err)
		}
		rollout, err := parseSQLiteRollout(payload)
		if err != nil {
			return RolloutList{}, err
		}
		rollouts = append(rollouts, rollout)
	}
	if err := rows.Err(); err != nil {
		return RolloutList{}, fmt.Errorf("iterate rollouts: %w", err)
	}
	return RolloutList{
		Rollouts: rollouts,
		Total:    total,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}, nil
}

// ListActiveRolloutConflicts finds non-terminal rollouts that already target any node.
func (s *SQLiteNodeStore) ListActiveRolloutConflicts(ctx context.Context, nodeIDs []string) ([]RolloutNodeConflict, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	targets := rolloutConflictTargetSet(nodeIDs)
	if len(targets) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT rollout_json
FROM rollouts
WHERE state NOT IN (?, ?, ?)
ORDER BY created_at DESC, id DESC
`, string(protocol.RolloutStateCompleted), string(protocol.RolloutStateAborted), string(protocol.RolloutStateFailed))
	if err != nil {
		return nil, fmt.Errorf("query active rollout conflicts: %w", err)
	}
	defer rows.Close()

	conflicts := []RolloutNodeConflict{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan active rollout: %w", err)
		}
		rollout, err := parseSQLiteRollout(payload)
		if err != nil {
			return nil, err
		}
		if rolloutStateTerminal(rollout.State) {
			continue
		}
		for _, nodeID := range rollout.Spec.NodeIDs {
			if _, ok := targets[nodeID]; !ok {
				continue
			}
			conflicts = append(conflicts, RolloutNodeConflict{
				NodeID:    nodeID,
				RolloutID: rollout.ID,
				State:     rollout.State,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active rollout conflicts: %w", err)
	}
	sortRolloutNodeConflicts(conflicts)
	return conflicts, nil
}

// UpdateRollout replaces a rollout snapshot.
func (s *SQLiteNodeStore) UpdateRollout(ctx context.Context, rollout protocol.Rollout) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	rollout.ID = strings.TrimSpace(rollout.ID)
	if rollout.ID == "" {
		return errors.New("rollout ID is required")
	}
	clone, err := cloneRollout(rollout)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(clone)
	if err != nil {
		return fmt.Errorf("marshal rollout: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE rollouts
SET state = ?, rollout_json = ?, updated_at = ?, finished_at = ?
WHERE id = ?
`, string(clone.State), string(payload), formatDBTime(clone.UpdatedAt), nullableDBTime(clone.FinishedAt), clone.ID)
	if err != nil {
		return fmt.Errorf("update rollout: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count rollout update: %w", err)
	}
	if rowsAffected == 0 {
		return ErrRolloutNotFound
	}
	return nil
}

// PruneTerminalRollouts removes terminal rollouts finished before before.
func (s *SQLiteNodeStore) PruneTerminalRollouts(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite node store is closed")
	}
	if before.IsZero() {
		return 0, errors.New("rollout retention cutoff is required")
	}
	result, err := s.db.ExecContext(ctx, `
DELETE FROM rollouts
WHERE state IN (?, ?, ?)
AND finished_at IS NOT NULL
AND finished_at != ''
AND julianday(finished_at) < julianday(?)
`, string(protocol.RolloutStateCompleted), string(protocol.RolloutStateAborted), string(protocol.RolloutStateFailed), formatDBTime(before.UTC()))
	if err != nil {
		return 0, fmt.Errorf("prune terminal rollouts: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned terminal rollouts: %w", err)
	}
	return deleted, nil
}

// AppendAuditEvent stores an audit event and assigns an ID when needed.
func (s *SQLiteNodeStore) AppendAuditEvent(ctx context.Context, event protocol.AuditEvent) (protocol.AuditEvent, error) {
	if s == nil || s.db == nil {
		return protocol.AuditEvent{}, errors.New("sqlite node store is closed")
	}
	event.Actor = strings.TrimSpace(event.Actor)
	event.ActorName = strings.TrimSpace(event.ActorName)
	event.Action = strings.TrimSpace(event.Action)
	event.TargetNode = strings.TrimSpace(event.TargetNode)
	event.Detail = strings.TrimSpace(event.Detail)
	if event.Actor == "" {
		return protocol.AuditEvent{}, errors.New("audit actor is required")
	}
	if event.Action == "" {
		return protocol.AuditEvent{}, errors.New("audit action is required")
	}
	if event.ID == "" {
		id, err := newRandomID("audit_")
		if err != nil {
			return protocol.AuditEvent{}, err
		}
		event.ID = id
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	} else {
		event.CreatedAt = event.CreatedAt.UTC()
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_events (
	id,
	actor,
	actor_name,
	action,
	target_node,
	detail,
	created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
`, event.ID, event.Actor, event.ActorName, event.Action, event.TargetNode, event.Detail, formatDBTime(event.CreatedAt))
	if err != nil {
		return protocol.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	return event, nil
}

// ListAuditEvents returns recent audit events newest first.
func (s *SQLiteNodeStore) ListAuditEvents(ctx context.Context, limit int) ([]protocol.AuditEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	return s.listAuditEvents(ctx, AuditFilter{Limit: limit}, 100)
}

// ListAuditEventsFiltered returns recent audit events matching all filters.
func (s *SQLiteNodeStore) ListAuditEventsFiltered(ctx context.Context, filter AuditFilter) ([]protocol.AuditEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite node store is closed")
	}
	return s.listAuditEvents(ctx, filter, 500)
}

// PruneAuditEvents removes audit events created before before.
func (s *SQLiteNodeStore) PruneAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite node store is closed")
	}
	if before.IsZero() {
		return 0, errors.New("audit retention cutoff is required")
	}

	result, err := s.db.ExecContext(ctx, `
DELETE FROM audit_events
WHERE created_at != ''
AND julianday(created_at) < julianday(?)
`, formatDBTime(before.UTC()))
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned audit events: %w", err)
	}
	return deleted, nil
}

func (s *SQLiteNodeStore) listAuditEvents(ctx context.Context, filter AuditFilter, maxLimit int) ([]protocol.AuditEvent, error) {
	limit := normalizeAuditLimit(filter.Limit, maxLimit)
	nodeID := strings.TrimSpace(filter.NodeID)
	action := strings.TrimSpace(filter.Action)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, actor, actor_name, action, target_node, detail, created_at
FROM audit_events
WHERE (? = '' OR target_node = ?)
AND (? = '' OR action = ?)
ORDER BY created_at DESC, id DESC
LIMIT ?
`, nodeID, nodeID, action, action, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	events := []protocol.AuditEvent{}
	for rows.Next() {
		var event protocol.AuditEvent
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Actor, &event.ActorName, &event.Action, &event.TargetNode, &event.Detail, &createdAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		parsed, err := parseDBTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse audit event %q created_at: %w", event.ID, err)
		}
		event.CreatedAt = parsed
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

func normalizeAuditLimit(limit int, maxLimit int) int {
	if maxLimit <= 0 {
		maxLimit = 100
	}
	if limit <= 0 {
		return 100
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// GetDesiredConfig returns the layered desired runtime config.
func (s *SQLiteNodeStore) GetDesiredConfig(ctx context.Context) (protocol.DesiredConfig, error) {
	if s == nil || s.db == nil {
		return protocol.DesiredConfig{}, errors.New("sqlite node store is closed")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT config_json FROM desired_config WHERE id = 1`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.DesiredConfig{}, nil
	}
	if err != nil {
		return protocol.DesiredConfig{}, fmt.Errorf("query desired config: %w", err)
	}
	var desired protocol.DesiredConfig
	if err := json.Unmarshal([]byte(payload), &desired); err != nil {
		return protocol.DesiredConfig{}, fmt.Errorf("parse desired config: %w", err)
	}
	return desired, nil
}

// SetDesiredConfig replaces the layered desired runtime config.
func (s *SQLiteNodeStore) SetDesiredConfig(ctx context.Context, desired protocol.DesiredConfig, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	entry, err := newDesiredConfigHistoryEntry(desired, desiredConfigHistoryActorOperator, now)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin desired config transaction: %w", err)
	}
	defer tx.Rollback()
	if err := insertDesiredConfigTx(ctx, tx, entry); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit desired config transaction: %w", err)
	}
	return nil
}

// ListDesiredConfigHistory returns a bounded desired-config history page.
func (s *SQLiteNodeStore) ListDesiredConfigHistory(ctx context.Context, filter DesiredConfigHistoryFilter) (DesiredConfigHistoryList, error) {
	if s == nil || s.db == nil {
		return DesiredConfigHistoryList{}, errors.New("sqlite node store is closed")
	}
	filter = NormalizeDesiredConfigHistoryFilter(filter)

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM desired_config_history`).Scan(&total); err != nil {
		return DesiredConfigHistoryList{}, fmt.Errorf("count desired config history: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, config_json, updated_at, actor
FROM desired_config_history
ORDER BY updated_at DESC, id DESC
LIMIT ? OFFSET ?
`, filter.Limit, filter.Offset)
	if err != nil {
		return DesiredConfigHistoryList{}, fmt.Errorf("query desired config history: %w", err)
	}
	defer rows.Close()

	history := []protocol.DesiredConfigHistoryEntry{}
	for rows.Next() {
		entry, err := scanSQLiteDesiredConfigHistoryEntry(rows)
		if err != nil {
			return DesiredConfigHistoryList{}, err
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		return DesiredConfigHistoryList{}, fmt.Errorf("iterate desired config history: %w", err)
	}
	return DesiredConfigHistoryList{
		History: history,
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
	}, nil
}

// RevertDesiredConfig restores a past desired-config version and records a new history entry.
func (s *SQLiteNodeStore) RevertDesiredConfig(ctx context.Context, historyID string) (protocol.DesiredConfigHistoryEntry, error) {
	if s == nil || s.db == nil {
		return protocol.DesiredConfigHistoryEntry{}, errors.New("sqlite node store is closed")
	}
	historyID = strings.TrimSpace(historyID)
	if historyID == "" {
		return protocol.DesiredConfigHistoryEntry{}, ErrDesiredConfigHistoryNotFound
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("begin desired config revert transaction: %w", err)
	}
	defer tx.Rollback()

	var payload string
	err = tx.QueryRowContext(ctx, `
SELECT config_json
FROM desired_config_history
WHERE id = ?
`, historyID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.DesiredConfigHistoryEntry{}, ErrDesiredConfigHistoryNotFound
	}
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("query desired config history entry: %w", err)
	}
	var desired protocol.DesiredConfig
	if err := json.Unmarshal([]byte(payload), &desired); err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("parse desired config history entry: %w", err)
	}
	entry, err := newDesiredConfigHistoryEntry(desired, desiredConfigHistoryActorOperator, time.Now().UTC())
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, err
	}
	if err := insertDesiredConfigTx(ctx, tx, entry); err != nil {
		return protocol.DesiredConfigHistoryEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("commit desired config revert transaction: %w", err)
	}
	return entry, nil
}

// SetProviderSecret stores opaque encrypted provider secret bytes by env var name.
func (s *SQLiteNodeStore) SetProviderSecret(ctx context.Context, envName string, ciphertext []byte, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return errors.New("provider secret env name is required")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO provider_secrets (env_name, ciphertext, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(env_name) DO UPDATE SET
	ciphertext = excluded.ciphertext,
	updated_at = excluded.updated_at
`, envName, append([]byte(nil), ciphertext...), formatDBTime(now.UTC()))
	if err != nil {
		return fmt.Errorf("upsert provider secret: %w", err)
	}
	return nil
}

// GetProviderSecret returns opaque encrypted provider secret bytes by env var name.
func (s *SQLiteNodeStore) GetProviderSecret(ctx context.Context, envName string) ([]byte, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("sqlite node store is closed")
	}
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return nil, false, nil
	}
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
SELECT ciphertext
FROM provider_secrets
WHERE env_name = ?
`, envName).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query provider secret: %w", err)
	}
	return append([]byte(nil), ciphertext...), true, nil
}

// DeleteProviderSecret removes opaque encrypted provider secret bytes by env var name.
func (s *SQLiteNodeStore) DeleteProviderSecret(ctx context.Context, envName string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite node store is closed")
	}
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM provider_secrets WHERE env_name = ?`, envName); err != nil {
		return fmt.Errorf("delete provider secret: %w", err)
	}
	return nil
}

// HasProviderSecret reports whether encrypted provider secret bytes exist.
func (s *SQLiteNodeStore) HasProviderSecret(ctx context.Context, envName string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite node store is closed")
	}
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return false, nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `
SELECT 1
FROM provider_secrets
WHERE env_name = ?
LIMIT 1
`, envName).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("query provider secret existence: %w", err)
	}
	return exists == 1, nil
}

func insertDesiredConfigTx(ctx context.Context, tx *sql.Tx, entry protocol.DesiredConfigHistoryEntry) error {
	payload, err := json.Marshal(entry.Config)
	if err != nil {
		return fmt.Errorf("marshal desired config: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO desired_config (id, config_json, updated_at)
VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	config_json = excluded.config_json,
	updated_at = excluded.updated_at
`, string(payload), formatDBTime(entry.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert desired config: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO desired_config_history (
	id,
	config_json,
	updated_at,
	actor
) VALUES (?, ?, ?, ?)
`, entry.ID, string(payload), formatDBTime(entry.UpdatedAt), entry.Actor)
	if err != nil {
		return fmt.Errorf("insert desired config history: %w", err)
	}
	return nil
}

func parseSQLiteRollout(payload string) (protocol.Rollout, error) {
	var rollout protocol.Rollout
	if err := json.Unmarshal([]byte(payload), &rollout); err != nil {
		return protocol.Rollout{}, fmt.Errorf("parse rollout: %w", err)
	}
	return rollout, nil
}

type sqliteScanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteNodeStore) loadOperatorToken(ctx context.Context, tokenID string) (protocol.OperatorToken, error) {
	return scanSQLiteOperatorToken(s.db.QueryRowContext(ctx, `
SELECT id, name, scope, created_at, last_used_at, revoked_at
FROM operator_tokens
WHERE id = ?
`, tokenID))
}

func scanSQLiteOperatorToken(scanner sqliteScanner) (protocol.OperatorToken, error) {
	var token protocol.OperatorToken
	var scopeText, createdAtText string
	var lastUsedAtText, revokedAtText sql.NullString
	if err := scanner.Scan(&token.ID, &token.Name, &scopeText, &createdAtText, &lastUsedAtText, &revokedAtText); err != nil {
		return protocol.OperatorToken{}, err
	}
	token.Scope, _ = protocol.NormalizeOperatorTokenScope(protocol.OperatorTokenScope(scopeText))
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return protocol.OperatorToken{}, fmt.Errorf("parse operator token created_at: %w", err)
	}
	lastUsedAt, err := parseOptionalDBTime(lastUsedAtText)
	if err != nil {
		return protocol.OperatorToken{}, fmt.Errorf("parse operator token last_used_at: %w", err)
	}
	revokedAt, err := parseOptionalDBTime(revokedAtText)
	if err != nil {
		return protocol.OperatorToken{}, fmt.Errorf("parse operator token revoked_at: %w", err)
	}
	token.CreatedAt = createdAt
	token.LastUsedAt = lastUsedAt
	token.RevokedAt = revokedAt
	return token, nil
}

func scanSQLiteDesiredConfigHistoryEntry(scanner sqliteScanner) (protocol.DesiredConfigHistoryEntry, error) {
	var entry protocol.DesiredConfigHistoryEntry
	var payload string
	var updatedAtText string
	if err := scanner.Scan(&entry.ID, &payload, &updatedAtText, &entry.Actor); err != nil {
		return protocol.DesiredConfigHistoryEntry{}, err
	}
	if err := json.Unmarshal([]byte(payload), &entry.Config); err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("parse desired config history: %w", err)
	}
	updatedAt, err := parseDBTime(updatedAtText)
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, fmt.Errorf("parse desired config history updated_at: %w", err)
	}
	entry.UpdatedAt = updatedAt
	entry.DesiredHash = hashDesiredConfig(entry.Config)
	return entry, nil
}

func (s *SQLiteNodeStore) loadJob(ctx context.Context, jobID string) (protocol.Job, error) {
	var job protocol.Job
	var status, createdAt string
	var claimedAt, claimExpiresAt, finishedAt sql.NullString

	err := s.db.QueryRowContext(ctx, `
SELECT id, node_id, type, status, payload_json, result_json, error, created_at, claimed_at, claim_expires_at, finished_at
FROM jobs
WHERE id = ?
`, jobID).Scan(&job.ID, &job.NodeID, &job.Type, &status, &job.PayloadJSON, &job.ResultJSON, &job.Error, &createdAt, &claimedAt, &claimExpiresAt, &finishedAt)
	if err != nil {
		return protocol.Job{}, fmt.Errorf("load job %q: %w", jobID, err)
	}

	job.Status = protocol.JobStatus(status)
	parsed, err := parseDBTime(createdAt)
	if err != nil {
		return protocol.Job{}, fmt.Errorf("parse job %q created_at: %w", jobID, err)
	}
	job.CreatedAt = parsed

	if claimedAt.Valid && claimedAt.String != "" {
		parsed, err := parseDBTime(claimedAt.String)
		if err != nil {
			return protocol.Job{}, fmt.Errorf("parse job %q claimed_at: %w", jobID, err)
		}
		job.ClaimedAt = parsed
	}
	if claimExpiresAt.Valid && claimExpiresAt.String != "" {
		parsed, err := parseDBTime(claimExpiresAt.String)
		if err != nil {
			return protocol.Job{}, fmt.Errorf("parse job %q claim_expires_at: %w", jobID, err)
		}
		job.ClaimExpiresAt = parsed
	}
	if finishedAt.Valid && finishedAt.String != "" {
		parsed, err := parseDBTime(finishedAt.String)
		if err != nil {
			return protocol.Job{}, fmt.Errorf("parse job %q finished_at: %w", jobID, err)
		}
		job.FinishedAt = parsed
	}

	return job, nil
}

func (s *SQLiteNodeStore) expireClaimedJobsTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id, claim_expires_at
FROM jobs
WHERE status = ? AND claim_expires_at IS NOT NULL AND claim_expires_at != ''
`, string(protocol.JobStatusClaimed))
	if err != nil {
		return fmt.Errorf("query expired claimed jobs: %w", err)
	}

	var expiredJobIDs []string
	for rows.Next() {
		var jobID, claimExpiresAt string
		if err := rows.Scan(&jobID, &claimExpiresAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan claimed job lease: %w", err)
		}
		parsed, err := parseDBTime(claimExpiresAt)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("parse job %q claim_expires_at: %w", jobID, err)
		}
		if !parsed.After(now) {
			expiredJobIDs = append(expiredJobIDs, jobID)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate claimed job leases: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close claimed job lease rows: %w", err)
	}

	for _, jobID := range expiredJobIDs {
		_, err := tx.ExecContext(ctx, `
UPDATE jobs
SET status = ?, error = ?, finished_at = ?, claim_expires_at = NULL
WHERE id = ? AND status = ?
`, string(protocol.JobStatusFailed), jobClaimTimeoutError, formatDBTime(now), jobID, string(protocol.JobStatusClaimed))
		if err != nil {
			return fmt.Errorf("mark job %q timed out: %w", jobID, err)
		}
	}

	return nil
}

func configureSQLite(ctx context.Context, db *sql.DB) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&journalMode); err != nil {
		return fmt.Errorf("configure sqlite wal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("configure sqlite wal mode: got %q", journalMode)
	}
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

func parseOptionalDBTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseDBTime(value.String)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
