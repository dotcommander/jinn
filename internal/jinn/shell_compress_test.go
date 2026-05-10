package jinn

import (
	"context"
	"strings"
	"testing"
)

func TestShellCompress_TestResult_FramingSurvives(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// Emit fake go test verbose output that triggers testResultStrategy.
	cmd := `printf '=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- PASS: TestB (0.00s)\n=== RUN   TestC\n--- PASS: TestC (0.00s)\nPASS\nok  \texample.com/pkg\t0.01s\n'`

	result, _, err := e.runShell(context.Background(), args("command", cmd))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "✓") {
		t.Errorf("expected ✓ in compressed output, got: %s", result)
	}
	if !strings.Contains(result, "3 passed") {
		t.Errorf("expected '3 passed' in compressed output, got: %s", result)
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected [exit: 0] framing in output, got: %s", result)
	}
	if !strings.Contains(result, "[classification:") {
		t.Errorf("expected [classification: framing in output, got: %s", result)
	}

	// Sanity: compressed result should be shorter than the raw printf payload.
	rawPayload := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- PASS: TestB (0.00s)\n=== RUN   TestC\n--- PASS: TestC (0.00s)\nPASS\nok  \texample.com/pkg\t0.01s\n"
	if len(result) >= len(rawPayload)+100 {
		t.Errorf("expected result shorter than raw payload+framing overhead, len(result)=%d", len(result))
	}
}

func TestShellCompress_GitStatus_FramingSurvives(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// Emit fake git status output that triggers gitStatusStrategy.
	// Must start with "On branch " and contain "Changes" or "Untracked".
	// Entries must match `^\t(modified|...):\s+(.*)` for staged/unstaged
	// and `^\t<name>` under "Untracked files:".
	cmd := "printf 'On branch main\\nYour branch is up to date with '\"'\"'origin/main'\"'\"'.\\n\\nChanges not staged for commit:\\n  (use \"git add <file>...\")\\n\\tmodified:   foo.go\\n\\tmodified:   bar.go\\n\\nUntracked files:\\n\\tbaz.go\\n'"

	result, _, err := e.runShell(context.Background(), args("command", cmd))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "On branch main") {
		t.Errorf("expected 'On branch main' in output, got: %s", result)
	}
	if !strings.Contains(result, "M foo.go") {
		t.Errorf("expected 'M foo.go' in compressed output, got: %s", result)
	}
	if !strings.Contains(result, "M bar.go") {
		t.Errorf("expected 'M bar.go' in compressed output, got: %s", result)
	}
	if !strings.Contains(result, "+baz.go") {
		t.Errorf("expected '+baz.go' in compressed output, got: %s", result)
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected [exit: 0] framing in output, got: %s", result)
	}
	if !strings.Contains(result, "[classification:") {
		t.Errorf("expected [classification: framing in output, got: %s", result)
	}

	// Sanity: result should be shorter than the verbose raw payload.
	rawPayload := "On branch main\nYour branch is up to date with 'origin/main'.\n\nChanges not staged for commit:\n  (use \"git add <file>...\")\n\tmodified:   foo.go\n\tmodified:   bar.go\n\nUntracked files:\n\tbaz.go\n"
	if len(result) >= len(rawPayload)+100 {
		t.Errorf("expected result shorter than raw payload+framing overhead, len(result)=%d", len(result))
	}
}
