package jinn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedRunPlanCannotElevateForce(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	victim := filepath.Join(dir, "nested-force-victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	child := map[string]any{
		"root":  "child",
		"force": true,
		"nodes": []any{map[string]any{
			"id":      "child",
			"mutates": true,
			"force":   true,
			"commands": []any{map[string]any{
				"shell": "rm " + shellQuote(victim),
			}},
		}},
	}
	plan := &PlanTree{
		Root: "outer",
		Nodes: []PlanNode{{
			ID:      "outer",
			Mutates: true,
			Commands: []PlanOp{{Tool: "run_plan", Args: map[string]any{
				"plan": child,
			}}},
		}},
	}

	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("nested child force elevated parent authority: %v", err)
	}
	op := result.Transcript[0].Ops[0]
	if op.OK || op.ExitCode == 0 || !strings.Contains(op.Error, string(StopMutationBlocked)) {
		t.Fatalf("nested child result = %+v, want blocked failure", op)
	}
}

func TestNestedRunPlanUsesInheritedForceWhenAuthorized(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	victim := filepath.Join(dir, "nested-authorized-victim.txt")
	if err := os.WriteFile(victim, []byte("remove"), 0o644); err != nil {
		t.Fatal(err)
	}

	child := map[string]any{
		"root":  "child",
		"force": true,
		"nodes": []any{map[string]any{
			"id":      "child",
			"mutates": true,
			"force":   true,
			"commands": []any{map[string]any{
				"shell": "rm " + shellQuote(victim),
			}},
		}},
	}
	plan := &PlanTree{
		Root:  "outer",
		Force: true,
		Nodes: []PlanNode{{
			ID:      "outer",
			Mutates: true,
			Force:   true,
			Commands: []PlanOp{{Tool: "run_plan", Args: map[string]any{
				"plan": child,
			}}},
		}},
	}

	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if result.StoppedReason != StopLeaf || !result.Transcript[0].Ops[0].OK {
		t.Fatalf("authorized nested plan = %+v, want successful leaf", result)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("authorized nested plan did not delete victim: %v", err)
	}
}

func TestNestedRunPlanFailureCannotTriggerFallbackMutation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	victim := filepath.Join(dir, "nested-fallback-victim.txt")
	fallback := filepath.Join(dir, "fallback-must-not-exist.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	child := map[string]any{
		"root":  "child",
		"force": true,
		"nodes": []any{map[string]any{
			"id":      "child",
			"mutates": true,
			"force":   true,
			"commands": []any{map[string]any{
				"shell": "rm " + shellQuote(victim),
			}},
		}},
	}
	plan := &PlanTree{
		Root: "outer",
		Nodes: []PlanNode{
			{
				ID:      "outer",
				Mutates: true,
				Commands: []PlanOp{{Tool: "run_plan", Args: map[string]any{
					"plan": child,
				}}},
				Edges: []PlanEdge{{When: Condition{Kind: "exitCode", Op: "eq", Value: 0}, To: "fallback"}},
			},
			{
				ID:      "fallback",
				Mutates: true,
				Commands: []PlanOp{{Tool: "write_file", Args: map[string]any{
					"path": fallback, "content": "must not execute",
				}}},
			},
		},
	}

	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("blocked nested child mutated victim: %v", err)
	}
	if _, err := os.Stat(fallback); !os.IsNotExist(err) {
		t.Fatalf("blocked nested child triggered fallback mutation: stat = %v", err)
	}
	if len(result.PathTaken) != 1 || result.PathTaken[0] != "outer" {
		t.Fatalf("PathTaken = %v, want [outer]", result.PathTaken)
	}
	if op := result.Transcript[0].Ops[0]; op.OK || op.ExitCode == 0 {
		t.Fatalf("nested child result = %+v, want failed nonzero result", op)
	}
}

func TestNestedRunPlanHonorsParentMaxDepth(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, "depth-limit-target.txt")

	var nestedPlan func(int) map[string]any
	nestedPlan = func(depth int) map[string]any {
		id := fmt.Sprintf("node-%d", depth)
		var command map[string]any
		if depth == 0 {
			command = map[string]any{
				"tool": "write_file",
				"args": map[string]any{"path": target, "content": "must not execute"},
			}
		} else {
			command = map[string]any{
				"tool": "run_plan",
				"args": map[string]any{"plan": nestedPlan(depth - 1)},
			}
		}
		return map[string]any{
			"root":      id,
			"max_depth": 1,
			"nodes": []any{map[string]any{
				"id":       id,
				"mutates":  true,
				"commands": []any{command},
			}},
		}
	}

	outer := nestedPlan(4)
	plan, err := coercePlan(outer)
	if err != nil {
		t.Fatalf("coercePlan(): %v", err)
	}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("nested plans ignored parent max_depth: stat = %v", err)
	}
	if op := result.Transcript[0].Ops[0]; op.OK || op.ExitCode == 0 {
		t.Fatalf("nested depth result = %+v, want failed nonzero result", op)
	}
}

func TestRunPlanAllowsDocumentedReadOnlyMetaActions(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)
	if _, err := e.memoryTool(context.Background(), args("action", "save", "key", "read-only", "value", "ok")); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	tests := []struct {
		name string
		op   PlanOp
	}{
		{name: "memory_recall", op: PlanOp{Tool: "memory", Args: args("action", "recall", "key", "read-only")}},
		{name: "memory_list", op: PlanOp{Tool: "memory", Args: args("action", "list")}},
		{name: "undo_list", op: PlanOp{Tool: "undo", Args: args("action", "list")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := e.runPlanTree(context.Background(), &PlanTree{
				Root:  "n1",
				Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{tt.op}}},
			})
			if err != nil {
				t.Fatalf("runPlanTree(): %v", err)
			}
			if result.StoppedReason != StopLeaf {
				t.Fatalf("StoppedReason = %q, want %q", result.StoppedReason, StopLeaf)
			}
			if op := result.Transcript[0].Ops[0]; !op.OK {
				t.Fatalf("read-only operation was blocked: %+v", op)
			}
		})
	}
}
