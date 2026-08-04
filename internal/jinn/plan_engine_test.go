package jinn

import (
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
		tt := tt
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
