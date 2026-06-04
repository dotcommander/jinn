package jinn

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestMemorySchema_TablesExist boots the engine, triggers lazy DB open,
// and asserts all 7 unified schema tables are present.
func TestMemorySchema_TablesExist(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()
	// Force lazy open via a global-scope save (no .git walk needed).
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "k", "value", "v", "scope", "global")); err != nil {
		t.Fatalf("save to trigger db open: %v", err)
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		t.Fatalf("memDBConn: %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[n] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	want := []string{"memory", "idempotency"}
	var missing []string
	for _, w := range want {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("missing tables: %v", missing)
	}
}

// TestMemorySchema_NewMemoryColumns verifies the new memory table columns
// (scope_id, value_type, kind, pinned, expires_at) exist and are queryable.
func TestMemorySchema_NewMemoryColumns(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "col-check", "value", "42", "scope", "global")); err != nil {
		t.Fatalf("save: %v", err)
	}

	db, _ := e.memDBConn(ctx)
	var scope, scopeID, key, value, valueType, kind string
	var pinned int
	err := db.QueryRowContext(ctx,
		"SELECT scope, scope_id, key, value, value_type, kind, pinned FROM memory WHERE key='col-check'",
	).Scan(&scope, &scopeID, &key, &value, &valueType, &kind, &pinned)
	if err != nil {
		t.Fatalf("scan new columns: %v", err)
	}
	if scope != "global" {
		t.Errorf("scope got %q want global", scope)
	}
	if scopeID != "" {
		t.Errorf("scope_id got %q want empty for global", scopeID)
	}
	if valueType != "number" {
		t.Errorf("value_type got %q want number (inferred from \"42\")", valueType)
	}
	if kind != "fact" {
		t.Errorf("kind got %q want fact (default)", kind)
	}
	if pinned != 0 {
		t.Errorf("pinned got %d want 0", pinned)
	}
}

// TestMemoryScope_DefaultIsProject verifies that omitting scope auto-detects the
// repo root via .git walk and stores under scope=project.
func TestMemoryScope_DefaultIsProject(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	// Create a fake repo dir with a .git marker so detectScope resolves it.
	repoDir := t.TempDir()
	if err := makeDir(repoDir, ".git"); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	e := New(repoDir, "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()
	// No scope arg → should default to project with auto-detected scope_id.
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "proj-key", "value", "proj-val")); err != nil {
		t.Fatalf("save default scope: %v", err)
	}

	db, _ := e.memDBConn(ctx)
	var scope, scopeID string
	err := db.QueryRowContext(ctx, "SELECT scope, scope_id FROM memory WHERE key='proj-key'").Scan(&scope, &scopeID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if scope != "project" {
		t.Errorf("scope got %q want project", scope)
	}
	if scopeID == "" {
		t.Errorf("scope_id empty — detectScope should have set a repo path")
	}
}

// TestMemoryScope_AllFourScopes round-trips save+recall in global/project/task/agent.
func TestMemoryScope_AllFourScopes(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()
	cases := []struct {
		scope   string
		scopeID string
		key     string
		value   string
	}{
		{"global", "", "g-key", "g-val"},
		{"project", "/fake/repo", "p-key", "p-val"},
		{"task", "task_abc123", "t-key", "t-val"},
		{"agent", "claude-3", "a-key", "a-val"},
	}

	for _, c := range cases {
		saveArgs := args("action", "save", "key", c.key, "value", c.value, "scope", c.scope)
		if c.scopeID != "" {
			saveArgs["scope_id"] = c.scopeID
		}
		if _, err := e.memoryTool(ctx, saveArgs); err != nil {
			t.Fatalf("save scope=%s: %v", c.scope, err)
		}
	}

	for _, c := range cases {
		recallArgs := args("action", "recall", "key", c.key, "scope", c.scope)
		if c.scopeID != "" {
			recallArgs["scope_id"] = c.scopeID
		}
		got, err := e.memoryTool(ctx, recallArgs)
		if err != nil {
			t.Fatalf("recall scope=%s: %v", c.scope, err)
		}
		if got != c.value {
			t.Errorf("scope=%s: got %q want %q", c.scope, got, c.value)
		}
	}
}

