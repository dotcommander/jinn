package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// newTaskEngine returns an Engine + context wired to a fresh isolated DB.
func newTaskEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

func decodeTask(t *testing.T, s string) Task {
	t.Helper()
	var task Task
	if err := json.Unmarshal([]byte(s), &task); err != nil {
		t.Fatalf("decode task: %v\nraw: %s", err, s)
	}
	return task
}

func decodeTasks(t *testing.T, s string) []Task {
	t.Helper()
	var tasks []Task
	if err := json.Unmarshal([]byte(s), &tasks); err != nil {
		t.Fatalf("decode tasks: %v\nraw: %s", err, s)
	}
	return tasks
}

// TestTask_CreateGetList verifies create→get→list round-trip and ordering.
func TestTask_CreateGetList(t *testing.T) {
	e, ctx := newTaskEngine(t)

	// Create two tasks with different priorities.
	r1, err := e.taskTool(ctx, args("action", "create", "title", "low priority", "priority", float64(1)))
	if err != nil {
		t.Fatalf("create low: %v", err)
	}
	r2, err := e.taskTool(ctx, args("action", "create", "title", "high priority", "priority", float64(10)))
	if err != nil {
		t.Fatalf("create high: %v", err)
	}

	t1 := decodeTask(t, r1)
	t2 := decodeTask(t, r2)

	if t1.Status != "pending" {
		t.Errorf("status want pending, got %q", t1.Status)
	}
	if t1.Priority != 1 {
		t.Errorf("priority want 1, got %d", t1.Priority)
	}

	// get round-trip
	got, err := e.taskTool(ctx, args("action", "get", "task_id", t1.ID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	g := decodeTask(t, got)
	if g.ID != t1.ID || g.Title != "low priority" {
		t.Errorf("get mismatch: %+v", g)
	}

	// list — high priority first
	lr, err := e.taskTool(ctx, args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	tasks := decodeTasks(t, lr)
	if len(tasks) < 2 {
		t.Fatalf("want ≥2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != t2.ID {
		t.Errorf("want high-priority first, got %q", tasks[0].ID)
	}
}

// TestTask_BeginSetsInProgress verifies begin transitions status and emits task_started.
func TestTask_BeginSetsInProgress(t *testing.T) {
	e, ctx := newTaskEngine(t)

	cr, _ := e.taskTool(ctx, args("action", "create", "title", "begin-me"))
	task := decodeTask(t, cr)

	br, err := e.taskTool(ctx, args("action", "begin", "task_id", task.ID, "agent", "test-agent"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	begun := decodeTask(t, br)
	if begun.Status != "in_progress" {
		t.Errorf("status want in_progress, got %q", begun.Status)
	}
	if begun.Version <= task.Version {
		t.Errorf("version should have bumped: before=%d after=%d", task.Version, begun.Version)
	}

	// Verify task_started event was emitted.
	evts, err := e.eventTool(ctx, args("action", "list", "task_id", task.ID, "kind", "task_started"))
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if !strings.Contains(evts, "task_started") {
		t.Errorf("task_started event not found: %s", evts)
	}
}

// TestTask_SetStatus covers all statuses including blocked+reason.
func TestTask_SetStatus(t *testing.T) {
	e, ctx := newTaskEngine(t)

	cr, _ := e.taskTool(ctx, args("action", "create", "title", "status-test"))
	task := decodeTask(t, cr)

	cases := []struct {
		status        string
		blockedReason string
	}{
		{"in_progress", ""},
		{"completed", ""},
		{"pending", ""},
		{"blocked", "failure:test failure"},
	}
	for _, c := range cases {
		a := args("action", "set_status", "task_id", task.ID, "status", c.status)
		if c.blockedReason != "" {
			a["blocked_reason"] = c.blockedReason
		}
		r, err := e.taskTool(ctx, a)
		if err != nil {
			t.Fatalf("set_status %q: %v", c.status, err)
		}
		got := decodeTask(t, r)
		if got.Status != c.status {
			t.Errorf("status want %q got %q", c.status, got.Status)
		}
		if c.blockedReason != "" && got.BlockedReason != c.blockedReason {
			t.Errorf("blocked_reason want %q got %q", c.blockedReason, got.BlockedReason)
		}
	}
}

// TestTask_CreateEmitsTaskCreatedEvent verifies the task_created event.
func TestTask_CreateEmitsTaskCreatedEvent(t *testing.T) {
	e, ctx := newTaskEngine(t)

	cr, _ := e.taskTool(ctx, args("action", "create", "title", "event-check"))
	task := decodeTask(t, cr)

	evts, err := e.eventTool(ctx, args("action", "list", "task_id", task.ID))
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if !strings.Contains(evts, "task_created") {
		t.Errorf("task_created event not found in: %s", evts)
	}
}

// TestTask_InvalidAction verifies unknown actions are rejected.
func TestTask_InvalidAction(t *testing.T) {
	e, ctx := newTaskEngine(t)
	_, err := e.taskTool(ctx, args("action", "explode"))
	if err == nil {
		t.Fatal("want error for unknown action, got nil")
	}
}

// TestTask_AgentResolution verifies the three-tier fallback.
func TestTask_AgentResolution(t *testing.T) {
	// arg wins
	if got := resolveAgent(args("agent", "from-arg")); got != "from-arg" {
		t.Errorf("arg: want from-arg, got %q", got)
	}

	// env wins when no arg
	t.Setenv("JINN_CLIENT", "from-env")
	if got := resolveAgent(args()); got != "from-env" {
		t.Errorf("env: want from-env, got %q", got)
	}

	// fallback when neither set
	t.Setenv("JINN_CLIENT", "")
	if got := resolveAgent(args()); got != "agent" {
		t.Errorf("fallback: want agent, got %q", got)
	}
}

// TestTask_ProjectAutoDetect verifies resolveProjectID falls back to .git walk.
func TestTask_ProjectAutoDetect(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	// Engine rooted inside a fake repo dir.
	repoDir := t.TempDir()
	if err := makeDir(repoDir, ".git"); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	e := New(repoDir, "dev")
	t.Cleanup(func() { _ = e.Close() })

	// No project_id in args → should auto-detect.
	pid := e.resolveProjectID(args())
	if pid == "" {
		t.Error("expected non-empty project_id from .git walk, got empty")
	}
	if !strings.Contains(pid, repoDir) && pid != repoDir {
		// EvalSymlinks may change the exact path; just check it's non-empty
		// (already asserted above) and a plausible filesystem path.
		if !strings.HasPrefix(pid, "/") {
			t.Errorf("expected absolute path, got %q", pid)
		}
	}
}

// TestTask_LazyProjectsRow verifies that creating a task with a project_id
// inserts a row in the projects table.
func TestTask_LazyProjectsRow(t *testing.T) {
	e, ctx := newTaskEngine(t)

	projectID := "/fake/project/path"
	_, err := e.taskTool(ctx, args("action", "create", "title", "proj-task", "project_id", projectID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	db, _ := e.memDBConn(ctx)
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", projectID).Scan(&count); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 projects row, got %d", count)
	}
}

// TestTask_StatusFilter verifies list filters by status.
func TestTask_StatusFilter(t *testing.T) {
	e, ctx := newTaskEngine(t)

	cr, _ := e.taskTool(ctx, args("action", "create", "title", "pending-task"))
	task := decodeTask(t, cr)
	e.taskTool(ctx, args("action", "begin", "task_id", task.ID)) //nolint:errcheck

	r, err := e.taskTool(ctx, args("action", "list", "status", "pending"))
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	tasks := decodeTasks(t, r)
	for _, tk := range tasks {
		if tk.Status != "pending" {
			t.Errorf("list filtered by pending returned status %q", tk.Status)
		}
	}

	r2, err := e.taskTool(ctx, args("action", "list", "status", "in_progress"))
	if err != nil {
		t.Fatalf("list in_progress: %v", err)
	}
	inprog := decodeTasks(t, r2)
	found := false
	for _, tk := range inprog {
		if tk.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("task %q not in in_progress list", task.ID)
	}
}

// TestTask_GetNotFound verifies a clean error for missing task_id.
func TestTask_GetNotFound(t *testing.T) {
	e, ctx := newTaskEngine(t)
	_, err := e.taskTool(ctx, args("action", "get", "task_id", fmt.Sprintf("task_%d_zzzz", 1)))
	if err == nil {
		t.Fatal("want error for non-existent task_id")
	}
}
