package jinn

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestShellRisk_SafeCommand_Allowed(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, meta, err := e.runShell(context.Background(), args("command", "echo hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["risk"] != "safe" {
		t.Errorf("expected risk=safe, got %q", meta["risk"])
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output, got: %s", result)
	}
}

func TestShellRisk_DangerousCommand_Blocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "rm -rf /tmp/jinntest"))
	if err == nil {
		t.Fatal("expected error for dangerous command")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if meta["risk"] != "dangerous" {
		t.Errorf("expected risk=dangerous in meta, got %q", meta["risk"])
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(sErr.Suggestion, "force:true") {
		t.Errorf("expected force:true in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestShellRisk_DangerousCommand_Force(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create a file to rm so the command actually runs.
	writeTestFile(t, dir, "canary.txt", "delete me")
	result, meta, err := e.runShell(context.Background(), args(
		"command", "rm "+dir+"/canary.txt",
		"force", true,
	))
	if err != nil {
		t.Fatalf("unexpected error with force=true: %v", err)
	}
	if meta["risk"] != "dangerous" {
		t.Errorf("expected risk=dangerous, got %q", meta["risk"])
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected successful exit, got: %s", result)
	}
}

func TestShellRisk_MetaClassification(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "echo ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["classification"] != "success" {
		t.Errorf("expected classification=success, got %q", meta["classification"])
	}
}

func TestDispatch_MemoryRoute(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// list action requires no key — just verify the route reaches memoryTool.
	result, _, err := e.Dispatch(context.Background(), "memory", args("action", "list"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "keys") {
		t.Errorf("expected keys field in list result, got: %s", result.Text)
	}
}
