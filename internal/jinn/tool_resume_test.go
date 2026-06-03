package jinn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// newResumeEngine returns an Engine + context wired to a fresh isolated DB.
// Reuses the same isolation pattern as newTaskEngine. Tests using this are NOT
// parallel because t.Setenv forbids it.
func newResumeEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

// dispatchText runs a tool via Dispatch and returns its text result.
func dispatchText(t *testing.T, e *Engine, ctx context.Context, tool string, a map[string]interface{}) string {
	t.Helper()
	res, _, err := e.Dispatch(ctx, tool, a)
	if err != nil {
		t.Fatalf("dispatch %s: %v", tool, err)
	}
	return res.Text
}

// doResume runs the resume tool and decodes the brief packet.
func doResume(t *testing.T, e *Engine, ctx context.Context, a map[string]interface{}) BriefPacket {
	t.Helper()
	raw := dispatchText(t, e, ctx, "resume", a)
	var b BriefPacket
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("decode brief: %v\nraw: %s", err, raw)
	}
	return b
}

// createTask creates a task and returns its id.
func createTask(t *testing.T, e *Engine, ctx context.Context, a map[string]interface{}) string {
	t.Helper()
	a["action"] = "create"
	raw := dispatchText(t, e, ctx, "task", a)
	var task Task
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatalf("decode created task: %v\nraw: %s", err, raw)
	}
	return task.ID
}

func TestResume_Rule1_InProgressKept(t *testing.T) {
	e, ctx := newResumeEngine(t)
	a := createTask(t, e, ctx, args("title", "A", "priority", float64(1)))

	// resume once so A becomes the persisted focus (rule4)
	b1 := doResume(t, e, ctx, args())
	if b1.Task == nil || b1.Task.ID != a {
		t.Fatalf("expected initial focus A, got %+v", b1.Task)
	}

	// begin A -> in_progress
	dispatchText(t, e, ctx, "task", args("action", "begin", "task_id", a))

	// higher-priority pending B
	createTask(t, e, ctx, args("title", "B", "priority", float64(10)))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule1:") {
		t.Fatalf("expected rule1, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != a {
		t.Fatalf("expected focus A, got %+v", b.Task)
	}
}

func TestResume_Rule15_NonFailureBlockedKept(t *testing.T) {
	e, ctx := newResumeEngine(t)
	a := createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	doResume(t, e, ctx, args()) // focus A

	dispatchText(t, e, ctx, "task", args("action", "set_status", "task_id", a,
		"status", "blocked", "blocked_reason", "waiting on review"))

	createTask(t, e, ctx, args("title", "B", "priority", float64(10)))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule1.5") {
		t.Fatalf("expected rule1.5, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != a {
		t.Fatalf("expected focus A, got %+v", b.Task)
	}
}

func TestResume_Rule15_FailureBlockedNotKept(t *testing.T) {
	e, ctx := newResumeEngine(t)
	a := createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	doResume(t, e, ctx, args()) // focus A

	dispatchText(t, e, ctx, "task", args("action", "set_status", "task_id", a,
		"status", "blocked", "blocked_reason", "failure: build broke"))

	bID := createTask(t, e, ctx, args("title", "B", "priority", float64(10)))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule4") {
		t.Fatalf("expected rule4, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != bID {
		t.Fatalf("expected focus B, got %+v", b.Task)
	}
}

func TestResume_Rule2_TaskAssignedDelta(t *testing.T) {
	e, ctx := newResumeEngine(t)
	createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	doResume(t, e, ctx, args()) // focus A, advances cursor past A's events

	bID := createTask(t, e, ctx, args("title", "B", "priority", float64(1)))
	dispatchText(t, e, ctx, "event", args("action", "append", "kind", "task_assigned",
		"message", "assigned B", "task_id", bID))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule2") {
		t.Fatalf("expected rule2, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != bID {
		t.Fatalf("expected focus B, got %+v", b.Task)
	}
}

func TestResume_Rule3_UnblockedResumed(t *testing.T) {
	e, ctx := newResumeEngine(t)
	a := createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	doResume(t, e, ctx, args()) // focus A via rule4

	dispatchText(t, e, ctx, "task", args("action", "begin", "task_id", a))
	dispatchText(t, e, ctx, "task", args("action", "set_status", "task_id", a,
		"status", "blocked", "blocked_reason", "failure: stuck")) // rule1/1.5 skip
	dispatchText(t, e, ctx, "task", args("action", "set_status", "task_id", a, "status", "pending"))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule3") {
		t.Fatalf("expected rule3, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != a {
		t.Fatalf("expected focus A, got %+v", b.Task)
	}
}

