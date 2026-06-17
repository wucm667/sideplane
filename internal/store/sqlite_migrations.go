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
