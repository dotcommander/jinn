package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidatePlan(t *testing.T) {
	t.Parallel()

	t.Run("empty nodes", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{Root: "n1"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "plan has no nodes") {
			t.Errorf("expected 'plan has no nodes', got: %v", err)
		}
	})

	t.Run("duplicate node id", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{ID: "n1", Commands: []PlanOp{{Tool: "list_dir"}}},
				{ID: "n1", Commands: []PlanOp{{Tool: "list_dir"}}},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate node id:") {
			t.Errorf("expected 'duplicate node id:', got: %v", err)
		}
	})

	t.Run("parallel mutating node", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{ID: "n1", Parallel: true, Mutates: true},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var structured *ErrWithSuggestion
		if !errors.As(err, &structured) {
			t.Fatalf("error type = %T, want *ErrWithSuggestion", err)
		}
		if structured.Code != ErrCodePlanInvalid {
			t.Errorf("error code = %q, want %q", structured.Code, ErrCodePlanInvalid)
		}
		if !strings.Contains(err.Error(), "cannot combine parallel and mutates") {
			t.Errorf("expected parallel mutation error, got: %v", err)
		}
	})

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Nodes: []PlanNode{
				{ID: "n1"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "plan has no root node") {
			t.Errorf("expected 'plan has no root node', got: %v", err)
		}
	})

	t.Run("root not found", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n99",
			Nodes: []PlanNode{
				{ID: "n1", Commands: []PlanOp{{Tool: "list_dir"}}},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "root node") || !strings.Contains(msg, "not found") {
			t.Errorf("expected 'root node ... not found', got: %v", err)
		}
	})

	t.Run("edge targets unknown node", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir"}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "always"}, To: "n99"},
					},
				},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "targets unknown node") {
			t.Errorf("expected 'targets unknown node', got: %v", err)
		}
	})

	t.Run("tier rule violation", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir"}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "match", Stream: "stdout", Regex: "ok"}, To: "n2"},
					},
				},
				{ID: "n2", Mutates: true, Commands: []PlanOp{{Tool: "write_file"}}},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "low-confidence") || !strings.Contains(msg, "cannot gate mutating node") {
			t.Errorf("expected 'low-confidence ... cannot gate mutating node', got: %v", err)
		}
	})

	t.Run("tier rule pass on high-confidence edge", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:       "n1",
					Commands: []PlanOp{{Tool: "list_dir"}},
					Edges: []PlanEdge{
						{When: Condition{Kind: "exitCode", Op: "eq", Value: 0}, To: "n2"},
					},
				},
				{ID: "n2", Mutates: true, Commands: []PlanOp{{Tool: "write_file"}}},
			},
		})
		if err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})

	t.Run("minimal valid", func(t *testing.T) {
		t.Parallel()
		err := validatePlan(&PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{ID: "n1", Commands: []PlanOp{{Tool: "list_dir"}}},
			},
		})
		if err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})
}

func TestValidatePlanRejectsUnsafeShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan *PlanTree
		want string
	}{
		{
			name: "empty node",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{ID: "n1"}}},
			want: "has no commands",
		},
		{
			name: "dual operation",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{{Shell: "pwd", Tool: "list_dir"}}}}},
			want: "exactly one",
		},
		{
			name: "unknown tool",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{{Tool: "not_a_tool"}}}}},
			want: "unknown tool",
		},
		{
			name: "malformed condition",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{
				ID:       "n1",
				Commands: []PlanOp{{Tool: "list_dir"}},
				Edges:    []PlanEdge{{When: Condition{Kind: "exitCode", Op: "wat", Value: 0}, To: "n1"}},
			}}},
			want: "invalid condition",
		},
		{
			name: "always with comparison fields",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{
				ID:       "n1",
				Commands: []PlanOp{{Tool: "list_dir"}},
				Edges:    []PlanEdge{{When: Condition{Kind: "always", Op: "eq"}, To: "n1"}},
			}}},
			want: "invalid condition",
		},
		{
			name: "numeric extract without capture group",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{
				ID:       "n1",
				Commands: []PlanOp{{Tool: "list_dir"}},
				Edges:    []PlanEdge{{When: Condition{Kind: "numeric", Op: "eq", Value: 1, Extract: `[0-9]+`}, To: "n1"}},
			}}},
			want: "invalid condition",
		},
		{
			name: "json path null value",
			plan: &PlanTree{Root: "n1", Nodes: []PlanNode{{
				ID:       "n1",
				Commands: []PlanOp{{Tool: "list_dir"}},
				Edges:    []PlanEdge{{When: Condition{Kind: "jsonPath", Path: "count", Op: "eq", Value: nil}, To: "n1"}},
			}}},
			want: "invalid condition",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePlan(tt.plan)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validatePlan() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestPlanShellCommandPreservesDangerousClassification(t *testing.T) {
	t.Parallel()
	risk, _ := ClassifyCommand(planShellCommand(t.TempDir(), "rm disposable.txt"))
	if risk != RiskDangerous {
		t.Fatalf("cwd-wrapped rm risk = %s, want dangerous", risk)
	}
}

func TestRewritePlanShellClassificationPreservesHint(t *testing.T) {
	t.Parallel()
	input := "[exit: 1]\n[classification: error — old]\n[hint: keep this]"
	got := rewritePlanShellClassification(input, "expected_nonzero", "no matches")
	if !strings.Contains(got, "[classification: expected_nonzero — no matches]") {
		t.Errorf("rewritten classification missing: %q", got)
	}
	if !strings.Contains(got, "[hint: keep this]") {
		t.Errorf("hint was dropped: %q", got)
	}
}

func TestPlanLimitsAndTranscriptBudget(t *testing.T) {
	plan := &PlanTree{Root: "n", MaxDepth: maxPlanDepth + 1, Nodes: []PlanNode{{ID: "n", Commands: []PlanOp{{Tool: "list_dir"}}}}}
	if err := validatePlan(plan); err == nil {
		t.Fatal("depth cap accepted")
	}
	plan.MaxDepth = 0
	plan.Nodes[0].Commands = make([]PlanOp, maxPlanCommands+1)
	if err := validatePlan(plan); err == nil {
		t.Fatal("command cap accepted")
	}
	plan.Nodes[0].Commands = []PlanOp{{Tool: "list_dir"}}
	plan.Nodes[0].Edges = make([]PlanEdge, maxPlanEdges+1)
	if err := validatePlan(plan); err == nil {
		t.Fatal("edge cap accepted")
	}
	plan.Nodes[0].Edges = nil
	plan.Nodes = make([]PlanNode, maxPlanNodes+1)
	plan.Nodes[0] = PlanNode{ID: "n", Commands: []PlanOp{{Tool: "list_dir"}}}
	if err := validatePlan(plan); err == nil {
		t.Fatal("node cap accepted")
	}
	result := PlanRunResult{Transcript: make([]PlanNodeResult, 16)}
	for i := range result.Transcript {
		result.Transcript[i].NodeID = strings.Repeat("\x00", 256)
		for j := 0; j < maxPlanCommands; j++ {
			result.Transcript[i].Ops = append(result.Transcript[i].Ops, boundPlanOpResult(PlanOpResult{Result: strings.Repeat("\x00", 1<<20), Error: strings.Repeat("\x00", 1<<20), Stdout: strings.Repeat("\x00", 1<<20), Stderr: strings.Repeat("\x00", 1<<20)}))
		}
	}
	compactPlanTranscript(result.Transcript)
	data, err := json.Marshal(result)
	if err != nil || len(data) > maxPlanTranscript {
		t.Fatalf("transcript=%d err=%v", len(data), err)
	}
	max := PlanRunResult{Transcript: make([]PlanNodeResult, 29), PathTaken: make([]string, 29), StoppedReason: StopResourceLimit}
	for i := range max.Transcript {
		max.Transcript[i].NodeID = strings.Repeat("\x00", 256)
		max.PathTaken[i] = strings.Repeat("\x00", 256)
		opCount := 1
		if i < 15 {
			opCount = maxPlanCommands
		}
		for j := 0; j < opCount; j++ {
			max.Transcript[i].Ops = append(max.Transcript[i].Ops, boundPlanOpResultLimit(PlanOpResult{Result: strings.Repeat("\x00", 1024), Error: strings.Repeat("\x00", 1024), Stdout: strings.Repeat("\x00", 1024), Stderr: strings.Repeat("\x00", 1024)}, 1024))
		}
	}
	max.Transcript, max.PathTaken = fitPlanTranscript(max.Transcript, max.PathTaken, 29, 254, 29)
	max.StoppedReason = StopMutationBlocked
	data, err = json.Marshal(max)
	if err != nil || len(data) > maxPlanTranscript {
		t.Fatalf("max serialized transcript=%d err=%v", len(data), err)
	}
	e, _ := testEngine(t)
	plan = &PlanTree{Root: "n", Nodes: []PlanNode{{ID: "n", Commands: []PlanOp{{Tool: "list_dir"}}}}}
	if _, err := e.runPlanTree(context.Background(), plan); err != nil {
		t.Fatalf("valid bounded plan: %v", err)
	}
}