func TestResume_Rule4_HighestPriorityPending(t *testing.T) {
	e, ctx := newResumeEngine(t)
	createTask(t, e, ctx, args("title", "P1", "priority", float64(1)))
	p5 := createTask(t, e, ctx, args("title", "P5", "priority", float64(5)))
	createTask(t, e, ctx, args("title", "P3", "priority", float64(3)))

	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule4") {
		t.Fatalf("expected rule4, got %q", b.FocusRule)
	}
	if b.Task == nil || b.Task.ID != p5 {
		t.Fatalf("expected focus P5, got %+v", b.Task)
	}
}

func TestResume_Rule5_EmptyNoWork(t *testing.T) {
	e, ctx := newResumeEngine(t)
	b := doResume(t, e, ctx, args())
	if !strings.HasPrefix(b.FocusRule, "rule5") {
		t.Fatalf("expected rule5, got %q", b.FocusRule)
	}
	if b.Task != nil {
		t.Fatalf("expected nil task, got %+v", b.Task)
	}
	if len(b.RelevantMemory) != 0 {
		t.Fatalf("expected empty relevant_memory, got %v", b.RelevantMemory)
	}
	if len(b.RecentEvents) != 0 {
		t.Fatalf("expected empty recent_events, got %v", b.RecentEvents)
	}
	if len(b.Pipeline) != 0 {
		t.Fatalf("expected empty pipeline, got %v", b.Pipeline)
	}
	if b.Counts == nil || b.Counts.Pending != 0 || b.Counts.InProgress != 0 ||
		b.Counts.Completed != 0 || b.Counts.Blocked != 0 {
		t.Fatalf("expected zero counts, got %+v", b.Counts)
	}
}

func TestResume_CursorMonotonicAdvance(t *testing.T) {
	e, ctx := newResumeEngine(t)
	createTask(t, e, ctx, args("title", "A", "priority", float64(1)))

	b1 := doResume(t, e, ctx, args())
	firstCursor := b1.Cursor.New
	if firstCursor <= 0 {
		t.Fatalf("expected cursor advanced past 0, got %d", firstCursor)
	}

	// append another event
	dispatchText(t, e, ctx, "event", args("action", "append", "kind", "progress", "message", "work"))

	b2 := doResume(t, e, ctx, args())
	if b2.Cursor.Old != firstCursor {
		t.Fatalf("expected cursor.old == %d, got %d", firstCursor, b2.Cursor.Old)
	}
	if b2.Cursor.New <= b2.Cursor.Old {
		t.Fatalf("expected cursor to strictly advance with new events: old=%d new=%d",
			b2.Cursor.Old, b2.Cursor.New)
	}
}

func TestResume_PeekDoesNotAdvance(t *testing.T) {
	e, ctx := newResumeEngine(t)
	createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	dispatchText(t, e, ctx, "event", args("action", "append", "kind", "progress", "message", "x"))

	raw1 := dispatchText(t, e, ctx, "resume", args("peek", true))
	raw2 := dispatchText(t, e, ctx, "resume", args("peek", true))

	var p1, p2 BriefPacket
	if err := json.Unmarshal([]byte(raw1), &p1); err != nil {
		t.Fatalf("decode peek1: %v", err)
	}
	if err := json.Unmarshal([]byte(raw2), &p2); err != nil {
		t.Fatalf("decode peek2: %v", err)
	}
	if p1.Cursor.Old != p1.Cursor.New {
		t.Fatalf("peek1 cursor advanced: old=%d new=%d", p1.Cursor.Old, p1.Cursor.New)
	}
	if p2.Cursor.Old != p2.Cursor.New {
		t.Fatalf("peek2 cursor advanced: old=%d new=%d", p2.Cursor.Old, p2.Cursor.New)
	}
	if p1.Cursor.New != p2.Cursor.New {
		t.Fatalf("peeks differ: %d vs %d", p1.Cursor.New, p2.Cursor.New)
	}
	// re-marshal and compare byte-identical
	m1, _ := json.Marshal(p1)
	m2, _ := json.Marshal(p2)
	if string(m1) != string(m2) {
		t.Fatalf("peek packets not identical:\n%s\n%s", m1, m2)
	}

	peekCursor := p1.Cursor.New
	b := doResume(t, e, ctx, args())
	if b.Cursor.New <= peekCursor {
		t.Fatalf("non-peek resume did not advance past peek cursor: peek=%d new=%d",
			peekCursor, b.Cursor.New)
	}
}

