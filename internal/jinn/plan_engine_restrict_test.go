package jinn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPlanPhase1Restrictions(t *testing.T) {
	t.Parallel()

	t.Run("mutates_node_dangerous_blocked", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		targetPath := filepath.Join(dir, "should_not_exist")
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: true,
					Commands: []PlanOp{
						{Shell: "rm -rf " + targetPath},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopMutationBlocked {
			t.Errorf("expected StoppedReason %q, got %q", StopMutationBlocked, result.StoppedReason)
		}
		if len(result.Transcript) < 1 || len(result.Transcript[0].Ops) < 1 {
			t.Fatalf("expected at least one transcript entry with one op")
		}
		if result.Transcript[0].Ops[0].OK {
			t.Error("expected op OK == false for dangerous mutation without force flags")
		}
	})

	t.Run("shell_op_above_risk_safe_blocked", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: false,
					Commands: []PlanOp{
						{Shell: "rm file.txt"},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopMutationBlocked {
			t.Errorf("expected StoppedReason %q, got %q", StopMutationBlocked, result.StoppedReason)
		}
		if len(result.Transcript) < 1 || len(result.Transcript[0].Ops) < 1 {
			t.Fatalf("expected at least one transcript entry with one op")
		}
		if result.Transcript[0].Ops[0].OK {
			t.Error("expected first op OK == false for dangerous shell command")
		}
	})

	t.Run("tool_op_outside_allowlist_blocked", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		targetPath := filepath.Join(dir, "should_not_exist2.txt")
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: false,
					Commands: []PlanOp{
						{Tool: "write_file", Args: map[string]interface{}{"path": targetPath, "content": "x"}},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopMutationBlocked {
			t.Errorf("expected StoppedReason %q, got %q", StopMutationBlocked, result.StoppedReason)
		}
		_, statErr := os.Stat(targetPath)
		if !os.IsNotExist(statErr) {
			t.Errorf("expected file %q to not exist, but it does", targetPath)
		}
	})
}

func TestRunPlanGolden(t *testing.T) {
	t.Parallel()

	e, dir := testEngine(t)
	fixturePath := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(fixturePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := map[string]interface{}{
		"plan": map[string]interface{}{
			"root": "n1",
			"cwd":  dir,
			"nodes": []interface{}{
				map[string]interface{}{
					"id": "n1",
					"commands": []interface{}{
						map[string]interface{}{
							"tool": "stat_file",
							"args": map[string]interface{}{"path": "fixture.txt"},
						},
					},
					"edges": []interface{}{
						map[string]interface{}{
							"when": map[string]interface{}{"kind": "always"},
							"to":   "n2",
						},
					},
				},
				map[string]interface{}{
					"id": "n2",
					"commands": []interface{}{
						map[string]interface{}{
							"tool": "list_dir",
							"args": map[string]interface{}{"path": "."},
						},
					},
				},
			},
		},
	}

	tr, _, err := e.Dispatch(context.Background(), "run_plan", args)
	if err != nil {
		t.Fatalf("unexpected Dispatch error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	rawResult, ok := tr.Meta["plan_run"]
	if !ok {
		t.Fatal("expected Meta[\"plan_run\"] to be present")
	}
	result, ok := rawResult.(*PlanRunResult)
	if !ok {
		t.Fatalf("expected Meta[\"plan_run\"] to be *PlanRunResult, got %T", rawResult)
	}
	if result.StoppedReason != StopLeaf {
		t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
	}
	if len(result.PathTaken) != 2 {
		t.Errorf("expected PathTaken length 2, got %d: %v", len(result.PathTaken), result.PathTaken)
	}
	if len(result.PathTaken) >= 2 && (result.PathTaken[0] != "n1" || result.PathTaken[1] != "n2") {
		t.Errorf("expected PathTaken [n1 n2], got %v", result.PathTaken)
	}
	if len(result.Transcript) < 2 {
		t.Fatalf("expected at least 2 transcript entries, got %d", len(result.Transcript))
	}
	if !result.Transcript[0].Ops[0].OK {
		t.Errorf("expected n1 (stat_file) op OK == true, got false: %+v", result.Transcript[0].Ops[0])
	}
	if !result.Transcript[1].Ops[0].OK {
		t.Errorf("expected n2 (list_dir) op OK == true, got false: %+v", result.Transcript[1].Ops[0])
	}
}
