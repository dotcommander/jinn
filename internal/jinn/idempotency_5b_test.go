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

// TestIdempotency5b_PushReplayOnce verifies that push with the same (agent,
// request_id) twice leaves exactly one event, artifact, and memory row; the
// second result is byte-identical to the first.
func TestIdempotency5b_PushReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	taskRaw, err := e.taskTool(ctx, args("action", "create", "title", "push-idem", "agent", "agent"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var task Task
	if err := json.Unmarshal([]byte(taskRaw), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	pushArgs := args(
		"agent", "agent",
		"task_id", task.ID,
		"request_id", "push-req-5b-001",
		"event", map[string]interface{}{"kind": "progress", "message": "work"},
		"artifacts", []interface{}{
			map[string]interface{}{"file_path": "/tmp/out.json", "content_type": "application/json"},
		},
		"memories", []interface{}{
			map[string]interface{}{"key": "push-mem", "value": "v1", "scope": "global"},
		},
	)

	first, err := e.pushTool(ctx, pushArgs)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	second, err := e.pushTool(ctx, pushArgs)
	if err != nil {
		t.Fatalf("second push (replay): %v", err)
	}
	if first != second {
		t.Errorf("push replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}

	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM events WHERE kind='progress'`); n != 1 {
		t.Errorf("want 1 progress event, got %d", n)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM artifacts WHERE file_path='/tmp/out.json'`); n != 1 {
		t.Errorf("want 1 artifact row, got %d", n)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM memory WHERE key='push-mem'`); n != 1 {
		t.Errorf("want 1 memory row, got %d", n)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM events WHERE kind='task_created'`); n != 1 {
		t.Errorf("want 1 task_created event, got %d", n)
	}
}

// TestIdempotency5b_ResumeReplayOnce verifies that resumeAdvance with the same
// (agent, request_id) advances the cursor exactly once; result is byte-identical.
// Peek (no request_id) is unaffected.
func TestIdempotency5b_ResumeReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	if _, err := e.eventTool(ctx, args(
		"action", "append", "kind", "progress",
		"message", "something", "agent", "agent",
	)); err != nil {
		t.Fatalf("append event: %v", err)
	}

	resumeArgs := args("agent", "agent", "request_id", "resume-req-5b-001")

	first, err := e.resumeTool(ctx, resumeArgs)
	if err != nil {
		t.Fatalf("first resume: %v", err)
	}
	second, err := e.resumeTool(ctx, resumeArgs)
	if err != nil {
		t.Fatalf("second resume (replay): %v", err)
	}
	if first != second {
		t.Errorf("resume replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}

	var b1, b2 BriefPacket
	if err := json.Unmarshal([]byte(first), &b1); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if err := json.Unmarshal([]byte(second), &b2); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if b1.Cursor.New != b2.Cursor.New {
		t.Errorf("cursor.new differs: first=%d second=%d (double-advanced)", b1.Cursor.New, b2.Cursor.New)
	}

	// Peek: read-only, cursor.old == cursor.new.
	peekRaw, err := e.resumeTool(ctx, args("agent", "agent", "peek", true))
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	var p BriefPacket
	if err := json.Unmarshal([]byte(peekRaw), &p); err != nil {
		t.Fatalf("decode peek: %v", err)
	}
	if p.Cursor.Old != p.Cursor.New {
		t.Errorf("peek must not advance cursor: old=%d new=%d", p.Cursor.Old, p.Cursor.New)
	}
}

// TestIdempotency5b_EventAppendReplayOnce verifies that event.append with the
// same (agent, request_id) twice produces exactly one event row and byte-identical results.
func TestIdempotency5b_EventAppendReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	a := args(
		"action", "append", "kind", "progress",
		"message", "5b event replay", "agent", "agent",
		"request_id", "event-req-5b-001",
	)

	first, err := e.eventTool(ctx, a)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := e.eventTool(ctx, a)
	if err != nil {
		t.Fatalf("second append (replay): %v", err)
	}
	if first != second {
		t.Errorf("event.append replay not byte-identical:\n first:  %s\n second: %s", first, second)
	}
	if n := dbCount(t, e, ctx, `SELECT COUNT(*) FROM events WHERE kind='progress'`); n != 1 {
		t.Errorf("want 1 event row, got %d", n)
	}
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

// TestIdempotency5b_MemoryGCReplayOnce verifies that memory.gc with the same
// (agent, request_id) twice returns an identical deleted count and is idempotent.
func TestIdempotency5b_MemoryGCReplayOnce(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory (scope, scope_id, key, value, value_type, kind, pinned, expires_at)
		 VALUES ('global', '', 'expired-gc-5b', 'bye', 'string', 'fact', 0, datetime('now', '-1 hour'))`,
	); err != nil {
		t.Fatalf("insert expired row: %v", err)
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
