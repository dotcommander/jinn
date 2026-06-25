package jinn

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// memoryTableDDL returns the canonical greenfield memory-table DDL with the
// given table-name clause (e.g. "IF NOT EXISTS memory" or "memory"). It is the
// single source of truth for the schema, used by both initial creation and the
// legacy rebuild.
func memoryTableDDL(nameClause string) string {
	return `CREATE TABLE ` + nameClause + ` (
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
	)`
}

// ensureSchema creates the memory + idempotency schema and migrates older DBs.
// Tables/indexes are idempotent via CREATE ... IF NOT EXISTS; migrateMemoryColumns
// reconciles memory tables created by an older jinn before indexes are built.
func ensureSchema(ctx context.Context, db *sql.DB) error {
	tables := []string{
		memoryTableDDL("IF NOT EXISTS memory"),
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

	// Reconcile a memory table created by an older jinn before building indexes
	// that reference newer columns (e.g. idx_memory_expires on expires_at).
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

// memoryColumnSet returns the set of column names currently on the memory table.
func memoryColumnSet(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(memory)`)
	if err != nil {
		return nil, fmt.Errorf("memory: schema: table_info: %w", err)
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
			return nil, fmt.Errorf("memory: schema: table_info scan: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: schema: table_info rows: %w", err)
	}
	return existing, nil
}

// migrateMemoryColumns reconciles a memory table created by an older jinn.
// Modern tables (with id/created_at/updated_at) get missing later columns added
// in place. Pre-redesign tables, which cannot be patched with ADD COLUMN (an
// AUTOINCREMENT PK and CURRENT_TIMESTAMP defaults are not ALTER-able), are
// rebuilt with their rows preserved.
func migrateMemoryColumns(ctx context.Context, db *sql.DB) error {
	existing, err := memoryColumnSet(ctx, db)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}

	if !existing["id"] || !existing["created_at"] || !existing["updated_at"] {
		return rebuildMemoryTable(ctx, db, existing)
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

// rebuildMemoryTable rebuilds a pre-redesign memory table into the canonical
// schema, copying rows in a single transaction. Each canonical column is sourced
// from the matching legacy column if present, else a default (with legacy
// created/updated aliases mapped onto created_at/updated_at).
func rebuildMemoryTable(ctx context.Context, db *sql.DB, existing map[string]bool) error {
	pick := func(col, fallback string) string {
		if existing[col] {
			return col
		}
		return fallback
	}
	createdExpr := "CURRENT_TIMESTAMP"
	switch {
	case existing["created_at"]:
		createdExpr = "created_at"
	case existing["created"]:
		createdExpr = "created"
	}
	updatedExpr := "CURRENT_TIMESTAMP"
	switch {
	case existing["updated_at"]:
		updatedExpr = "updated_at"
	case existing["updated"]:
		updatedExpr = "updated"
	}

	selectExprs := strings.Join([]string{
		pick("scope", "''"),
		pick("scope_id", "''"),
		pick("key", "''"),
		pick("value", "''"),
		pick("value_type", "'string'"),
		pick("kind", "'fact'"),
		pick("pinned", "0"),
		pick("expires_at", "NULL"),
		createdExpr,
		updatedExpr,
	}, ", ")

	stmts := []string{
		`ALTER TABLE memory RENAME TO memory_legacy`,
		memoryTableDDL("memory"),
		`INSERT INTO memory (scope, scope_id, key, value, value_type, kind, pinned, expires_at, created_at, updated_at) SELECT ` + selectExprs + ` FROM memory_legacy`,
		`DROP TABLE memory_legacy`,
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: schema: rebuild: begin: %w", err)
	}
	defer tx.Rollback()
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("memory: schema: rebuild: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: schema: rebuild: commit: %w", err)
	}
	return nil
}
