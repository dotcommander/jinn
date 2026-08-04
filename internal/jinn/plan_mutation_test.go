package jinn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanMutation(t *testing.T) {
	// Serial-only: uses historyEngine which calls t.Setenv.
	t.Run("caution_risk_executes_and_records_undo", func(t *testing.T) {
		e, dir := historyEngine(t)

		targetPath := filepath.Join(dir, "output.txt")
		content := "caution write test"
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: true,
					Commands: []PlanOp{
						{Tool: "write_file", Args: map[string]any{"path": targetPath, "content": content}},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}

		got, readErr := os.ReadFile(targetPath)
		if readErr != nil {
			t.Fatalf("expected file to exist: %v", readErr)
		}
		if string(got) != content {
			t.Errorf("file content: got %q, want %q", string(got), content)
		}

		hf, loadErr := e.loadHistory()
		if loadErr != nil {
			t.Fatalf("loadHistory: %v", loadErr)
		}
		if len(hf.Entries) != 1 {
			t.Fatalf("history entries: got %d, want 1", len(hf.Entries))
		}
		if hf.Entries[0].Op != "write_file" {
			t.Errorf("history entry op: got %q, want write_file", hf.Entries[0].Op)
		}
	})

	t.Run("dangerous_risk_executes_with_double_force", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		victimPath := filepath.Join(dir, "victim.txt")
		if err := os.WriteFile(victimPath, []byte("precious data"), 0o644); err != nil {
			t.Fatal(err)
		}

		plan := &PlanTree{
			Force: true,
			Root:  "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: true,
					Force:   true,
					Commands: []PlanOp{
						{Shell: "rm " + victimPath},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.Transcript) < 1 || len(result.Transcript[0].Ops) < 1 {
			t.Fatalf("expected at least one transcript entry with one op")
		}
		if !result.Transcript[0].Ops[0].OK {
			t.Error("expected op OK == true for dangerous mutation with double force")
		}

		_, statErr := os.Stat(victimPath)
		if !os.IsNotExist(statErr) {
			t.Errorf("expected victim file to be deleted, but it still exists: %v", statErr)
		}
	})

	t.Run("nested_run_shell_force_cannot_bypass_plan_gate", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		victimPath := filepath.Join(dir, "nested-victim.txt")
		if err := os.WriteFile(victimPath, []byte("precious data"), 0o644); err != nil {
			t.Fatal(err)
		}
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{{
				ID:      "n1",
				Mutates: true,
				Commands: []PlanOp{{Tool: "run_shell", Args: map[string]any{
					"command": "rm " + shellQuote(victimPath),
					"force":   true,
				}}},
			}},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("runPlanTree(): %v", err)
		}
		if result.StoppedReason != StopMutationBlocked {
			t.Fatalf("StoppedReason = %q, want %q", result.StoppedReason, StopMutationBlocked)
		}
		if _, err := os.Stat(victimPath); err != nil {
			t.Fatalf("nested force bypass deleted victim: %v", err)
		}
	})

	t.Run("nested_run_shell_executes_with_double_force", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		victimPath := filepath.Join(dir, "nested-double-force-victim.txt")
		if err := os.WriteFile(victimPath, []byte("disposable"), 0o644); err != nil {
			t.Fatal(err)
		}
		plan := &PlanTree{
			Force: true,
			Root:  "n1",
			Nodes: []PlanNode{{
				ID:      "n1",
				Mutates: true,
				Force:   true,
				Commands: []PlanOp{{Tool: "run_shell", Args: map[string]any{
					"command": "rm " + shellQuote(victimPath),
					"force":   false,
				}}},
			}},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("runPlanTree(): %v", err)
		}
		if result.StoppedReason != StopLeaf || !result.Transcript[0].Ops[0].OK {
			t.Fatalf("nested dangerous run = %+v, want successful leaf", result)
		}
		if got := result.Transcript[0].Ops[0].Risk; got != RiskDangerous.String() {
			t.Fatalf("nested dangerous risk = %q, want %q", got, RiskDangerous.String())
		}
		if _, err := os.Stat(victimPath); !os.IsNotExist(err) {
			t.Fatalf("nested double force did not delete victim: %v", err)
		}
	})
}

func TestPlanMutationFalsePositiveMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  string
	}{
		{"stderr_redirect_devnull", "echo ok 2>/dev/null"},
		{"stdout_redirect_devnull", "echo ok > /dev/null"},
		{"bash_test_comparison", "[[ $x > 3 ]]"},
		{"bash_arithmetic_comparison", "(( 4 > 2 ))"},
		{"quoted_js_comparison", `echo "const f = (x) => x >= 3;"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			risk, reason := ClassifyCommand(tc.cmd)
			if risk == RiskDangerous {
				t.Errorf("ClassifyCommand(%q) = RiskDangerous (%s), want != RiskDangerous — false positive", tc.cmd, reason)
			}
		})
	}
}
