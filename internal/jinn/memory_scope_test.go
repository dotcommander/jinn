package jinn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestMemory_ExplicitAbsScope(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)

	abs := t.TempDir()

	if _, err := e.memoryTool(context.Background(), args("action", "save", "key", "k", "value", "V", "scope", abs)); err != nil {
		t.Fatalf("save abs: %v", err)
	}

	out, err := e.memoryTool(context.Background(), args("action", "recall", "key", "k", "scope", abs))
	if err != nil {
		t.Fatalf("recall abs: %v", err)
	}
	if out != "V" {
		t.Errorf("recall value: %q", out)
	}

	_, err = e.memoryTool(context.Background(), args("action", "recall", "key", "k"))
	if err == nil {
		t.Fatal("expected key-not-found in current project scope")
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
	if !strings.Contains(ews.Suggestion, "global") || !strings.Contains(ews.Suggestion, "absolute") {
		t.Errorf("suggestion should mention 'global' and 'absolute': %q", ews.Suggestion)
	}
}

func TestMemory_MigrationFromLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	jinnDir := filepath.Join(dir, "jinn")
	if err := os.MkdirAll(jinnDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyPath := filepath.Join(jinnDir, "memory.json")
	legacy := `{"version":1,"entries":{"legacy.key":{"value":"oldval","updated":"2020-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	e, _ := testEngine(t)

	out, err := e.memoryTool(context.Background(), args("action", "recall", "key", "legacy.key", "scope", "global"))
	if err != nil {
		t.Fatalf("recall legacy: %v", err)
	}
	if out != "oldval" {
		t.Errorf("recall value: %q", out)
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("memory.json should be gone after migration, stat err: %v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Errorf("memory.json.migrated should exist after migration: %v", err)
	}

	// Idempotency: a second recall still succeeds without re-migrating.
	out, err = e.memoryTool(context.Background(), args("action", "recall", "key", "legacy.key", "scope", "global"))
	if err != nil {
		t.Fatalf("second recall: %v", err)
	}
	if out != "oldval" {
		t.Errorf("second recall value: %q", out)
	}
}
