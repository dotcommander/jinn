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

// memDBConn lazily opens the singleton SQLite handle, creating the unified schema
// on first open. Guarded by e.memMu.
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
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: open: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := ensureSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	e.memDB = db
	return db, nil
}

// memoryUpsertTx upserts a value for (scope, scope_id, key) inside a transaction.
// pin-stickiness: CASE WHEN excluded.pinned=1 THEN 1 ELSE pinned END ensures
// a plain save (pin=false) cannot clear an existing pin.
func memoryUpsertTx(ctx context.Context, tx *sql.Tx, scope, scopeID, key, value, kind string, pin bool, expiresAt *time.Time) error {
	valueType := inferValueType(value)
	now := time.Now().UTC().Format(time.RFC3339)

	var expiresStr interface{}
	if expiresAt != nil {
		// SQLite datetime comparison requires "YYYY-MM-DD HH:MM:SS" format.
		expiresStr = expiresAt.UTC().Format("2006-01-02 15:04:05")
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory(scope, scope_id, key, value, value_type, kind, pinned, expires_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, scope_id, key) DO UPDATE SET
			value      = excluded.value,
			value_type = excluded.value_type,
			kind       = excluded.kind,
			pinned     = CASE WHEN excluded.pinned = 1 THEN 1 ELSE pinned END,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, scope, scopeID, key, value, valueType, kind, boolToInt(pin), expiresStr, now)
	if err != nil {
		return fmt.Errorf("memory: upsert: %w", err)
	}
	return nil
}

// memorySaveScoped upserts a value for (scope, scope_id, key) against the DB
// directly. Wraps memoryUpsertTx in a one-statement transaction.
func (e *Engine) memorySaveScoped(ctx context.Context, scope, scopeID, key, value, kind string, pin bool, expiresAt *time.Time) error {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return err
	}
	return transact(ctx, db, func(tx *sql.Tx) error {
		return memoryUpsertTx(ctx, tx, scope, scopeID, key, value, kind, pin, expiresAt)
	})
}

// memoryRecallScoped fetches the value for (scope, scope_id, key). The bool is
// false when no row exists; err is non-nil only on a real query failure.
func (e *Engine) memoryRecallScoped(ctx context.Context, scope, scopeID, key string) (string, bool, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", false, err
	}
	var v string
	err = db.QueryRowContext(ctx,
		"SELECT value FROM memory WHERE scope=? AND scope_id=? AND key=?",
		scope, scopeID, key,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("memory: recall: %w", err)
	}
	return v, true, nil
}

// memoryListScoped returns the keys in a scope+scope_id, sorted.
// Slice is non-nil so it serializes as [] rather than null.
func (e *Engine) memoryListScoped(ctx context.Context, scope, scopeID string) ([]string, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		"SELECT key FROM memory WHERE scope=? AND scope_id=? ORDER BY key",
		scope, scopeID,
	)
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
	return keys, rows.Err()
}

// memoryEntry holds a full row returned by memoryListScopedWithValues.
type memoryEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	ValueType string `json:"value_type"`
	Kind      string `json:"kind"`
	Pinned    bool   `json:"pinned"`
	UpdatedAt string `json:"updated_at"`
}

// memoryListScopedWithValues returns all entries in a scope+scope_id with values.
func (e *Engine) memoryListScopedWithValues(ctx context.Context, scope, scopeID string) ([]memoryEntry, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		"SELECT key, value, value_type, kind, pinned, updated_at FROM memory WHERE scope=? AND scope_id=? ORDER BY key",
		scope, scopeID,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()

	entries := []memoryEntry{}
	for rows.Next() {
		var e memoryEntry
		var pinnedInt int
		if err := rows.Scan(&e.Key, &e.Value, &e.ValueType, &e.Kind, &pinnedInt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("memory: list scan: %w", err)
		}
		e.Pinned = pinnedInt == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// memoryForgetScoped deletes (scope, scope_id, key). Zero rows affected is not an error.
func (e *Engine) memoryForgetScoped(ctx context.Context, scope, scopeID, key string) error {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		"DELETE FROM memory WHERE scope=? AND scope_id=? AND key=?",
		scope, scopeID, key,
	)
	if err != nil {
		return fmt.Errorf("memory: forget: %w", err)
	}
	return nil
}

// memoryGCTx deletes expired, non-pinned memory rows within an existing transaction.
// When scope is non-empty only that scope is swept; otherwise all scopes are swept.
func (e *Engine) memoryGCTx(ctx context.Context, tx *sql.Tx, scope string) (int, error) {
	var (
		res sql.Result
		err error
	)
	if scope == "" {
		res, err = tx.ExecContext(ctx,
			`DELETE FROM memory WHERE pinned=0 AND expires_at IS NOT NULL AND expires_at <= datetime('now')`)
	} else {
		res, err = tx.ExecContext(ctx,
			`DELETE FROM memory WHERE pinned=0 AND expires_at IS NOT NULL AND expires_at <= datetime('now') AND scope=?`,
			scope)
	}
	if err != nil {
		return 0, fmt.Errorf("memory: gc: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("memory: gc rows: %w", err)
	}
	return int(n), nil
}

// defaultIdempotencyRetention bounds the idempotency dedup table. request_id
// dedup only guards short-lived retries, so rows older than this are safe to
// prune. Too short risks re-running a legitimately replayed request; too long
// wastes space — 7 days is a generous middle ground.
const defaultIdempotencyRetention = 7 * 24 * time.Hour

// idempotencyGCTx deletes idempotency rows older than retention within an
// existing transaction. created_at is stored via SQLite CURRENT_TIMESTAMP
// (UTC "YYYY-MM-DD HH:MM:SS"), so the cutoff is computed with datetime('now',
// '-N seconds') to compare in the identical stored format. Pruning is global
// (idempotency rows are not project-scoped). Only rows strictly older than the
// cutoff are removed, leaving recent in-flight rows untouched.
func idempotencyGCTx(ctx context.Context, tx *sql.Tx, retention time.Duration) (int, error) {
	cutoff := fmt.Sprintf("-%d seconds", int64(retention.Seconds()))
	res, err := tx.ExecContext(ctx,
		`DELETE FROM idempotency WHERE created_at < datetime('now', ?)`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("idempotency: gc: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("idempotency: gc rows: %w", err)
	}
	return int(n), nil
}

// memoryGC deletes expired, non-pinned memory rows. When scope is non-empty
// only that scope is swept; otherwise all scopes are swept.
func (e *Engine) memoryGC(ctx context.Context, scope string) (int, error) {
	db, err := e.memDBConn(ctx)
	if err != nil {
		return 0, err
	}
	var n int
	txErr := transact(ctx, db, func(tx *sql.Tx) error {
		var gcErr error
		n, gcErr = e.memoryGCTx(ctx, tx, scope)
		return gcErr
	})
	return n, txErr
}
