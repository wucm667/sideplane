package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type sqliteMigration struct {
	version    int
	name       string
	statements []string
}

var sqliteMigrations = []sqliteMigration{
	{
		version: 1,
		name:    "create node heartbeat tables",
		statements: []string{
			`
CREATE TABLE IF NOT EXISTS nodes (
	node_id TEXT PRIMARY KEY,
	hostname TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	sidecar_version TEXT NOT NULL DEFAULT '',
	last_heartbeat_at TEXT NOT NULL,
	config_hash TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
)`,
			`
CREATE TABLE IF NOT EXISTS node_runtimes (
	node_id TEXT NOT NULL,
	runtime_index INTEGER NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	config_hash TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (node_id, runtime_index),
	FOREIGN KEY (node_id) REFERENCES nodes(node_id) ON DELETE CASCADE
)`,
			`
CREATE TABLE IF NOT EXISTS heartbeats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id TEXT NOT NULL,
	observed_at TEXT NOT NULL,
	sent_at TEXT,
	payload_json TEXT NOT NULL,
	summary_json TEXT NOT NULL,
	FOREIGN KEY (node_id) REFERENCES nodes(node_id) ON DELETE CASCADE
)`,
			`
CREATE INDEX IF NOT EXISTS idx_heartbeats_node_observed
ON heartbeats(node_id, observed_at DESC)`,
		},
	},
	{
		version: 2,
		name:    "create enrollment credential tables",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS enrollment_tokens (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at TEXT NOT NULL,
		used_at TEXT,
		created_at TEXT NOT NULL
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_expires_at
	ON enrollment_tokens(expires_at)`,
			`
	CREATE TABLE IF NOT EXISTS node_credentials (
		node_id TEXT PRIMARY KEY,
		credential_hash TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY (node_id) REFERENCES nodes(node_id) ON DELETE CASCADE
	)`,
		},
	},
	{
		version: 3,
		name:    "create jobs table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		node_id TEXT NOT NULL,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		payload_json TEXT NOT NULL DEFAULT '',
		result_json TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		claimed_at TEXT,
		finished_at TEXT,
		FOREIGN KEY (node_id) REFERENCES nodes(node_id) ON DELETE CASCADE
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_jobs_node_status
	ON jobs(node_id, status)`,
		},
	},
	{
		version: 4,
		name:    "add job claim lease deadline",
		statements: []string{
			`ALTER TABLE jobs ADD COLUMN claim_expires_at TEXT`,
		},
	},
	{
		version: 5,
		name:    "create audit events table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS audit_events (
		id TEXT PRIMARY KEY,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		target_node TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_audit_events_created_at
	ON audit_events(created_at DESC)`,
		},
	},
	{
		version: 6,
		name:    "create desired config table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS desired_config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		config_json TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
		},
	},
	{
		version: 7,
		name:    "add runtime warnings",
		statements: []string{
			`ALTER TABLE node_runtimes ADD COLUMN warnings_json TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 8,
		name:    "create node labels table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS node_labels (
		node_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (node_id, key),
		FOREIGN KEY (node_id) REFERENCES nodes(node_id) ON DELETE CASCADE
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_node_labels_key_value
	ON node_labels(key, value)`,
		},
	},
	{
		version: 9,
		name:    "create rollouts table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS rollouts (
		id TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		rollout_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		finished_at TEXT
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_rollouts_created_at
	ON rollouts(created_at DESC, id DESC)`,
			`
	CREATE INDEX IF NOT EXISTS idx_rollouts_terminal_finished_at
	ON rollouts(state, finished_at)`,
		},
	},
	{
		version: 10,
		name:    "create operator tokens table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS operator_tokens (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		last_used_at TEXT,
		revoked_at TEXT
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_operator_tokens_created_at
	ON operator_tokens(created_at DESC, id DESC)`,
			`
	CREATE INDEX IF NOT EXISTS idx_operator_tokens_revoked_at
	ON operator_tokens(revoked_at)`,
		},
	},
	{
		version: 11,
		name:    "create desired config history table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS desired_config_history (
		id TEXT PRIMARY KEY,
		config_json TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		actor TEXT NOT NULL
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_desired_config_history_updated_at
	ON desired_config_history(updated_at DESC, id DESC)`,
		},
	},
	{
		version: 12,
		name:    "add operator token scope",
		statements: []string{
			`ALTER TABLE operator_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'admin'`,
		},
	},
	{
		version: 13,
		name:    "create alert webhooks table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS alert_webhooks (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		events_json TEXT NOT NULL,
		secret TEXT NOT NULL DEFAULT '',
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_alert_webhooks_created_at
	ON alert_webhooks(created_at DESC, id DESC)`,
		},
	},
	{
		version: 14,
		name:    "create server settings table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS server_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		expected_sidecar_version TEXT NOT NULL DEFAULT ''
	)`,
		},
	},
	{
		version: 15,
		name:    "create rollout templates table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS rollout_templates (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
			`
	CREATE INDEX IF NOT EXISTS idx_rollout_templates_created_at
	ON rollout_templates(created_at DESC, id DESC)`,
		},
	},
	{
		version: 16,
		name:    "add node maintenance mode",
		statements: []string{
			`ALTER TABLE nodes ADD COLUMN maintenance INTEGER NOT NULL DEFAULT 0`,
		},
	},
	{
		version: 17,
		name:    "add audit actor name",
		statements: []string{
			`ALTER TABLE audit_events ADD COLUMN actor_name TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 18,
		name:    "add alert webhook kind",
		statements: []string{
			`ALTER TABLE alert_webhooks ADD COLUMN kind TEXT NOT NULL DEFAULT 'generic'`,
		},
	},
	{
		version: 19,
		name:    "add expected runtime versions",
		statements: []string{
			`ALTER TABLE server_settings ADD COLUMN expected_runtime_versions_json TEXT NOT NULL DEFAULT '{}'`,
		},
	},
	{
		version: 20,
		name:    "add runtime deployment mode",
		statements: []string{
			`ALTER TABLE node_runtimes ADD COLUMN deployment_mode TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 21,
		name:    "create provider secrets table",
		statements: []string{
			`
	CREATE TABLE IF NOT EXISTS provider_secrets (
		env_name TEXT PRIMARY KEY,
		ciphertext BLOB NOT NULL,
		updated_at TEXT NOT NULL
	)`,
		},
	},
}

// LatestSQLiteSchemaVersion returns the newest migration version compiled into
// this binary.
func LatestSQLiteSchemaVersion() int {
	if len(sqliteMigrations) == 0 {
		return 0
	}
	return sqliteMigrations[len(sqliteMigrations)-1].version
}

func runSQLiteMigrations(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite migrations: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	applied, err := appliedSQLiteMigrations(ctx, tx)
	if err != nil {
		return err
	}

	for _, migration := range sqliteMigrations {
		if applied[migration.version] {
			continue
		}
		for _, statement := range migration.statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply sqlite migration %d %q: %w", migration.version, migration.name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, applied_at)
VALUES (?, ?, ?)
`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record sqlite migration %d: %w", migration.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite migrations: %w", err)
	}

	return nil
}

func appliedSQLiteMigrations(ctx context.Context, tx *sql.Tx) (map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migrations: %w", err)
	}

	return applied, nil
}