func TestResume_PeekNoRowCreated(t *testing.T) {
	e, ctx := newResumeEngine(t)
	dispatchText(t, e, ctx, "resume", args("peek", true))

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_state WHERE agent_name = ?`, "agent").Scan(&count); err != nil {
		t.Fatalf("count agent_state: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no agent_state row after peek, got %d", count)
	}
}

func TestResume_RelevantMemoryScoping(t *testing.T) {
	e, ctx := newResumeEngine(t)
	a := createTask(t, e, ctx, args("title", "A", "priority", float64(1)))
	doResume(t, e, ctx, args()) // focus A

	// global key g (pinned -> sorts first)
	dispatchText(t, e, ctx, "memory", args("action", "save", "scope", "global", "key", "g", "value", "gv", "pin", true))
	// task-scoped key t
	dispatchText(t, e, ctx, "memory", args("action", "save", "scope", "task", "scope_id", a, "key", "tk", "value", "tv"))
	// agent-scoped key ag (must be EXCLUDED)
	dispatchText(t, e, ctx, "memory", args("action", "save", "scope", "agent", "scope_id", "agent", "key", "ag", "value", "av"))

	// expired global key x — insert directly (parseExpiresIn rejects past times)
	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}
	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory (scope, scope_id, key, value, value_type, kind, pinned, expires_at)
		 VALUES ('global', '', 'x', 'xv', 'string', 'fact', 0, ?)`, past); err != nil {
		t.Fatalf("insert expired: %v", err)
	}

	b := doResume(t, e, ctx, args())
	keys := map[string]bool{}
	for _, m := range b.RelevantMemory {
		keys[m.Key] = true
	}
	if !keys["g"] || !keys["tk"] {
		t.Fatalf("expected global+task memory present, got keys %v", keys)
	}
	if keys["ag"] {
		t.Fatalf("agent-scoped memory must be excluded, got keys %v", keys)
	}
	if keys["x"] {
		t.Fatalf("expired memory must be excluded, got keys %v", keys)
	}
	if len(b.RelevantMemory) == 0 || b.RelevantMemory[0].Key != "g" {
		t.Fatalf("expected pinned global 'g' first, got %v", b.RelevantMemory)
	}
}

func TestResume_PipelineExcludesFocus(t *testing.T) {
	e, ctx := newResumeEngine(t)
	p5 := createTask(t, e, ctx, args("title", "P5", "priority", float64(5)))
	createTask(t, e, ctx, args("title", "P3", "priority", float64(3)))
	createTask(t, e, ctx, args("title", "P1", "priority", float64(1)))

	b := doResume(t, e, ctx, args()) // focus P5 via rule4
	if b.Task == nil || b.Task.ID != p5 {
		t.Fatalf("expected focus P5, got %+v", b.Task)
	}
	if b.Counts.Pending != 2 {
		// after focus selection, P5 stays pending (no begin) -> 3 pending total,
		// but pipeline excludes focus. Counts reflect all pending (3).
		t.Logf("counts.pending=%d", b.Counts.Pending)
	}
	if b.Counts.Pending != 3 {
		t.Fatalf("expected 3 pending tasks, got %d", b.Counts.Pending)
	}
	if len(b.Pipeline) != 2 {
		t.Fatalf("expected pipeline length 2, got %d (%v)", len(b.Pipeline), b.Pipeline)
	}
	for _, p := range b.Pipeline {
		if p.ID == p5 {
			t.Fatalf("pipeline must exclude focus P5, got %v", b.Pipeline)
		}
	}
	if b.Pipeline[0].Priority < b.Pipeline[1].Priority {
		t.Fatalf("pipeline not ordered by priority DESC: %v", b.Pipeline)
	}
}

func TestResume_EmptyStateBrief(t *testing.T) {
	e, ctx := newResumeEngine(t)
	raw := dispatchText(t, e, ctx, "resume", args())
	// slices must serialize as [] not null
	if strings.Contains(raw, `"relevant_memory":null`) ||
		strings.Contains(raw, `"recent_events":null`) ||
		strings.Contains(raw, `"artifacts":null`) ||
		strings.Contains(raw, `"pipeline":null`) ||
		strings.Contains(raw, `"deltas":null`) {
		t.Fatalf("empty-state brief has null slices: %s", raw)
	}
	if !strings.Contains(raw, `"task":null`) {
		t.Fatalf("expected null task in empty-state brief: %s", raw)
	}
}
