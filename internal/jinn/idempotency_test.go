package jinn

import (
	"context"
	"testing"
)

// newIdempotencyEngine returns an Engine wired to a fresh isolated DB.
// Non-parallel because t.Setenv is used for JINN_CONFIG_DIR isolation.
func newIdempotencyEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

// TestIdempotency_EmptyRequestIDNoRow verifies that a memory.save call without
// a request_id succeeds normally, writes NO idempotency row, and still persists
// the memory row.
func TestIdempotency_EmptyRequestIDNoRow(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	_, err := e.memoryTool(ctx, args(
		"action", "save",
		"key", "no-idem",
		"value", "v",
		"scope", "global",
		"agent", "test-agent",
		// no request_id
	))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM idempotency`).Scan(&count); err != nil {
		t.Fatalf("count idempotency: %v", err)
	}
	if count != 0 {
		t.Errorf("want 0 idempotency rows for empty request_id, got %d", count)
	}

	// Memory row was still written.
	var memCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory WHERE key='no-idem'`).Scan(&memCount); err != nil {
		t.Fatalf("count memory: %v", err)
	}
	if memCount != 1 {
		t.Errorf("want 1 memory row, got %d", memCount)
	}
}
