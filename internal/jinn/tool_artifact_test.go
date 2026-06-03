package jinn

import (
	"context"
	"encoding/json"
	"testing"
)

// newArtifactEngine returns an Engine + context with JINN_CONFIG_DIR isolation.
// Non-parallel because t.Setenv is used.
func newArtifactEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

// createTestTask is a helper that creates a task and returns its ID.
func createTestTask(t *testing.T, e *Engine, ctx context.Context, title string) string {
	t.Helper()
	raw, err := e.taskTool(ctx, args("action", "create", "title", title))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var task Task
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	return task.ID
}

// TestArtifact_AddList verifies the add→list round-trip.
func TestArtifact_AddList(t *testing.T) {
	e, ctx := newArtifactEngine(t)

	taskID := createTestTask(t, e, ctx, "artifact task")

	// Add an artifact.
	raw, err := e.artifactTool(ctx, args(
		"action", "add",
		"task_id", taskID,
		"file_path", "/tmp/output.json",
		"content_type", "application/json",
	))
	if err != nil {
		t.Fatalf("artifact add: %v", err)
	}

	var artifact Artifact
	if err := json.Unmarshal([]byte(raw), &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if artifact.ID == "" {
		t.Error("expected non-empty artifact id")
	}
	if artifact.TaskID != taskID {
		t.Errorf("task_id: got %q want %q", artifact.TaskID, taskID)
	}
	if artifact.FilePath != "/tmp/output.json" {
		t.Errorf("file_path: got %q want /tmp/output.json", artifact.FilePath)
	}
	if artifact.ContentType != "application/json" {
		t.Errorf("content_type: got %q want application/json", artifact.ContentType)
	}
	if artifact.EventID == 0 {
		t.Error("expected non-zero event_id linked to artifact")
	}
	savedEventID := artifact.EventID

	// List confirms the artifact is stored.
	listRaw, err := e.artifactTool(ctx, args("action", "list", "task_id", taskID))
	if err != nil {
		t.Fatalf("artifact list: %v", err)
	}
	var artifacts []Artifact
	if err := json.Unmarshal([]byte(listRaw), &artifacts); err != nil {
		t.Fatalf("decode artifact list: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].ID != artifact.ID {
		t.Errorf("listed artifact id mismatch: got %q want %q", artifacts[0].ID, artifact.ID)
	}

	// Verify add emits an artifact_added event and that event_id matches.
	evRaw, err := e.eventTool(ctx, args("action", "list", "task_id", taskID, "kind", "artifact_added"))
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	var events []*Event
	if err := json.Unmarshal([]byte(evRaw), &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.Kind == "artifact_added" && ev.ID == savedEventID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected artifact_added event with id=%d; events: %v", savedEventID, events)
	}
}

// TestArtifact_AddRequiresTaskID verifies validation.
func TestArtifact_AddRequiresTaskID(t *testing.T) {
	e, ctx := newArtifactEngine(t)

	_, err := e.artifactTool(ctx, args("action", "add", "file_path", "/tmp/x.txt"))
	if err == nil {
		t.Error("expected error when task_id missing")
	}
}

// TestArtifact_AddRequiresFilePath verifies validation.
func TestArtifact_AddRequiresFilePath(t *testing.T) {
	e, ctx := newArtifactEngine(t)

	taskID := createTestTask(t, e, ctx, "fp task")
	_, err := e.artifactTool(ctx, args("action", "add", "task_id", taskID))
	if err == nil {
		t.Error("expected error when file_path missing")
	}
}

// TestArtifact_ListEmpty verifies list returns [] not null for empty task.
func TestArtifact_ListEmpty(t *testing.T) {
	e, ctx := newArtifactEngine(t)

	taskID := createTestTask(t, e, ctx, "empty task")
	raw, err := e.artifactTool(ctx, args("action", "list", "task_id", taskID))
	if err != nil {
		t.Fatalf("artifact list: %v", err)
	}
	var artifacts []Artifact
	if err := json.Unmarshal([]byte(raw), &artifacts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(artifacts) != 0 {
		t.Errorf("expected 0 artifacts, got %d", len(artifacts))
	}
}
