package jinn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
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

func TestRunShell_RequiresCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, _, err := e.runShell(context.Background(), args("command", "   "))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected ErrWithSuggestion, got %T", err)
	}
	if sErr.Code != ErrCodeInvalidArgs {
		t.Fatalf("Code = %q, want %q", sErr.Code, ErrCodeInvalidArgs)
	}
}

func TestRunShell_NilContextPanics(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil ctx, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "nil ctx") {
			t.Fatalf("expected panic message containing 'nil ctx', got %v", r)
		}
	}()
	_, _, _ = e.runShell(nil, args("command", "echo ok")) //nolint:staticcheck // intentional nil ctx: this test asserts runShell panics on a nil context
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
	result, meta, err := e.runShell(context.Background(), args("command", "rm -rf /", "dry_run", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[dry-run]") {
		t.Errorf("expected dry-run prefix, got: %s", result)
	}
	if !strings.Contains(result, "rm -rf /") {
		t.Errorf("expected command in output, got: %s", result)
	}
	if meta["risk"] != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", meta["risk"], RiskDangerous)
	}
	if meta["classification"] != string(ClassSuccess) {
		t.Errorf("classification = %q, want %q", meta["classification"], ClassSuccess)
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

func TestRunShell_OverflowSpillKeepsFullOutputAndResponseTail(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	cmd := `i=0; while [ $i -lt 18000 ]; do printf 'line%05d padding padding padding padding padding padding padding padding padding padding\n' "$i"; i=$((i+1)); done; echo SENTINEL_END`
	result, _, err := e.runShell(context.Background(), args("command", cmd, "timeout", float64(20)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "SENTINEL_END") {
		t.Fatalf("response tail lost final output: %s", result[len(result)-300:])
	}
	path := shellSpillPath(result)
	if path == "" {
		t.Fatalf("expected Full output path, got: %s", result[len(result)-300:])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if !strings.Contains(string(data), "line00000") || !strings.Contains(string(data), "SENTINEL_END") {
		t.Fatalf("spill should contain first and last output, got len=%d", len(data))
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

func TestRunShell_CancelKillsBackgroundProcesses(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	pidFile := t.TempDir() + "/child.pid"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type runResult struct {
		result string
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, _, err := e.runShell(ctx, args(
			"command", fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile),
			"timeout", float64(30),
		))
		done <- runResult{result: result, err: err}
	}()

	pid := waitForPIDFile(t, pidFile)
	cancel()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("expected cancellation error")
		}
		var sErr *ErrWithSuggestion
		if !errors.As(r.err, &sErr) {
			t.Fatalf("expected ErrWithSuggestion, got %T: %v", r.err, r.err)
		}
		if sErr.Code != ErrCodeCanceled {
			t.Fatalf("expected Code=%q, got %q", ErrCodeCanceled, sErr.Code)
		}
		if r.result != "" {
			t.Fatalf("expected empty result on cancellation error, got: %s", r.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runShell did not return promptly after cancellation")
	}

	waitForProcessExit(t, pid)
}

func TestSubprocessEnv_AllowsExplicitOverlayWithoutSecrets(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("OPENAI_API_KEY", "secret")
	t.Setenv("GITHUB_TOKEN", "secret")

	env := subprocessEnv(map[string]string{"GOCACHE": "/tmp/jinn-cache"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/bin") {
		t.Fatalf("expected PATH allowlist entry, got %#v", env)
	}
	if !strings.Contains(joined, "GOCACHE=/tmp/jinn-cache") {
		t.Fatalf("expected explicit GOCACHE overlay, got %#v", env)
	}
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "GITHUB_TOKEN") {
		t.Fatalf("secret-bearing env leaked into subprocess env: %#v", env)
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

func shellSpillPath(result string) string {
	const marker = "Full output: "
	idx := strings.LastIndex(result, marker)
	if idx < 0 {
		return ""
	}
	rest := result[idx+len(marker):]
	if end := strings.Index(rest, "]"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
			lastErr = convErr
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child pid file was not written: %v", lastErr)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("background child process %d survived run_shell cancellation", pid)
}
