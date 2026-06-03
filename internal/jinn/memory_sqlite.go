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

	if err := e.migrateLegacyMemory(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	e.memDB = db
	return db, nil
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
