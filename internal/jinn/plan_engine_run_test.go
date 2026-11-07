package jinn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type cancelAfterErrChecksContext struct {
	context.Context
	allowErrChecks int32
	errChecks      atomic.Int32
}

func (c *cancelAfterErrChecksContext) Err() error {
	if c.errChecks.Add(1) > c.allowErrChecks {
		return context.Canceled
	}
	return nil
}

func TestRunPlanTree(t *testing.T) {
	t.Parallel()

	t.Run("leaf_success", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
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
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.PathTaken) != 1 || result.PathTaken[0] != "n1" {
			t.Errorf("expected PathTaken [n1], got %v", result.PathTaken)
		}
		if result.DepthReached != 0 {
			t.Errorf("expected DepthReached 0, got %d", result.DepthReached)
		}
		if len(result.Transcript) != 1 {
			t.Fatalf("expected 1 transcript entry, got %d", len(result.Transcript))
		}
		if !result.Transcript[0].Ops[0].OK {
			t.Error("expected first op OK == true")
		}
	})

	t.Run("first_match_wins_edge_ordering", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "always"}, To: "a"},
						{When: Condition{Kind: "always"}, To: "b"},
					},
				},
				{ID: "a", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
				{ID: "b", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.PathTaken) != 2 || result.PathTaken[0] != "n1" || result.PathTaken[1] != "a" {
			t.Errorf("expected PathTaken [n1 a], got %v", result.PathTaken)
		}
		if result.EdgesEvaluated != 1 || result.EdgesMatched != 1 {
			t.Errorf("edge counts = evaluated:%d matched:%d, want 1:1", result.EdgesEvaluated, result.EdgesMatched)
		}
	})

	t.Run("no_edge_match", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "exitCode", Op: "eq", Value: float64(999)}, To: "unreachable"},
					},
				},
				{ID: "unreachable", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopNoEdgeMatch {
			t.Errorf("expected StoppedReason %q, got %q", StopNoEdgeMatch, result.StoppedReason)
		}
	})

	t.Run("max_depth", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		plan := &PlanTree{
			Root:     "n1",
			MaxDepth: 2,
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "always"}, To: "n1"},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopMaxDepth {
			t.Errorf("expected StoppedReason %q, got %q", StopMaxDepth, result.StoppedReason)
		}
		if result.DepthReached <= 2 {
			t.Errorf("expected DepthReached > 2, got %d", result.DepthReached)
		}
	})

	t.Run("aborted", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{ID: "n1", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		result, err := e.runPlanTree(ctx, plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopAborted {
			t.Errorf("expected StoppedReason %q, got %q", StopAborted, result.StoppedReason)
		}
	})

	t.Run("transcript_shape", func(t *testing.T) {
		t.Parallel()
		e, _ := testEngine(t)
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
		if len(result.Transcript) != 1 {
			t.Fatalf("expected 1 transcript entry, got %d", len(result.Transcript))
		}
		if len(result.Transcript[0].Ops) != 1 {
			t.Fatalf("expected 1 op in transcript, got %d", len(result.Transcript[0].Ops))
		}
		if result.Transcript[0].Ops[0].Result == "" {
			t.Error("expected non-empty Result string")
		}
	})

	t.Run("cwd_resolution", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		cwd := filepath.Join(dir, "subdir")
		if err := os.Mkdir(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, "marker.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		plan := &PlanTree{
			Cwd:  "subdir",
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Shell: "pwd"}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "fileExists", Path: "marker.txt"}, To: "found"},
					},
				},
				{ID: "found", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.PathTaken) != 2 || result.PathTaken[0] != "n1" || result.PathTaken[1] != "found" {
			t.Errorf("expected PathTaken [n1 found], got %v", result.PathTaken)
		}
		if got := result.Transcript[0].Ops[0].Result; !strings.Contains(got, cwd) {
			t.Errorf("shell did not run in plan cwd %q: %s", cwd, got)
		}
		if op := result.Transcript[0].Ops[0]; op.Risk != RiskSafe.String() || op.Classification != string(ClassSuccess) {
			t.Errorf("pwd metadata = %+v, want safe successful original-command metadata", op)
		}
	})
}
