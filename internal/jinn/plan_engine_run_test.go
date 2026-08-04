package jinn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestRunPlanRejectsInvalidCwd(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	plan := &PlanTree{
		Cwd:   t.TempDir(),
		Root:  "n1",
		Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]any{"path": "."}}}}},
	}
	if _, err := e.runPlanTree(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "outside working directory") {
		t.Fatalf("runPlanTree() error = %v, want outside working directory", err)
	}
}

func TestRunPlanFailedToolDoesNotRouteAsSuccess(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	plan := &PlanTree{
		Root: "n1",
		Nodes: []PlanNode{
			{
				ID:       "n1",
				Commands: []PlanOp{{Tool: "read_file", Args: map[string]any{"path": "missing.txt"}}},
				Edges:    []PlanEdge{{When: Condition{Kind: "exitCode", Op: "eq", Value: 0}, To: "unexpected"}},
			},
			{ID: "unexpected", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]any{"path": "."}}}},
		},
	}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if result.StoppedReason != StopNoEdgeMatch {
		t.Fatalf("StoppedReason = %q, want %q", result.StoppedReason, StopNoEdgeMatch)
	}
	op := result.Transcript[0].Ops[0]
	if op.OK || op.ExitCode == 0 {
		t.Fatalf("failed tool result = %+v, want failed nonzero result", op)
	}
}

func TestRunPlanConditionErrorStopsWalk(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, "must-not-be-written.txt")
	args := map[string]any{
		"plan": map[string]any{
			"root": "guard",
			"nodes": []any{
				map[string]any{
					"id": "guard",
					"commands": []any{
						map[string]any{"tool": "list_dir", "args": map[string]any{"path": "."}},
					},
					"edges": []any{
						map[string]any{"when": map[string]any{"kind": "fileExists", "path": t.TempDir()}, "to": "mutate"},
						map[string]any{"when": map[string]any{"kind": "always"}, "to": "mutate"},
					},
				},
				map[string]any{
					"id":      "mutate",
					"mutates": true,
					"commands": []any{
						map[string]any{"tool": "write_file", "args": map[string]any{"path": target, "content": "must not execute"}},
					},
				},
			},
		},
	}
	tr, _, err := e.Dispatch(context.Background(), "run_plan", args)
	if err != nil {
		t.Fatalf("Dispatch(run_plan): %v", err)
	}
	result, ok := tr.Meta["plan_run"].(*PlanRunResult)
	if !ok {
		t.Fatalf("plan_run type = %T, want *PlanRunResult", tr.Meta["plan_run"])
	}
	if result.StoppedReason != StopError {
		t.Fatalf("StoppedReason = %q, want %q", result.StoppedReason, StopError)
	}
	if len(result.PathTaken) != 1 || result.PathTaken[0] != "guard" {
		t.Fatalf("PathTaken = %v, want [guard]", result.PathTaken)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("condition error allowed mutation: stat %q = %v", target, err)
	}
}

func TestRunPlanShellMetadataUsesOriginalCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	plan := &PlanTree{
		Root: "n1",
		Nodes: []PlanNode{{
			ID:       "n1",
			Commands: []PlanOp{{Shell: "grep -q absent /dev/null"}},
		}},
	}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	op := result.Transcript[0].Ops[0]
	if op.Risk != RiskSafe.String() {
		t.Errorf("risk = %q, want %q", op.Risk, RiskSafe.String())
	}
	if op.ExitCode != 1 || op.Classification != string(ClassExpectedNonzero) {
		t.Errorf("shell result = %+v, want grep's expected nonzero exit", op)
	}
	if !strings.Contains(op.Result, "[classification: expected_nonzero") {
		t.Errorf("result classification was not rewritten for original command: %q", op.Result)
	}
}

func TestRunPlanConditionSemantics(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	tests := []struct {
		name string
		last PlanOpResult
		cond Condition
		want bool
	}{
		{
			name: "empty regex remains valid",
			last: PlanOpResult{Result: "anything"},
			cond: Condition{Kind: "match", Stream: "stdout", Regex: ""},
			want: true,
		},
		{
			name: "negated exit code",
			last: PlanOpResult{ExitCode: 0},
			cond: Condition{Kind: "exitCode", Op: "eq", Value: 0, Negate: true},
			want: false,
		},
		{
			name: "negated numeric",
			last: PlanOpResult{Result: "7"},
			cond: Condition{Kind: "numeric", Op: "eq", Value: 7, Negate: true},
			want: false,
		},
		{
			name: "json does not coerce string to number",
			last: PlanOpResult{Result: `{"count":201}`},
			cond: Condition{Kind: "jsonPath", Path: "count", Op: "eq", Value: "201"},
			want: false,
		},
		{
			name: "negated json equality",
			last: PlanOpResult{Result: `{"count":201}`},
			cond: Condition{Kind: "jsonPath", Path: "count", Op: "eq", Value: 201, Negate: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := e.evaluateCondition(tt.last, tt.cond)
			if err != nil {
				t.Fatalf("evaluateCondition(): %v", err)
			}
			if got != tt.want {
				t.Errorf("evaluateCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunPlanParallelReads(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	commands := make([]PlanOp, 32)
	for i := range commands {
		name := filepath.Join("files", string(rune('a'+i))+".txt")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
		commands[i] = PlanOp{Tool: "read_file", Args: map[string]any{"path": name}}
	}
	plan := &PlanTree{Root: "n1", Nodes: []PlanNode{{ID: "n1", Parallel: true, Commands: commands}}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil {
		t.Fatalf("runPlanTree(): %v", err)
	}
	if result.StoppedReason != StopLeaf {
		t.Fatalf("StoppedReason = %q, want %q", result.StoppedReason, StopLeaf)
	}
	for _, op := range result.Transcript[0].Ops {
		if !op.OK {
			t.Fatalf("parallel read failed: %+v", op)
		}
	}
}

func TestRunPlanEvaluatesEdgesAgainstUntruncatedOutput(t *testing.T) {
	e, _ := testEngine(t)
	token := "tail-token-beyond-transcript-limit"
	plan := &PlanTree{Root: "first", Nodes: []PlanNode{
		{ID: "first", Commands: []PlanOp{{Shell: "printf '" + strings.Repeat("x", 2000) + token + "'"}}, Edges: []PlanEdge{{When: Condition{Kind: "match", Regex: token, Stream: "stdout"}, To: "done"}}},
		{ID: "done", Commands: []PlanOp{{Tool: "list_dir", Args: args("path", ".")}}},
	}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil || result.StoppedReason != StopLeaf || len(result.PathTaken) != 2 {
		t.Fatalf("raw edge result=%+v err=%v", result, err)
	}
	if strings.Contains(result.Transcript[0].Ops[0].Stdout, token) {
		t.Fatal("transcript retained unbounded raw output")
	}
}

func TestRunPlanOperationBudgetUsesResourceLimit(t *testing.T) {
	e, _ := testEngine(t)
	commands := make([]PlanOp, maxPlanCommands)
	for i := range commands {
		commands[i] = PlanOp{Tool: "list_dir", Args: args("path", ".")}
	}
	plan := &PlanTree{Root: "loop", MaxDepth: maxPlanDepth, Nodes: []PlanNode{{ID: "loop", Commands: commands, Edges: []PlanEdge{{When: Condition{Kind: "always"}, To: "loop"}}}}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil || result.StoppedReason != StopResourceLimit {
		t.Fatalf("budget result=%+v err=%v", result, err)
	}
}
