package jinn

import (
	"context"
	"database/sql"
	"fmt"
)

// ensureSchema creates the memory + idempotency schema and applies additive
// column migrations. Tables/indexes are idempotent via CREATE ... IF NOT EXISTS.
// The DDL is the authoritative greenfield schema; for DBs created by an older
// jinn we additively ALTER any missing memory columns (no migration framework,
// Beta, no back-compat for anything beyond additive columns).
func ensureSchema(ctx context.Context, db *sql.DB) error {
	tables := []string{
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
		`CREATE TABLE IF NOT EXISTS idempotency (
			agent_name  TEXT NOT NULL,
			request_id  TEXT NOT NULL,
			command     TEXT NOT NULL,
			result_json TEXT NOT NULL,
			created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (agent_name, request_id)
		)`,
	}
	for _, s := range tables {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("memory: schema: %w", err)
		}
	}

	// Additive migration: a memory table created by an older jinn may predate
	// columns added later. ALTER each missing column before building indexes
	// that reference them (e.g. idx_memory_expires on expires_at).
	if err := migrateMemoryColumns(ctx, db); err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memory_scope   ON memory(scope, scope_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_expires ON memory(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_agent ON idempotency(agent_name)`,
	}
	for _, s := range indexes {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("memory: schema: %w", err)
		}
	}
	return nil
}

// migrateMemoryColumns additively adds memory columns absent from a table
// created by an older jinn. Each ADD COLUMN is safe (nullable or defaulted).
func migrateMemoryColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(memory)`)
	if err != nil {
		return fmt.Errorf("memory: schema: table_info: %w", err)
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("memory: schema: table_info scan: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("memory: schema: table_info rows: %w", err)
	}

	cols := []struct {
		name string
		ddl  string
	}{
		{"scope_id", `ALTER TABLE memory ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
		{"value_type", `ALTER TABLE memory ADD COLUMN value_type TEXT NOT NULL DEFAULT 'string'`},
		{"kind", `ALTER TABLE memory ADD COLUMN kind TEXT NOT NULL DEFAULT 'fact'`},
		{"pinned", `ALTER TABLE memory ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`},
		{"expires_at", `ALTER TABLE memory ADD COLUMN expires_at TIMESTAMP`},
	}
	for _, c := range cols {
		if existing[c.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("memory: schema: add column %s: %w", c.name, err)
		}
	}
	return nil
}
