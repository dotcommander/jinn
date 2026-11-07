package jinn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPlanExpectGate(t *testing.T) {
	t.Parallel()

	t.Run("expect_passes_then_edges_fire", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Shell: `echo '{"status":"ok"}'`}},
					Expect:   &Condition{Kind: "jsonPath", Path: "status", Op: "eq", Value: "ok"},
					Edges:    []PlanEdge{{When: Condition{Kind: "always"}, To: "done"}},
				},
				{ID: "done", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		if err := validatePlan(plan); err != nil {
			t.Fatalf("unexpected validate error: %v", err)
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.PathTaken) != 2 || result.PathTaken[0] != "n1" || result.PathTaken[1] != "done" {
			t.Errorf("expected PathTaken [n1 done], got %v", result.PathTaken)
		}
		if result.EdgesEvaluated != 1 {
			t.Errorf("expected 1 edge evaluated, got %d", result.EdgesEvaluated)
		}
		if result.ExpectFailure != nil {
			t.Errorf("expected no ExpectFailure, got %+v", result.ExpectFailure)
		}
	})

	// Discriminating check: a malformed stage result ({} where status is
	// expected) must fail the node, evaluate zero edges, and stop naming
	// the failed expectation — not silently end no_edge_match or misroute.
	t.Run("expect_fails_no_edge_evaluated", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Shell: `echo '{}'`}},
					Expect:   &Condition{Kind: "jsonPath", Path: "status", Op: "eq", Value: "ok"},
					Edges:    []PlanEdge{{When: Condition{Kind: "always"}, To: "done"}},
				},
				{ID: "done", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopExpectFailed {
			t.Errorf("expected StoppedReason %q, got %q", StopExpectFailed, result.StoppedReason)
		}
		if result.EdgesEvaluated != 0 {
			t.Errorf("expected 0 edges evaluated on expect failure, got %d", result.EdgesEvaluated)
		}
		if len(result.PathTaken) != 1 || result.PathTaken[0] != "n1" {
			t.Errorf("expected PathTaken [n1], got %v", result.PathTaken)
		}
		if result.ExpectFailure == nil {
			t.Fatal("expected ExpectFailure to be set")
		}
		if result.ExpectFailure.Node != "n1" {
			t.Errorf("expected ExpectFailure.Node n1, got %q", result.ExpectFailure.Node)
		}
		if result.ExpectFailure.Condition.Kind != "jsonPath" || result.ExpectFailure.Condition.Path != "status" {
			t.Errorf("expected ExpectFailure condition jsonPath/status, got %+v", result.ExpectFailure.Condition)
		}
		if !strings.Contains(result.ExpectFailure.Value, "{}") {
			t.Errorf("expected ExpectFailure.Value to carry the offending result, got %q", result.ExpectFailure.Value)
		}
	})

	t.Run("invalid_expect_rejected_at_validation", func(t *testing.T) {
		t.Parallel()
		plan := &PlanTree{
			Root:  "n1",
			Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{{Shell: "true"}}, Expect: &Condition{Kind: "bogus"}}},
		}
		err := validatePlan(plan)
		if err == nil {
			t.Fatal("expected validation error for invalid expect")
		}
		if !strings.Contains(err.Error(), "invalid expect") {
			t.Errorf("expected error naming expect, got %v", err)
		}
	})

	t.Run("expect_string_shorthand_coerced", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"root": "n1",
			"nodes": []any{
				map[string]any{
					"id":       "n1",
					"commands": []any{map[string]any{"shell": "true"}},
					"expect":   "always",
				},
			},
		}
		plan, err := coercePlan(raw)
		if err != nil {
			t.Fatalf("unexpected coerce error: %v", err)
		}
		if plan.Nodes[0].Expect == nil || plan.Nodes[0].Expect.Kind != "always" {
			t.Errorf("expected coerced expect always, got %+v", plan.Nodes[0].Expect)
		}
	})
}

func TestRunPlanAuditTrail(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	plan := &PlanTree{
		Root: "n1",
		Nodes: []PlanNode{
			{ID: "n1", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
		},
	}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AuditPath == "" {
		t.Fatal("expected AuditPath to be set")
	}
	data, err := os.ReadFile(filepath.Join(dir, result.AuditPath))
	if err != nil {
		t.Fatalf("audit file not readable: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines (node + run), got %d: %s", len(lines), string(data))
	}
	var events []map[string]any
	for _, line := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not valid JSON: %v (%s)", err, line)
		}
		events = append(events, ev)
	}
	if events[0]["type"] != "node" || events[0]["node_id"] != "n1" {
		t.Errorf("expected first event node/n1, got %v", events[0])
	}
	if events[1]["type"] != "run" || events[1]["stopped_reason"] != string(StopLeaf) {
		t.Errorf("expected final run event with stopped_reason leaf, got %v", events[1])
	}
}
