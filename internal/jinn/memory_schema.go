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
		`CREATE TABLE IF NOT EXISTS events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			kind       TEXT    NOT NULL,
			agent_name TEXT    NOT NULL,
			project_id TEXT    NOT NULL DEFAULT '',
			task_id    TEXT,
			message    TEXT    NOT NULL,
			metadata   TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_agent       ON events(agent_name)`,
		`CREATE INDEX IF NOT EXISTS idx_events_task        ON events(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_cur ON events(project_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_kind_id     ON events(kind, id)`,

		`CREATE TABLE IF NOT EXISTS tasks (
			id             TEXT PRIMARY KEY,
			title          TEXT NOT NULL,
			description    TEXT,
			status         TEXT NOT NULL,
			priority       INTEGER NOT NULL DEFAULT 0,
			blocked_reason TEXT,
			project_id     TEXT NOT NULL DEFAULT '',
			version        INTEGER NOT NULL DEFAULT 1,
			created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status  ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_focus   ON tasks(status, project_id, priority DESC, created_at ASC)`,

		`CREATE TABLE IF NOT EXISTS agent_state (
			agent_name         TEXT PRIMARY KEY,
			last_seen_event_id INTEGER NOT NULL DEFAULT 0,
			focus_task_id      TEXT,
			focus_project_id   TEXT,
			version            INTEGER NOT NULL DEFAULT 1,
			last_active_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

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
			half_life_days  REAL,
			source_event_id INTEGER,
			source_task_id  TEXT,
			created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(scope, scope_id, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_scope   ON memory(scope, scope_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_expires ON memory(expires_at)`,

		`CREATE TABLE IF NOT EXISTS artifacts (
			id           TEXT PRIMARY KEY,
			task_id      TEXT NOT NULL,
			event_id     INTEGER NOT NULL,
			file_path    TEXT NOT NULL,
			content_type TEXT,
			project_id   TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_task    ON artifacts(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_project ON artifacts(project_id)`,

		`CREATE TABLE IF NOT EXISTS projects (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			metadata   TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

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