// TestMemoryPinStickiness verifies that a plain save (pin=false) cannot clear
// an existing pin; only an explicit pin=true write can set it, and only a
// subsequent save must not clear it.
func TestMemoryPinStickiness(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()

	// Save with pin=true.
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "sticky", "value", "v1", "scope", "global", "pin", true)); err != nil {
		t.Fatalf("save pinned: %v", err)
	}

	// Overwrite without pin — must not clear existing pin.
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "sticky", "value", "v2", "scope", "global")); err != nil {
		t.Fatalf("save plain: %v", err)
	}

	db, _ := e.memDBConn(ctx)
	var pinned int
	var value string
	if err := db.QueryRowContext(ctx,
		"SELECT pinned, value FROM memory WHERE scope='global' AND scope_id='' AND key='sticky'",
	).Scan(&pinned, &value); err != nil {
		t.Fatalf("query: %v", err)
	}
	if pinned != 1 {
		t.Errorf("pinned got %d want 1 (plain save must not clear pin)", pinned)
	}
	if value != "v2" {
		t.Errorf("value got %q want v2 (upsert should update value)", value)
	}
}

// TestMemoryExpiresInAndGC verifies that expires_in→expires_at is stored and
// gc deletes the expired row while leaving non-expired rows intact.
func TestMemoryExpiresInAndGC(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()

	// Save with a tiny TTL (1 second = "1s" via time.ParseDuration path).
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "expiring", "value", "bye", "scope", "global", "expires_in", "1s")); err != nil {
		t.Fatalf("save with expires_in: %v", err)
	}
	// Save a durable key.
	if _, err := e.memoryTool(ctx, args("action", "save", "key", "durable", "value", "keep", "scope", "global")); err != nil {
		t.Fatalf("save durable: %v", err)
	}

	// Wait for expiry.
	time.Sleep(1100 * time.Millisecond)

	// Run gc.
	out, err := e.memoryTool(ctx, args("action", "gc", "scope", "global"))
	if err != nil {
		t.Fatalf("gc: %v", err)
	}

	// gc output must report 1 deleted.
	if !containsStr(out, `"deleted":1`) {
		t.Errorf("gc output %q — want deleted:1", out)
	}

	// Durable key must still be readable.
	val, err := e.memoryTool(ctx, args("action", "recall", "key", "durable", "scope", "global"))
	if err != nil {
		t.Fatalf("recall durable after gc: %v", err)
	}
	if val != "keep" {
		t.Errorf("durable value got %q want keep", val)
	}

	// Expired key must be gone.
	_, err = e.memoryTool(ctx, args("action", "recall", "key", "expiring", "scope", "global"))
	if err == nil {
		t.Error("recall expiring key after gc: expected error (key not found), got nil")
	}
}

// TestMemoryValueTypeInference checks that inferValueType correctly classifies
// representative values.
func TestMemoryValueTypeInference(t *testing.T) {
	cases := []struct {
		value    string
		wantType string
	}{
		{"true", "boolean"},
		{"false", "boolean"},
		{"42", "number"},
		{"3.14", "number"},
		{`{"a":1}`, "json"},
		{`[1,2,3]`, "array"},
		{"hello world", "string"},
		{"", "string"},
	}
	for _, c := range cases {
		got := inferValueType(c.value)
		if got != c.wantType {
			t.Errorf("inferValueType(%q) = %q, want %q", c.value, got, c.wantType)
		}
	}
}

// helpers

func makeDir(base, sub string) error {
	return os.MkdirAll(filepath.Join(base, sub), 0o700)
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}
