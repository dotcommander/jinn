package jinn

import (
	"context"
	"strings"
	"testing"
)

func TestRunShell_Echo(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "echo hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[exit: 0]") || !strings.Contains(result, "hello") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestRunShell_ExitCode(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "exit 42"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[exit: 42]") {
		t.Errorf("expected exit 42, got: %s", result)
	}
}

func TestRunShell_TimeoutClamp(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "echo ok", "timeout", float64(9999)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("clamped timeout should still execute: %s", result)
	}
}

func TestRunShell_DryRun(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "rm -rf /", "dry_run", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[dry-run]") {
		t.Errorf("expected dry-run prefix, got: %s", result)
	}
	if !strings.Contains(result, "rm -rf /") {
		t.Errorf("expected command in output, got: %s", result)
	}
}

func TestRunShell_CollapseRepeated(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "for i in $(seq 1 10); do echo same; done"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "identical lines collapsed") {
		t.Errorf("expected collapsed lines, got: %s", result)
	}
}

// --- Feature 6: exit-code classification tests ---

func TestRunShell_Classification_Success(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, err := e.runShell(context.Background(), args("command", "echo hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "classification: success") {
		t.Errorf("expected classification: success, got: %s", result)
	}
}

func TestRunShell_Classification_GrepNoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	_ = dir
	// grep exits 1 when no match — should be expected_nonzero.
	result, err := e.runShell(context.Background(), args("command", "grep ZZZNOMATCH /dev/null; true"))
	// We run 'grep ... ; true' to ensure exit 0 from the shell, then test
	// the classifier directly.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result

	// Test the classifier directly for grep exit 1.
	class, reason := classifyExitCode("grep", 1)
	if class != ClassExpectedNonzero {
		t.Errorf("grep exit 1: expected ClassExpectedNonzero, got %s", class)
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestRunShell_Classification_DiffDiffer(t *testing.T) {
	t.Parallel()
	class, _ := classifyExitCode("diff", 1)
	if class != ClassExpectedNonzero {
		t.Errorf("diff exit 1: expected ClassExpectedNonzero, got %s", class)
	}
}

func TestRunShell_Classification_UnknownCommandError(t *testing.T) {
	t.Parallel()
	class, _ := classifyExitCode("mycommand", 1)
	if class != ClassError {
		t.Errorf("unknown command exit 1: expected ClassError, got %s", class)
	}
}

func TestRunShell_Classification_Timeout(t *testing.T) {
	t.Parallel()
	class, reason := classifyExitCode("anything", 124)
	if class != ClassTimeout {
		t.Errorf("exit 124: expected ClassTimeout, got %s", class)
	}
	if !strings.Contains(reason, "time limit") {
		t.Errorf("expected 'time limit' in reason, got: %s", reason)
	}
}

func TestRunShell_Classification_Signal(t *testing.T) {
	t.Parallel()
	class, _ := classifyExitCode("anything", 137) // 128+9 = SIGKILL
	if class != ClassSignal {
		t.Errorf("exit 137: expected ClassSignal, got %s", class)
	}
}

func TestExtractArgv0(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd  string
		want string
	}{
		{"grep -r foo .", "grep"},
		{"rg pattern", "rg"},
		{"/usr/bin/diff a b", "diff"},
		{"single", "single"},
		{"  leading-space cmd", "leading-space"},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got := extractArgv0(tc.cmd)
			if got != tc.want {
				t.Errorf("extractArgv0(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}
