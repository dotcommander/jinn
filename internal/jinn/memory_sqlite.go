package jinn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// memoryDBPath resolves the SQLite memory database path. Checks JINN_CONFIG_DIR
// first for test isolation; falls back to os.UserConfigDir().
func memoryDBPath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("memory: resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn", "memory.db"), nil
}

// memDBConn lazily opens the singleton SQLite handle, creating the schema and
// running the legacy JSON migration on first open. Guarded by e.memMu.
func (e *Engine) memDBConn(ctx context.Context) (*sql.DB, error) {
	e.memMu.Lock()
	defer e.memMu.Unlock()
	if e.memDB != nil {
		return e.memDB, nil
	}

	path, err := memoryDBPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("memory: mkdir: %w", err)
	}

	// modernc.org/sqlite recognizes _pragma=name(value) and _txlock=immediate
	// (verified against v1.51.0). SetMaxOpenConns(1) serializes writes; the
	// _txlock+busy_timeout pragmas harden the single connection further.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	// Driver name is exactly "sqlite" — that is what modernc registers (not the
	// CGO mattn driver's alternate spelling).
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: open: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS memory (scope TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, updated TEXT NOT NULL, PRIMARY KEY (scope, key))"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: schema table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory(scope)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: schema index: %w", err)
	}

	if err := e.continuityTables(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	if err := e.migrateLegacyMemory(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	e.memDB = db
	return db, nil
}

// continuityTables creates the flattened vybe continuity schema (events, tasks,
// agent_state, artifacts, projects, idempotency) on the shared memory.db handle.
// Idempotent via CREATE TABLE/INDEX IF NOT EXISTS; no migration framework (Beta,
// no back-compat). Schema is the FINAL collapsed state of vybe's 30+ migrations.
//
// COLLISION NOTE: vybe also defines a `memory` table (id-PK, UNIQUE(scope,scope_id,key),
// value_type NOT NULL, pinned/kind/source_* columns). jinn already owns a 4-column
// `memory(scope,key,value,updated)` table with PK(scope,key) that the memory tool
// depends on via ON CONFLICT(scope,key). The two are structurally incompatible, and
// Phase 1 ports no tool that needs vybe's memory columns, so the vybe `memory`
// superset is DELIBERATELY NOT created here — widening is deferred to the phase that
// ports the memory tool wrappers. Do not add a `CREATE TABLE IF NOT EXISTS memory(...superset...)`
// here: it would silently no-op against jinn's existing table and mislead future readers.
func (e *Engine) continuityTables(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			task_id TEXT,
			message TEXT NOT NULL,
			metadata TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			project_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_agent_name ON events(agent_name)`,
		`CREATE INDEX IF NOT EXISTS idx_events_task_id ON events(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_cursor ON events(project_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_kind_id ON events(kind, id)`,

		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			project_id TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			blocked_reason TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_focus_selection ON tasks(status, project_id, priority DESC, created_at ASC)`,

		`CREATE TABLE IF NOT EXISTS agent_state (
			agent_name TEXT PRIMARY KEY,
			last_seen_event_id INTEGER NOT NULL DEFAULT 0,
			focus_task_id TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			last_active_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			focus_project_id TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			event_id INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			content_type TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			project_id TEXT,
			FOREIGN KEY (task_id) REFERENCES tasks(id),
			FOREIGN KEY (event_id) REFERENCES events(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_project_id ON artifacts(project_id)`,

		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			metadata TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS idempotency (
			agent_name TEXT NOT NULL,
			request_id TEXT NOT NULL,
			command TEXT NOT NULL,
			result_json TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (agent_name, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_agent ON idempotency(agent_name)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("memory: continuity schema: %w", err)
		}
	}
	return nil
}

// memorySaveScoped upserts a value for (scope, key).
func (e *Engine) memorySaveScoped(ctx context.Context, scope, key, value string) error {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO memory(scope,key,value,updated) VALUES(?,?,?,?) ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value, updated=excluded.updated", scope, key, value, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("memory: save: %w", err)
	}
	return nil
}

// memoryRecallScoped fetches the value for (scope, key). The bool is false when
// no row exists; err is non-nil only on a real query failure.
func (e *Engine) memoryRecallScoped(ctx context.Context, scope, key string) (string, bool, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", false, err
	}
	var v string
	err = db.QueryRowContext(ctx, "SELECT value FROM memory WHERE scope=? AND key=?", scope, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("memory: recall: %w", err)
	}
	return v, true, nil
}

// memoryListScoped returns the keys in a scope, sorted. The slice is non-nil so
// it serializes as [] rather than null.
func (e *Engine) memoryListScoped(ctx context.Context, scope string) ([]string, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT key FROM memory WHERE scope=? ORDER BY key", scope)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("memory: list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: list rows: %w", err)
	}
	return keys, nil
}

// memoryEntry holds a key/value/updated triple returned by memoryListScopedWithValues.
type memoryEntry struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Updated string `json:"updated"`
}

// memoryListScopedWithValues returns all entries in a scope with their values,
// sorted by key. Mirrors the ordering of memoryListScoped.
func (e *Engine) memoryListScopedWithValues(ctx context.Context, scope string) ([]memoryEntry, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT key, value, updated FROM memory WHERE scope=? ORDER BY key", scope)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()

	entries := []memoryEntry{}
	for rows.Next() {
		var entry memoryEntry
		if err := rows.Scan(&entry.Key, &entry.Value, &entry.Updated); err != nil {
			return nil, fmt.Errorf("memory: list scan: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: list rows: %w", err)
	}
	return entries, nil
}

// memoryForgetScoped deletes (scope, key). Zero rows affected is not an error.
func (e *Engine) memoryForgetScoped(ctx context.Context, scope, key string) error {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM memory WHERE scope=? AND key=?", scope, key); err != nil {
		return fmt.Errorf("memory: forget: %w", err)
	}
	return nil
}
