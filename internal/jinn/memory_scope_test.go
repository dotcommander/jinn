package jinn

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Scope tests are NOT parallel: they use t.Setenv (process-wide), which is
// incompatible with t.Parallel(). Distinct scopes are obtained via distinct
// t.TempDir() working directories; behavior (found / not-found) is asserted,
// never specific auto-scope path strings (those are tempdirs).

func TestMemory_ScopeIsolation(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	tmpA := t.TempDir()
	tmpB := t.TempDir()
	engine1 := New(tmpA, "dev")
	engine2 := New(tmpB, "dev")

	if _, err := engine1.memoryTool(context.Background(), args("action", "save", "key", "k", "value", "A")); err != nil {
		t.Fatalf("engine1 save: %v", err)
	}

	_, err := engine2.memoryTool(context.Background(), args("action", "recall", "key", "k"))
	if err == nil {
		t.Fatal("expected key-not-found across distinct project scopes")
	}
	if !strings.Contains(err.Error(), "key not found") {
		t.Errorf("error text: %q", err.Error())
	}
}

func TestMemory_GlobalScope(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)

	out, err := e.memoryTool(context.Background(), args("action", "save", "key", "g", "value", "V", "scope", "global"))
	if err != nil {
		t.Fatalf("save global: %v", err)
	}
	if out != "saved: g" {
		t.Errorf("save output: %q", out)
	}

	out, err = e.memoryTool(context.Background(), args("action", "recall", "key", "g", "scope", "global"))
	if err != nil {
		t.Fatalf("recall global: %v", err)
	}
	if out != "V" {
		t.Errorf("recall value: %q", out)
	}

	_, err = e.memoryTool(context.Background(), args("action", "recall", "key", "g"))
	if err == nil {
		t.Fatal("expected key-not-found in current project scope")
	}
	if !strings.Contains(err.Error(), "key not found") {
		t.Errorf("error text: %q", err.Error())
	}
}

// TestMemory_ExplicitProjectScopeID verifies that passing scope="project" with
// an explicit scope_id isolates from the auto-detected project scope.
func TestMemory_ExplicitProjectScopeID(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)

	explicitID := t.TempDir()

	if _, err := e.memoryTool(context.Background(), args("action", "save", "key", "k", "value", "V", "scope", "project", "scope_id", explicitID)); err != nil {
		t.Fatalf("save explicit project scope: %v", err)
	}

	out, err := e.memoryTool(context.Background(), args("action", "recall", "key", "k", "scope", "project", "scope_id", explicitID))
	if err != nil {
		t.Fatalf("recall explicit project scope: %v", err)
	}
	if out != "V" {
		t.Errorf("recall value: %q", out)
	}

	// Must not appear in default (auto-detected) project scope.
	_, err = e.memoryTool(context.Background(), args("action", "recall", "key", "k"))
	if err == nil {
		t.Fatal("expected key-not-found in default project scope")
	}
	if !strings.Contains(err.Error(), "key not found") {
		t.Errorf("error text: %q", err.Error())
	}
}

func TestMemory_JunkScopeRejected(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)

	_, err := e.memoryTool(context.Background(), args("action", "save", "key", "k", "value", "V", "scope", "relative/junk"))
	if err == nil {
		t.Fatal("expected error for junk scope")
	}
	var ews *ErrWithSuggestion
	if !errors.As(err, &ews) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	// New scope model: valid values are global|project|task|agent.
	if !strings.Contains(ews.Suggestion, "global") || !strings.Contains(ews.Suggestion, "project") {
		t.Errorf("suggestion should mention valid scopes: %q", ews.Suggestion)
	}
}

// TestMemory_LegacyJSONIgnored verifies that a stale memory.json file is
// simply ignored — no migration, no error — since migration was removed.
func TestMemory_LegacyJSONIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	// Write a legacy file; engine should not read or rename it.
	if err := makeDir(dir, "jinn"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Engine starts cleanly; legacy.key is NOT imported.
	e, _ := testEngine(t)
	_, err := e.memoryTool(context.Background(), args("action", "recall", "key", "legacy.key", "scope", "global"))
	if err == nil {
		t.Fatal("expected key-not-found — legacy JSON must not be auto-imported")
	}
	if !strings.Contains(err.Error(), "key not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
