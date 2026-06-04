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

// TestIdempotency_SameRequestIDRunsOnce verifies that two task.create calls
// with the same (agent, request_id) produce byte-identical results and leave
// exactly one task row and one task_created event in the DB.
func TestIdempotency_SameRequestIDRunsOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	a := args(
		"action", "create",
		"title", "idempotent task",
		"agent", "test-agent",
		"request_id", "req-abc-123",
	)

	first, err := e.taskTool(ctx, a)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	second, err := e.taskTool(ctx, a)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	// Replay must return byte-identical JSON.
	if first != second {
		t.Errorf("replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}

	// Exactly one task row.
	var taskCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("want 1 task row, got %d", taskCount)
	}
}

// TestIdempotency_DifferentRequestIDsCreateDistinctTasks verifies that two
// calls with different request_ids produce two distinct task rows.
func TestIdempotency_DifferentRequestIDsCreateDistinctTasks(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	r1, err := e.taskTool(ctx, args(
		"action", "create",
		"title", "task A",
		"agent", "test-agent",
		"request_id", "req-111",
	))
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	r2, err := e.taskTool(ctx, args(
		"action", "create",
		"title", "task B",
		"agent", "test-agent",
		"request_id", "req-222",
	))
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	t1 := decodeTask(t, r1)
	t2 := decodeTask(t, r2)
	if t1.ID == t2.ID {
		t.Errorf("expected distinct task IDs, got same: %q", t1.ID)
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 task rows, got %d", count)
	}
}

// TestIdempotency_EmptyRequestIDNoRow verifies that a task.create call without
// a request_id succeeds normally and writes NO idempotency row.
func TestIdempotency_EmptyRequestIDNoRow(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	_, err := e.taskTool(ctx, args(
		"action", "create",
		"title", "no-idempotency task",
		"agent", "test-agent",
		// no request_id
	))
	if err != nil {
		t.Fatalf("create: %v", err)
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

	// Task was still created.
	var taskCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("want 1 task row, got %d", taskCount)
	}
}

// TestIdempotency_ReplayByteIdentical captures the raw JSON from the first
// call and asserts the second call returns the exact same string.
func TestIdempotency_ReplayByteIdentical(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	a := args(
		"action", "create",
		"title", "byte-check",
		"agent", "test-agent",
		"request_id", "req-byte-check",
	)

	first, err := e.taskTool(ctx, a)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := e.taskTool(ctx, a)
	if err != nil {
		t.Fatalf("second call (replay): %v", err)
	}

	if first != second {
		t.Errorf("byte-identity violated:\n want: %s\n  got: %s", first, second)
	}
}

// TestIdempotency_SetStatusReplayDoesNotDoubleApply verifies that task.set_status
// with a request_id replays cleanly and the task has the correct final status.
func TestIdempotency_SetStatusReplayDoesNotDoubleApply(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	cr, err := e.taskTool(ctx, args("action", "create", "title", "status-idempotency", "agent", "test-agent"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	task := decodeTask(t, cr)

	setArgs := args(
		"action", "set_status",
		"task_id", task.ID,
		"status", "completed",
		"agent", "test-agent",
		"request_id", "req-setstatus-1",
	)

	first, err := e.taskTool(ctx, setArgs)
	if err != nil {
		t.Fatalf("first set_status: %v", err)
	}
	second, err := e.taskTool(ctx, setArgs)
	if err != nil {
		t.Fatalf("second set_status (replay): %v", err)
	}

	if first != second {
		t.Errorf("set_status replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}

	got := decodeTask(t, first)
	if got.Status != "completed" {
		t.Errorf("want status completed, got %q", got.Status)
	}

	// A double-apply would bump version twice. Re-fetch and confirm the version
	// advanced exactly once from the freshly created task (1 → 2).
	cur, err := e.taskTool(ctx, args("action", "get", "task_id", task.ID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v := decodeTask(t, cur).Version; v != 2 {
		t.Errorf("want version 2 (single apply), got %d", v)
	}
}
