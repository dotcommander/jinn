package jinn

import (
	"context"
	"sort"
	"testing"
)

// TestContinuitySchema_TablesExist boots the engine under JINN_CONFIG_DIR
// isolation, triggers the lazy memory.db open, and asserts the flattened vybe
// continuity tables were created alongside jinn's existing memory table.
func TestContinuitySchema_TablesExist(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })

	ctx := context.Background()
	// Force lazy open of e.memDB.
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
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		got[n] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("rows err: %v", err)
	}
	rows.Close()

	want := []string{"memory", "events", "tasks", "agent_state", "artifacts", "projects", "idempotency"}
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

	// jinn's existing memory table must remain unchanged (4-col, value still readable).
	var v string
	if err := db.QueryRowContext(ctx, "SELECT value FROM memory WHERE scope='global' AND key='k'").Scan(&v); err != nil {
		t.Fatalf("memory tool semantics broken after schema add: %v", err)
	}
	if v != "v" {
		t.Fatalf("memory value got %q want %q", v, "v")
	}
}
