package jinn

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunShell_Echo(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, _, err := e.runShell(context.Background(), args("command", "echo hello"))
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
	result, _, err := e.runShell(context.Background(), args("command", "exit 42"))
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
	result, _, err := e.runShell(context.Background(), args("command", "echo ok", "timeout", float64(9999)))
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
	result, _, err := e.runShell(context.Background(), args("command", "rm -rf /", "dry_run", true))
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
	result, _, err := e.runShell(context.Background(), args("command", "for i in $(seq 1 10); do echo same; done"))
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
	result, _, err := e.runShell(context.Background(), args("command", "echo hi"))
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
	result, _, err := e.runShell(context.Background(), args("command", "grep ZZZNOMATCH /dev/null; true"))
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

func TestRunShell_LargeOutput_Truncation(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// Generate 3000 lines of output — should trigger line-limit truncation.
	result, _, err := e.runShell(context.Background(), args("command", "for i in $(seq 1 3000); do echo \"line $i\"; done"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected exit 0, got: %s", result[:200])
	}
	if !strings.Contains(result, "Showing") || !strings.Contains(result, "Full output:") {
		t.Errorf("expected truncation notice with temp file path, got:\n%s", result[len(result)-300:])
	}
	if !strings.Contains(result, "of 3000 lines") {
		t.Errorf("expected 'of 3000 lines' in truncation notice, got tail:\n%s", result[len(result)-300:])
	}
}

func TestRunShell_LargeBytes_Truncation(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// Generate output exceeding 50KB with unique lines (avoid collapseRepeatedLines).
	// Each line ~600 bytes, 100 lines = ~60KB.
	result, _, err := e.runShell(context.Background(), args("command", "for i in $(seq 1 100); do printf \"line$i %0.sx\" $(seq 1 550); echo; done"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected exit 0")
	}
	if !strings.Contains(result, "Showing") {
		t.Errorf("expected truncation notice for byte-limit, got tail:\n%s", result[len(result)-300:])
	}
}

func TestRunShell_TimeoutEnforcedWithoutTimeoutBinary(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// sleep 10 with timeout=1; our pgid-based timer must fire well before 10s.
	result, _, err := e.runShell(context.Background(), args("command", "sleep 10", "timeout", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("expected 'timed out' in result, got: %s", result)
	}
	if !strings.Contains(result, "[exit: 124]") {
		t.Errorf("expected exit 124, got: %s", result)
	}
}

func TestRunShell_KillsBackgroundProcesses(t *testing.T) {
	t.Parallel()
	// Launch a background sleep that would outlive a normal child-only kill,
	// then wait so the shell itself blocks until either the sleep finishes or
	// the process group is killed. With timeout=2 the pgid SIGKILL must reap
	// the background sleep before it reaches 30s.
	e, _ := testEngine(t)
	start := time.Now()
	result, _, err := e.runShell(context.Background(), args(
		"command", "sleep 30 & echo started; wait",
		"timeout", float64(2),
	))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("process group kill should have fired within ~2s, elapsed: %v", elapsed)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("expected 'timed out' in result, got: %s", result)
	}
}

func TestRunShell_SmallOutput_NoTruncation(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, _, err := e.runShell(context.Background(), args("command", "echo hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "Showing") {
		t.Errorf("small output should NOT be truncated, got: %s", result)
	}
	if strings.Contains(result, "Full output:") {
		t.Errorf("small output should NOT have temp file reference")
	}
}
