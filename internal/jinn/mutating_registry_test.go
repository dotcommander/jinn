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
// raw JSON result.
func dispatchMutating(t *testing.T, e *Engine, ctx context.Context, a mutatingAction, requestID string) (string, error) {
	t.Helper()
	switch a.Command {
	case "memory.save":
		return e.memoryTool(ctx, args("action", "save", "key", "mut-key", "value", "v1", "scope", "global", "agent", "agent", "request_id", requestID))
	case "memory.forget":
		return e.memoryTool(ctx, args("action", "forget", "key", "mut-key", "scope", "global", "agent", "agent", "request_id", requestID))
	case "memory.gc":
		return e.memoryTool(ctx, args("action", "gc", "scope", "global", "agent", "agent", "request_id", requestID))
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

			reqID := "mut-replay-" + a.Command

			first, err := dispatchMutating(t, e, ctx, a, reqID)
			if err != nil {
				t.Fatalf("%s first dispatch: %v", a.Command, err)
			}
			second, err := dispatchMutating(t, e, ctx, a, reqID)
			if err != nil {
				t.Fatalf("%s second dispatch (replay): %v", a.Command, err)
			}
			if first != second {
				t.Errorf("%s replay not byte-identical:\n first:  %s\n second: %s", a.Command, first, second)
			}
		})
	}
}
