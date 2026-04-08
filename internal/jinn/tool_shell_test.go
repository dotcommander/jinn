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
