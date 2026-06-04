package jinn

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Non-parallel: t.Setenv (via newIdempotencyEngine) is process-wide. Do NOT add
// t.Parallel() here — matches the convention in idempotency_test.go /
// idempotency_5b_test.go.

// runIdempotentLiteralRE captures the 5th positional argument (the command
// string literal) of a runIdempotent(...) call.
var runIdempotentLiteralRE = regexp.MustCompile(`runIdempotent\([^,]+,[^,]+,[^,]+,[^,]+,\s*"([^"]+)"`)

// TestMutatingRegistryCompleteness asserts the set of command literals actually
// passed to runIdempotent in the non-test source equals the set declared in
// mutatingActions. This is the drift trap: a new mutating handler (or a renamed
// command) fails here until the registry is updated.
func TestMutatingRegistryCompleteness(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	sourceCommands := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range runIdempotentLiteralRE.FindAllStringSubmatch(string(data), -1) {
			sourceCommands[m[1]] = true
		}
	}

	registryCommands := map[string]bool{}
	for _, a := range mutatingActions {
		registryCommands[a.Command] = true
	}

	for cmd := range sourceCommands {
		if !registryCommands[cmd] {
			t.Errorf("command %q is passed to runIdempotent in source but MISSING from mutatingActions registry", cmd)
		}
	}
	for cmd := range registryCommands {
		if !sourceCommands[cmd] {
			t.Errorf("command %q is declared in mutatingActions but NOT found at any runIdempotent call site in source", cmd)
		}
	}
}

// dispatchMutating builds minimal valid args for the given action (setting the
// supplied request_id) and dispatches the matching tool method. It returns the
// raw JSON result. preTaskID is a task created by the caller for actions that
// require an existing task.
func dispatchMutating(t *testing.T, e *Engine, ctx context.Context, a mutatingAction, requestID, preTaskID string) (string, error) {
	t.Helper()
	switch a.Command {
	case "task.create":
		return e.taskTool(ctx, args("action", "create", "title", "mut-create", "agent", "agent", "request_id", requestID))
	case "task.begin":
		return e.taskTool(ctx, args("action", "begin", "task_id", preTaskID, "agent", "agent", "request_id", requestID))
	case "task.set_status":
		return e.taskTool(ctx, args("action", "set_status", "task_id", preTaskID, "status", "completed", "agent", "agent", "request_id", requestID))
	case "memory.save":
		return e.memoryTool(ctx, args("action", "save", "key", "mut-key", "value", "v1", "scope", "global", "agent", "agent", "request_id", requestID))
	case "memory.forget":
		return e.memoryTool(ctx, args("action", "forget", "key", "mut-key", "scope", "global", "agent", "agent", "request_id", requestID))
	case "memory.gc":
		return e.memoryTool(ctx, args("action", "gc", "scope", "global", "agent", "agent", "request_id", requestID))
	case "event.append":
		return e.eventTool(ctx, args("action", "append", "kind", "progress", "message", "mut-event", "agent", "agent", "request_id", requestID))
	case "artifact.add":
		return e.artifactTool(ctx, args("action", "add", "task_id", preTaskID, "file_path", "/tmp/mut.json", "content_type", "application/json", "agent", "agent", "request_id", requestID))
	case "push":
		return e.pushTool(ctx, args(
			"agent", "agent",
			"task_id", preTaskID,
			"request_id", requestID,
			"event", map[string]interface{}{"kind": "progress", "message": "work"},
		))
	case "resume":
		return e.resumeTool(ctx, args("agent", "agent", "request_id", requestID))
	default:
		t.Fatalf("dispatchMutating: unhandled command %q", a.Command)
		return "", nil
	}
}

// TestMutatingRegistryReplay proves each registry action is wired through
// runIdempotent: dispatching twice with the same request_id returns a
// byte-identical result.
func TestMutatingRegistryReplay(t *testing.T) {
	for _, a := range mutatingActions {
		a := a
		t.Run(a.Command, func(t *testing.T) {
			e, ctx := newIdempotencyEngine(t)

			// Pre-create a task for actions that need an existing task_id.
			cr, err := e.taskTool(ctx, args("action", "create", "title", "pre", "agent", "agent"))
			if err != nil {
				t.Fatalf("pre-create task: %v", err)
			}
			preTaskID := decodeTask(t, cr).ID

			// resume needs a delta to advance over; append one event.
			if a.Command == "resume" {
				if _, err := e.eventTool(ctx, args("action", "append", "kind", "progress", "message", "seed", "agent", "agent")); err != nil {
					t.Fatalf("seed event: %v", err)
				}
			}

			reqID := "mut-replay-" + a.Command

			first, err := dispatchMutating(t, e, ctx, a, reqID, preTaskID)
			if err != nil {
				t.Fatalf("%s first dispatch: %v", a.Command, err)
			}
			second, err := dispatchMutating(t, e, ctx, a, reqID, preTaskID)
			if err != nil {
				t.Fatalf("%s second dispatch (replay): %v", a.Command, err)
			}
			if first != second {
				t.Errorf("%s replay not byte-identical:\n first:  %s\n second: %s", a.Command, first, second)
			}
		})
	}
}

// TestMutatingRegistryResumePeekException is the BLOCKING negative assertion:
// resume with peek=true performs ZERO writes and creates NO idempotency row,
// even when a request_id is supplied. resumePeek is the documented read-only
// exception to the mutating set.
func TestMutatingRegistryResumePeekException(t *testing.T) {
	e, ctx := newIdempotencyEngine(t)

	if _, err := e.eventTool(ctx, args("action", "append", "kind", "progress", "message", "peek-seed", "agent", "agent")); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	const q = `SELECT COUNT(*) FROM idempotency WHERE command='resume'`
	before := dbCount(t, e, ctx, q)

	if _, err := e.resumeTool(ctx, args("agent", "agent", "peek", true, "request_id", "peek-req-001")); err != nil {
		t.Fatalf("peek: %v", err)
	}

	after := dbCount(t, e, ctx, q)
	if before != after {
		t.Errorf("resume peek must not create an idempotency row: command='resume' count before=%d after=%d", before, after)
	}
}
