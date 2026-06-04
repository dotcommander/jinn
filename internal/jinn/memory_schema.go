package jinn

import (
	"context"
	"database/sql"
	"fmt"
)

// ensureSchema creates the unified continuity + memory schema. Idempotent via
// CREATE TABLE/INDEX IF NOT EXISTS. DDL is the authoritative greenfield schema;
// no migration framework (Beta, no back-compat).
func ensureSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memory (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			scope           TEXT NOT NULL,
			scope_id        TEXT NOT NULL DEFAULT '',
			key             TEXT NOT NULL,
			value           TEXT NOT NULL,
			value_type      TEXT NOT NULL DEFAULT 'string',
			kind            TEXT NOT NULL DEFAULT 'fact',
			pinned          INTEGER NOT NULL DEFAULT 0,
			expires_at      TIMESTAMP,
			created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(scope, scope_id, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_scope   ON memory(scope, scope_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_expires ON memory(expires_at)`,

		`CREATE TABLE IF NOT EXISTS idempotency (
			agent_name  TEXT NOT NULL,
			request_id  TEXT NOT NULL,
			command     TEXT NOT NULL,
			result_json TEXT NOT NULL,
			created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (agent_name, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_agent ON idempotency(agent_name)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("memory: schema: %w", err)
		}
	}
	return nil
}
