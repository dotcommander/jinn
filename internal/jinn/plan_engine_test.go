package jinn

import (
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
				{ID: "n1"},
				{ID: "n1"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate node id:") {
			t.Errorf("expected 'duplicate node id:', got: %v", err)
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
				{ID: "n1"},
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
					ID: "n1",
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
					ID: "n1",
					Edges: []PlanEdge{
						{When: Condition{Kind: "match"}, To: "n2"},
					},
				},
				{ID: "n2", Mutates: true},
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
					ID: "n1",
					Edges: []PlanEdge{
						{When: Condition{Kind: "exitCode"}, To: "n2"},
					},
				},
				{ID: "n2", Mutates: true},
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
				{ID: "n1"},
			},
		})
		if err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})
}
