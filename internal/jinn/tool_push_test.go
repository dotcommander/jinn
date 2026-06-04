package jinn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newPushEngine returns an Engine + context with JINN_CONFIG_DIR isolation.
// Non-parallel because t.Setenv is used.
func newPushEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

// pushSummary matches the summary returned by pushTool.
type pushSummary struct {
	MemoryCount int      `json:"memory_count"`
	ArtifactIDs []string `json:"artifact_ids"`
	Task        *Task    `json:"task"`
}

func decodePushSummary(t *testing.T, raw string) pushSummary {
	t.Helper()
	var s pushSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("decode push summary: %v\nraw: %s", err, raw)
	}
	return s
}

// listArtifactsForTask is a decode helper for artifact list results.
func listArtifactsForTask(t *testing.T, e *Engine, ctx context.Context, taskID string) []Artifact {
	t.Helper()
	raw, err := e.artifactTool(ctx, args("action", "list", "task_id", taskID))
	if err != nil {
		t.Fatalf("artifact list: %v", err)
	}
	var out []Artifact
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	return out
}

// TestPush_AllFields exercises every sub-operation in one call.
func TestPush_AllFields(t *testing.T) {
	e, ctx := newPushEngine(t)
	taskID := createTestTask(t, e, ctx, "push all fields task")

	raw, err := e.pushTool(ctx, map[string]interface{}{
		"task_id": taskID,
		"agent":   "test-agent",
		"memories": []interface{}{
			map[string]interface{}{"key": "mem.one", "value": "v1"},
			map[string]interface{}{"key": "mem.two", "value": "v2", "kind": "directive"},
		},
		"artifacts": []interface{}{
			map[string]interface{}{"file_path": "/tmp/a.txt", "content_type": "text/plain"},
			map[string]interface{}{"file_path": "/tmp/b.json"},
		},
		"task_status": map[string]interface{}{"status": "in_progress"},
	})
	if err != nil {
		t.Fatalf("push all fields: %v", err)
	}
	s := decodePushSummary(t, raw)

	if s.MemoryCount != 2 {
		t.Errorf("memory_count: got %d want 2", s.MemoryCount)
	}
	if len(s.ArtifactIDs) != 2 {
		t.Errorf("artifact_ids len: got %d want 2", len(s.ArtifactIDs))
	}
	if s.Task == nil || s.Task.Status != "in_progress" {
		t.Errorf("task status: got %v want in_progress", s.Task)
	}

	if arts := listArtifactsForTask(t, e, ctx, taskID); len(arts) != 2 {
		t.Errorf("expected 2 artifacts linked to task, got %d", len(arts))
	}

	v1, err := e.memoryTool(ctx, args("action", "recall", "key", "mem.one"))
	if err != nil {
		t.Fatalf("recall mem.one: %v", err)
	}
	if v1 != "v1" {
		t.Errorf("mem.one: got %q want v1", v1)
	}
}

// TestPush_OnlyMemories verifies memories-only push (no artifacts).
func TestPush_OnlyMemories(t *testing.T) {
	e, ctx := newPushEngine(t)

	raw, err := e.pushTool(ctx, map[string]interface{}{
		"memories": []interface{}{
			map[string]interface{}{"key": "solo.key", "value": "solo.val", "scope": "global"},
		},
	})
	if err != nil {
		t.Fatalf("push only memories: %v", err)
	}
	s := decodePushSummary(t, raw)
	if s.MemoryCount != 1 {
		t.Errorf("memory_count: got %d want 1", s.MemoryCount)
	}
	v, err := e.memoryTool(ctx, args("action", "recall", "key", "solo.key", "scope", "global"))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if v != "solo.val" {
		t.Errorf("got %q want solo.val", v)
	}
}

// TestPush_ArtifactsLinkToTask verifies artifacts are inserted linked to task_id.
func TestPush_ArtifactsLinkToTask(t *testing.T) {
	e, ctx := newPushEngine(t)
	taskID := createTestTask(t, e, ctx, "artifact link task")

	raw, err := e.pushTool(ctx, map[string]interface{}{
		"task_id":   taskID,
		"artifacts": []interface{}{map[string]interface{}{"file_path": "/tmp/synth.txt"}},
	})
	if err != nil {
		t.Fatalf("push artifacts: %v", err)
	}
	s := decodePushSummary(t, raw)
	if len(s.ArtifactIDs) != 1 {
		t.Fatalf("expected 1 artifact id, got %d", len(s.ArtifactIDs))
	}

	arts := listArtifactsForTask(t, e, ctx, taskID)
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	if arts[0].TaskID != taskID {
		t.Errorf("artifact task_id=%q want %q", arts[0].TaskID, taskID)
	}
}

// TestPush_Atomicity: an invalid memory key rolls back the ENTIRE batch.
func TestPush_Atomicity(t *testing.T) {
	e, ctx := newPushEngine(t)
	taskID := createTestTask(t, e, ctx, "atomicity task")

	_, err := e.pushTool(ctx, map[string]interface{}{
		"task_id": taskID,
		"memories": []interface{}{
			map[string]interface{}{"key": strings.Repeat("x", 200), "value": "oops"},
		},
		"artifacts":   []interface{}{map[string]interface{}{"file_path": "/tmp/nope.txt"}},
		"task_status": map[string]interface{}{"status": "completed"},
	})
	if err == nil {
		t.Fatal("expected error for invalid memory key")
	}

	// No memory written.
	v, merr := e.memoryTool(ctx, args("action", "recall", "key", strings.Repeat("x", 200)))
	if merr == nil {
		t.Errorf("memory should not have been written; got %q", v)
	}
	// No artifacts.
	if arts := listArtifactsForTask(t, e, ctx, taskID); len(arts) != 0 {
		t.Errorf("expected 0 artifacts after rollback, got %d", len(arts))
	}
	// Task status unchanged (still pending).
	taskRaw, err := e.taskTool(ctx, args("action", "get", "task_id", taskID))
	if err != nil {
		t.Fatalf("task get: %v", err)
	}
	var task Task
	if err := json.Unmarshal([]byte(taskRaw), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.Status != "pending" {
		t.Errorf("task status: got %q want pending", task.Status)
	}
}

// TestPush_RequiresAtLeastOneOp verifies empty push fails.
func TestPush_RequiresAtLeastOneOp(t *testing.T) {
	e, ctx := newPushEngine(t)
	_, err := e.pushTool(ctx, map[string]interface{}{})
	if err == nil {
		t.Error("expected error when push has no operations")
	}
}

// TestPush_ArtifactsRequireTaskID verifies task_id is required for artifacts.
func TestPush_ArtifactsRequireTaskID(t *testing.T) {
	e, ctx := newPushEngine(t)
	_, err := e.pushTool(ctx, map[string]interface{}{
		"artifacts": []interface{}{map[string]interface{}{"file_path": "/tmp/x.txt"}},
	})
	if err == nil {
		t.Error("expected error when artifacts provided without task_id")
	}
}
