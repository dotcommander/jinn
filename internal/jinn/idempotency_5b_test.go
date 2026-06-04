package jinn

import (
	"context"
	"encoding/json"
	"testing"
)

// Non-parallel: t.Setenv is process-wide. Uses newIdempotencyEngine from idempotency_test.go.

// dbCount is a helper that runs a COUNT(*) query and returns the result.
func dbCount(t *testing.T, e *Engine, ctx context.Context, query string, args ...any) int {
	t.Helper()
	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("dbCount(%q): %v", query, err)
	}
	return n
}

// TestIdempotency5b_MemorySaveReplayOnce verifies that memory.save with the
// same (agent, request_id) twice produces one memory row and byte-identical results;
// a different request_id is an independent (idempotent-to-itself) write.
func TestIdempotency5b_MemorySaveReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	a := args(
		"action", "save", "key", "replay-key", "value", "v1",
		"scope", "global", "agent", "agent",
		"request_id", "mem-req-5b-001",
	)

	first, err := e.memoryTool(ctx, a)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := e.memoryTool(ctx, a)
	if err != nil {
		t.Fatalf("second save (replay): %v", err)
	}
	if first != second {
		t.Errorf("memory.save replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM memory WHERE key='replay-key'`); n != 1 {
		t.Errorf("want 1 memory row, got %d", n)
	}

	// Different request_id → independent write (upsert; still 1 row).
	if _, err := e.memoryTool(ctx, args(
		"action", "save", "key", "replay-key", "value", "v2",
		"scope", "global", "agent", "agent",
		"request_id", "mem-req-5b-002",
	)); err != nil {
		t.Fatalf("different request_id save: %v", err)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM memory WHERE key='replay-key'`); n != 1 {
		t.Errorf("want 1 memory row after different request_id, got %d", n)
	}
}

// TestIdempotency5b_GCPrunesStaleIdempotency verifies that memory.gc prunes
// idempotency rows older than the retention window while keeping recent rows,
// and reports the pruned count.
func TestIdempotency5b_GCPrunesStaleIdempotency(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}

	// Old row: created_at well beyond the default 7d retention.
	if _, insErr := db.ExecContext(ctx,
		`INSERT INTO idempotency (agent_name, request_id, command, result_json, created_at)
		 VALUES ('agent', 'old-idem-5b', 'memory.save', '"ok"', datetime('now', '-30 days'))`,
	); insErr != nil {
		t.Fatalf("insert old idempotency row: %v", insErr)
	}
	// Recent row: created now, must survive.
	if _, insErr := db.ExecContext(ctx,
		`INSERT INTO idempotency (agent_name, request_id, command, result_json, created_at)
		 VALUES ('agent', 'recent-idem-5b', 'memory.save', '"ok"', datetime('now'))`,
	); insErr != nil {
		t.Fatalf("insert recent idempotency row: %v", insErr)
	}

	gcRaw, err := e.memoryTool(ctx, args("action", "gc", "agent", "agent"))
	if err != nil {
		t.Fatalf("gc: %v", err)
	}

	var gc struct {
		Deleted            int `json:"deleted"`
		IdempotencyDeleted int `json:"idempotency_deleted"`
	}
	if err := json.Unmarshal([]byte(gcRaw), &gc); err != nil {
		t.Fatalf("decode gc result %q: %v", gcRaw, err)
	}
	if gc.IdempotencyDeleted != 1 {
		t.Errorf("want idempotency_deleted=1, got %d (raw=%s)", gc.IdempotencyDeleted, gcRaw)
	}

	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM idempotency WHERE request_id='old-idem-5b'`); n != 0 {
		t.Errorf("want old idempotency row deleted, got %d", n)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM idempotency WHERE request_id='recent-idem-5b'`); n != 1 {
		t.Errorf("want recent idempotency row to survive, got %d", n)
	}
}

// TestIdempotency5b_MemoryGCReplayOnce verifies that memory.gc with the same
// (agent, request_id) twice returns an identical deleted count and is idempotent.
func TestIdempotency5b_MemoryGCReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	if _, insErr := db.ExecContext(ctx,
		`INSERT INTO memory (scope, scope_id, key, value, value_type, kind, pinned, expires_at)
		 VALUES ('global', '', 'expired-gc-5b', 'bye', 'string', 'fact', 0, datetime('now', '-1 hour'))`,
	); insErr != nil {
		t.Fatalf("insert expired row: %v", insErr)
	}

	gcArgs := args(
		"action", "gc", "scope", "global",
		"agent", "agent", "request_id", "gc-req-5b-001",
	)

	first, err := e.memoryTool(ctx, gcArgs)
	if err != nil {
		t.Fatalf("first gc: %v", err)
	}
	second, err := e.memoryTool(ctx, gcArgs)
	if err != nil {
		t.Fatalf("second gc (replay): %v", err)
	}
	if first != second {
		t.Errorf("memory.gc replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}
	if !containsStr(first, `"deleted":1`) {
		t.Errorf("gc result: want deleted:1, got %s", first)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM memory WHERE key='expired-gc-5b'`); n != 0 {
		t.Errorf("want 0 expired rows after gc, got %d", n)
	}
}
