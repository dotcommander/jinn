package jinn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoercePlan(t *testing.T) {
	t.Parallel()

	t.Run("native map", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"root": "n1",
			"nodes": []any{
				map[string]any{
					"id": "n1",
					"commands": []any{
						map[string]any{"shell": "echo hi"},
					},
				},
			},
		}
		tree, err := coercePlan(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tree.Root != "n1" {
			t.Errorf("expected root 'n1', got %q", tree.Root)
		}
		if len(tree.Nodes) != 1 || tree.Nodes[0].ID != "n1" {
			t.Errorf("expected 1 node with id 'n1', got %d nodes", len(tree.Nodes))
		}
	})

	t.Run("json encoded string", func(t *testing.T) {
		t.Parallel()
		planJSON := `{"root":"n1","nodes":[{"id":"n1"}]}`
		tree, err := coercePlan(planJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tree.Root != "n1" {
			t.Errorf("expected root 'n1', got %q", tree.Root)
		}
	})

	t.Run("nodes as json string", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"root":  "n1",
			"nodes": `[{"id":"n1"}]`,
		}
		tree, err := coercePlan(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tree.Nodes) != 1 || tree.Nodes[0].ID != "n1" {
			t.Errorf("expected 1 node with id 'n1', got %d nodes", len(tree.Nodes))
		}
	})

	t.Run("edge when as shorthand string", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"root": "n1",
			"nodes": []any{
				map[string]any{
					"id": "n1",
					"edges": []any{
						map[string]any{
							"when": "always",
							"to":   "n2",
						},
					},
				},
				map[string]any{
					"id": "n2",
				},
			},
		}
		tree, err := coercePlan(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tree.Nodes[0].Edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(tree.Nodes[0].Edges))
		}
		if tree.Nodes[0].Edges[0].When.Kind != "always" {
			t.Errorf("expected Kind 'always', got %q", tree.Nodes[0].Edges[0].When.Kind)
		}
	})

	t.Run("edge when unrecognized shorthand", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"root": "n1",
			"nodes": []any{
				map[string]any{
					"id": "n1",
					"edges": []any{
						map[string]any{
							"when": "not-a-real-condition grammar",
							"to":   "n2",
						},
					},
				},
				map[string]any{
					"id": "n2",
				},
			},
		}
		_, err := coercePlan(raw)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not a recognized condition shorthand") {
			t.Errorf("expected 'not a recognized condition shorthand', got: %v", err)
		}
	})
}

func TestCoerceCondition(t *testing.T) {
	t.Parallel()

	t.Run("always", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("always")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "always" {
			t.Errorf("expected Kind 'always', got %q", c.Kind)
		}
	})

	t.Run("exit eq 0", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("exit eq 0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "exitCode" || c.Op != "eq" {
			t.Errorf("expected Kind=exitCode Op=eq, got Kind=%q Op=%q", c.Kind, c.Op)
		}
		// Value is float64 from JSON round-trip? No — coerceCondition returns directly, no JSON.
		if c.Value != 0 {
			t.Errorf("expected Value=0, got %v (%T)", c.Value, c.Value)
		}
	})

	t.Run("stdout =~ regex", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("stdout =~ /foo.*bar/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "match" || c.Stream != "stdout" || c.Regex != "foo.*bar" || c.Negate {
			t.Errorf("got Kind=%q Stream=%q Regex=%q Negate=%v", c.Kind, c.Stream, c.Regex, c.Negate)
		}
	})

	t.Run("stderr !~ regex", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("stderr !~ /err/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "match" || c.Stream != "stderr" || c.Regex != "err" || !c.Negate {
			t.Errorf("got Kind=%q Stream=%q Regex=%q Negate=%v", c.Kind, c.Stream, c.Regex, c.Negate)
		}
	})

	t.Run("file exists", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("file exists /tmp/x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "fileExists" || c.Path != "/tmp/x" || c.Negate {
			t.Errorf("got Kind=%q Path=%q Negate=%v", c.Kind, c.Path, c.Negate)
		}
	})

	t.Run("file missing", func(t *testing.T) {
		t.Parallel()
		c, err := coerceCondition("file missing /tmp/y")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Kind != "fileExists" || c.Path != "/tmp/y" || !c.Negate {
			t.Errorf("got Kind=%q Path=%q Negate=%v", c.Kind, c.Path, c.Negate)
		}
	})

	t.Run("unrecognized", func(t *testing.T) {
		t.Parallel()
		_, err := coerceCondition("garbage input")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not a recognized condition shorthand") {
			t.Errorf("expected 'not a recognized condition shorthand', got: %v", err)
		}
	})
}

func TestCoercePlanJSONRoundtrip(t *testing.T) {
	t.Parallel()
	// Verify that a coercePlan result roundtrips through JSON cleanly.
	raw := map[string]any{
		"root":  "n1",
		"cwd":   "/tmp",
		"nodes": []any{map[string]any{"id": "n1"}},
	}
	tree, err := coercePlan(raw)
	if err != nil {
		t.Fatalf("coercePlan: %v", err)
	}
	data, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back PlanTree
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Root != "n1" || back.Cwd != "/tmp" {
		t.Errorf("roundtrip mismatch: root=%q cwd=%q", back.Root, back.Cwd)
	}
}
